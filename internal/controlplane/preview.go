package controlplane

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
)

type ChangeAction string

const (
	ChangeCreate    ChangeAction = "create"
	ChangeUpdate    ChangeAction = "update"
	ChangeDelete    ChangeAction = "delete"
	ChangeUnchanged ChangeAction = "unchanged"
)

type ChangeItem struct {
	Summary     map[string]any  `json:"summary,omitempty"`
	Kind        ResourceKind    `json:"kind"`
	ID          string          `json:"id"`
	Action      ChangeAction    `json:"action"`
	Title       string          `json:"title,omitempty"`
	PruneSource string          `json:"prune_source,omitempty"`
	Warnings    []string        `json:"warnings,omitempty"`
	Before      json.RawMessage `json:"before,omitempty"`
	After       json.RawMessage `json:"after,omitempty"`
	Managed     bool            `json:"managed"`
}

type PlanSummary struct {
	Kind      ResourceKind `json:"kind"`
	Create    int          `json:"create"`
	Update    int          `json:"update"`
	Delete    int          `json:"delete"`
	Unchanged int          `json:"unchanged"`
}

type ApplyPlan struct {
	Summary []PlanSummary `json:"summary"`
	Changes []ChangeItem  `json:"changes"`
}

type PreviewOptions struct {
	PruneKinds       []ResourceKind
	Prune            bool
	IncludeUnchanged bool
}

func BuildApplyPlan(ctx context.Context, svc *Service, bundle FileBundle, options PreviewOptions) (ApplyPlan, error) {
	pruneKinds := resolvePruneKinds(bundle, ApplyOptions{
		Prune:      options.Prune,
		PruneKinds: options.PruneKinds,
	})
	summaries := make(map[ResourceKind]*PlanSummary, len(SupportedKinds()))
	changes := make([]ChangeItem, 0)

	for _, kind := range applyOrder() {
		desired, hasDesired := bundle.Resources[kind]
		_, shouldPrune := pruneKinds[kind]
		if !hasDesired && !shouldPrune {
			continue
		}
		if !hasDesired {
			desired = map[string]json.RawMessage{}
		}

		items, err := svc.List(ctx, kind)
		if err != nil {
			return ApplyPlan{}, err
		}
		currentByID := make(map[string]Envelope, len(items))
		for _, item := range items {
			id, err := ExtractResourceID(item.Value)
			if err != nil || id == "" {
				continue
			}
			currentByID[id] = item
		}

		desiredIDs := sortedKeys(desired)
		for _, id := range desiredIDs {
			after, err := NormalizeResourceBody(desired[id], id)
			if err != nil {
				return ApplyPlan{}, err
			}
			current, exists := currentByID[id]
			change := ChangeItem{
				Kind:    kind,
				ID:      id,
				After:   after,
				Managed: true,
			}
			if !exists {
				change.Action = ChangeCreate
				enrichChangeItem(&change)
				recordSummary(summaries, kind, change.Action)
				changes = append(changes, change)
				continue
			}

			change.Before = current.Value
			if semanticResourceEqual(current.Value, after) {
				change.Action = ChangeUnchanged
				enrichChangeItem(&change)
				recordSummary(summaries, kind, change.Action)
				if options.IncludeUnchanged {
					changes = append(changes, change)
				}
			} else {
				change.Action = ChangeUpdate
				enrichChangeItem(&change)
				recordSummary(summaries, kind, change.Action)
				changes = append(changes, change)
			}
		}

		if shouldPrune {
			pruneSource := pruneSourceLabel(bundle, options, kind)
			currentIDs := sortedEnvelopeKeys(currentByID)
			for _, id := range currentIDs {
				if _, keep := desired[id]; keep {
					continue
				}
				change := ChangeItem{
					Kind:        kind,
					ID:          id,
					Action:      ChangeDelete,
					Before:      currentByID[id].Value,
					Managed:     true,
					PruneSource: pruneSource,
				}
				enrichChangeItem(&change)
				recordSummary(summaries, kind, change.Action)
				changes = append(changes, change)
			}
		}
	}

	return ApplyPlan{
		Summary: flattenSummaries(summaries),
		Changes: changes,
	}, nil
}

func semanticResourceEqual(current, desired json.RawMessage) bool {
	currentValue, err := previewComparableValue(current)
	if err != nil {
		return false
	}
	desiredValue, err := previewComparableValue(desired)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(currentValue, desiredValue)
}

func previewComparableValue(raw json.RawMessage) (map[string]any, error) {
	value, err := decodeJSONObject(raw)
	if err != nil {
		return nil, err
	}
	value = maps.Clone(value)
	delete(value, "create_time")
	delete(value, "update_time")
	return value, nil
}

func pruneSourceLabel(bundle FileBundle, options PreviewOptions, kind ResourceKind) string {
	for _, item := range options.PruneKinds {
		if item == kind {
			return "explicit_prune_kinds"
		}
	}
	for _, item := range bundle.Meta.ManagedKinds {
		if item == kind {
			return "managed_kinds"
		}
	}
	return "bundle_omitted"
}

