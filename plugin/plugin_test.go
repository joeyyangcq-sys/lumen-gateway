package plugin

import (
	"context"
	"io"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	internalplugin "github.com/joey/lumen-gateway/internal/plugin"
)

type dummyConfig struct {
	Header string `yaml:"header"`
}

type dummyCloser struct {
	closed bool
}

func (d *dummyCloser) Close() error {
	d.closed = true
	return nil
}

func TestPublicPluginAPI(t *testing.T) {
	r := internalplugin.NewRegistry()

	// 1. AllScopes
	scopes := AllScopes()
	if len(scopes) != 5 {
		t.Errorf("expected 5 scopes, got %d", len(scopes))
	}

	// 2. RegisterTypedContext
	err := RegisterTypedContext(r, Metadata{
		Name:   "p1",
		Scopes: []Scope{ScopeRoute},
	}, func(cfg dummyConfig) (ContextHandler, error) {
		return func(ctx context.Context, pc PluginContext) {}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3. RegisterTypedContextWithCloser
	closer := &dummyCloser{}
	err = RegisterTypedContextWithCloser(r, Metadata{
		Name:   "p2",
		Scopes: []Scope{ScopeRoute},
	}, func(cfg dummyConfig) (ContextHandler, io.Closer, error) {
		return func(ctx context.Context, pc PluginContext) {}, closer, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Trigger factories to execute the wrapped closers setup
	def1, _ := r.Definition("p1")
	_, _ = def1.Factory()(map[string]any{"header": "val"})

	def2, _ := r.Definition("p2")
	_, _ = def2.Factory()(map[string]any{"header": "val"})

	// Close registry to invoke closer
	_ = r.Close()
	if !closer.closed {
		t.Error("expected closer to be closed")
	}

	// 4. RegisterTyped
	r2 := internalplugin.NewRegistry()
	err = RegisterTyped(r2, Metadata{
		Name:   "p3",
		Scopes: []Scope{ScopeRoute},
	}, func(cfg dummyConfig) (HandlerFunc, error) {
		return func(ctx context.Context, c *app.RequestContext) {}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 5. Decode
	var out dummyConfig
	err = Decode(map[string]any{"header": "decoded-val"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.Header != "decoded-val" {
		t.Errorf("expected decoded-val, got %q", out.Header)
	}
}
