package controlplane

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseBundleSupportsMapsAndArrays(t *testing.T) {
	bundle, err := ParseBundle([]byte(`
_meta:
  format: lumen.apisix.bundle/v1
  managed_kinds:
    - routes
    - services
upstreams:
  up-1:
    scheme: http
    nodes:
      "127.0.0.1:9001": 1
services:
  - id: svc-1
    upstream_id: up-1
routes:
  route-1:
    uri: /demo
    service_id: svc-1
`))
	if err != nil {
		t.Fatalf("ParseBundle() error = %v", err)
	}
	if got := string(bundle.Resources[KindUpstream]["up-1"]); got == "" {
		t.Fatal("upstream resource missing")
	}
	if got := string(bundle.Resources[KindService]["svc-1"]); got == "" {
		t.Fatal("service resource missing")
	}
	if got := string(bundle.Resources[KindRoute]["route-1"]); got == "" {
		t.Fatal("route resource missing")
	}
	if bundle.Meta.Format != "lumen.apisix.bundle/v1" {
		t.Fatalf("bundle meta format = %q", bundle.Meta.Format)
	}
	if len(bundle.Meta.ManagedKinds) != 2 || bundle.Meta.ManagedKinds[0] != KindRoute || bundle.Meta.ManagedKinds[1] != KindService {
		t.Fatalf("bundle managed kinds = %#v", bundle.Meta.ManagedKinds)
	}
}

func TestApplyBundleUsesDependencyOrder(t *testing.T) {
	store := &recordingStore{}
	svc := New(store)
	bundle := FileBundle{
		Resources: map[ResourceKind]map[string]json.RawMessage{
			KindRoute:    {"route-1": json.RawMessage(`{"id":"route-1","service_id":"svc-1","uri":"/demo"}`)},
			KindService:  {"svc-1": json.RawMessage(`{"id":"svc-1","upstream_id":"up-1"}`)},
			KindUpstream: {"up-1": json.RawMessage(`{"id":"up-1","nodes":{"127.0.0.1:9001":1}}`)},
		},
	}

	result, err := ApplyBundle(context.Background(), svc, bundle)
	if err != nil {
		t.Fatalf("ApplyBundle() error = %v", err)
	}
	if got := store.order; len(got) != 3 || got[0] != "upstreams:up-1" || got[1] != "services:svc-1" || got[2] != "routes:route-1" {
		t.Fatalf("apply order = %#v", got)
	}
	if result.Counts[KindRoute] != 1 || result.Counts[KindService] != 1 || result.Counts[KindUpstream] != 1 {
		t.Fatalf("counts = %#v", result.Counts)
	}
}

