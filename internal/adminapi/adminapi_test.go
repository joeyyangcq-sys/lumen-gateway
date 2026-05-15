package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
		validateResourceResult: controlplane.ValidationResult{Valid: true},
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
		body := string(c.Response.Body())
		if !strings.Contains(body, `"summary":{"title":"/users"`) {
			t.Fatalf("list body missing route summary: %s", body)
		}
	})

	t.Run("list with page page_size keyword", func(t *testing.T) {
		c := newRequestContext("GET", "/apisix/admin/routes?page=2&page_size=1&keyword=orders", nil)
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		body := string(c.Response.Body())
		if !strings.Contains(body, `"total":1`) {
			t.Fatalf("body missing filtered total: %s", body)
		}
		if !strings.Contains(body, `"page":2`) || !strings.Contains(body, `"page_size":1`) {
			t.Fatalf("body missing pagination metadata: %s", body)
		}
		if !strings.Contains(body, `"keyword":"orders"`) {
			t.Fatalf("body missing keyword: %s", body)
		}
		if !strings.Contains(body, `"list":[]`) {
			t.Fatalf("second page should be empty: %s", body)
		}
	})

	t.Run("list with keyword first page", func(t *testing.T) {
		c := newRequestContext("GET", "/apisix/admin/routes?page=1&page_size=1&keyword=orders", nil)
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		body := string(c.Response.Body())
		if !strings.Contains(body, `/orders`) || strings.Contains(body, `/users`) {
			t.Fatalf("filtered page body = %s", body)
		}
		if !strings.Contains(body, `"summary":{"title":"/orders"`) {
			t.Fatalf("filtered page missing summary: %s", body)
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
		if body := string(c.Response.Body()); !strings.Contains(body, `"deleted":"1"`) {
			t.Fatalf("delete body = %s, want deleted as string", body)
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
			if errors.Is(tt.err, controlplane.ErrNotFound) && !strings.Contains(string(c.Response.Body()), `"message":"Key not found"`) {
				t.Fatalf("not found body = %s", c.Response.Body())
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

func TestHandlerControlPreviewApplyExport(t *testing.T) {
	svc := &fakeService{
		validateBundleResult: controlplane.ValidationResult{
			Valid:  false,
			Issues: []controlplane.ValidationIssue{{Resource: "routes", ResourceID: "1", Field: "service_id", Message: "references unknown service \"svc\""}},
		},
		validateResourceResult: controlplane.ValidationResult{
			Valid: true,
		},
		previewResult: controlplane.ApplyPlan{
			Summary: []controlplane.PlanSummary{{Kind: controlplane.KindRoute, Create: 1}},
			Changes: []controlplane.ChangeItem{{
				Kind:    controlplane.KindRoute,
				ID:      "1",
				Action:  controlplane.ChangeCreate,
				Title:   "/demo",
				Summary: map[string]any{"service_id": "svc-1"},
			}},
		},
		applyResult: controlplane.ApplyResult{
			Counts: map[controlplane.ResourceKind]int{controlplane.KindRoute: 1},
		},
		exportResult: controlplane.FileBundle{
			Meta: controlplane.BundleMeta{
				Format:       "lumen.apisix.bundle/v1",
				ManagedKinds: []controlplane.ResourceKind{controlplane.KindRoute},
			},
			Resources: map[controlplane.ResourceKind]map[string]json.RawMessage{
				controlplane.KindRoute: {
					"1": json.RawMessage(`{"id":"1","uri":"/demo"}`),
				},
			},
		},
		historyResult: controlplane.HistoryEntry{
			ID:        "h1",
			CreatedAt: "2026-05-10T00:00:00Z",
			Source:    "control_import_apply",
			Summary: controlplane.HistorySummary{
				Counts: map[controlplane.ResourceKind]int{controlplane.KindRoute: 1},
			},
		},
		historyListResult: []controlplane.HistoryEntry{
			{ID: "h2", CreatedAt: "2026-05-10T01:00:00Z", Source: "history_rollback", RollbackOf: "h1", Summary: controlplane.HistorySummary{Counts: map[controlplane.ResourceKind]int{controlplane.KindRoute: 1}}},
			{ID: "h1", CreatedAt: "2026-05-10T00:00:00Z", Source: "control_import_apply", Summary: controlplane.HistorySummary{Counts: map[controlplane.ResourceKind]int{controlplane.KindRoute: 1}}},
		},
		rollbackResult: controlplane.ApplyResult{
			Counts: map[controlplane.ResourceKind]int{controlplane.KindRoute: 1},
		},
		rollbackHistory: controlplane.HistoryEntry{
			ID:         "h3",
			CreatedAt:  "2026-05-10T02:00:00Z",
			Source:     "history_rollback",
			RollbackOf: "h1",
			Summary: controlplane.HistorySummary{
				Counts: map[controlplane.ResourceKind]int{controlplane.KindRoute: 1},
			},
		},
	}
	handler := NewWithService(svc, "secret")

	t.Run("validate resource", func(t *testing.T) {
		c := newRequestContext("POST", "/apisix/admin/control/validate", []byte(`{"kind":"routes","id":"1","resource":{"uri":"/demo","service_id":"svc"}}`))
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if svc.lastValidateKind != controlplane.KindRoute || svc.lastValidateID != "1" {
			t.Fatalf("validate resource target = (%q,%q), want (routes,1)", svc.lastValidateKind, svc.lastValidateID)
		}
		if body := string(c.Response.Body()); !strings.Contains(body, `"valid":true`) {
			t.Fatalf("validate resource body = %s", body)
		}
	})

	t.Run("validate bundle", func(t *testing.T) {
		c := newRequestContext("POST", "/apisix/admin/control/validate", []byte(`{"bundle":{"routes":{"1":{"uri":"/demo","service_id":"svc"}}},"prune":true,"prune_kinds":["routes"]}`))
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if !svc.lastValidateOptions.Prune || len(svc.lastValidateOptions.PruneKinds) != 1 || svc.lastValidateOptions.PruneKinds[0] != controlplane.KindRoute {
			t.Fatalf("validate bundle options = %#v", svc.lastValidateOptions)
		}
		if body := string(c.Response.Body()); !strings.Contains(body, `"issues"`) {
			t.Fatalf("validate bundle body = %s", body)
		}
	})

	t.Run("preview", func(t *testing.T) {
		c := newRequestContext("POST", "/apisix/admin/control/imports/preview", []byte(`{"bundle":{"routes":{"1":{"uri":"/demo"}}},"prune":true,"prune_kinds":["routes"],"include_unchanged":true}`))
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if !svc.lastPreviewOptions.Prune || !svc.lastPreviewOptions.IncludeUnchanged {
			t.Fatalf("preview options = %#v", svc.lastPreviewOptions)
		}
		if len(svc.lastPreviewOptions.PruneKinds) != 1 || svc.lastPreviewOptions.PruneKinds[0] != controlplane.KindRoute {
			t.Fatalf("preview prune kinds = %#v", svc.lastPreviewOptions.PruneKinds)
		}
		if body := string(c.Response.Body()); !strings.Contains(body, `"title":"/demo"`) || !strings.Contains(body, `"service_id":"svc-1"`) {
			t.Fatalf("preview body = %s, want title and summary", body)
		}
	})

	t.Run("apply", func(t *testing.T) {
		c := newRequestContext("POST", "/apisix/admin/control/imports/apply", []byte(`{"content":"routes:\n  1:\n    uri: /demo\n","prune":true}`))
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if !svc.lastApplyOptions.Prune {
			t.Fatalf("apply options = %#v", svc.lastApplyOptions)
		}
		if body := string(c.Response.Body()); !strings.Contains(body, `"routes":1`) {
			t.Fatalf("apply body = %s", body)
		}
		if !strings.Contains(string(c.Response.Body()), `"history":{"id":"h1"`) {
			t.Fatalf("apply body missing history = %s", c.Response.Body())
		}
		if !strings.Contains(string(c.Response.Body()), `"operation":{"operation_id":"h1"`) {
			t.Fatalf("apply body missing operation = %s", c.Response.Body())
		}
	})

	t.Run("export json", func(t *testing.T) {
		c := newRequestContext("GET", "/apisix/admin/control/exports?kind=routes", nil)
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if len(svc.lastExportOptions.IncludeKinds) != 1 || svc.lastExportOptions.IncludeKinds[0] != controlplane.KindRoute {
			t.Fatalf("export kinds = %#v", svc.lastExportOptions.IncludeKinds)
		}
		if body := string(c.Response.Body()); !strings.Contains(body, `"_meta"`) {
			t.Fatalf("export body = %s", body)
		}
	})

	t.Run("export yaml", func(t *testing.T) {
		c := newRequestContext("GET", "/apisix/admin/control/exports?format=yaml", nil)
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if body := string(c.Response.Body()); !strings.Contains(body, `"content":"_meta:`) {
			t.Fatalf("yaml export body = %s", body)
		}
	})

	t.Run("history list", func(t *testing.T) {
		c := newRequestContext("GET", "/apisix/admin/control/history?limit=2", nil)
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if svc.lastHistoryLimit != 2 {
			t.Fatalf("history limit = %d, want 2", svc.lastHistoryLimit)
		}
		if body := string(c.Response.Body()); !strings.Contains(body, `"total":2`) {
			t.Fatalf("history list body = %s", body)
		}
	})

	t.Run("history rollback", func(t *testing.T) {
		c := newRequestContext("POST", "/apisix/admin/control/history/h1/rollback", nil)
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
		if svc.lastRollbackID != "h1" {
			t.Fatalf("rollback id = %q, want h1", svc.lastRollbackID)
		}
		if body := string(c.Response.Body()); !strings.Contains(body, `"history":{"id":"h3"`) {
			t.Fatalf("history rollback body = %s", body)
		}
		if body := string(c.Response.Body()); !strings.Contains(body, `"rollback_of":"h1"`) || !strings.Contains(body, `"operation":{"operation_id":"h3"`) {
			t.Fatalf("history rollback operation body = %s", body)
		}
	})

	t.Run("validate bad request uses control error model", func(t *testing.T) {
		c := newRequestContext("POST", "/apisix/admin/control/validate", []byte(`{"kind":"unknown","resource":{}}`))
		c.Request.Header.Set("X-API-KEY", "secret")
		handler.ServeHTTP(context.Background(), c)
		if got := c.Response.StatusCode(); got != 400 {
			t.Fatalf("status = %d, want 400", got)
		}
		if body := string(c.Response.Body()); !strings.Contains(body, `"code":"invalid_request"`) || !strings.Contains(body, `"message":"unsupported resource kind"`) {
			t.Fatalf("validate bad request body = %s", body)
		}
	})
}

func TestHandlerControlSchema(t *testing.T) {
	handler := NewWithService(&fakeService{}, "secret")
	c := newRequestContext("GET", "/apisix/admin/control/schema", nil)
	c.Request.Header.Set("X-API-KEY", "secret")

	if handled := handler.ServeHTTP(context.Background(), c); !handled {
		t.Fatalf("ServeHTTP() handled = false, want true")
	}
	if got := c.Response.StatusCode(); got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}

	body := string(c.Response.Body())
	for _, needle := range []string{
		`"resources"`,
		`"plugins"`,
		`"capabilities"`,
		`"kind":"routes"`,
		`"name":"proxy-rewrite"`,
		`"name":"request-id"`,
		`"bundle_formats":["json","yaml"]`,
		`"validate":true`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("schema body missing %s: %s", needle, body)
		}
	}
}

type fakeService struct {
	listResult             []controlplane.Envelope
	listErr                error
	getResult              controlplane.Envelope
	getErr                 error
	putResult              controlplane.Envelope
	putErr                 error
	postResult             controlplane.Envelope
	postErr                error
	patchResult            controlplane.Envelope
	patchErr               error
	deleteResult           controlplane.DeleteResult
	deleteErr              error
	validateBundleResult   controlplane.ValidationResult
	validateBundleErr      error
	validateResourceResult controlplane.ValidationResult
	validateResourceErr    error
	previewResult          controlplane.ApplyPlan
	previewErr             error
	applyResult            controlplane.ApplyResult
	applyErr               error
	exportResult           controlplane.FileBundle
	exportErr              error
	historyResult          controlplane.HistoryEntry
	historyErr             error
	historyListResult      []controlplane.HistoryEntry
	historyListErr         error
	rollbackResult         controlplane.ApplyResult
	rollbackHistory        controlplane.HistoryEntry
	rollbackErr            error

	lastKind            controlplane.ResourceKind
	lastID              string
	lastBody            json.RawMessage
	lastValidateKind    controlplane.ResourceKind
	lastValidateID      string
	lastValidateBody    json.RawMessage
	lastValidateOptions controlplane.ValidateOptions
	lastPreviewOptions  controlplane.PreviewOptions
	lastApplyOptions    controlplane.ApplyOptions
	lastExportOptions   controlplane.ExportOptions
	lastHistorySource   string
	lastHistoryLimit    int
	lastRollbackID      string
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

func (s *fakeService) ValidateBundle(_ context.Context, bundle controlplane.FileBundle, options controlplane.ValidateOptions) (controlplane.ValidationResult, error) {
	s.lastValidateOptions = options
	return s.validateBundleResult, s.validateBundleErr
}

func (s *fakeService) ValidateResource(_ context.Context, kind controlplane.ResourceKind, id string, body json.RawMessage) (controlplane.ValidationResult, error) {
	s.lastValidateKind = kind
	s.lastValidateID = id
	s.lastValidateBody = body
	return s.validateResourceResult, s.validateResourceErr
}

func (s *fakeService) PreviewBundle(_ context.Context, bundle controlplane.FileBundle, options controlplane.PreviewOptions) (controlplane.ApplyPlan, error) {
	s.lastPreviewOptions = options
	return s.previewResult, s.previewErr
}

func (s *fakeService) ApplyBundle(_ context.Context, bundle controlplane.FileBundle, options controlplane.ApplyOptions) (controlplane.ApplyResult, error) {
	s.lastApplyOptions = options
	return s.applyResult, s.applyErr
}

func (s *fakeService) ExportBundle(_ context.Context, options controlplane.ExportOptions) (controlplane.FileBundle, error) {
	s.lastExportOptions = options
	return s.exportResult, s.exportErr
}

func (s *fakeService) SaveHistorySnapshot(_ context.Context, source string) (controlplane.HistoryEntry, error) {
	s.lastHistorySource = source
	return s.historyResult, s.historyErr
}

func (s *fakeService) ListHistory(_ context.Context, limit int) ([]controlplane.HistoryEntry, error) {
	s.lastHistoryLimit = limit
	return s.historyListResult, s.historyListErr
}

func (s *fakeService) RollbackHistory(_ context.Context, id string) (controlplane.ApplyResult, controlplane.HistoryEntry, error) {
	s.lastRollbackID = id
	return s.rollbackResult, s.rollbackHistory, s.rollbackErr
}

func (s *fakeService) Close() error { return nil }

func newRequestContext(method, path string, body []byte) *app.RequestContext {
	c := app.NewContext(0)
	c.Request.SetMethod(method)
	c.Request.SetRequestURI(path)
	if body != nil {
		c.Request.SetBodyRaw(body)
	}
	return c
}
