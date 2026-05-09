package plugin

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
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
