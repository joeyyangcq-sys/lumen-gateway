package adminapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joey/lumen-gateway/internal/controlplane"
)

func TestUsageGuideAdminControlWorkflow(t *testing.T) {
	store := newUsageStore()
	history := &usageHistoryStore{}
	svc := controlplane.New(
		store,
		controlplane.WithHistory(history, 10, controlplane.ExportOptions{
			EtcdPrefix: "/apisix",
		}),
	)
	handler := NewWithService(svc, "usage-key")

	bundleV1 := mustReadUsageFixture(t, "bundle_v1.yaml")
	bundleV2 := mustReadUsageFixture(t, "bundle_v2.yaml")

	previewReq := newRequestContext("POST", "/apisix/admin/control/imports/preview", []byte(`{"content":`+jsonString(string(bundleV1))+`,"prune":true}`))
	previewReq.Request.Header.Set("X-API-KEY", "usage-key")
	if !handler.ServeHTTP(context.Background(), previewReq) {
		t.Fatal("preview request was not handled")
	}
	if got := previewReq.Response.StatusCode(); got != 200 {
		t.Fatalf("preview status = %d, want 200", got)
	}
	if body := string(previewReq.Response.Body()); !strings.Contains(body, `"action":"create"`) {
		t.Fatalf("preview body = %s, want create actions", body)
	}

	applyV1 := newRequestContext("POST", "/apisix/admin/control/imports/apply", []byte(`{"content":`+jsonString(string(bundleV1))+`,"prune":true}`))
	applyV1.Request.Header.Set("X-API-KEY", "usage-key")
	if !handler.ServeHTTP(context.Background(), applyV1) {
		t.Fatal("apply v1 request was not handled")
	}
	if got := applyV1.Response.StatusCode(); got != 200 {
		t.Fatalf("apply v1 status = %d, want 200", got)
	}

	applyV2 := newRequestContext("POST", "/apisix/admin/control/imports/apply", []byte(`{"content":`+jsonString(string(bundleV2))+`,"prune":true}`))
	applyV2.Request.Header.Set("X-API-KEY", "usage-key")
	if !handler.ServeHTTP(context.Background(), applyV2) {
		t.Fatal("apply v2 request was not handled")
	}
	if got := applyV2.Response.StatusCode(); got != 200 {
		t.Fatalf("apply v2 status = %d, want 200", got)
	}

	exportReq := newRequestContext("GET", "/apisix/admin/control/exports?kind=routes&format=json", nil)
	exportReq.Request.Header.Set("X-API-KEY", "usage-key")
	if !handler.ServeHTTP(context.Background(), exportReq) {
		t.Fatal("export request was not handled")
	}
	if got := exportReq.Response.StatusCode(); got != 200 {
		t.Fatalf("export status = %d, want 200", got)
	}
	if body := string(exportReq.Response.Body()); !strings.Contains(body, `"/demo-v2"`) {
		t.Fatalf("export body = %s, want demo-v2 route", body)
	}

	historyReq := newRequestContext("GET", "/apisix/admin/control/history?limit=10", nil)
	historyReq.Request.Header.Set("X-API-KEY", "usage-key")
	if !handler.ServeHTTP(context.Background(), historyReq) {
		t.Fatal("history request was not handled")
	}
	if got := historyReq.Response.StatusCode(); got != 200 {
		t.Fatalf("history status = %d, want 200", got)
	}
	if body := string(historyReq.Response.Body()); !strings.Contains(body, `"total":2`) {
		t.Fatalf("history body = %s, want total 2", body)
	}

	entries, err := history.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("history.List() error = %v", err)
	}
	rollbackID := entries[len(entries)-1].ID

	rollbackReq := newRequestContext("POST", "/apisix/admin/control/history/"+rollbackID+"/rollback", nil)
	rollbackReq.Request.Header.Set("X-API-KEY", "usage-key")
	if !handler.ServeHTTP(context.Background(), rollbackReq) {
		t.Fatal("rollback request was not handled")
	}
	if got := rollbackReq.Response.StatusCode(); got != 200 {
		t.Fatalf("rollback status = %d, want 200", got)
	}

	currentRoute, err := store.Get(context.Background(), controlplane.KindRoute, "demo-route")
	if err != nil {
		t.Fatalf("store.Get(route) error = %v", err)
	}
	if !strings.Contains(string(currentRoute.Value), `"/demo"`) {
		t.Fatalf("route after rollback = %s, want /demo", string(currentRoute.Value))
	}
	if strings.Contains(string(currentRoute.Value), `"/demo-v2"`) {
		t.Fatalf("route after rollback = %s, want v2 to be rolled back", string(currentRoute.Value))
	}
}

func mustReadUsageFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage", name))
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", name, err)
	}
	return data
}

func jsonString(value string) string {
	payload, _ := json.Marshal(value)
	return string(payload)
}

type usageStore struct {
	data map[controlplane.ResourceKind]map[string]json.RawMessage
}

func newUsageStore() *usageStore {
	return &usageStore{data: map[controlplane.ResourceKind]map[string]json.RawMessage{}}
}

func (s *usageStore) List(_ context.Context, kind controlplane.ResourceKind) ([]controlplane.Envelope, error) {
	group := s.data[kind]
	out := make([]controlplane.Envelope, 0, len(group))
	for id, body := range group {
		out = append(out, controlplane.Envelope{
			Key:   "/apisix/" + string(kind) + "/" + id,
			Value: append(json.RawMessage(nil), body...),
		})
	}
	return out, nil
}

func (s *usageStore) Get(_ context.Context, kind controlplane.ResourceKind, id string) (controlplane.Envelope, error) {
	group := s.data[kind]
	if group == nil {
		return controlplane.Envelope{}, controlplane.ErrNotFound
	}
	body, ok := group[id]
	if !ok {
		return controlplane.Envelope{}, controlplane.ErrNotFound
	}
	return controlplane.Envelope{
		Key:   "/apisix/" + string(kind) + "/" + id,
		Value: append(json.RawMessage(nil), body...),
	}, nil
}

func (s *usageStore) Put(_ context.Context, kind controlplane.ResourceKind, id string, body json.RawMessage) (controlplane.Envelope, error) {
	group := s.data[kind]
	if group == nil {
		group = map[string]json.RawMessage{}
		s.data[kind] = group
	}
	group[id] = append(json.RawMessage(nil), body...)
	return controlplane.Envelope{
		Key:   "/apisix/" + string(kind) + "/" + id,
		Value: append(json.RawMessage(nil), body...),
	}, nil
}

func (s *usageStore) Delete(_ context.Context, kind controlplane.ResourceKind, id string) (controlplane.DeleteResult, error) {
	if group := s.data[kind]; group != nil {
		delete(group, id)
	}
	return controlplane.DeleteResult{
		Key:     "/apisix/" + string(kind) + "/" + id,
		Deleted: 1,
	}, nil
}

func (s *usageStore) Close() error { return nil }

type usageHistoryStore struct {
	entries []controlplane.HistoryEntry
}

func (s *usageHistoryStore) Save(_ context.Context, entry controlplane.HistoryEntry, limit int) (controlplane.HistoryEntry, error) {
	s.entries = append([]controlplane.HistoryEntry{entry}, s.entries...)
	if limit > 0 && len(s.entries) > limit {
		s.entries = s.entries[:limit]
	}
	return entry, nil
}

func (s *usageHistoryStore) List(_ context.Context, limit int) ([]controlplane.HistoryEntry, error) {
	if limit <= 0 || limit >= len(s.entries) {
		return append([]controlplane.HistoryEntry(nil), s.entries...), nil
	}
	return append([]controlplane.HistoryEntry(nil), s.entries[:limit]...), nil
}

func (s *usageHistoryStore) Get(_ context.Context, id string) (controlplane.HistoryEntry, error) {
	for _, entry := range s.entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return controlplane.HistoryEntry{}, controlplane.ErrNotFound
}

func (s *usageHistoryStore) Close() error { return nil }
