package builtin

import (
	"context"
	"slices"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/plugin"
)

func TestRequestTransformerMutatesHostHeadersQueryAndMethod(t *testing.T) {
	registry := newRegistry(t)
	handler, err := registry.Factory("request_transformer")(map[string]any{
		"method": "post",
		"host":   "users.internal",
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

	if got := string(c.Method()); got != "POST" {
		t.Fatalf("method = %q, want POST", got)
	}
	if got := string(c.Host()); got != "users.internal" {
		t.Fatalf("Host = %q, want users.internal", got)
	}
	if got := c.Request.Header.Get("X-Added"); got != "1" {
		t.Fatalf("X-Added = %q, want 1", got)
	}
	if got := headerValues(&c.Request.Header, "X-Keep"); !slices.Equal(got, []string{"old", "new"}) {
		t.Fatalf("X-Keep values = %#v, want [old new]", got)
	}
	if got := c.Request.Header.Get("X-Remove"); got != "" {
		t.Fatalf("X-Remove = %q, want empty", got)
	}
	if got := string(c.Query("added")); got != "yes" {
		t.Fatalf("added query = %q, want yes", got)
	}
	if got := queryValues(c, "keep"); !slices.Equal(got, []string{"old", "new"}) {
		t.Fatalf("keep query values = %#v, want [old new]", got)
	}
	if got := string(c.Query("remove")); got != "" {
		t.Fatalf("remove query = %q, want empty", got)
	}
	if got := string(c.Query("set")); got != "ok" {
		t.Fatalf("set query = %q, want ok", got)
	}
}

func TestRewritePathRegexFeedsCapturedValuesIntoRequestTransformer(t *testing.T) {
	registry := newRegistry(t)

	rewrite, err := registry.Factory("rewrite_path_regex")(map[string]any{
		"rules": []map[string]any{{
			"pattern":     "^/orders/(\\d+)/items/(\\d+)$",
			"replacement": "/internal/orders/$1/line-items/$2",
		}},
	})
	if err != nil {
		t.Fatalf("rewrite_path_regex factory error = %v", err)
	}

	transform, err := registry.Factory("request_transformer")(map[string]any{
		"method": "patch",
		"set": map[string]any{
			"headers": map[string]string{
				"X-Order-ID": "$1",
				"X-Item-ID":  "$2",
			},
		},
	})
	if err != nil {
		t.Fatalf("request_transformer factory error = %v", err)
	}

	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.URI().SetPath("/orders/42/items/9")
	c.SetHandlers([]app.HandlerFunc{
		rewrite,
		transform,
		func(context.Context, *app.RequestContext) {},
	})
	c.Next(context.Background())

	if got := string(c.Method()); got != "PATCH" {
		t.Fatalf("method = %q, want PATCH", got)
	}
	if got := string(c.Path()); got != "/internal/orders/42/line-items/9" {
		t.Fatalf("path = %q, want rewritten regex path", got)
	}
	if got := c.Request.Header.Get("X-Order-ID"); got != "42" {
		t.Fatalf("X-Order-ID = %q, want 42", got)
	}
	if got := c.Request.Header.Get("X-Item-ID"); got != "9" {
		t.Fatalf("X-Item-ID = %q, want 9", got)
	}
}

func TestRequestTransformerInterpolatesCommonRequestVariables(t *testing.T) {
	registry := newRegistry(t)
	handler, err := registry.Factory("request_transformer")(map[string]any{
		"host": "$http_x_forwarded_host",
		"set": map[string]any{
			"headers": map[string]string{
				"X-Route":     "$uri",
				"X-Full-Path": "$request_uri",
				"X-User":      "$arg_user",
				"X-Client":    "$remote_addr",
				"X-Request":   "$request_id",
			},
		},
	})
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}

	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.URI().SetPath("/orders")
	c.Request.URI().SetQueryString("user=alice")
	c.Request.Header.Set("X-Forwarded-Host", "edge.internal")
	c.Request.Header.Set("X-Forwarded-For", "203.0.113.10")
	plugin.SetRequestID(c, "req-1")

	runHandler(c, handler)

	if got := string(c.Host()); got != "edge.internal" {
		t.Fatalf("host = %q, want edge.internal", got)
	}
	if got := c.Request.Header.Get("X-Route"); got != "/orders" {
		t.Fatalf("X-Route = %q, want /orders", got)
	}
	if got := c.Request.Header.Get("X-Full-Path"); got != "/orders?user=alice" {
		t.Fatalf("X-Full-Path = %q, want /orders?user=alice", got)
	}
	if got := c.Request.Header.Get("X-User"); got != "alice" {
		t.Fatalf("X-User = %q, want alice", got)
	}
	if got := c.Request.Header.Get("X-Client"); got != "203.0.113.10" {
		t.Fatalf("X-Client = %q, want 203.0.113.10", got)
	}
	if got := c.Request.Header.Get("X-Request"); got != "req-1" {
		t.Fatalf("X-Request = %q, want req-1", got)
	}
}

func TestRequestTransformerInterpolatesRuntimeMetadataVariables(t *testing.T) {
	registry := newRegistry(t)
	handler, err := registry.Factory("request_transformer")(map[string]any{
		"set": map[string]any{
			"headers": map[string]string{
				"X-Route":    "$route_id",
				"X-Service":  "$service_id",
				"X-Upstream": "$upstream_id",
				"X-Host":     "$upstream_host",
				"X-Endpoint": "$endpoint_addr",
			},
		},
	})
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}

	c := app.NewContext(0)
	plugin.SetRouteID(c, "route-a")
	plugin.SetServiceID(c, "service-a")
	plugin.SetUpstreamID(c, "upstream-a")
	plugin.SetUpstreamHost(c, "orders.internal")
	plugin.SetEndpointAddress(c, "10.0.0.8:9080")

	runHandler(c, handler)

	if got := c.Request.Header.Get("X-Route"); got != "route-a" {
		t.Fatalf("X-Route = %q, want route-a", got)
	}
	if got := c.Request.Header.Get("X-Service"); got != "service-a" {
		t.Fatalf("X-Service = %q, want service-a", got)
	}
	if got := c.Request.Header.Get("X-Upstream"); got != "upstream-a" {
		t.Fatalf("X-Upstream = %q, want upstream-a", got)
	}
	if got := c.Request.Header.Get("X-Host"); got != "orders.internal" {
		t.Fatalf("X-Host = %q, want orders.internal", got)
	}
	if got := c.Request.Header.Get("X-Endpoint"); got != "10.0.0.8:9080" {
		t.Fatalf("X-Endpoint = %q, want 10.0.0.8:9080", got)
	}
}

