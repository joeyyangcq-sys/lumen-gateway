package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/config"
	"github.com/joey/lumen-gateway/internal/observability"
	"github.com/joey/lumen-gateway/internal/plugin"
	"github.com/joey/lumen-gateway/internal/proxy"
)

func TestBuildSnapshotBuildsPluginChainsForAllScopes(t *testing.T) {
	snapshot, err := BuildSnapshot(gatewayOptions("127.0.0.1:9001"))
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
	if len(snapshot.Services["user-service"].Handlers) != 2 {
		t.Fatalf("service handlers = %d, want 2", len(snapshot.Services["user-service"].Handlers))
	}
	if len(snapshot.Upstreams["user-upstream"].Handlers) != 1 {
		t.Fatalf("upstream handlers = %d, want 1", len(snapshot.Upstreams["user-upstream"].Handlers))
	}
}

func TestServeHTTPProxiesToMatchedUpstream(t *testing.T) {
	var gotHost string
	var gotPath string
	var gotQuery url.Values

	gw, err := New(gatewayOptions("127.0.0.1:9001"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	installTransport(gw, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.Host
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"X-Upstream": []string{"ok"}},
			Body:       io.NopCloser(strings.NewReader("proxied")),
		}, nil
	}))

	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.SetHost("original.test")
	c.Request.URI().SetPath("/api/users")
	c.Request.URI().SetQueryString("id=1")

	gw.ServeHTTP(context.Background(), c)

	if got := c.Response.StatusCode(); got != http.StatusCreated {
		t.Fatalf("status = %d, want %d", got, http.StatusCreated)
	}
	if got := string(c.Response.Body()); got != "proxied" {
		t.Fatalf("body = %q, want proxied", got)
	}
	if got := gotHost; got != "users.internal" {
		t.Fatalf("upstream host = %q, want users.internal", got)
	}
	if got := gotPath; got != "/users" {
		t.Fatalf("upstream path = %q, want /users", got)
	}
	if got := gotQuery.Get("id"); got != "1" {
		t.Fatalf("id query = %q, want 1", got)
	}
	if got := gotQuery.Get("matched_route"); got != "user-api" {
		t.Fatalf("matched_route query = %q, want user-api", got)
	}
	if got := gotQuery.Get("service"); got != "user-service" {
		t.Fatalf("service query = %q, want user-service", got)
	}
	if got := gotQuery.Get("upstream"); got != "user-upstream" {
		t.Fatalf("upstream query = %q, want user-upstream", got)
	}
	if got := c.Response.Header.Get("X-Upstream"); got != "ok" {
		t.Fatalf("X-Upstream = %q, want ok", got)
	}
	if got := c.Response.Header.Get("X-Global"); got != "true" {
		t.Fatalf("X-Global = %q, want true", got)
	}
	if got := c.Response.Header.Get("X-Service"); got != "user-service" {
		t.Fatalf("X-Service = %q, want user-service", got)
	}
	if got := c.Response.Header.Get("X-Upstream-Host"); got != "users.internal" {
		t.Fatalf("X-Upstream-Host = %q, want users.internal", got)
	}
	if got := c.Response.Header.Get("X-Endpoint"); got != "127.0.0.1:9001" {
		t.Fatalf("X-Endpoint = %q, want 127.0.0.1:9001", got)
	}
}

func TestServeHTTPSupportsApisixPassHostModes(t *testing.T) {
	tests := []struct {
		name         string
		passHost     string
		upstreamHost string
		wantHost     func(string) string
	}{
		{
			name:     "node",
			passHost: "node",
			wantHost: func(addr string) string {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return addr
				}
				return host
			},
		},
		{
			name:         "rewrite",
			passHost:     "rewrite",
			upstreamHost: "orders.internal",
			wantHost: func(string) string {
				return "orders.internal"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotHost string
			endpointAddress := "10.0.0.8:9080"
			options := gatewayOptions(endpointAddress)
			upstreamOptions := options.Upstreams["user-upstream"]
			upstreamOptions.PassHost = tt.passHost
			upstreamOptions.UpstreamHost = tt.upstreamHost
			options.Upstreams["user-upstream"] = upstreamOptions

			gw, err := New(options)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			installTransport(gw, roundTripFunc(func(r *http.Request) (*http.Response, error) {
				gotHost = r.Host
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}))

			c := app.NewContext(0)
			c.Request.SetMethod("GET")
			c.Request.SetHost("original.test")
			c.Request.URI().SetPath("/api/users")

			gw.ServeHTTP(context.Background(), c)

			if got := c.Response.StatusCode(); got != http.StatusOK {
				t.Fatalf("status = %d, want %d", got, http.StatusOK)
			}
			if got := gotHost; got != tt.wantHost(endpointAddress) {
				t.Fatalf("upstream host = %q, want %q", got, tt.wantHost(endpointAddress))
			}
		})
	}
}

