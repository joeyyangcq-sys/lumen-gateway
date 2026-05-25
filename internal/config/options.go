package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Options struct {
	Servers       map[string]ServerOptions   `yaml:"servers"`
	Routes        map[string]RouteOptions    `yaml:"routes"`
	Services      map[string]ServiceOptions  `yaml:"services"`
	Upstreams     map[string]UpstreamOptions `yaml:"upstreams"`
	Plugins       map[string]PluginOptions   `yaml:"plugins"`
	Logging       LoggingOptions             `yaml:"logging"`
	GlobalPlugins []PluginRef                `yaml:"global_plugins"`
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
	ID       string      `yaml:"-"`
	Service  string      `yaml:"service"`
	Hosts    []string    `yaml:"hosts"`
	Methods  []string    `yaml:"methods"`
	Paths    []string    `yaml:"paths"`
	Plugins  []PluginRef `yaml:"plugins"`
	Priority int         `yaml:"priority"`
}

type ServiceOptions struct {
	ID       string         `yaml:"-"`
	Protocol string         `yaml:"protocol"`
	Upstream string         `yaml:"upstream"`
	Plugins  []PluginRef    `yaml:"plugins"`
	Timeout  TimeoutOptions `yaml:"timeout"`
}

type TimeoutOptions struct {
	Connect time.Duration `yaml:"connect"`
	Read    time.Duration `yaml:"read"`
	Write   time.Duration `yaml:"write"`
}

type UpstreamOptions struct {
	Balancer     BalancerOptions    `yaml:"balancer"`
	ID           string             `yaml:"-"`
	Scheme       string             `yaml:"scheme"`
	PassHost     string             `yaml:"pass_host"`
	UpstreamHost string             `yaml:"upstream_host"`
	Endpoints    []EndpointOptions  `yaml:"endpoints"`
	Plugins      []PluginRef        `yaml:"plugins"`
	HealthCheck  HealthCheckOptions `yaml:"health_check"`
	Timeout      TimeoutOptions     `yaml:"timeout"`
}

type BalancerOptions struct {
	Params any    `yaml:"params"`
	Type   string `yaml:"type"`
}

type HealthCheckOptions struct {
	Active  ActiveHealthOptions  `yaml:"active"`
	Passive PassiveHealthOptions `yaml:"passive"`
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
	Tags    map[string]string `yaml:"tags"`
	Address string            `yaml:"address"`
	Weight  uint32            `yaml:"weight"`
}

type PluginOptions struct {
	Params any    `yaml:"params"`
	ID     string `yaml:"-"`
	Name   string `yaml:"name"`
}

type PluginRef struct {
	Params any    `yaml:"params"`
	Use    string `yaml:"use"`
	Name   string `yaml:"name"`
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

	if err := o.Logging.Validate(); err != nil {
		return err
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
		switch upstream.Scheme {
		case "", "http", "https":
		default:
			return fmt.Errorf("upstream %q scheme %q is not supported", id, upstream.Scheme)
		}
		switch upstream.PassHost {
		case "", "pass", "node", "rewrite":
		default:
			return fmt.Errorf("upstream %q pass_host %q is not supported", id, upstream.PassHost)
		}
		if upstream.PassHost == "rewrite" && upstream.UpstreamHost == "" {
			return fmt.Errorf("upstream %q upstream_host cannot be empty when pass_host=rewrite", id)
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

func (o LoggingOptions) Validate() error {
	switch strings.ToLower(strings.TrimSpace(o.Level)) {
	case "", "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("logging.level %q is not supported", o.Level)
	}

	switch strings.ToLower(strings.TrimSpace(o.Format)) {
	case "", "text", "json":
	default:
		return fmt.Errorf("logging.format %q is not supported", o.Format)
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