func TestResponseTransformerRunsAfterNextAndOverridesResponse(t *testing.T) {
	registry := newRegistry(t)
	handler, err := registry.Factory("response_transformer")(map[string]any{
		"status":       299,
		"body":         `eyJvayI6dHJ1ZX0=`,
		"body_base64":  true,
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
	if got := headerValues(&c.Response.Header, "X-Keep"); !slices.Equal(got, []string{"old", "new"}) {
		t.Fatalf("X-Keep values = %#v, want [old new]", got)
	}
	if got := c.Response.Header.Get("X-Set"); got != "2" {
		t.Fatalf("X-Set = %q, want 2", got)
	}
	if got := c.Response.Header.Get("X-Remove"); got != "" {
		t.Fatalf("X-Remove = %q, want empty", got)
	}
}

func TestRequestIDUsesIncomingValueOrGeneratesOne(t *testing.T) {
	registry := newRegistry(t)
	handler, err := registry.Factory("request_id")(map[string]any{
		"header_name":         "X-Req-Id",
		"include_in_response": true,
	})
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}

	c := app.NewContext(0)
	c.Request.Header.Set("X-Req-Id", "incoming-id")
	runHandler(c, handler)

	if got := c.Request.Header.Get("X-Req-Id"); got != "incoming-id" {
		t.Fatalf("request id = %q, want incoming-id", got)
	}
	if got := c.Response.Header.Get("X-Req-Id"); got != "incoming-id" {
		t.Fatalf("response request id = %q, want incoming-id", got)
	}
	if got := plugin.FromRequestContext(c).RequestID(); got != "incoming-id" {
		t.Fatalf("context request id = %q, want incoming-id", got)
	}

	generated := app.NewContext(0)
	runHandler(generated, handler)
	id := generated.Request.Header.Get("X-Req-Id")
	if id == "" {
		t.Fatal("generated request id is empty")
	}
	if got := generated.Response.Header.Get("X-Req-Id"); got != id {
		t.Fatalf("response request id = %q, want %q", got, id)
	}
}

func TestLimitCountAppliesRouteScopedQuotaAndHeaders(t *testing.T) {
	registry := newRegistry(t)
	handler, err := registry.Factory("limit_count")(map[string]any{
		"count":         1,
		"time_window":   30,
		"key_type":      "var",
		"key":           "remote_addr",
		"rejected_code": 429,
		"rejected_msg":  "rate limited",
	})
	if err != nil {
		t.Fatalf("factory error = %v", err)
	}

	newCtx := func(routeID string) *app.RequestContext {
		c := app.NewContext(0)
		plugin.SetRouteID(c, routeID)
		c.Request.Header.Set("X-Forwarded-For", "198.51.100.10")
		return c
	}

	first := newCtx("route-a")
	first.SetHandlers([]app.HandlerFunc{
		handler,
		func(_ context.Context, next *app.RequestContext) {
			next.Response.SetStatusCode(204)
		},
	})
	first.Next(context.Background())
	if got := first.Response.StatusCode(); got != 204 {
		t.Fatalf("first status = %d, want 204", got)
	}
	if got := first.Response.Header.Get("X-RateLimit-Limit"); got != "1" {
		t.Fatalf("limit header = %q, want 1", got)
	}
	if got := first.Response.Header.Get("X-RateLimit-Remaining"); got != "0" {
		t.Fatalf("remaining header = %q, want 0", got)
	}

	second := newCtx("route-a")
	second.SetHandlers([]app.HandlerFunc{
		handler,
		func(_ context.Context, next *app.RequestContext) {
			next.Response.SetStatusCode(200)
			next.Response.SetBodyString("should-not-run")
		},
	})
	second.Next(context.Background())
	if got := second.Response.StatusCode(); got != 429 {
		t.Fatalf("second status = %d, want 429", got)
	}
	if got := string(second.Response.Body()); got != "rate limited" {
		t.Fatalf("second body = %q, want rate limited", got)
	}
	if got := second.Response.Body(); string(got) == "should-not-run" {
		t.Fatal("limit-count did not abort remaining handlers")
	}

	otherRoute := newCtx("route-b")
	otherRoute.SetHandlers([]app.HandlerFunc{
		handler,
		func(_ context.Context, next *app.RequestContext) {
			next.Response.SetStatusCode(200)
		},
	})
	otherRoute.Next(context.Background())
	if got := otherRoute.Response.StatusCode(); got != 200 {
		t.Fatalf("other route status = %d, want 200", got)
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

func headerValues(header interface{ VisitAll(func(key, value []byte)) }, name string) []string {
	values := make([]string, 0)
	header.VisitAll(func(key, value []byte) {
		if string(key) == name {
			values = append(values, string(value))
		}
	})
	return values
}

func queryValues(c *app.RequestContext, name string) []string {
	values := make([]string, 0)
	c.Request.URI().QueryArgs().VisitAll(func(key, value []byte) {
		if string(key) == name {
			values = append(values, string(value))
		}
	})
	return values
}
