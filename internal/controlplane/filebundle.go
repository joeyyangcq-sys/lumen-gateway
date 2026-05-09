package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"gopkg.in/yaml.v3"
)

type FileBundle struct {
	Resources map[ResourceKind]map[string]json.RawMessage
}

type ApplyResult struct {
	Counts map[ResourceKind]int
}

type SyncOptions struct {
	PollInterval time.Duration
	OnApply      func(ApplyResult)
}

func LoadBundleFile(path string) (FileBundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileBundle{}, err
	}
	return ParseBundle(data)
}

func ParseBundle(data []byte) (FileBundle, error) {
	var decoded map[string]any
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		return FileBundle{}, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	resources := make(map[ResourceKind]map[string]json.RawMessage)
	for _, kind := range SupportedKinds() {
		raw, ok := decoded[string(kind)]
		if !ok {
			continue
		}
		normalized, err := normalizeBundleResources(kind, normalizeYAMLValue(raw))
		if err != nil {
			return FileBundle{}, err
		}
		if len(normalized) > 0 {
			resources[kind] = normalized
		}
	}
	return FileBundle{Resources: resources}, nil
}

func ExportBundle(ctx context.Context, svc *Service) (FileBundle, error) {
	result := FileBundle{Resources: make(map[ResourceKind]map[string]json.RawMessage)}
	for _, kind := range SupportedKinds() {
		items, err := svc.List(ctx, kind)
		if err != nil {
			return FileBundle{}, err
		}
		group := make(map[string]json.RawMessage, len(items))
		for _, item := range items {
			id, err := ExtractResourceID(item.Value)
			if err != nil || id == "" {
				continue
			}
			group[id] = item.Value
		}
		if len(group) > 0 {
			result.Resources[kind] = group
		}
	}
	return result, nil
}

func WriteBundleFile(path string, bundle FileBundle) error {
	payload, err := bundle.ToYAML()
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o644)
}

func (b FileBundle) ToYAML() ([]byte, error) {
	root := make(map[string]any, len(b.Resources))
	for _, kind := range SupportedKinds() {
		items, ok := b.Resources[kind]
		if !ok || len(items) == 0 {
			continue
		}
		group := make(map[string]any, len(items))
		keys := make([]string, 0, len(items))
		for id := range items {
			keys = append(keys, id)
		}
		slices.Sort(keys)
		for _, id := range keys {
			var decoded any
			if err := json.Unmarshal(items[id], &decoded); err != nil {
				return nil, err
			}
			group[id] = decoded
		}
		root[string(kind)] = group
	}
	return yaml.Marshal(root)
}

func ApplyBundle(ctx context.Context, svc *Service, bundle FileBundle) (ApplyResult, error) {
	result := ApplyResult{Counts: make(map[ResourceKind]int)}
	for _, kind := range applyOrder() {
		group, ok := bundle.Resources[kind]
		if !ok {
			continue
		}
		ids := make([]string, 0, len(group))
		for id := range group {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			if _, err := svc.Put(ctx, kind, id, group[id]); err != nil {
				return ApplyResult{}, fmt.Errorf("%s %s: %w", kind, id, err)
			}
			result.Counts[kind]++
		}
	}
	return result, nil
}

func SyncBundleFile(ctx context.Context, svc *Service, path string, options SyncOptions) error {
	interval := options.PollInterval
	if interval <= 0 {
		interval = time.Second
	}

	apply := func() error {
		bundle, err := LoadBundleFile(path)
		if err != nil {
			return err
		}
		result, err := ApplyBundle(ctx, svc, bundle)
		if err != nil {
			return err
		}
		if options.OnApply != nil {
			options.OnApply(result)
		}
		return nil
	}

	if err := apply(); err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	lastMod := info.ModTime()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if !info.ModTime().After(lastMod) {
				continue
			}
			lastMod = info.ModTime()
			if err := apply(); err != nil {
				return err
			}
		}
	}
}

func normalizeBundleResources(kind ResourceKind, raw any) (map[string]json.RawMessage, error) {
	switch value := raw.(type) {
	case map[string]any:
		out := make(map[string]json.RawMessage, len(value))
		for id, resource := range value {
			resourceMap, ok := resource.(map[string]any)
			if ok {
				resourceMap["id"] = id
				resource = resourceMap
			}
			encoded, err := json.Marshal(resource)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
			}
			out[id] = encoded
		}
		return out, nil
	case []any:
		out := make(map[string]json.RawMessage, len(value))
		for _, item := range value {
			encoded, err := json.Marshal(item)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
			}
			id, err := ExtractResourceID(encoded)
			if err != nil {
				return nil, err
			}
			if id == "" {
				return nil, fmt.Errorf("%w: %s item missing id", ErrInvalidBody, kind)
			}
			out[id] = encoded
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %s must be object or array", ErrInvalidBody, kind)
	}
}

func normalizeYAMLValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = normalizeYAMLValue(child)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[fmt.Sprint(key)] = normalizeYAMLValue(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			out = append(out, normalizeYAMLValue(child))
		}
		return out
	default:
		return value
	}
}

func applyOrder() []ResourceKind {
	return []ResourceKind{
		KindUpstream,
		KindService,
		KindPluginConfig,
		KindGlobalRule,
		KindRoute,
	}
}
