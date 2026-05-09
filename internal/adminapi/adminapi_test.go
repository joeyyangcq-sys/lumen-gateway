package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/joey/lumen-gateway/internal/controlplane"
)

func TestHandlerListGetPutDelete(t *testing.T) {
	svc := &fakeService{
		listResult: []controlplane.Envelope{
			{Key: "/apisix/routes/1", Value: json.RawMessage(`{"id":"1","uri":"/users"}`)},
			{Key: "/apisix/routes/2", Value: json.RawMessage(`{"id":"2","uri":"/orders"}`)},
		},
		getResult: controlplane.Envelope{
			Key:   "/apisix/routes/1",
			Value: json.RawMessage(`{"id":"1","uri":"/users"}`),
		},
		putResult: controlplane.Envelope{
			Key:   "/apisix/routes/9",
			Value: json.RawMessage(`{"id":"9","uri":"/v9"}`),
		},
		postResult: controlplane.Envelope{
			Key:   "/apisix/routes/10",
			Value: json.RawMessage(`{"id":"10","uri":"/v10"}`),
		},
		patchResult: controlplane.Envelope{
			Key:   "/apisix/routes/1",
			Value: json.RawMessage(`{"id":"1","uri":"/users-v2"}`),
		},
		deleteResult: controlplane.DeleteResult{
			Key:     "/apisix/routes/9",
			Deleted: 1,
		},
	}
	handler := NewWithService(svc, "secret")

	t.Run("list", func(t *testing.T) {
		c := newRequestContext("GET", "/apisix/admin/routes", nil)
		c.Request.Header.Set("X-API-KEY", "secret")

		if handled := handler.ServeHTTP(context.Background(), c); !handled {
			t.Fatalf("ServeHTTP() handled = false, want true")
		}
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if svc.lastKind != controlplane.KindRoute {
			t.Fatalf("last kind = %q, want routes", svc.lastKind)
		}
	})

	t.Run("get", func(t *testing.T) {
		c := newRequestContext("GET", "/apisix/admin/routes/1", nil)
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if svc.lastID != "1" {
			t.Fatalf("last id = %q, want 1", svc.lastID)
		}
	})

	t.Run("put", func(t *testing.T) {
		svc.getResult = controlplane.Envelope{}
		svc.getErr = controlplane.ErrNotFound
		c := newRequestContext("PUT", "/apisix/admin/routes/9", []byte(`{"uri":"/v9"}`))
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 201 {
			t.Fatalf("status = %d, want 201", got)
		}
		if string(svc.lastBody) != `{"uri":"/v9"}` {
			t.Fatalf("last body = %s", svc.lastBody)
		}
	})

	t.Run("put update", func(t *testing.T) {
		svc.getResult = controlplane.Envelope{Key: "/apisix/routes/9", Value: json.RawMessage(`{"id":"9","uri":"/old"}`)}
		svc.getErr = nil
		c := newRequestContext("PUT", "/apisix/admin/routes/9", []byte(`{"uri":"/v9"}`))
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
	})

	t.Run("post", func(t *testing.T) {
		c := newRequestContext("POST", "/apisix/admin/routes", []byte(`{"uri":"/v10"}`))
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 201 {
			t.Fatalf("status = %d, want 201", got)
		}
		if string(svc.lastBody) != `{"uri":"/v10"}` {
			t.Fatalf("last body = %s", svc.lastBody)
		}
	})

	t.Run("patch", func(t *testing.T) {
		c := newRequestContext("PATCH", "/apisix/admin/routes/1", []byte(`{"uri":"/users-v2"}`))
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if string(svc.lastBody) != `{"uri":"/users-v2"}` {
			t.Fatalf("last body = %s", svc.lastBody)
		}
	})

	t.Run("delete", func(t *testing.T) {
		c := newRequestContext("DELETE", "/apisix/admin/routes/9", nil)
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if svc.lastID != "9" {
			t.Fatalf("last id = %q, want 9", svc.lastID)
		}
	})
}

func TestHandlerMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "not found", err: controlplane.ErrNotFound, wantStatus: 404},
		{name: "invalid body", err: controlplane.ErrInvalidBody, wantStatus: 400},
		{name: "unsupported kind", err: controlplane.ErrUnsupportedKind, wantStatus: 404},
		{name: "default", err: errors.New("boom"), wantStatus: 502},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewWithService(&fakeService{listErr: tt.err}, "secret")
			c := newRequestContext("GET", "/apisix/admin/routes", nil)
			c.Request.Header.Set("X-API-KEY", "secret")
			handler.ServeHTTP(context.Background(), c)
			if got := c.Response.StatusCode(); got != tt.wantStatus {
				t.Fatalf("status = %d, want %d", got, tt.wantStatus)
			}
		})
	}
}

func TestHandlerRejectsMissingKey(t *testing.T) {
	handler := NewWithService(&fakeService{}, "secret")
	c := newRequestContext("GET", "/apisix/admin/routes", nil)

	if handled := handler.ServeHTTP(context.Background(), c); !handled {
		t.Fatalf("ServeHTTP() handled = false, want true")
	}
	if got := c.Response.StatusCode(); got != 401 {
		t.Fatalf("status = %d, want 401", got)
	}
}

func TestHandlerIgnoresNonAdminPaths(t *testing.T) {
	handler := NewWithService(&fakeService{}, "secret")
	c := newRequestContext("GET", "/metrics", nil)
	if handled := handler.ServeHTTP(context.Background(), c); handled {
		t.Fatalf("ServeHTTP() handled = true, want false")
	}
}

type fakeService struct {
	listResult   []controlplane.Envelope
	listErr      error
	getResult    controlplane.Envelope
	getErr       error
	putResult    controlplane.Envelope
	putErr       error
	postResult   controlplane.Envelope
	postErr      error
	patchResult  controlplane.Envelope
	patchErr     error
	deleteResult controlplane.DeleteResult
	deleteErr    error

	lastKind controlplane.ResourceKind
	lastID   string
	lastBody json.RawMessage
}

func (s *fakeService) List(_ context.Context, kind controlplane.ResourceKind) ([]controlplane.Envelope, error) {
	s.lastKind = kind
	return s.listResult, s.listErr
}

func (s *fakeService) Get(_ context.Context, kind controlplane.ResourceKind, id string) (controlplane.Envelope, error) {
	s.lastKind = kind
	s.lastID = id
	return s.getResult, s.getErr
}

func (s *fakeService) Put(_ context.Context, kind controlplane.ResourceKind, id string, body json.RawMessage) (controlplane.Envelope, error) {
	s.lastKind = kind
	s.lastID = id
	s.lastBody = body
	return s.putResult, s.putErr
}

func (s *fakeService) Post(_ context.Context, kind controlplane.ResourceKind, body json.RawMessage) (controlplane.Envelope, error) {
	s.lastKind = kind
	s.lastID = ""
	s.lastBody = body
	return s.postResult, s.postErr
}

func (s *fakeService) Patch(_ context.Context, kind controlplane.ResourceKind, id string, body json.RawMessage) (controlplane.Envelope, error) {
	s.lastKind = kind
	s.lastID = id
	s.lastBody = body
	return s.patchResult, s.patchErr
}

func (s *fakeService) Delete(_ context.Context, kind controlplane.ResourceKind, id string) (controlplane.DeleteResult, error) {
	s.lastKind = kind
	s.lastID = id
	return s.deleteResult, s.deleteErr
}

func (s *fakeService) Close() error { return nil }

func newRequestContext(method, path string, body []byte) *app.RequestContext {
	c := app.NewContext(0)
	c.Request.SetMethod(method)
	c.Request.URI().SetPath(path)
	if body != nil {
		c.Request.SetBodyRaw(body)
	}
	return c
}
