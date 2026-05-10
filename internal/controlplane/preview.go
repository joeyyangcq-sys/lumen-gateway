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
	Kind        ResourceKind    `json:"kind"`
	ID          string          `json:"id"`
	Action      ChangeAction    `json:"action"`
	Before      json.RawMessage `json:"before,omitempty"`
	After       json.RawMessage `json:"after,omitempty"`
	Managed     bool            `json:"managed"`
	PruneSource string          `json:"prune_source,omitempty"`
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
	Prune            bool
	PruneKinds       []ResourceKind
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
				recordSummary(summaries, kind, change.Action)
				changes = append(changes, change)
				continue
			}

			change.Before = current.Value
			if semanticResourceEqual(current.Value, after) {
				change.Action = ChangeUnchanged
				recordSummary(summaries, kind, change.Action)
				if options.IncludeUnchanged {
					changes = append(changes, change)
				}
			} else {
				change.Action = ChangeUpdate
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
