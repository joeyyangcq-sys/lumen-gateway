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
