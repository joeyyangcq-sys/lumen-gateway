package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/config"
)

func TestBuildSnapshotBuildsPluginChainsForAllScopes(t *testing.T) {
	snapshot, err := BuildSnapshot(gatewayOptions())
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}

	if len(snapshot.GlobalHandlers) != 1 {
		t.Fatalf("global handlers = %d, want 1", len(snapshot.GlobalHandlers))
	}
	if len(snapshot.ServerHandlers) != 1 {
		t.Fatalf("server handlers = %d, want 1", len(snapshot.ServerHandlers))
	}
	if len(snapshot.RouteHandlers["user-api"]) != 2 {
		t.Fatalf("route handlers = %d, want 2", len(snapshot.RouteHandlers["user-api"]))
	}
	if len(snapshot.Services["user-service"].Handlers) != 1 {
		t.Fatalf("service handlers = %d, want 1", len(snapshot.Services["user-service"].Handlers))
	}
	if len(snapshot.Upstreams["user-upstream"].Handlers) != 1 {
		t.Fatalf("upstream handlers = %d, want 1", len(snapshot.Upstreams["user-upstream"].Handlers))
	}
}

func TestServeHTTPAppliesPluginsAndReturnsMatchedRouteDebugResponse(t *testing.T) {
	gw, err := New(gatewayOptions())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.SetHost("original.test")
	c.Request.URI().SetPath("/api/users")
	c.Request.URI().SetQueryString("id=1")

	gw.ServeHTTP(context.Background(), c)

	if got := c.Response.StatusCode(); got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	body := string(c.Response.Body())
	for _, want := range []string{
		"route=user-api",
		"service=user-service",
		"upstream=user-upstream",
		"host=users.internal",
		"path=/users",
		"matched_route=user-api",
		"upstream=user-upstream",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want substring %q", body, want)
		}
	}
	if got := c.Response.Header.Get("X-Global"); got != "true" {
		t.Fatalf("X-Global = %q, want true", got)
	}
	if got := c.Response.Header.Get("X-Service"); got != "user-service" {
		t.Fatalf("X-Service = %q, want user-service", got)
	}
}

func TestServeHTTPReturnsNotFoundForUnknownRoute(t *testing.T) {
	gw, err := New(gatewayOptions())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.URI().SetPath("/missing")

	gw.ServeHTTP(context.Background(), c)

	if got := c.Response.StatusCode(); got != 404 {
		t.Fatalf("status = %d, want 404", got)
	}
}

func gatewayOptions() config.Options {
	return config.Options{
		GlobalPlugins: []config.PluginRef{{
			Name: "response_transformer",
			Params: map[string]any{
				"add": map[string]any{
					"headers": map[string]string{"X-Global": "true"},
				},
			},
		}},
		Plugins: map[string]config.PluginOptions{
			"service-marker": {
				ID:   "service-marker",
				Name: "response_transformer",
				Params: map[string]any{
					"set": map[string]any{
						"headers": map[string]string{"X-Service": "user-service"},
					},
				},
			},
		},
		Servers: map[string]config.ServerOptions{
			"main": {
				ID:     "main",
				Listen: ":8080",
				Plugins: []config.PluginRef{{
					Name: "request_transformer",
					Params: map[string]any{
						"add": map[string]any{
							"headers": map[string]string{"X-Server": "main"},
						},
					},
				}},
			},
		},
		Routes: map[string]config.RouteOptions{
			"user-api": {
				ID:      "user-api",
				Methods: []string{"GET"},
				Paths:   []string{"/api/users"},
				Service: "user-service",
				Plugins: []config.PluginRef{
					{
						Name: "strip_prefix",
						Params: map[string]any{
							"prefix": "/api",
						},
					},
					{
						Name: "request_transformer",
						Params: map[string]any{
							"host": "users.internal",
							"set": map[string]any{
								"query": map[string]string{"matched_route": "user-api"},
							},
						},
					},
				},
			},
		},
		Services: map[string]config.ServiceOptions{
			"user-service": {
				ID:       "user-service",
				Protocol: "http",
				Upstream: "user-upstream",
				Plugins: []config.PluginRef{
					{Use: "service-marker"},
				},
			},
		},
		Upstreams: map[string]config.UpstreamOptions{
			"user-upstream": {
				ID: "user-upstream",
				Endpoints: []config.EndpointOptions{
					{Address: "127.0.0.1:9001", Weight: 1},
				},
				Plugins: []config.PluginRef{{
					Name: "request_transformer",
					Params: map[string]any{
						"add": map[string]any{
							"query": map[string]string{"upstream": "user-upstream"},
						},
					},
				}},
			},
		},
	}
}
