package plugin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/observability"
)

func TestRegisterTypedDecodesConfigAndStoresMetadata(t *testing.T) {
	type cfg struct {
		Value string `yaml:"value"`
	}

	registry := NewRegistry()
	err := RegisterTyped(registry, Metadata{
		Name:     "typed",
		Priority: 1200,
		Scopes:   []Scope{ScopeRoute},
	}, func(config cfg) (app.HandlerFunc, error) {
		return func(_ context.Context, c *app.RequestContext) {
			c.Response.SetBodyString(config.Value)
		}, nil
	})
	if err != nil {
		t.Fatalf("RegisterTyped() error = %v", err)
	}

	definition, ok := registry.Definition("typed")
	if !ok {
		t.Fatal("Definition() ok = false, want true")
	}
	if definition.Metadata().Priority != 1200 {
		t.Fatalf("priority = %d, want 1200", definition.Metadata().Priority)
	}
	if !definition.Supports(ScopeRoute) {
		t.Fatal("Supports(route) = false, want true")
	}
	if definition.Supports(ScopeGlobal) {
		t.Fatal("Supports(global) = true, want false")
	}

	handler, err := definition.Factory()(map[string]any{"value": "hello"})
	if err != nil {
		t.Fatalf("Factory() error = %v", err)
	}

	c := app.NewContext(0)
	handler(context.Background(), c)
	if got := string(c.Response.Body()); got != "hello" {
		t.Fatalf("body = %q, want hello", got)
	}
}

func TestRegisterRejectsEmptyScopes(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(Metadata{Name: "missing-scopes"}, func(any) (app.HandlerFunc, error) {
		return func(context.Context, *app.RequestContext) {}, nil
	})
	if err == nil {
		t.Fatal("Register() error = nil, want validation error")
	}
}

func TestRegisterTypedContextExposesPluginContextMetadataAndMutators(t *testing.T) {
	type cfg struct {
		Header string `yaml:"header"`
	}

	registry := NewRegistry()
	err := RegisterTypedContext(registry, Metadata{
		Name:     "contextual",
		Priority: 50,
		Scopes:   []Scope{ScopeRoute},
	}, func(config cfg) (ContextHandler, error) {
		return func(_ context.Context, pc PluginContext) {
			pc.SetRequestMethod("PATCH")
			pc.SetRequestPath("/rewritten")
			pc.SetRequestHeader(config.Header, pc.RouteID()+":"+pc.ServiceID()+":"+pc.UpstreamID())
			pc.SetResponseHeader("X-Upstream-Host", pc.UpstreamHost())
			pc.SetResponseHeader("X-Endpoint-Addr", pc.EndpointAddress())
			pc.SetResponseHeader("X-Request-Id", pc.RequestID())
			pc.SetValue("seen", true)
		}, nil
	})
	if err != nil {
		t.Fatalf("RegisterTypedContext() error = %v", err)
	}

	definition, ok := registry.Definition("contextual")
	if !ok {
		t.Fatal("Definition() ok = false, want true")
	}

	handler, err := definition.Factory()(map[string]any{"header": "X-Meta"})
	if err != nil {
		t.Fatalf("Factory() error = %v", err)
	}

	c := app.NewContext(0)
	SetRouteID(c, "route-1")
	SetServiceID(c, "service-1")
	SetUpstreamID(c, "upstream-1")
	SetUpstreamHost(c, "users.internal")
	SetEndpointAddress(c, "10.0.0.8:9080")
	SetRequestID(c, "req-1")

	handler(context.Background(), c)

	if got := string(c.Method()); got != "PATCH" {
		t.Fatalf("method = %q, want PATCH", got)
	}
	if got := string(c.Path()); got != "/rewritten" {
		t.Fatalf("path = %q, want /rewritten", got)
	}
	if got := c.Request.Header.Get("X-Meta"); got != "route-1:service-1:upstream-1" {
		t.Fatalf("X-Meta = %q, want route-service-upstream tuple", got)
	}
	if got := c.Response.Header.Get("X-Upstream-Host"); got != "users.internal" {
		t.Fatalf("X-Upstream-Host = %q, want users.internal", got)
	}
	if got := c.Response.Header.Get("X-Endpoint-Addr"); got != "10.0.0.8:9080" {
		t.Fatalf("X-Endpoint-Addr = %q, want 10.0.0.8:9080", got)
	}
	if got := c.Response.Header.Get("X-Request-Id"); got != "req-1" {
		t.Fatalf("X-Request-Id = %q, want req-1", got)
	}
	if got, ok := c.Get("seen"); !ok || got != true {
		t.Fatalf("context value = %#v, ok=%v, want true", got, ok)
	}
}

