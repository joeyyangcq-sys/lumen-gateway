package gateway

import (
	"fmt"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/balancer"
	"github.com/joey/lumen-gateway/internal/balancer/roundrobin"
	"github.com/joey/lumen-gateway/internal/config"
	"github.com/joey/lumen-gateway/internal/plugin"
	"github.com/joey/lumen-gateway/internal/plugin/builtin"
	"github.com/joey/lumen-gateway/internal/proxy"
	"github.com/joey/lumen-gateway/internal/router"
)

type RuntimeCompiler interface {
	Compile(options config.Options) (*RuntimeSnapshot, error)
}

type RegistryFactory func() (*plugin.Registry, error)

type BalancerFactory func(options config.UpstreamOptions, endpoints []balancer.Endpoint) (balancer.Balancer, error)

type ProxyFactory func(service config.ServiceOptions, upstream *Upstream) (proxy.Proxy, error)

type CompilerOption func(*Compiler)

type Compiler struct {
	registryFactory RegistryFactory
	balancerFactory BalancerFactory
	proxyFactory    ProxyFactory
}

func NewRuntimeCompiler(opts ...CompilerOption) *Compiler {
	compiler := &Compiler{
		registryFactory: defaultRegistryFactory,
		balancerFactory: defaultBalancerFactory,
		proxyFactory:    defaultProxyFactory,
	}
	for _, opt := range opts {
		opt(compiler)
	}
	return compiler
}

func WithRegistryFactory(factory RegistryFactory) CompilerOption {
	return func(c *Compiler) {
		if factory != nil {
			c.registryFactory = factory
		}
	}
}

func WithBalancerFactory(factory BalancerFactory) CompilerOption {
	return func(c *Compiler) {
		if factory != nil {
			c.balancerFactory = factory
		}
	}
}

func WithProxyFactory(factory ProxyFactory) CompilerOption {
	return func(c *Compiler) {
		if factory != nil {
			c.proxyFactory = factory
		}
	}
}

// BuildRegistry builds and returns the plugin registry using the configured
// registry factory. Useful for inspecting available plugins without performing
// a full gateway compile — for example, to populate an admin-API catalog.
func (c *Compiler) BuildRegistry() (*plugin.Registry, error) {
	return c.registryFactory()
}

func (c *Compiler) Compile(options config.Options) (*RuntimeSnapshot, error) {
	registry, err := c.registryFactory()
	if err != nil {
		return nil, err
	}
	compiled := false
	defer func() {
		if !compiled {
			_ = registry.Close()
		}
	}()

	globalHandlers, err := buildPluginHandlers(registry, options, plugin.ScopeGlobal, options.GlobalPlugins)
	if err != nil {
		return nil, err
	}

	serverHandlers := make([]app.HandlerFunc, 0)
	for _, serverOptions := range options.Servers {
		handlers, err := buildPluginHandlers(registry, options, plugin.ScopeServer, serverOptions.Plugins)
		if err != nil {
			return nil, fmt.Errorf("server %q plugins: %w", serverOptions.ID, err)
		}
		serverHandlers = handlers
		break
	}

	upstreams := make(map[string]*Upstream, len(options.Upstreams))
	for id, upstreamOptions := range options.Upstreams {
		handlers, err := buildPluginHandlers(registry, options, plugin.ScopeUpstream, upstreamOptions.Plugins)
		if err != nil {
			return nil, fmt.Errorf("upstream %q plugins: %w", id, err)
		}
		endpoints := make([]*Endpoint, 0, len(upstreamOptions.Endpoints))
		balancerEndpoints := make([]balancer.Endpoint, 0, len(upstreamOptions.Endpoints))
		for _, endpointOptions := range upstreamOptions.Endpoints {
			weight := endpointOptions.Weight
			if weight == 0 {
				weight = 1
			}
			endpoint := &Endpoint{
				Addr:      endpointOptions.Address,
				Load:      weight,
				Tags:      endpointOptions.Tags,
				Available: true,
			}
			endpoints = append(endpoints, endpoint)
			balancerEndpoints = append(balancerEndpoints, endpoint)
		}
		upstreamBalancer, err := c.balancerFactory(upstreamOptions, balancerEndpoints)
		if err != nil {
			return nil, fmt.Errorf("upstream %q balancer: %w", id, err)
		}
		upstreams[id] = &Upstream{
			ID:        id,
			Options:   upstreamOptions,
			Handlers:  handlers,
			Endpoints: endpoints,
			Balancer:  upstreamBalancer,
		}
	}

	services := make(map[string]*Service, len(options.Services))
	for id, serviceOptions := range options.Services {
		handlers, err := buildPluginHandlers(registry, options, plugin.ScopeService, serviceOptions.Plugins)
		if err != nil {
			return nil, fmt.Errorf("service %q plugins: %w", id, err)
		}
		upstream := upstreams[serviceOptions.Upstream]
		serviceProxy, err := c.proxyFactory(serviceOptions, upstream)
		if err != nil {
			return nil, fmt.Errorf("service %q proxy: %w", id, err)
		}
		services[id] = &Service{
			ID:       id,
			Options:  serviceOptions,
			Handlers: handlers,
			Upstream: upstream,
			Proxy:    serviceProxy,
		}
		services[id].ExecutionHandlers = buildServiceExecutionHandlers(services[id])
	}

	r := router.New()
	routeHandlers := make(map[string][]app.HandlerFunc, len(options.Routes))
	for id, routeOptions := range options.Routes {
		routeOptions.ID = id
		if err := r.Add(routeOptions); err != nil {
			return nil, err
		}
		handlers, err := buildPluginHandlers(registry, options, plugin.ScopeRoute, routeOptions.Plugins)
		if err != nil {
			return nil, fmt.Errorf("route %q plugins: %w", id, err)
		}
		routeHandlers[id] = buildRouteExecutionHandlers(
			globalHandlers,
			serverHandlers,
			handlers,
			services[routeOptions.Service],
		)
	}

	compiled = true
	snapshot := newRuntimeSnapshot(registry)
	snapshot.Router = r
	snapshot.GlobalHandlers = globalHandlers
	snapshot.ServerHandlers = serverHandlers
	snapshot.RouteHandlers = routeHandlers
	snapshot.Services = services
	snapshot.Upstreams = upstreams
	return snapshot, nil
}

func defaultRegistryFactory() (*plugin.Registry, error) {
	registry := plugin.NewRegistry()
	if err := builtin.Register(registry); err != nil {
		return nil, err
	}
	return registry, nil
}

func defaultBalancerFactory(options config.UpstreamOptions, endpoints []balancer.Endpoint) (balancer.Balancer, error) {
	switch options.Balancer.Type {
	case "", "round_robin":
		return roundrobin.New(endpoints, options.Balancer.Params)
	default:
		return nil, fmt.Errorf("balancer type %q is not supported", options.Balancer.Type)
	}
}

func defaultProxyFactory(service config.ServiceOptions, upstream *Upstream) (proxy.Proxy, error) {
	return proxy.NewHTTP(proxy.HTTPOptions{
		Timeout: mergedTimeout(service, upstream),
	}), nil
}
