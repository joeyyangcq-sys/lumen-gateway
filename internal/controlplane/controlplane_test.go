package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestServicePutNormalizesBodyAndDelegates(t *testing.T) {
	store := &fakeStore{}
	service := New(store)

	env, err := service.Put(context.Background(), KindRoute, "9", json.RawMessage(`{"uri":"/demo"}`))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if store.lastKind != KindRoute || store.lastID != "9" {
		t.Fatalf("store kind/id = %q/%q", store.lastKind, store.lastID)
	}
	if env.Key != "/apisix/routes/9" {
		t.Fatalf("env key = %q, want /apisix/routes/9", env.Key)
	}
	assertJSONContains(t, store.lastBody, map[string]any{"id": "9", "uri": "/demo"})
	assertHasNumericField(t, store.lastBody, "create_time")
	assertHasNumericField(t, store.lastBody, "update_time")
}

func TestServicePostUsesProvidedIDOrGeneratesOne(t *testing.T) {
	t.Run("provided id", func(t *testing.T) {
		store := &fakeStore{}
		service := New(store)

		env, err := service.Post(context.Background(), KindRoute, json.RawMessage(`{"id":"provided","uri":"/demo"}`))
		if err != nil {
			t.Fatalf("Post() error = %v", err)
		}
		if store.lastID != "provided" {
			t.Fatalf("last id = %q, want provided", store.lastID)
		}
		if env.Key != "/apisix/routes/provided" {
			t.Fatalf("env key = %q, want /apisix/routes/provided", env.Key)
		}
	})

	t.Run("generated id", func(t *testing.T) {
		store := &fakeStore{}
		service := New(store)

		_, err := service.Post(context.Background(), KindRoute, json.RawMessage(`{"uri":"/demo"}`))
		if err != nil {
			t.Fatalf("Post() error = %v", err)
		}
		if store.lastID == "" {
			t.Fatal("last id is empty, want generated id")
		}
	})
}

func TestServicePatchMergesJSON(t *testing.T) {
	store := &fakeStore{
		getResult: Envelope{
			Key:   "/apisix/routes/1",
			Value: json.RawMessage(`{"id":"1","uri":"/demo","create_time":100,"plugins":{"request-id":{"header_name":"X-Request-Id"},"limit-count":{"count":10}}}`),
		},
	}
	service := New(store)

	env, err := service.Patch(context.Background(), KindRoute, "1", json.RawMessage(`{"uri":"/demo-v2","plugins":{"limit-count":{"count":20},"request-id":null}}`))
	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	if env.Key != "/apisix/routes/1" {
		t.Fatalf("env key = %q, want /apisix/routes/1", env.Key)
	}
	assertJSONContains(t, store.lastBody, map[string]any{
		"id":          "1",
		"uri":         "/demo-v2",
		"create_time": float64(100),
	})
}

func TestServicePutPreservesCreateTimeOnUpdate(t *testing.T) {
	store := &fakeStore{
		getResult: Envelope{
			Key:   "/apisix/routes/1",
			Value: json.RawMessage(`{"id":"1","uri":"/old","create_time":123,"update_time":123}`),
		},
	}
	service := New(store)

	if _, err := service.Put(context.Background(), KindRoute, "1", json.RawMessage(`{"uri":"/new"}`)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	assertJSONContains(t, store.lastBody, map[string]any{
		"id":          "1",
		"uri":         "/new",
		"create_time": float64(123),
	})
	assertHasNumericField(t, store.lastBody, "update_time")
}

func TestServiceRejectsUnsupportedKind(t *testing.T) {
	service := New(&fakeStore{})
	_, err := service.List(context.Background(), ResourceKind("unknown"))
	if !errors.Is(err, ErrUnsupportedKind) {
		t.Fatalf("List() error = %v, want ErrUnsupportedKind", err)
	}
}

func TestNormalizeResourceBodyInjectsID(t *testing.T) {
	got, err := NormalizeResourceBody([]byte(`{"plugins":{"a":{}}}`), "42")
	if err != nil {
		t.Fatalf("NormalizeResourceBody() error = %v", err)
	}
	if string(got) != `{"id":"42","plugins":{"a":{}}}` {
		t.Fatalf("normalized body = %s", got)
	}
}

type fakeStore struct {
	lastKind  ResourceKind
	lastID    string
	lastBody  json.RawMessage
	getResult Envelope
	getErr    error
}

func (s *fakeStore) List(context.Context, ResourceKind) ([]Envelope, error) {
	return nil, nil
}

func (s *fakeStore) Get(context.Context, ResourceKind, string) (Envelope, error) {
	if s.getErr == nil && s.getResult.Key == "" && len(s.getResult.Value) == 0 {
		return Envelope{}, ErrNotFound
	}
	return s.getResult, s.getErr
}

func (s *fakeStore) Put(_ context.Context, kind ResourceKind, id string, body json.RawMessage) (Envelope, error) {
	s.lastKind = kind
	s.lastID = id
	s.lastBody = body
	return Envelope{
		Key:   "/apisix/" + string(kind) + "/" + id,
		Value: body,
	}, nil
}

func (s *fakeStore) Delete(context.Context, ResourceKind, string) (DeleteResult, error) {
	return DeleteResult{}, nil
}

func (s *fakeStore) Close() error {
	return nil
}

func assertJSONContains(t *testing.T, raw json.RawMessage, expected map[string]any) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	for key, want := range expected {
		if got := decoded[key]; got != want {
			t.Fatalf("field %q = %#v, want %#v", key, got, want)
		}
	}
}

func assertHasNumericField(t *testing.T, raw json.RawMessage, field string) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	value, ok := decoded[field].(float64)
	if !ok || value <= 0 {
		t.Fatalf("field %q = %#v, want positive number", field, decoded[field])
	}
}