func TestApplyBundleWithPruneDeletesMissingResources(t *testing.T) {
	store := &pruneStore{
		recordingStore: recordingStore{},
		exportStore: exportStore{
			listResults: map[ResourceKind][]Envelope{
				KindRoute: {
					{Key: "/apisix/routes/keep", Value: json.RawMessage(`{"id":"keep","uri":"/keep","service_id":"svc-1"}`)},
					{Key: "/apisix/routes/drop", Value: json.RawMessage(`{"id":"drop","uri":"/drop","service_id":"svc-1"}`)},
				},
			},
		},
	}
	svc := New(store)
	bundle := FileBundle{
		Resources: map[ResourceKind]map[string]json.RawMessage{
			KindRoute: {
				"keep": json.RawMessage(`{"id":"keep","uri":"/keep","service_id":"svc-1"}`),
			},
		},
	}

	_, err := ApplyBundleWithOptions(context.Background(), svc, bundle, ApplyOptions{Prune: true})
	if err != nil {
		t.Fatalf("ApplyBundleWithOptions() error = %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "routes:drop" {
		t.Fatalf("deleted = %#v, want routes:drop", store.deleted)
	}
}

func TestApplyBundleWithPruneOnlyTouchesSelectedKinds(t *testing.T) {
	store := &pruneStore{
		recordingStore: recordingStore{},
		exportStore: exportStore{
			listResults: map[ResourceKind][]Envelope{
				KindRoute: {
					{Key: "/apisix/routes/drop-route", Value: json.RawMessage(`{"id":"drop-route","uri":"/drop","service_id":"svc-1"}`)},
				},
				KindService: {
					{Key: "/apisix/services/drop-service", Value: json.RawMessage(`{"id":"drop-service","upstream_id":"up-1"}`)},
				},
			},
		},
	}
	svc := New(store)
	bundle := FileBundle{
		Resources: map[ResourceKind]map[string]json.RawMessage{
			KindRoute: {
				"keep-route": json.RawMessage(`{"id":"keep-route","uri":"/keep","service_id":"svc-1"}`),
			},
		},
	}

	_, err := ApplyBundleWithOptions(context.Background(), svc, bundle, ApplyOptions{
		Prune:      true,
		PruneKinds: []ResourceKind{KindRoute},
	})
	if err != nil {
		t.Fatalf("ApplyBundleWithOptions() error = %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "routes:drop-route" {
		t.Fatalf("deleted = %#v, want only routes:drop-route", store.deleted)
	}
}

func TestApplyBundleWithPruneUsesBundleManagedKinds(t *testing.T) {
	store := &pruneStore{
		recordingStore: recordingStore{},
		exportStore: exportStore{
			listResults: map[ResourceKind][]Envelope{
				KindRoute: {
					{Key: "/apisix/routes/drop-route", Value: json.RawMessage(`{"id":"drop-route","uri":"/drop","service_id":"svc-1"}`)},
				},
				KindService: {
					{Key: "/apisix/services/drop-service", Value: json.RawMessage(`{"id":"drop-service","upstream_id":"up-1"}`)},
				},
			},
		},
	}
	svc := New(store)
	bundle := FileBundle{
		Meta: BundleMeta{ManagedKinds: []ResourceKind{KindRoute}},
		Resources: map[ResourceKind]map[string]json.RawMessage{
			KindRoute: {
				"keep-route": json.RawMessage(`{"id":"keep-route","uri":"/keep","service_id":"svc-1"}`),
			},
		},
	}

	_, err := ApplyBundleWithOptions(context.Background(), svc, bundle, ApplyOptions{Prune: true})
	if err != nil {
		t.Fatalf("ApplyBundleWithOptions() error = %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "routes:drop-route" {
		t.Fatalf("deleted = %#v, want only routes:drop-route", store.deleted)
	}
}

func TestExportBundleAndWriteFile(t *testing.T) {
	svc := New(&exportStore{
		listResults: map[ResourceKind][]Envelope{
			KindRoute: {
				{Key: "/apisix/routes/1", Value: json.RawMessage(`{"id":"1","uri":"/demo","service_id":"svc-1"}`)},
			},
		},
	})

	bundle, err := ExportBundleWithOptions(context.Background(), svc, ExportOptions{
		EtcdPrefix:  "/apisix",
		IncludeMeta: true,
	})
	if err != nil {
		t.Fatalf("ExportBundle() error = %v", err)
	}
	if _, ok := bundle.Resources[KindRoute]["1"]; !ok {
		t.Fatalf("exported bundle missing route 1")
	}
	if bundle.Meta.Format == "" || bundle.Meta.EtcdPrefix != "/apisix" {
		t.Fatalf("bundle meta = %#v", bundle.Meta)
	}
	if len(bundle.Meta.ManagedKinds) != len(SupportedKinds()) {
		t.Fatalf("managed kinds = %#v", bundle.Meta.ManagedKinds)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.yaml")
	if err := WriteBundleFile(path, bundle); err != nil {
		t.Fatalf("WriteBundleFile() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("written bundle is empty")
	}
	if !strings.Contains(string(data), "_meta:") || !strings.Contains(string(data), "managed_kinds:") {
		t.Fatalf("written bundle missing metadata:\n%s", data)
	}
}

func TestSyncBundleFileReappliesOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.yaml")
	if err := os.WriteFile(path, []byte(`
upstreams:
  up-1:
    nodes:
      "127.0.0.1:9001": 1
services:
  svc-1:
    upstream_id: up-1
routes:
  route-1:
    uri: /demo
    service_id: svc-1
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	store := &recordingStore{}
	svc := New(store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	applies := make(chan ApplyResult, 2)
	applyCount := 0
	go func() {
		done <- SyncBundleFile(ctx, svc, path, SyncOptions{
			PollInterval: 50 * time.Millisecond,
			OnApply: func(result ApplyResult) {
				applyCount++
				applies <- result
				if applyCount == 2 {
					cancel()
				}
			},
		})
	}()

	select {
	case <-applies:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial apply")
	}

	time.Sleep(60 * time.Millisecond)
	if err := os.WriteFile(path, []byte(`
upstreams:
  up-1:
    nodes:
      "127.0.0.1:9001": 1
services:
  svc-1:
    upstream_id: up-1
routes:
  route-1:
    uri: /demo-v2
    service_id: svc-1
`), 0o644); err != nil {
		t.Fatalf("WriteFile() update error = %v", err)
	}

	select {
	case <-applies:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for reapply")
	}

	if err := <-done; err != context.Canceled {
		t.Fatalf("SyncBundleFile() error = %v, want context.Canceled", err)
	}
}

type recordingStore struct {
	fakeStore
	order []string
}

func (s *recordingStore) Put(ctx context.Context, kind ResourceKind, id string, body json.RawMessage) (Envelope, error) {
	s.order = append(s.order, string(kind)+":"+id)
	return s.fakeStore.Put(ctx, kind, id, body)
}

type exportStore struct {
	fakeStore
	listResults map[ResourceKind][]Envelope
}

func (s *exportStore) List(_ context.Context, kind ResourceKind) ([]Envelope, error) {
	return s.listResults[kind], nil
}

type pruneStore struct {
	recordingStore
	exportStore
	deleted []string
}

func (s *pruneStore) Put(ctx context.Context, kind ResourceKind, id string, body json.RawMessage) (Envelope, error) {
	return s.recordingStore.Put(ctx, kind, id, body)
}

func (s *pruneStore) List(ctx context.Context, kind ResourceKind) ([]Envelope, error) {
	return s.exportStore.List(ctx, kind)
}

func (s *pruneStore) Get(ctx context.Context, kind ResourceKind, id string) (Envelope, error) {
	return s.recordingStore.Get(ctx, kind, id)
}

func (s *pruneStore) Delete(_ context.Context, kind ResourceKind, id string) (DeleteResult, error) {
	s.deleted = append(s.deleted, string(kind)+":"+id)
	return DeleteResult{Key: "/apisix/" + string(kind) + "/" + id, Deleted: 1}, nil
}

func (s *pruneStore) Close() error {
	return nil
}