type fakeCloser struct {
	closed bool
	err    error
}

func (f *fakeCloser) Close() error {
	f.closed = true
	return f.err
}

func TestRegisterTypedContextWithCloser(t *testing.T) {
	registry := NewRegistry()
	closer := &fakeCloser{}
	err := RegisterTypedContextWithCloser(registry, Metadata{
		Name:   "closer-plugin",
		Scopes: []Scope{ScopeGlobal},
	}, func(cfg string) (ContextHandler, io.Closer, error) {
		return func(ctx context.Context, pc PluginContext) {}, closer, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	def, ok := registry.Definition("closer-plugin")
	if !ok {
		t.Fatal("Definition not found")
	}

	_, err = def.Factory()("test")
	if err != nil {
		t.Fatal(err)
	}

	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}
	if !closer.closed {
		t.Error("expected closer to be closed")
	}
}

func TestRegistryEdgeCases(t *testing.T) {
	registry := NewRegistry()
	// Unnamed
	if err := registry.Register(Metadata{}, nil); err == nil {
		t.Error("expected error for empty name")
	}
	// Nil factory
	if err := registry.Register(Metadata{Name: "nil-fac"}, nil); err == nil {
		t.Error("expected error for nil factory")
	}
	// Duplicate registry
	_ = registry.Register(Metadata{Name: "dup", Scopes: []Scope{ScopeGlobal}}, func(params any) (app.HandlerFunc, error) {
		return nil, nil
	})
	if err := registry.Register(Metadata{Name: "dup", Scopes: []Scope{ScopeGlobal}}, func(params any) (app.HandlerFunc, error) {
		return nil, nil
	}); err == nil {
		t.Error("expected error for duplicate registration")
	}

	// Factory not found
	if fac := registry.Factory("not-exists"); fac != nil {
		t.Error("expected nil factory for unregistered name")
	}

	// Definitions
	defs := registry.Definitions()
	if len(defs) != 1 || defs[0].Metadata().Name != "dup" {
		t.Errorf("expected 1 definition 'dup', got %v", defs)
	}
}

func TestPluginContextFullAPI(t *testing.T) {
	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.SetRequestURI("/test?a=1&b=2")
	c.Request.Header.Set("X-Request", "val")
	c.Response.Header.Set("X-Response", "res")

	pc := FromRequestContext(c)

	// Route context values
	if pc.Phase() != "request" {
		t.Errorf("expected phase request, got %q", pc.Phase())
	}
	pc.SetPhase("response")
	if pc.Phase() != "response" {
		t.Errorf("expected phase response, got %q", pc.Phase())
	}

	// Host, Path, Method, URI
	pc.SetRequestHost("test.com")
	if pc.RequestHost() != "test.com" {
		t.Errorf("expected host test.com, got %q", pc.RequestHost())
	}
	if pc.RequestMethod() != "GET" {
		t.Errorf("expected method GET, got %q", pc.RequestMethod())
	}
	if pc.RequestPath() != "/test" {
		t.Errorf("expected path /test, got %q", pc.RequestPath())
	}
	if pc.RequestURI() != "/test?a=1&b=2" {
		t.Errorf("expected URI, got %q", pc.RequestURI())
	}

	// Queries
	if pc.RequestQuery("a") != "1" {
		t.Errorf("expected query a=1, got %q", pc.RequestQuery("a"))
	}
	pc.AddRequestQuery("c", "3")
	pc.SetRequestQuery("a", "10")
	if pc.RequestQuery("a") != "10" || pc.RequestQuery("c") != "3" {
		t.Error("expected query mutations")
	}
	pc.DelRequestQuery("b")
	if pc.RequestQuery("b") != "" {
		t.Error("expected deleted query")
	}

	// Headers
	if pc.RequestHeader("X-Request") != "val" {
		t.Errorf("expected header X-Request=val, got %q", pc.RequestHeader("X-Request"))
	}
	pc.AddRequestHeader("X-Add", "1")
	pc.SetRequestHeader("X-Request", "2")
	if pc.RequestHeader("X-Request") != "2" || pc.RequestHeader("X-Add") != "1" {
		t.Error("expected header mutations")
	}
	pc.DelRequestHeader("X-Add")
	if pc.RequestHeader("X-Add") != "" {
		t.Error("expected deleted header")
	}

	// Response Headers
	if pc.ResponseHeader("X-Response") != "res" {
		t.Errorf("expected response header res, got %q", pc.ResponseHeader("X-Response"))
	}
	pc.AddResponseHeader("X-Resp-Add", "1")
	pc.SetResponseHeader("X-Response", "2")
	if pc.ResponseHeader("X-Response") != "2" || pc.ResponseHeader("X-Resp-Add") != "1" {
		t.Error("expected response header mutations")
	}
	pc.DelResponseHeader("X-Resp-Add")
	if pc.ResponseHeader("X-Resp-Add") != "" {
		t.Error("expected deleted response header")
	}

	// Request Body
	pc.SetRequestBody([]byte("hello"))
	if string(pc.RequestBody()) != "hello" {
		t.Errorf("expected body hello, got %s", pc.RequestBody())
	}

	// Response Status and Body
	pc.SetResponseStatus(http.StatusCreated)
	if pc.ResponseStatus() != http.StatusCreated {
		t.Errorf("expected status 201, got %d", pc.ResponseStatus())
	}
	pc.SetResponseBody([]byte("world"))
	if string(pc.ResponseBody()) != "world" {
		t.Errorf("expected body world, got %s", pc.ResponseBody())
	}

	// Client IP
	c.Request.Header.Set("X-Forwarded-For", "1.1.1.1")
	if pc.ClientIP() != "1.1.1.1" {
		t.Errorf("expected IP 1.1.1.1, got %s", pc.ClientIP())
	}

	// Abort
	if IsAborted(c) {
		t.Error("expected not aborted")
	}
	pc.Abort()
	if !IsAborted(c) {
		t.Error("expected aborted")
	}

	// Regex captures
	if pc.RegexCaptures() != nil {
		t.Error("expected nil captures")
	}
	pc.SetRegexCaptures([]string{"1", "2"})
	caps := pc.RegexCaptures()
	if len(caps) != 2 || caps[0] != "1" {
		t.Errorf("expected captures [1, 2], got %v", caps)
	}

	// Gateway error
	errGate := fmt.Errorf("gate err")
	if pc.GatewayError() != nil {
		t.Error("expected nil gateway error")
	}
	pc.SetGatewayError(errGate)
	if pc.GatewayError() != errGate {
		t.Error("expected gateway error")
	}

	// Upstream status code
	if pc.UpstreamStatusCode() != 0 {
		t.Error("expected 0 upstream status")
	}
	pc.SetUpstreamStatusCode(504)
	if pc.UpstreamStatusCode() != 504 {
		t.Error("expected 504 upstream status")
	}

	// AllScopes
	if len(AllScopes()) != 5 {
		t.Error("expected 5 scopes")
	}

	// IsGatewayError
	if !IsGatewayError(errGate, errGate) {
		t.Error("expected true for same error")
	}

	// ProxyInfo
	proxyInfo := observability.ProxyInfo{Scheme: "http", Address: "1.1.1.1:80"}
	pc.SetProxyInfo(proxyInfo)
	if pc.ProxyInfo().Address != "1.1.1.1:80" {
		t.Error("expected ProxyInfo match")
	}

	// Raw and Next
	if pc.Raw() != c {
		t.Error("expected Raw() to return raw request context")
	}
	pc.Next(context.Background())
}

func TestRegistryCloserError(t *testing.T) {
	registry := NewRegistry()
	closerErr := fmt.Errorf("closer err")
	closer := &fakeCloser{err: closerErr}
	registry.addCloser(closer)
	registry.addCloser(nil) // should ignore
	if err := registry.Close(); err != closerErr {
		t.Errorf("expected closer err, got %v", err)
	}
}

