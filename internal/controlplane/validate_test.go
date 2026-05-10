package controlplane

import (
	"context"
	"encoding/json"
	"testing"
)

func TestValidateResourceUsesCurrentSnapshot(t *testing.T) {
	store := &validationStore{
		data: map[ResourceKind]map[string]json.RawMessage{
			KindService: {
				"svc-1": json.RawMessage(`{"id":"svc-1","upstream_id":"up-1"}`),
			},
			KindUpstream: {
				"up-1": json.RawMessage(`{"id":"up-1","nodes":{"127.0.0.1:9081":1}}`),
			},
		},
	}
	svc := New(store)

	result, err := svc.ValidateResource(context.Background(), KindRoute, "route-1", json.RawMessage(`{"uri":"/demo","service_id":"svc-1"}`))
	if err != nil {
		t.Fatalf("ValidateResource() error = %v", err)
	}
	if !result.Valid {
		t.Fatalf("result.Valid = false, want true: %#v", result)
	}
}

func TestValidateBundleReturnsStructuredIssue(t *testing.T) {
	svc := New(&validationStore{})
	bundle, err := ParseBundle([]byte(`
routes:
  route-1:
    id: route-1
    uri: /demo
    service_id: missing-service
`))
	if err != nil {
		t.Fatalf("ParseBundle() error = %v", err)
	}

	result, err := svc.ValidateBundle(context.Background(), bundle, ValidateOptions{})
	if err != nil {
		t.Fatalf("ValidateBundle() error = %v", err)
	}
	if result.Valid {
		t.Fatalf("result.Valid = true, want false")
	}
	if len(result.Issues) != 1 {
		t.Fatalf("issues = %#v, want one issue", result.Issues)
	}
	issue := result.Issues[0]
	if issue.Resource != "routes" {
		t.Fatalf("issue.Resource = %q, want routes", issue.Resource)
	}
	if issue.ResourceID != "route-1" {
		t.Fatalf("issue.ResourceID = %q, want route-1", issue.ResourceID)
	}
	if issue.Field != "service_id" {
		t.Fatalf("issue.Field = %q, want service_id", issue.Field)
	}
}

type validationStore struct {
	data map[ResourceKind]map[string]json.RawMessage
}

func (s *validationStore) List(_ context.Context, kind ResourceKind) ([]Envelope, error) {
	group := s.data[kind]
	out := make([]Envelope, 0, len(group))
	for id, body := range group {
		out = append(out, Envelope{
			Key:   "/apisix/" + string(kind) + "/" + id,
			Value: append(json.RawMessage(nil), body...),
		})
	}
	return out, nil
}

func (s *validationStore) Get(_ context.Context, kind ResourceKind, id string) (Envelope, error) {
	group := s.data[kind]
	if group == nil {
		return Envelope{}, ErrNotFound
	}
	body, ok := group[id]
	if !ok {
		return Envelope{}, ErrNotFound
	}
	return Envelope{
		Key:   "/apisix/" + string(kind) + "/" + id,
		Value: append(json.RawMessage(nil), body...),
	}, nil
}

func (s *validationStore) Put(_ context.Context, kind ResourceKind, id string, body json.RawMessage) (Envelope, error) {
	if s.data == nil {
		s.data = map[ResourceKind]map[string]json.RawMessage{}
	}
	group := s.data[kind]
	if group == nil {
		group = map[string]json.RawMessage{}
		s.data[kind] = group
	}
	group[id] = append(json.RawMessage(nil), body...)
	return Envelope{Key: "/apisix/" + string(kind) + "/" + id, Value: body}, nil
}

func (s *validationStore) Delete(_ context.Context, kind ResourceKind, id string) (DeleteResult, error) {
	if group := s.data[kind]; group != nil {
		delete(group, id)
	}
	return DeleteResult{Key: "/apisix/" + string(kind) + "/" + id, Deleted: 1}, nil
}

func (s *validationStore) Close() error { return nil }
