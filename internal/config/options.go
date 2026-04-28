package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Options struct {
	GlobalPlugins []PluginRef                `yaml:"global_plugins"`
	Servers       map[string]ServerOptions   `yaml:"servers"`
	Routes        map[string]RouteOptions    `yaml:"routes"`
	Services      map[string]ServiceOptions  `yaml:"services"`
	Upstreams     map[string]UpstreamOptions `yaml:"upstreams"`
	Plugins       map[string]PluginOptions   `yaml:"plugins"`
	Logging       LoggingOptions             `yaml:"logging"`
}

type LoggingOptions struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type ServerOptions struct {
	ID      string      `yaml:"-"`
	Listen  string      `yaml:"listen"`
	Plugins []PluginRef `yaml:"plugins"`
}

type RouteOptions struct {
	ID      string      `yaml:"-"`
	Hosts   []string    `yaml:"hosts"`
	Methods []string    `yaml:"methods"`
	Paths   []string    `yaml:"paths"`
	Service string      `yaml:"service"`
	Plugins []PluginRef `yaml:"plugins"`
}

type ServiceOptions struct {
	ID       string         `yaml:"-"`
	Protocol string         `yaml:"protocol"`
	Upstream string         `yaml:"upstream"`
	Timeout  TimeoutOptions `yaml:"timeout"`
	Plugins  []PluginRef    `yaml:"plugins"`
}

type TimeoutOptions struct {
	Connect time.Duration `yaml:"connect"`
	Read    time.Duration `yaml:"read"`
	Write   time.Duration `yaml:"write"`
}

type UpstreamOptions struct {
	ID          string             `yaml:"-"`
	Balancer    BalancerOptions    `yaml:"balancer"`
	HealthCheck HealthCheckOptions `yaml:"health_check"`
	Endpoints   []EndpointOptions  `yaml:"endpoints"`
	Plugins     []PluginRef        `yaml:"plugins"`
}

type BalancerOptions struct {
	Type   string `yaml:"type"`
	Params any    `yaml:"params"`
}

type HealthCheckOptions struct {
	Passive PassiveHealthOptions `yaml:"passive"`
	Active  ActiveHealthOptions  `yaml:"active"`
}

type PassiveHealthOptions struct {
	MaxFails    uint          `yaml:"max_fails"`
	FailTimeout time.Duration `yaml:"fail_timeout"`
}

type ActiveHealthOptions struct {
	Path     string        `yaml:"path"`
	Method   string        `yaml:"method"`
	Interval time.Duration `yaml:"interval"`
}

type EndpointOptions struct {
	Address string            `yaml:"address"`
	Weight  uint32            `yaml:"weight"`
	Tags    map[string]string `yaml:"tags"`
}

type PluginOptions struct {
	ID     string `yaml:"-"`
	Name   string `yaml:"name"`
	Params any    `yaml:"params"`
}

type PluginRef struct {
	Use    string `yaml:"use"`
	Name   string `yaml:"name"`
	Params any    `yaml:"params"`
}

func Load(path string) (Options, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Options{}, err
	}

	options := Options{}
	if err := yaml.Unmarshal(data, &options); err != nil {
		return Options{}, err
	}

	options.fillIDs()
	if err := options.Validate(); err != nil {
		return Options{}, err
	}

	return options, nil
}

func (o *Options) fillIDs() {
	for id, server := range o.Servers {
		server.ID = id
		o.Servers[id] = server
	}
	for id, route := range o.Routes {
		route.ID = id
		o.Routes[id] = route
	}
	for id, service := range o.Services {
		service.ID = id
		o.Services[id] = service
	}
	for id, upstream := range o.Upstreams {
		upstream.ID = id
		o.Upstreams[id] = upstream
	}
	for id, plugin := range o.Plugins {
		plugin.ID = id
		o.Plugins[id] = plugin
	}
}

func (o Options) Validate() error {
	if len(o.Servers) == 0 {
		return errors.New("at least one server is required")
	}
	if len(o.Routes) == 0 {
		return errors.New("at least one route is required")
	}

	for id, server := range o.Servers {
		if server.Listen == "" {
			return fmt.Errorf("server %q listen cannot be empty", id)
		}
		if err := o.validatePluginRefs("server "+id, server.Plugins); err != nil {
			return err
		}
	}

	if err := o.validatePluginRefs("global_plugins", o.GlobalPlugins); err != nil {
		return err
	}

	for id, route := range o.Routes {
		if len(route.Paths) == 0 {
			return fmt.Errorf("route %q paths cannot be empty", id)
		}
		if route.Service == "" {
			return fmt.Errorf("route %q service cannot be empty", id)
		}
		if _, ok := o.Services[route.Service]; !ok {
			return fmt.Errorf("route %q references unknown service %q", id, route.Service)
		}
		if err := o.validatePluginRefs("route "+id, route.Plugins); err != nil {
			return err
		}
	}

	for id, service := range o.Services {
		if service.Protocol == "" {
			service.Protocol = "http"
		}
		if service.Upstream == "" {
			return fmt.Errorf("service %q upstream cannot be empty", id)
		}
		if _, ok := o.Upstreams[service.Upstream]; !ok {
			return fmt.Errorf("service %q references unknown upstream %q", id, service.Upstream)
		}
		if err := o.validatePluginRefs("service "+id, service.Plugins); err != nil {
			return err
		}
	}

	for id, upstream := range o.Upstreams {
		if len(upstream.Endpoints) == 0 {
			return fmt.Errorf("upstream %q endpoints cannot be empty", id)
		}
		for _, endpoint := range upstream.Endpoints {
			if endpoint.Address == "" {
				return fmt.Errorf("upstream %q endpoint address cannot be empty", id)
			}
		}
		if err := o.validatePluginRefs("upstream "+id, upstream.Plugins); err != nil {
			return err
		}
	}

	return nil
}

func (o Options) validatePluginRefs(owner string, refs []PluginRef) error {
	for _, ref := range refs {
		if ref.Use != "" {
			if _, ok := o.Plugins[ref.Use]; !ok {
				return fmt.Errorf("%s references unknown plugin %q", owner, ref.Use)
			}
			continue
		}
		if ref.Name == "" {
			return fmt.Errorf("%s plugin name cannot be empty", owner)
		}
	}
	return nil
}
