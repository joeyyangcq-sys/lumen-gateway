package controlplane

import (
	"context"
	"encoding/json"
	"testing"
)

func TestServiceHistorySnapshotAndRollback(t *testing.T) {
	store := &historyAwareStore{
		exportStore: exportStore{
			listResults: map[ResourceKind][]Envelope{
				KindRoute: {
					{Key: "/apisix/routes/1", Value: json.RawMessage(`{"id":"1","uri":"/demo","service_id":"svc-1"}`)},
				},
			},
		},
	}
	history := &fakeHistoryStore{
		getResult: HistoryEntry{
			ID: "h1",
			Bundle: FileBundle{Meta: BundleMeta{ManagedKinds: []ResourceKind{KindRoute}}, Resources: map[ResourceKind]map[string]json.RawMessage{
				KindRoute: {"1": json.RawMessage(`{"id":"1","uri":"/rolled","service_id":"svc-1"}`)},
			}},
		},
	}
	svc := New(store, WithHistory(history, 10, ExportOptions{EtcdPrefix: "/apisix"}))

	entry, err := svc.SaveHistorySnapshot(context.Background(), "control_import_apply")
	if err != nil {
		t.Fatalf("SaveHistorySnapshot() error = %v", err)
	}
	if entry.ID == "" || history.lastSaved.Source != "control_import_apply" {
		t.Fatalf("saved history = %#v", history.lastSaved)
	}
	if got := history.lastSaved.Summary.Counts[KindRoute]; got != 1 {
		t.Fatalf("saved history summary = %#v, want route count 1", history.lastSaved.Summary)
	}
	if history.lastSaved.Bundle.Meta.EtcdPrefix != "/apisix" {
		t.Fatalf("saved bundle meta = %#v", history.lastSaved.Bundle.Meta)
	}

	result, rolledEntry, err := svc.RollbackHistory(context.Background(), "h1")
	if err != nil {
		t.Fatalf("RollbackHistory() error = %v", err)
	}
	if rolledEntry.ID == "" || rolledEntry.ID == "h1" {
		t.Fatalf("rolled entry = %#v", rolledEntry)
	}
	if rolledEntry.Source != "history_rollback" || rolledEntry.RollbackOf != "h1" {
		t.Fatalf("rolled entry metadata = %#v", rolledEntry)
	}
	if result.Counts[KindRoute] != 1 {
		t.Fatalf("rollback result = %#v", result)
	}
	if len(store.putBodies) != 1 || string(store.putBodies["routes/1"]) == "" {
		t.Fatalf("put bodies = %#v", store.putBodies)
	}
	if len(history.saved) < 2 {
		t.Fatalf("history saves = %#v, want snapshot + rollback snapshot", history.saved)
	}
}

type fakeHistoryStore struct {
	saved      []HistoryEntry
	lastSaved  HistoryEntry
	listResult []HistoryEntry
	getResult  HistoryEntry
}

func (s *fakeHistoryStore) Save(_ context.Context, entry HistoryEntry, limit int) (HistoryEntry, error) {
	s.lastSaved = entry
	s.saved = append(s.saved, entry)
	return entry, nil
}

func (s *fakeHistoryStore) List(_ context.Context, limit int) ([]HistoryEntry, error) {
	return s.listResult, nil
}

func (s *fakeHistoryStore) Get(_ context.Context, id string) (HistoryEntry, error) {
	if s.getResult.ID == "" {
		return HistoryEntry{}, ErrNotFound
	}
	return s.getResult, nil
}

func (s *fakeHistoryStore) Close() error { return nil }

type historyAwareStore struct {
	exportStore
	putBodies map[string]json.RawMessage
}

func (s *historyAwareStore) Put(_ context.Context, kind ResourceKind, id string, body json.RawMessage) (Envelope, error) {
	if s.putBodies == nil {
		s.putBodies = map[string]json.RawMessage{}
	}
	s.putBodies[string(kind)+"/"+id] = body
	return Envelope{Key: "/apisix/" + string(kind) + "/" + id, Value: body}, nil
}

func (s *historyAwareStore) Get(_ context.Context, kind ResourceKind, id string) (Envelope, error) {
	return Envelope{}, ErrNotFound
}

func (s *historyAwareStore) Delete(_ context.Context, kind ResourceKind, id string) (DeleteResult, error) {
	return DeleteResult{Key: "/apisix/" + string(kind) + "/" + id, Deleted: 1}, nil
}

func (s *historyAwareStore) Close() error { return nil }
