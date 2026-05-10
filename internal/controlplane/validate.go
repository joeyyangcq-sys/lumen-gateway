package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/joey/lumen-gateway/internal/apisix"
	"github.com/joey/lumen-gateway/internal/translate"
)

type ValidationIssue struct {
	Resource   string `json:"resource,omitempty"`
	ResourceID string `json:"resource_id,omitempty"`
	Field      string `json:"field,omitempty"`
	Message    string `json:"message"`
}

type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Issues []ValidationIssue `json:"issues,omitempty"`
}

type ValidateOptions struct {
	Prune      bool
	PruneKinds []ResourceKind
}

func (s *Service) ValidateBundle(ctx context.Context, bundle FileBundle, options ValidateOptions) (ValidationResult, error) {
	snapshot, err := s.snapshotFromStore(ctx)
	if err != nil {
		return ValidationResult{}, err
	}
	var fallbackKind ResourceKind
	var fallbackID string

	pruneKinds := resolvePruneKinds(bundle, ApplyOptions{
		Prune:      options.Prune,
		PruneKinds: options.PruneKinds,
	})

	for _, kind := range SupportedKinds() {
		group, hasGroup := bundle.Resources[kind]
		if !hasGroup {
			group = map[string]json.RawMessage{}
		}

		for id, body := range group {
			if fallbackKind == "" {
				fallbackKind = kind
				fallbackID = id
			}
			normalized, err := NormalizeResourceBody(body, id)
			if err != nil {
				return ValidationResult{}, err
			}
			if err := applyResourceBody(&snapshot, kind, id, normalized); err != nil {
				return ValidationResult{}, err
			}
		}

		if _, shouldPrune := pruneKinds[kind]; shouldPrune {
			existingIDs := snapshotResourceIDs(snapshot, kind)
			for _, id := range existingIDs {
				if _, keep := group[id]; keep {
					continue
				}
				removeResource(&snapshot, kind, id)
			}
		}
	}

	return validateSnapshot(snapshot, fallbackKind, fallbackID)
}

func (s *Service) ValidateResource(ctx context.Context, kind ResourceKind, id string, body json.RawMessage) (ValidationResult, error) {
	if err := validateKind(kind); err != nil {
		return ValidationResult{}, err
	}

	if id == "" {
		extractedID, err := ExtractResourceID(body)
		if err != nil {
			return ValidationResult{}, err
		}
		if extractedID != "" {
			id = extractedID
		} else {
			id = "_preview"
		}
	}

	normalized, err := NormalizeResourceBody(body, id)
	if err != nil {
		return ValidationResult{}, err
	}

	snapshot, err := s.snapshotFromStore(ctx)
	if err != nil {
		return ValidationResult{}, err
	}
	if err := applyResourceBody(&snapshot, kind, id, normalized); err != nil {
		return ValidationResult{}, err
	}

	return validateSnapshot(snapshot, kind, id)
}

func (s *Service) snapshotFromStore(ctx context.Context) (apisix.Snapshot, error) {
	snapshot := apisix.NewSnapshot()
	for _, kind := range SupportedKinds() {
		items, err := s.store.List(ctx, kind)
		if err != nil {
			return apisix.Snapshot{}, err
		}
		for _, item := range items {
			id, err := ExtractResourceID(item.Value)
			if err != nil {
				return apisix.Snapshot{}, err
			}
			if id == "" {
				continue
			}
			if err := applyResourceBody(&snapshot, kind, id, item.Value); err != nil {
				return apisix.Snapshot{}, err
			}
		}
	}
	return snapshot, nil
}

