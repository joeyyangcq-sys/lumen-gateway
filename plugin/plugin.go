// Package plugin exposes the public API for developing lumen-gateway plugins.
//
// External plugin packages should import this package (not internal/plugin) and
// provide a registration function with the signature:
//
//	func Register(r *plugin.Registry) error
//
// Then pass it to lumen.WithPlugins when building the binary:
//
//	lumen.Run(lumen.WithPlugins(myplugins.Register))
package plugin

import (
	"github.com/cloudwego/hertz/pkg/app"
	internalplugin "github.com/joey/lumen-gateway/internal/plugin"
)

// HandlerFunc is the raw Hertz handler type. Most plugins should use
// RegisterTypedContext instead of working with this directly.
type HandlerFunc = app.HandlerFunc

// Re-export core types as aliases so external packages can reference them
// without importing the internal path.
type (
	// Registry holds all registered plugin definitions. Pass a *Registry to
	// RegisterTypedContext or Register to add custom plugins.
	Registry = internalplugin.Registry

	// Metadata describes a plugin: its name, execution priority (higher runs
	// first), and the scopes it supports.
	Metadata = internalplugin.Metadata

	// Scope controls where a plugin may be attached (global, server, route,
	// service, upstream).
	Scope = internalplugin.Scope

	// Factory is the low-level plugin constructor: given raw params it returns
	// an http handler. Prefer RegisterTypedContext which handles decoding.
	Factory = internalplugin.Factory

	// ContextHandler is the typed handler signature used by RegisterTypedContext.
	// Receives a PluginContext and a next() to continue the chain.
	ContextHandler = internalplugin.ContextHandler

	// PluginContext provides read/write access to the current HTTP
	// request/response without exposing the underlying framework.
	PluginContext = internalplugin.PluginContext
)

// Scope constants — use these when declaring plugin Metadata.
const (
	ScopeGlobal   = internalplugin.ScopeGlobal
	ScopeServer   = internalplugin.ScopeServer
	ScopeRoute    = internalplugin.ScopeRoute
	ScopeService  = internalplugin.ScopeService
	ScopeUpstream = internalplugin.ScopeUpstream
)

// AllScopes returns every available scope. Convenient for plugins that work at
// any level.
func AllScopes() []Scope { return internalplugin.AllScopes() }

// RegisterTypedContext is the recommended way to register a plugin. T is your
// config struct; the framework decodes plugin params into T automatically.
//
//	type MyConfig struct {
//	    Header string `yaml:"header"`
//	}
//
//	plugin.RegisterTypedContext(r, plugin.Metadata{
//	    Name:     "my-plugin",
//	    Priority: 100,
//	    Scopes:   plugin.AllScopes(),
//	}, func(cfg MyConfig) (plugin.ContextHandler, error) {
//	    return func(ctx context.Context, pc plugin.PluginContext) {
//	        pc.SetRequestHeader(cfg.Header, "injected")
//	        pc.Next(ctx)
//	    }, nil
//	})
func RegisterTypedContext[T any](r *Registry, meta Metadata, factory func(T) (ContextHandler, error)) error {
	return internalplugin.RegisterTypedContext[T](r, meta, factory)
}

// RegisterTyped registers a plugin whose factory returns a raw handler func.
// Prefer RegisterTypedContext for new plugins.
// RegisterTyped registers a plugin whose factory returns a raw HandlerFunc.
// Prefer RegisterTypedContext for new plugins.
func RegisterTyped[T any](r *Registry, meta Metadata, factory func(T) (HandlerFunc, error)) error {
	return internalplugin.RegisterTyped[T](r, meta, factory)
}

// Decode decodes plugin params (any — typically map[string]any from YAML/JSON)
// into a typed config struct. Called automatically by RegisterTypedContext.
func Decode(params any, out any) error {
	return internalplugin.Decode(params, out)
}