func sortedKeys(items map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func sortedEnvelopeKeys(items map[string]Envelope) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func recordSummary(summary map[ResourceKind]*PlanSummary, kind ResourceKind, action ChangeAction) {
	item, ok := summary[kind]
	if !ok {
		item = &PlanSummary{Kind: kind}
		summary[kind] = item
	}
	switch action {
	case ChangeCreate:
		item.Create++
	case ChangeUpdate:
		item.Update++
	case ChangeDelete:
		item.Delete++
	case ChangeUnchanged:
		item.Unchanged++
	}
}

func flattenSummaries(summary map[ResourceKind]*PlanSummary) []PlanSummary {
	out := make([]PlanSummary, 0, len(summary))
	for _, kind := range applyOrder() {
		item, ok := summary[kind]
		if ok {
			out = append(out, *item)
		}
	}
	return out
}

func enrichChangeItem(change *ChangeItem) {
	if change == nil {
		return
	}
	raw := change.After
	if len(raw) == 0 {
		raw = change.Before
	}
	title, summary := summarizeChangeResource(change.Kind, change.ID, raw)
	change.Title = title
	change.Summary = summary
	change.Warnings = summarizeWarnings(*change)
}

func summarizeChangeResource(kind ResourceKind, id string, raw json.RawMessage) (string, map[string]any) {
	if len(raw) == 0 {
		return string(kind) + "/" + id, map[string]any{"id": id}
	}

	value, err := decodeJSONObject(raw)
	if err != nil {
		return string(kind) + "/" + id, map[string]any{"id": id}
	}

	switch kind {
	case KindRoute:
		title := firstNonEmptyString(
			lookupString(value, "name"),
			lookupString(value, "uri"),
			firstStringArrayItem(value, "uris"),
			id,
		)
		return title, compactSummary(map[string]any{
			"id":          id,
			"uri":         lookupString(value, "uri"),
			"uris":        lookupAny(value, "uris"),
			"methods":     lookupAny(value, "methods"),
			"service_id":  firstNonEmptyString(lookupString(value, "service"), lookupString(value, "service_id")),
			"upstream_id": lookupString(value, "upstream_id"),
			"priority":    lookupAny(value, "priority"),
		})
	case KindService:
		title := firstNonEmptyString(lookupString(value, "name"), id)
		return title, compactSummary(map[string]any{
			"id":               id,
			"upstream_id":      firstNonEmptyString(lookupString(value, "upstream"), lookupString(value, "upstream_id")),
			"plugin_config_id": lookupString(value, "plugin_config_id"),
		})
	case KindUpstream:
		title := firstNonEmptyString(lookupString(value, "name"), id)
		return title, compactSummary(map[string]any{
			"id":            id,
			"scheme":        lookupString(value, "scheme"),
			"pass_host":     lookupString(value, "pass_host"),
			"upstream_host": lookupString(value, "upstream_host"),
			"nodes_count":   countNodes(value),
		})
	case KindPluginConfig:
		title := firstNonEmptyString(lookupString(value, "name"), id)
		return title, compactSummary(map[string]any{
			"id":          id,
			"plugin_keys": sortedPluginKeys(value),
		})
	case KindGlobalRule:
		title := firstNonEmptyString(lookupString(value, "name"), id)
		return title, compactSummary(map[string]any{
			"id":          id,
			"plugin_keys": sortedPluginKeys(value),
		})
	default:
		return string(kind) + "/" + id, compactSummary(map[string]any{"id": id})
	}
}

func summarizeWarnings(change ChangeItem) []string {
	warnings := make([]string, 0, 2)
	if change.Action == ChangeDelete {
		warnings = append(warnings, "This change deletes an existing resource.")
		switch change.PruneSource {
		case "explicit_prune_kinds":
			warnings = append(warnings, "Delete is caused by explicit prune_kinds.")
		case "managed_kinds":
			warnings = append(warnings, "Delete is caused by managed_kinds ownership.")
		case "bundle_omitted":
			warnings = append(warnings, "Delete is caused by the resource being omitted from the bundle.")
		}
	}
	return warnings
}

func compactSummary(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		switch typed := value.(type) {
		case nil:
			continue
		case string:
			if typed == "" {
				continue
			}
		case []any:
			if len(typed) == 0 {
				continue
			}
		case []string:
			if len(typed) == 0 {
				continue
			}
		}
		out[key] = value
	}
	return out
}

func lookupString(value map[string]any, key string) string {
	raw, ok := value[key]
	if !ok || raw == nil {
		return ""
	}
	if text, ok := raw.(string); ok {
		return text
	}
	return ""
}

func lookupAny(value map[string]any, key string) any {
	return value[key]
}

func firstStringArrayItem(value map[string]any, key string) string {
	raw, ok := value[key]
	if !ok {
		return ""
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	first, ok := items[0].(string)
	if !ok {
		return ""
	}
	return first
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func countNodes(value map[string]any) int {
	raw, ok := value["nodes"]
	if !ok || raw == nil {
		return 0
	}
	switch typed := raw.(type) {
	case map[string]any:
		return len(typed)
	case []any:
		return len(typed)
	default:
		return 0
	}
}

func sortedPluginKeys(value map[string]any) []string {
	raw, ok := value["plugins"]
	if !ok || raw == nil {
		return nil
	}
	plugins, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(plugins))
	for key := range plugins {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
