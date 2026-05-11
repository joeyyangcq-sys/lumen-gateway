package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/joey/lumen-gateway/internal/apisix"
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

// validateSnapshot checks the snapshot for structural integrity (cross-references,
// required fields) and returns actionable, human-readable issues.
// It does NOT rely on the translate layer so that gateway-reload resilience
// (lenient translator) and write-time validation (strict) stay independent.
func validateSnapshot(snapshot apisix.Snapshot, targetKind ResourceKind, targetID string) (ValidationResult, error) {
	var issues []ValidationIssue

	// ── Upstreams ─────────────────────────────────────────────────────────────
	for id, up := range snapshot.Upstreams {
		if len(up.Nodes) == 0 {
			issues = append(issues, ValidationIssue{
				Resource:   string(KindUpstream),
				ResourceID: id,
				Field:      "nodes",
				Message:    "上游节点列表不能为空，请至少添加一个后端节点（host:port）",
			})
		}
	}

	// ── Services ──────────────────────────────────────────────────────────────
	for id, svc := range snapshot.Services {
		upstreamID := string(svc.UpstreamID)
		if upstreamID == "" && svc.Upstream == nil {
			issues = append(issues, ValidationIssue{
				Resource:   string(KindService),
				ResourceID: id,
				Field:      "upstream_id",
				Message:    "服务缺少 upstream_id，请先创建上游，再在服务中选择关联上游",
			})
			continue
		}
		if upstreamID != "" {
			if _, ok := snapshot.Upstreams[upstreamID]; !ok {
				issues = append(issues, ValidationIssue{
					Resource:   string(KindService),
					ResourceID: id,
					Field:      "upstream_id",
					Message:    fmt.Sprintf("服务引用的上游 %q 不存在，请先创建该上游再保存服务", upstreamID),
				})
			}
		}
	}

	// ── Routes ────────────────────────────────────────────────────────────────
	for id, route := range snapshot.Routes {
		if string(route.URI) == "" && len(route.URIs) == 0 {
			issues = append(issues, ValidationIssue{
				Resource:   string(KindRoute),
				ResourceID: id,
				Field:      "uri",
				Message:    "路由缺少请求路径，请填写 uri 字段（如 /api/v1/* 或 /example）",
			})
			continue
		}

		serviceID := string(route.ServiceID)
		upstreamID := string(route.UpstreamID)

		if serviceID == "" && upstreamID == "" && route.Upstream == nil {
			issues = append(issues, ValidationIssue{
				Resource:   string(KindRoute),
				ResourceID: id,
				Field:      "service_id",
				Message:    "路由缺少关联服务，请先创建服务，再在路由的「关联服务」中选择",
			})
			continue
		}
		if serviceID != "" {
			if _, ok := snapshot.Services[serviceID]; !ok {
				issues = append(issues, ValidationIssue{
					Resource:   string(KindRoute),
					ResourceID: id,
					Field:      "service_id",
					Message:    fmt.Sprintf("路由引用的服务 %q 不存在，请先创建该服务再保存路由", serviceID),
				})
			}
		}
		if upstreamID != "" {
			if _, ok := snapshot.Upstreams[upstreamID]; !ok {
				issues = append(issues, ValidationIssue{
					Resource:   string(KindRoute),
					ResourceID: id,
					Field:      "upstream_id",
					Message:    fmt.Sprintf("路由引用的上游 %q 不存在，请先创建该上游再保存路由", upstreamID),
				})
			}
		}
	}

	// Filter issues relevant to the target resource first, for better UX.
	primary := filterIssues(issues, targetKind, targetID)
	if len(primary) > 0 {
		return ValidationResult{Valid: false, Issues: primary}, nil
	}
	if len(issues) > 0 {
		return ValidationResult{Valid: false, Issues: issues}, nil
	}
	return ValidationResult{Valid: true}, nil
}

// filterIssues returns only the issues that match the given resource kind + id.
// Falls back to all issues if none match.
func filterIssues(issues []ValidationIssue, kind ResourceKind, id string) []ValidationIssue {
	if kind == "" {
		return nil
	}
	var out []ValidationIssue
	for _, issue := range issues {
		if issue.Resource == string(kind) && (id == "" || issue.ResourceID == id) {
			out = append(out, issue)
		}
	}
	return out
}


func sortedMapKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
