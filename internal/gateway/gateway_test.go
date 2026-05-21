package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
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
	if len(snapshot.RouteHandlers["user-api"]) != 5 {
		t.Fatalf("route handlers = %d, want prebuilt global/server/route/service chain", len(snapshot.RouteHandlers["user-api"]))
	}
	if len(snapshot.Services["user-service"].Handlers) != 2 {
		t.Fatalf("service handlers = %d, want 2", len(snapshot.Services["user-service"].Handlers))
	}
	if len(snapshot.Upstreams["user-upstream"].Handlers) != 1 {
		t.Fatalf("upstream handlers = %d, want 1", len(snapshot.Upstreams["user-upstream"].Handlers))
	}
	if len(snapshot.Services["user-service"].ExecutionHandlers) != 4 {
		t.Fatalf("service execution handlers = %d, want prebuilt service/upstream/proxy chain", len(snapshot.Services["user-service"].ExecutionHandlers))
	}
}

func TestNewWithCompilerUsesInjectedCompilerForBuildAndReload(t *testing.T) {
	compiled := 0
	compiler := runtimeCompilerFunc(func(options config.Options) (*RuntimeSnapshot, error) {
		compiled++
		return BuildSnapshot(options)
	})

	gw, err := NewWithCompiler(gatewayOptions("127.0.0.1:9001"), compiler)
	if err != nil {
		t.Fatalf("NewWithCompiler() error = %v", err)
	}
	if compiled != 1 {
		t.Fatalf("compiler compile count = %d, want 1 after NewWithCompiler", compiled)
	}

	if err := gw.Reload(gatewayOptions("127.0.0.1:9002")); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if compiled != 2 {
		t.Fatalf("compiler compile count = %d, want 2 after Reload", compiled)
	}
}

func TestNewWithCompilerReturnsCompileError(t *testing.T) {
	wantErr := errors.New("compile failed")
	compiler := runtimeCompilerFunc(func(config.Options) (*RuntimeSnapshot, error) {
		return nil, wantErr
	})

	_, err := NewWithCompiler(gatewayOptions("127.0.0.1:9001"), compiler)
	if !errors.Is(err, wantErr) {
		t.Fatalf("NewWithCompiler() error = %v, want %v", err, wantErr)
	}
}

