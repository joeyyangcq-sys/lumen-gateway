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
	Meta      BundleMeta
	Resources map[ResourceKind]map[string]json.RawMessage
}

type BundleMeta struct {
	Format       string         `json:"format,omitempty" yaml:"format,omitempty"`
	ExportedAt   string         `json:"exported_at,omitempty" yaml:"exported_at,omitempty"`
	EtcdPrefix   string         `json:"etcd_prefix,omitempty" yaml:"etcd_prefix,omitempty"`
	ManagedKinds []ResourceKind `json:"managed_kinds,omitempty" yaml:"managed_kinds,omitempty"`
}

type ApplyResult struct {
	Counts map[ResourceKind]int
}

type ExportOptions struct {
	EtcdPrefix   string
	IncludeKinds []ResourceKind
	IncludeMeta  bool
}

type SyncOptions struct {
	PollInterval time.Duration
	Prune        bool
	PruneKinds   []ResourceKind
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
	meta, err := parseBundleMeta(normalizeYAMLValue(decoded["_meta"]))
	if err != nil {
		return FileBundle{}, err
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
	return FileBundle{
		Meta:      meta,
		Resources: resources,
	}, nil
}

func ExportBundle(ctx context.Context, svc *Service) (FileBundle, error) {
	return ExportBundleWithOptions(ctx, svc, ExportOptions{IncludeMeta: true})
}

func ExportBundleWithOptions(ctx context.Context, svc *Service, options ExportOptions) (FileBundle, error) {
	includeKinds := normalizeResourceKindSelection(options.IncludeKinds)
	result := FileBundle{Resources: make(map[ResourceKind]map[string]json.RawMessage)}
	for _, kind := range includeKinds {
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
	if options.IncludeMeta {
		result.Meta = BundleMeta{
			Format:       "lumen.apisix.bundle/v1",
			ExportedAt:   time.Now().UTC().Format(time.RFC3339),
			EtcdPrefix:   options.EtcdPrefix,
			ManagedKinds: append([]ResourceKind(nil), includeKinds...),
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

func (b FileBundle) ToMap() (map[string]any, error) {
	root := make(map[string]any, len(b.Resources)+1)
	if !b.Meta.isZero() {
		root["_meta"] = b.Meta.toMap()
	}
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
	return root, nil
}

func (b FileBundle) ToYAML() ([]byte, error) {
	root, err := b.ToMap()
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(root)
}

func ApplyBundle(ctx context.Context, svc *Service, bundle FileBundle) (ApplyResult, error) {
	return ApplyBundleWithOptions(ctx, svc, bundle, ApplyOptions{})
}

func ApplyBundleWithOptions(ctx context.Context, svc *Service, bundle FileBundle, options ApplyOptions) (ApplyResult, error) {
	result := ApplyResult{Counts: make(map[ResourceKind]int)}
	pruneKinds := resolvePruneKinds(bundle, options)
	for _, kind := range applyOrder() {
		group, ok := bundle.Resources[kind]
		_, shouldPrune := pruneKinds[kind]
		if !ok && !shouldPrune {
			continue
		}
		if !ok {
			group = map[string]json.RawMessage{}
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
		if shouldPrune {
			items, err := svc.List(ctx, kind)
			if err != nil {
				return ApplyResult{}, err
			}
			for _, item := range items {
				existingID, err := ExtractResourceID(item.Value)
				if err != nil || existingID == "" {
					continue
				}
				if _, keep := group[existingID]; keep {
					continue
				}
				if _, err := svc.Delete(ctx, kind, existingID); err != nil {
					return ApplyResult{}, fmt.Errorf("prune %s %s: %w", kind, existingID, err)
				}
			}
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
		result, err := ApplyBundleWithOptions(ctx, svc, bundle, ApplyOptions{
			Prune:      options.Prune,
			PruneKinds: options.PruneKinds,
		})
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

func normalizeResourceKindSelection(kinds []ResourceKind) []ResourceKind {
	if len(kinds) == 0 {
		return SupportedKinds()
	}
	seen := make(map[ResourceKind]struct{}, len(kinds))
	out := make([]ResourceKind, 0, len(kinds))
	for _, kind := range kinds {
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	slices.Sort(out)
	return out
}

func parseBundleMeta(raw any) (BundleMeta, error) {
	metaMap, ok := raw.(map[string]any)
	if !ok || len(metaMap) == 0 {
		return BundleMeta{}, nil
	}
	meta := BundleMeta{}
	if format, ok := metaMap["format"].(string); ok {
		meta.Format = format
	}
	if exportedAt, ok := metaMap["exported_at"].(string); ok {
		meta.ExportedAt = exportedAt
	}
	if etcdPrefix, ok := metaMap["etcd_prefix"].(string); ok {
		meta.EtcdPrefix = etcdPrefix
	}
	if managedRaw, ok := metaMap["managed_kinds"]; ok {
		kinds, err := parseManagedKinds(managedRaw)
		if err != nil {
			return BundleMeta{}, err
		}
		meta.ManagedKinds = kinds
	}
	return meta, nil
}

func parseManagedKinds(raw any) ([]ResourceKind, error) {
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: _meta.managed_kinds must be an array", ErrInvalidBody)
	}
	kinds := make([]ResourceKind, 0, len(values))
	for _, item := range values {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%w: _meta.managed_kinds must contain strings", ErrInvalidBody)
		}
		kind, supported := ParseKind(text)
		if !supported {
			return nil, fmt.Errorf("%w: unsupported managed kind %s", ErrInvalidBody, text)
		}
		kinds = append(kinds, kind)
	}
	return normalizeResourceKindSelection(kinds), nil
}

func resolvePruneKinds(bundle FileBundle, options ApplyOptions) map[ResourceKind]struct{} {
	if !options.Prune {
		return nil
	}
	selected := options.PruneKinds
	if len(selected) == 0 {
		selected = bundle.Meta.ManagedKinds
	}
	if len(selected) == 0 {
		selected = bundle.resourceKinds()
	}
	set := make(map[ResourceKind]struct{}, len(selected))
	for _, kind := range normalizeResourceKindSelection(selected) {
		set[kind] = struct{}{}
	}
	return set
}

func (b FileBundle) resourceKinds() []ResourceKind {
	kinds := make([]ResourceKind, 0, len(b.Resources))
	for kind := range b.Resources {
		kinds = append(kinds, kind)
	}
	return normalizeResourceKindSelection(kinds)
}

func (m BundleMeta) isZero() bool {
	return m.Format == "" && m.ExportedAt == "" && m.EtcdPrefix == "" && len(m.ManagedKinds) == 0
}

func (m BundleMeta) toMap() map[string]any {
	out := map[string]any{}
	if m.Format != "" {
		out["format"] = m.Format
	}
	if m.ExportedAt != "" {
		out["exported_at"] = m.ExportedAt
	}
	if m.EtcdPrefix != "" {
		out["etcd_prefix"] = m.EtcdPrefix
	}
	if len(m.ManagedKinds) > 0 {
		values := make([]string, 0, len(m.ManagedKinds))
		for _, kind := range normalizeResourceKindSelection(m.ManagedKinds) {
			values = append(values, string(kind))
		}
		out["managed_kinds"] = values
	}
	return out
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
