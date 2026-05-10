package controlplane

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBuildApplyPlanReportsCreateUpdateDeleteAndUnchanged(t *testing.T) {
	svc := New(&exportStore{
		listResults: map[ResourceKind][]Envelope{
			KindRoute: {
				{Key: "/apisix/routes/keep", Value: json.RawMessage(`{"id":"keep","uri":"/keep","service_id":"svc-1","create_time":100,"update_time":100}`)},
				{Key: "/apisix/routes/update", Value: json.RawMessage(`{"id":"update","uri":"/old","service_id":"svc-1","create_time":100,"update_time":100}`)},
				{Key: "/apisix/routes/drop", Value: json.RawMessage(`{"id":"drop","uri":"/drop","service_id":"svc-1","create_time":100,"update_time":100}`)},
			},
		},
	})

	bundle := FileBundle{
		Resources: map[ResourceKind]map[string]json.RawMessage{
			KindRoute: {
				"keep":   json.RawMessage(`{"uri":"/keep","service_id":"svc-1"}`),
				"update": json.RawMessage(`{"uri":"/new","service_id":"svc-1"}`),
				"new":    json.RawMessage(`{"uri":"/created","service_id":"svc-1"}`),
			},
		},
	}

	plan, err := BuildApplyPlan(context.Background(), svc, bundle, PreviewOptions{
		Prune:            true,
		IncludeUnchanged: true,
	})
	if err != nil {
		t.Fatalf("BuildApplyPlan() error = %v", err)
	}
	if len(plan.Changes) != 4 {
		t.Fatalf("changes len = %d, want 4", len(plan.Changes))
	}
	assertChangeAction(t, plan, KindRoute, "keep", ChangeUnchanged)
	assertChangeAction(t, plan, KindRoute, "update", ChangeUpdate)
	assertChangeAction(t, plan, KindRoute, "new", ChangeCreate)
	assertChangeAction(t, plan, KindRoute, "drop", ChangeDelete)

	if len(plan.Summary) != 1 {
		t.Fatalf("summary len = %d, want 1", len(plan.Summary))
	}
	got := plan.Summary[0]
	if got.Create != 1 || got.Update != 1 || got.Delete != 1 || got.Unchanged != 1 {
		t.Fatalf("summary = %#v", got)
	}
}

func TestBuildApplyPlanUsesManagedKindsForSelectivePrune(t *testing.T) {
	svc := New(&exportStore{
		listResults: map[ResourceKind][]Envelope{
			KindRoute: {
				{Key: "/apisix/routes/drop-route", Value: json.RawMessage(`{"id":"drop-route","uri":"/drop","service_id":"svc-1"}`)},
			},
			KindService: {
				{Key: "/apisix/services/drop-service", Value: json.RawMessage(`{"id":"drop-service","upstream_id":"up-1"}`)},
			},
		},
	})

	bundle := FileBundle{
		Meta: BundleMeta{ManagedKinds: []ResourceKind{KindRoute}},
		Resources: map[ResourceKind]map[string]json.RawMessage{
			KindRoute: {
				"keep-route": json.RawMessage(`{"uri":"/keep","service_id":"svc-1"}`),
			},
		},
	}

	plan, err := BuildApplyPlan(context.Background(), svc, bundle, PreviewOptions{Prune: true})
	if err != nil {
		t.Fatalf("BuildApplyPlan() error = %v", err)
	}
	if hasChange(plan, KindService, "drop-service") {
		t.Fatalf("plan unexpectedly pruned service: %#v", plan.Changes)
	}
	change := mustFindChange(t, plan, KindRoute, "drop-route")
	if change.Action != ChangeDelete || change.PruneSource != "managed_kinds" {
		t.Fatalf("delete change = %#v", change)
	}
}

func TestBuildApplyPlanExplicitPruneKindsOverrideBundleManagedKinds(t *testing.T) {
	svc := New(&exportStore{
		listResults: map[ResourceKind][]Envelope{
			KindRoute: {
				{Key: "/apisix/routes/drop-route", Value: json.RawMessage(`{"id":"drop-route","uri":"/drop","service_id":"svc-1"}`)},
			},
			KindService: {
				{Key: "/apisix/services/drop-service", Value: json.RawMessage(`{"id":"drop-service","upstream_id":"up-1"}`)},
			},
		},
	})

	bundle := FileBundle{
		Meta: BundleMeta{ManagedKinds: []ResourceKind{KindRoute}},
		Resources: map[ResourceKind]map[string]json.RawMessage{
			KindRoute: {
				"keep-route": json.RawMessage(`{"uri":"/keep","service_id":"svc-1"}`),
			},
		},
	}

	plan, err := BuildApplyPlan(context.Background(), svc, bundle, PreviewOptions{
		Prune:      true,
		PruneKinds: []ResourceKind{KindService},
	})
	if err != nil {
		t.Fatalf("BuildApplyPlan() error = %v", err)
	}
	if hasChange(plan, KindRoute, "drop-route") {
		t.Fatalf("plan unexpectedly pruned route: %#v", plan.Changes)
	}
	change := mustFindChange(t, plan, KindService, "drop-service")
	if change.Action != ChangeDelete || change.PruneSource != "explicit_prune_kinds" {
		t.Fatalf("delete change = %#v", change)
	}
}

func assertChangeAction(t *testing.T, plan ApplyPlan, kind ResourceKind, id string, action ChangeAction) {
	t.Helper()
	change := mustFindChange(t, plan, kind, id)
	if change.Action != action {
		t.Fatalf("change %s/%s action = %s, want %s", kind, id, change.Action, action)
	}
}

func mustFindChange(t *testing.T, plan ApplyPlan, kind ResourceKind, id string) ChangeItem {
	t.Helper()
	for _, item := range plan.Changes {
		if item.Kind == kind && item.ID == id {
			return item
		}
	}
	t.Fatalf("missing change %s/%s in %#v", kind, id, plan.Changes)
	return ChangeItem{}
}

func hasChange(plan ApplyPlan, kind ResourceKind, id string) bool {
	for _, item := range plan.Changes {
		if item.Kind == kind && item.ID == id {
			return true
		}
	}
	return false
}