func TestReloadClosesPreviousSnapshotResources(t *testing.T) {
	var closed atomic.Int32
	compiler := NewRuntimeCompiler(WithRegistryFactory(func() (*plugin.Registry, error) {
		registry := plugin.NewRegistry()
		if err := plugin.RegisterTypedContextWithCloser[struct{}](registry, plugin.Metadata{
			Name:     "closeable",
			Priority: 0,
			Scopes:   []plugin.Scope{plugin.ScopeGlobal},
		}, func(struct{}) (plugin.ContextHandler, io.Closer, error) {
			return func(ctx context.Context, pc plugin.PluginContext) {
					pc.Next(ctx)
				}, closeFunc(func() error {
					closed.Add(1)
					return nil
				}), nil
		}); err != nil {
			return nil, err
		}
		return registry, nil
	}))

	options := gatewayOptions("127.0.0.1:9001")
	options.GlobalPlugins = []config.PluginRef{{Name: "closeable"}}

	gw, err := NewWithCompiler(options, compiler)
	if err != nil {
		t.Fatalf("NewWithCompiler() error = %v", err)
	}

	if err := gw.Reload(options); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if got := closed.Load(); got != 1 {
		t.Fatalf("closed resources after reload = %d, want 1", got)
	}

	if err := gw.Shutdown(); err != nil && !strings.Contains(err.Error(), "engine is not running") {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if got := closed.Load(); got != 2 {
		t.Fatalf("closed resources after shutdown = %d, want 2", got)
	}
}

func TestCompileClosesBuiltResourcesOnError(t *testing.T) {
	var closed atomic.Int32
	compiler := NewRuntimeCompiler(WithRegistryFactory(func() (*plugin.Registry, error) {
		registry := plugin.NewRegistry()
		if err := plugin.RegisterTypedContextWithCloser[struct{}](registry, plugin.Metadata{
			Name:     "closeable",
			Priority: 0,
			Scopes:   []plugin.Scope{plugin.ScopeGlobal},
		}, func(struct{}) (plugin.ContextHandler, io.Closer, error) {
			return func(ctx context.Context, pc plugin.PluginContext) {
					pc.Next(ctx)
				}, closeFunc(func() error {
					closed.Add(1)
					return nil
				}), nil
		}); err != nil {
			return nil, err
		}
		if err := plugin.RegisterTypedContext[struct{}](registry, plugin.Metadata{
			Name:     "failing",
			Priority: 0,
			Scopes:   []plugin.Scope{plugin.ScopeGlobal},
		}, func(struct{}) (plugin.ContextHandler, error) {
			return nil, errors.New("boom")
		}); err != nil {
			return nil, err
		}
		return registry, nil
	}))

	options := gatewayOptions("127.0.0.1:9001")
	options.GlobalPlugins = []config.PluginRef{
		{Name: "closeable"},
		{Name: "failing"},
	}

	if _, err := compiler.Compile(options); err == nil {
		t.Fatal("Compile() error = nil, want error")
	}
	if got := closed.Load(); got != 1 {
		t.Fatalf("closed resources after compile error = %d, want 1", got)
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
	if got := gotQuery.Get("route_upstream"); got != "user-upstream" {
		t.Fatalf("route_upstream query = %q, want user-upstream", got)
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
	if !strings.Contains(body, `lumen_gateway_requests_total{handler="proxy",method="GET",route_id="user-api",status_class="2xx"} 1`) {
		t.Fatalf("metrics body missing gateway request metric:\n%s", body)
	}
	if !strings.Contains(body, `lumen_upstream_requests_total`) {
		t.Fatalf("metrics body missing upstream request metric:\n%s", body)
	}
	if !strings.Contains(body, `phase="total"`) {
		t.Fatalf("metrics body missing upstream total phase metric:\n%s", body)
	}
}

func TestServeHTTPRecordsUnmatchedRequestMetrics(t *testing.T) {
	previous := observability.Default()
	recorder := observability.NewMemoryRecorder()
	observability.SetDefault(recorder)
	defer observability.SetDefault(previous)

	gw, err := New(gatewayOptions("127.0.0.1:9001"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.URI().SetPath("/missing")

	gw.ServeHTTP(context.Background(), c)

	metrics := recorder.RenderPrometheus()
	if !strings.Contains(metrics, `lumen_gateway_requests_total{handler="unmatched",method="GET",status_class="4xx"} 1`) {
		t.Fatalf("metrics missing unmatched request:\n%s", metrics)
	}
}

func TestServeHTTPDelegatesAdminHandler(t *testing.T) {
	gw, err := New(gatewayOptions("127.0.0.1:9001"), adminHandlerFunc(func(_ context.Context, c *app.RequestContext) bool {
		if string(c.Path()) != "/apisix/admin/routes" {
			return false
		}
		c.Response.SetStatusCode(200)
		c.Response.SetBodyString(`{"total":0}`)
		return true
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.URI().SetPath("/apisix/admin/routes")
	gw.ServeHTTP(context.Background(), c)

	if got := c.Response.StatusCode(); got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	if got := string(c.Response.Body()); got != `{"total":0}` {
		t.Fatalf("body = %q, want admin response", got)
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
								"query": map[string]string{
									"matched_route":  "$route_id",
									"route_upstream": "$upstream_id",
								},
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

type adminHandlerFunc func(context.Context, *app.RequestContext) bool

func (f adminHandlerFunc) ServeHTTP(ctx context.Context, c *app.RequestContext) bool {
	return f(ctx, c)
}

type runtimeCompilerFunc func(config.Options) (*RuntimeSnapshot, error)

func (f runtimeCompilerFunc) Compile(options config.Options) (*RuntimeSnapshot, error) {
	return f(options)
}

type closeFunc func() error

func (f closeFunc) Close() error {
	return f()
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