func applyResourceBody(snapshot *apisix.Snapshot, kind ResourceKind, id string, body json.RawMessage) error {
	switch kind {
	case KindRoute:
		var resource apisix.Route
		if err := json.Unmarshal(body, &resource); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidBody, err)
		}
		resource.ID = apisix.ID(id)
		snapshot.Routes[id] = resource
	case KindService:
		var resource apisix.Service
		if err := json.Unmarshal(body, &resource); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidBody, err)
		}
		resource.ID = apisix.ID(id)
		snapshot.Services[id] = resource
	case KindUpstream:
		var resource apisix.Upstream
		if err := json.Unmarshal(body, &resource); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidBody, err)
		}
		resource.ID = apisix.ID(id)
		snapshot.Upstreams[id] = resource
	case KindPluginConfig:
		var resource apisix.PluginConfig
		if err := json.Unmarshal(body, &resource); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidBody, err)
		}
		resource.ID = apisix.ID(id)
		snapshot.PluginConfig[id] = resource
	case KindGlobalRule:
		var resource apisix.GlobalRule
		if err := json.Unmarshal(body, &resource); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidBody, err)
		}
		resource.ID = apisix.ID(id)
		snapshot.GlobalRules[id] = resource
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedKind, kind)
	}
	return nil
}

func removeResource(snapshot *apisix.Snapshot, kind ResourceKind, id string) {
	switch kind {
	case KindRoute:
		delete(snapshot.Routes, id)
	case KindService:
		delete(snapshot.Services, id)
	case KindUpstream:
		delete(snapshot.Upstreams, id)
	case KindPluginConfig:
		delete(snapshot.PluginConfig, id)
	case KindGlobalRule:
		delete(snapshot.GlobalRules, id)
	}
}

func snapshotResourceIDs(snapshot apisix.Snapshot, kind ResourceKind) []string {
	switch kind {
	case KindRoute:
		return sortedMapKeys(snapshot.Routes)
	case KindService:
		return sortedMapKeys(snapshot.Services)
	case KindUpstream:
		return sortedMapKeys(snapshot.Upstreams)
	case KindPluginConfig:
		return sortedMapKeys(snapshot.PluginConfig)
	case KindGlobalRule:
		return sortedMapKeys(snapshot.GlobalRules)
	default:
		return nil
	}
}

func validateSnapshot(snapshot apisix.Snapshot, fallbackKind ResourceKind, fallbackID string) (ValidationResult, error) {
	_, err := translate.ApisixSnapshotToConfig(snapshot, translate.ApisixToConfigOptions{
		Listen: ":18080",
	})
	if err == nil {
		return ValidationResult{Valid: true}, nil
	}

	issue := inferValidationIssue(err, fallbackKind, fallbackID)
	return ValidationResult{
		Valid:  false,
		Issues: []ValidationIssue{issue},
	}, nil
}

var resourcePrefixPattern = regexp.MustCompile(`^(route|service|upstream|plugin_config|global rule) "([^"]+)": (.+)$`)

func inferValidationIssue(err error, fallbackKind ResourceKind, fallbackID string) ValidationIssue {
	message := err.Error()
	issue := ValidationIssue{
		Resource:   string(fallbackKind),
		ResourceID: fallbackID,
		Message:    message,
	}

	if match := resourcePrefixPattern.FindStringSubmatch(message); len(match) == 4 {
		issue.Resource = mapErrorResource(match[1])
		issue.ResourceID = match[2]
		issue.Message = match[3]
	}

	lower := strings.ToLower(issue.Message)
	switch {
	case strings.Contains(lower, "uri/uris"):
		issue.Field = "uri"
	case strings.Contains(lower, "references unknown service"):
		issue.Field = "service_id"
	case strings.Contains(lower, "missing upstream_id/upstream"):
		issue.Field = "upstream_id"
	case strings.Contains(lower, "references unknown upstream"):
		issue.Field = "upstream_id"
	case strings.Contains(lower, "plugin_config"):
		issue.Field = "plugin_config_id"
	case strings.Contains(lower, "nodes"):
		issue.Field = "nodes"
	case strings.Contains(lower, "pass_host"):
		issue.Field = "pass_host"
	case strings.Contains(lower, "upstream_host"):
		issue.Field = "upstream_host"
	case strings.Contains(lower, "plugins"):
		issue.Field = "plugins"
	}
	return issue
}

func mapErrorResource(label string) string {
	switch label {
	case "route":
		return string(KindRoute)
	case "service":
		return string(KindService)
	case "upstream":
		return string(KindUpstream)
	case "plugin_config":
		return string(KindPluginConfig)
	case "global rule":
		return string(KindGlobalRule)
	default:
		return label
	}
}

func sortedMapKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