func TestServeHTTPReturnsNotFoundForUnknownRoute(t *testing.T) {
	gw, err := New(gatewayOptions("127.0.0.1:9001"))
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

func TestServeHTTPExposesMetricsEndpoint(t *testing.T) {
	previous := observability.Default()
	recorder := observability.NewMemoryRecorder()
	observability.SetDefault(recorder)
	defer observability.SetDefault(previous)

	gw, err := New(gatewayOptions("127.0.0.1:9001"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	installTransport(gw, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	}))

	request := app.NewContext(0)
	request.Request.SetMethod("GET")
	request.Request.SetHost("original.test")
	request.Request.URI().SetPath("/api/users")
	gw.ServeHTTP(context.Background(), request)

	metrics := app.NewContext(0)
	metrics.Request.SetMethod("GET")
	metrics.Request.URI().SetPath("/metrics")
	gw.ServeHTTP(context.Background(), metrics)

	body := string(metrics.Response.Body())
	if metrics.Response.StatusCode() != 200 {
		t.Fatalf("metrics status = %d, want 200", metrics.Response.StatusCode())
	}
	if !strings.Contains(body, `lumen_plugin_executions_total{phase="request",plugin="request_transformer"`) {
		t.Fatalf("metrics body missing plugin execution metric:\n%s", body)
	}
	if !strings.Contains(body, `lumen_upstream_requests_total`) {
		t.Fatalf("metrics body missing upstream request metric:\n%s", body)
	}
	if !strings.Contains(body, `phase="total"`) {
		t.Fatalf("metrics body missing upstream total phase metric:\n%s", body)
	}
}

func TestBuildPluginHandlersOrdersByPriority(t *testing.T) {
	registry := plugin.NewRegistry()
	mustRegisterPlugin(t, registry, plugin.Metadata{
		Name:     "low",
		Priority: 10,
		Scopes:   []plugin.Scope{plugin.ScopeRoute},
	}, "low")
	mustRegisterPlugin(t, registry, plugin.Metadata{
		Name:     "high",
		Priority: 100,
		Scopes:   []plugin.Scope{plugin.ScopeRoute},
	}, "high")
	mustRegisterPlugin(t, registry, plugin.Metadata{
		Name:     "same-a",
		Priority: 50,
		Scopes:   []plugin.Scope{plugin.ScopeRoute},
	}, "same-a")
	mustRegisterPlugin(t, registry, plugin.Metadata{
		Name:     "same-b",
		Priority: 50,
		Scopes:   []plugin.Scope{plugin.ScopeRoute},
	}, "same-b")

	handlers, err := buildPluginHandlers(registry, config.Options{}, plugin.ScopeRoute, []config.PluginRef{
		{Name: "same-a"},
		{Name: "low"},
		{Name: "high"},
		{Name: "same-b"},
	})
	if err != nil {
		t.Fatalf("buildPluginHandlers() error = %v", err)
	}

	c := app.NewContext(0)
	for _, handler := range handlers {
		handler(context.Background(), c)
	}

	if got := string(c.Response.Body()); got != "high,same-a,same-b,low," {
		t.Fatalf("body = %q, want priority-ordered plugin execution", got)
	}
}

func TestBuildPluginHandlersRejectsUnsupportedScope(t *testing.T) {
	registry := plugin.NewRegistry()
	mustRegisterPlugin(t, registry, plugin.Metadata{
		Name:     "route-only",
		Priority: 0,
		Scopes:   []plugin.Scope{plugin.ScopeRoute},
	}, "route-only")

	_, err := buildPluginHandlers(registry, config.Options{}, plugin.ScopeGlobal, []config.PluginRef{
		{Name: "route-only"},
	})
	if err == nil {
		t.Fatal("buildPluginHandlers() error = nil, want unsupported scope error")
	}
	if !strings.Contains(err.Error(), "does not support global scope") {
		t.Fatalf("error = %q, want unsupported scope message", err.Error())
	}
}

func gatewayOptions(endpointAddress string) config.Options {
	return config.Options{
		GlobalPlugins: []config.PluginRef{{
			Name: "response_transformer",
			Params: map[string]any{
				"add": map[string]any{
					"headers": map[string]string{
						"X-Global":        "true",
						"X-Upstream-Host": "$upstream_host",
						"X-Endpoint":      "$endpoint_addr",
					},
				},
			},
		}},
		Plugins: map[string]config.PluginOptions{
			"service-marker": {
				ID:   "service-marker",
				Name: "response_transformer",
				Params: map[string]any{
					"set": map[string]any{
						"headers": map[string]string{"X-Service": "$service_id"},
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
								"query": map[string]string{"matched_route": "$route_id"},
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
					{
						Name: "request_transformer",
						Params: map[string]any{
							"set": map[string]any{
								"query": map[string]string{"service": "$service_id"},
							},
						},
					},
					{Use: "service-marker"},
				},
			},
		},
		Upstreams: map[string]config.UpstreamOptions{
			"user-upstream": {
				ID:       "user-upstream",
				Scheme:   "http",
				PassHost: "pass",
				Endpoints: []config.EndpointOptions{
					{Address: endpointAddress, Weight: 1},
				},
				Plugins: []config.PluginRef{{
					Name: "request_transformer",
					Params: map[string]any{
						"add": map[string]any{
							"query": map[string]string{"upstream": "$upstream_id"},
						},
					},
				}},
			},
		},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func installTransport(gw *Gateway, transport http.RoundTripper) {
	snapshot := gw.snapshot.Load()
	for _, service := range snapshot.Services {
		service.Proxy = proxy.NewHTTP(proxy.HTTPOptions{
			Timeout:   service.Options.Timeout,
			Transport: transport,
		})
	}
}

func mustRegisterPlugin(t *testing.T, registry *plugin.Registry, metadata plugin.Metadata, name string) {
	t.Helper()
	err := registry.Register(metadata, func(any) (app.HandlerFunc, error) {
		return func(_ context.Context, c *app.RequestContext) {
			c.Response.AppendBodyString(name + ",")
		}, nil
	})
	if err != nil {
		t.Fatalf("Register(%q) error = %v", metadata.Name, err)
	}
}
