package plugin

import (
	"errors"
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"
	"gopkg.in/yaml.v3"
)

type Factory func(params any) (app.HandlerFunc, error)

type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
	}
}

func (r *Registry) Register(name string, factory Factory) error {
	if name == "" {
		return errors.New("plugin name cannot be empty")
	}
	if factory == nil {
		return fmt.Errorf("plugin %q factory cannot be nil", name)
	}
	if _, ok := r.factories[name]; ok {
		return fmt.Errorf("plugin %q already exists", name)
	}
	r.factories[name] = factory
	return nil
}

func (r *Registry) Factory(name string) Factory {
	return r.factories[name]
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
