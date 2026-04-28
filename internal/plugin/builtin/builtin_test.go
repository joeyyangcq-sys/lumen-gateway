package builtin

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/plugin"
)

func TestRequestTransformerMutatesHostHeadersAndQuery(t *testing.T) {
	registry := newRegistry(t)
	handler, err := registry.Factory("request_transformer")(map[string]any{
		"host": "users.internal",
		"add": map[string]any{
			"headers": map[string]string{"X-Added": "1", "X-Keep": "new"},
			"query":   map[string]string{"added": "yes", "keep": "new"},
		},
		"set": map[string]any{
			"headers": map[string]string{"X-Set": "2"},
			"query":   map[string]string{"set": "ok"},
		},
		"remove": map[string]any{
			"headers": []string{"X-Remove"},
			"query":   []string{"remove"},
		},
	})
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}

	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.URI().SetPath("/api/users")
	c.Request.URI().SetQueryString("keep=old&remove=1")
	c.Request.Header.Set("Host", "original.test")
	c.Request.Header.Set("X-Keep", "old")
	c.Request.Header.Set("X-Remove", "gone")

	runHandler(c, handler)

	if got := string(c.Host()); got != "users.internal" {
		t.Fatalf("Host = %q, want users.internal", got)
	}
	if got := c.Request.Header.Get("X-Added"); got != "1" {
		t.Fatalf("X-Added = %q, want 1", got)
	}
	if got := c.Request.Header.Get("X-Keep"); got != "old" {
		t.Fatalf("X-Keep = %q, want old", got)
	}
	if got := c.Request.Header.Get("X-Remove"); got != "" {
		t.Fatalf("X-Remove = %q, want empty", got)
	}
	if got := string(c.Query("added")); got != "yes" {
		t.Fatalf("added query = %q, want yes", got)
	}
	if got := string(c.Query("keep")); got != "old" {
		t.Fatalf("keep query = %q, want old", got)
	}
	if got := string(c.Query("remove")); got != "" {
		t.Fatalf("remove query = %q, want empty", got)
	}
	if got := string(c.Query("set")); got != "ok" {
		t.Fatalf("set query = %q, want ok", got)
	}
}

func TestResponseTransformerRunsAfterNextAndOverridesResponse(t *testing.T) {
	registry := newRegistry(t)
	handler, err := registry.Factory("response_transformer")(map[string]any{
		"status":       299,
		"body":         `{"ok":true}`,
		"content_type": "application/json",
		"add": map[string]any{
			"headers": map[string]string{"X-Added": "1", "X-Keep": "new"},
		},
		"set": map[string]any{
			"headers": map[string]string{"X-Set": "2"},
		},
		"remove": map[string]any{
			"headers": []string{"X-Remove"},
		},
	})
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}

	c := app.NewContext(0)
	c.Response.Header.Set("X-Keep", "old")
	c.Response.Header.Set("X-Remove", "gone")
	c.SetHandlers([]app.HandlerFunc{
		handler,
		func(_ context.Context, next *app.RequestContext) {
			next.Response.SetStatusCode(200)
			next.Response.SetBodyString("original")
		},
	})
	c.Next(context.Background())

	if got := c.Response.StatusCode(); got != 299 {
		t.Fatalf("status = %d, want 299", got)
	}
	if got := string(c.Response.Body()); got != `{"ok":true}` {
		t.Fatalf("body = %q, want JSON override", got)
	}
	if got := c.Response.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := c.Response.Header.Get("X-Added"); got != "1" {
		t.Fatalf("X-Added = %q, want 1", got)
	}
	if got := c.Response.Header.Get("X-Keep"); got != "old" {
		t.Fatalf("X-Keep = %q, want old", got)
	}
	if got := c.Response.Header.Get("X-Set"); got != "2" {
		t.Fatalf("X-Set = %q, want 2", got)
	}
	if got := c.Response.Header.Get("X-Remove"); got != "" {
		t.Fatalf("X-Remove = %q, want empty", got)
	}
}

func TestPathTransformers(t *testing.T) {
	tests := []struct {
		name       string
		pluginName string
		params     any
		path       string
		wantPath   string
	}{
		{
			name:       "replace path",
			pluginName: "replace_path",
			params:     map[string]any{"path": "internal/users"},
			path:       "/api/users",
			wantPath:   "/internal/users",
		},
		{
			name:       "strip prefix",
			pluginName: "strip_prefix",
			params:     map[string]any{"prefix": "/api"},
			path:       "/api/users",
			wantPath:   "/users",
		},
		{
			name:       "add prefix",
			pluginName: "add_prefix",
			params:     map[string]any{"prefix": "/v1"},
			path:       "/users",
			wantPath:   "/v1/users",
		},
	}

	registry := newRegistry(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := registry.Factory(tt.pluginName)(tt.params)
			if err != nil {
				t.Fatalf("factory error = %v", err)
			}
			c := app.NewContext(0)
			c.Request.URI().SetPath(tt.path)

			runHandler(c, handler)

			if got := string(c.Path()); got != tt.wantPath {
				t.Fatalf("path = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

func newRegistry(t *testing.T) *plugin.Registry {
	t.Helper()
	registry := plugin.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return registry
}

func runHandler(c *app.RequestContext, handler app.HandlerFunc) {
	c.SetHandlers([]app.HandlerFunc{
		handler,
		func(context.Context, *app.RequestContext) {},
	})
	c.Next(context.Background())
}
