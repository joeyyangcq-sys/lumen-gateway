package plugin

import (
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/cloudwego/hertz/pkg/app"
	"gopkg.in/yaml.v3"
)

type Scope string

const (
	ScopeGlobal   Scope = "global"
	ScopeServer   Scope = "server"
	ScopeRoute    Scope = "route"
	ScopeService  Scope = "service"
	ScopeUpstream Scope = "upstream"
)

type Metadata struct {
	Name     string
	Scopes   []Scope
	Priority int
}

type Factory func(params any) (app.HandlerFunc, error)

type Definition struct {
	factory  Factory
	metadata Metadata
}

func (d Definition) Metadata() Metadata {
	return d.metadata
}

func (d Definition) Factory() Factory {
	return d.factory
}

func (d Definition) Supports(scope Scope) bool {
	return supportsScope(d.metadata.Scopes, scope)
}

type Registry struct {
	definitions map[string]Definition
	closers     []io.Closer
}

func NewRegistry() *Registry {
	return &Registry{
		definitions: make(map[string]Definition),
		closers:     make([]io.Closer, 0),
	}
}

func (r *Registry) Register(metadata Metadata, factory Factory) error {
	if metadata.Name == "" {
		return errors.New("plugin name cannot be empty")
	}
	if factory == nil {
		return fmt.Errorf("plugin %q factory cannot be nil", metadata.Name)
	}
	if len(metadata.Scopes) == 0 {
		return fmt.Errorf("plugin %q scopes cannot be empty", metadata.Name)
	}
	if _, ok := r.definitions[metadata.Name]; ok {
		return fmt.Errorf("plugin %q already exists", metadata.Name)
	}
	r.definitions[metadata.Name] = Definition{
		metadata: metadata,
		factory:  factory,
	}
	return nil
}

func RegisterTyped[T any](r *Registry, metadata Metadata, factory func(T) (app.HandlerFunc, error)) error {
	return r.Register(metadata, func(params any) (app.HandlerFunc, error) {
		var cfg T
		if err := Decode(params, &cfg); err != nil {
			return nil, err
		}
		return factory(cfg)
	})
}

func (r *Registry) Definition(name string) (Definition, bool) {
	definition, ok := r.definitions[name]
	return definition, ok
}

func (r *Registry) Factory(name string) Factory {
	definition, ok := r.Definition(name)
	if !ok {
		return nil
	}
	return definition.Factory()
}

// Definitions returns all registered plugin definitions. The order is
// non-deterministic (map iteration). Callers that need a stable order should
// sort by Name after calling this method.
func (r *Registry) Definitions() []Definition {
	defs := make([]Definition, 0, len(r.definitions))
	for _, def := range r.definitions {
		defs = append(defs, def)
	}
	return defs
}

func (r *Registry) addCloser(closer io.Closer) {
	if closer == nil {
		return
	}
	r.closers = append(r.closers, closer)
}

func (r *Registry) Close() error {
	if r == nil {
		return nil
	}

	var err error
	for i := len(r.closers) - 1; i >= 0; i-- {
		if closeErr := r.closers[i].Close(); err == nil {
			err = closeErr
		}
	}
	r.closers = nil
	return err
}

func AllScopes() []Scope {
	return []Scope{
		ScopeGlobal,
		ScopeServer,
		ScopeRoute,
		ScopeService,
		ScopeUpstream,
	}
}

func supportsScope(scopes []Scope, scope Scope) bool {
	return slices.Contains(scopes, scope)
}

func Decode(params any, out any) error {
	if params == nil {
		return nil
	}
	data, err := yaml.Marshal(params)
	if err != nil {
		return fmt.Errorf("failed to encode plugin params: %w", err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to decode plugin params: %w", err)
	}
	return nil
}
