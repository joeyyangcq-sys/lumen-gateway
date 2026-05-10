package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/joey/lumen-gateway/internal/balancer"
	"github.com/joey/lumen-gateway/internal/balancer/roundrobin"
	"github.com/joey/lumen-gateway/internal/config"
	"github.com/joey/lumen-gateway/internal/observability"
	"github.com/joey/lumen-gateway/internal/plugin"
	"github.com/joey/lumen-gateway/internal/plugin/builtin"
	"github.com/joey/lumen-gateway/internal/proxy"
	"github.com/joey/lumen-gateway/internal/router"
)

type Gateway struct {
	options  config.Options
	server   *server.Hertz
	admin    AdminHandler
	snapshot atomic.Pointer[RuntimeSnapshot]
}

type AdminHandler interface {
	ServeHTTP(ctx context.Context, c *app.RequestContext) bool
}

type RuntimeSnapshot struct {
	Router         *router.Router
	GlobalHandlers []app.HandlerFunc
	ServerHandlers []app.HandlerFunc
	RouteHandlers  map[string][]app.HandlerFunc
	Services       map[string]*Service
	Upstreams      map[string]*Upstream
}

type Service struct {
	ID       string
	Options  config.ServiceOptions
	Handlers []app.HandlerFunc
	Upstream *Upstream
	Proxy    proxy.Proxy
}

type Upstream struct {
	ID        string
	Options   config.UpstreamOptions
	Handlers  []app.HandlerFunc
	Endpoints []*Endpoint
	Balancer  balancer.Balancer
}

type Endpoint struct {
	Addr      string
	Load      uint32
	Tags      map[string]string
	Available bool
}

func (e *Endpoint) Address() string {
	return e.Addr
}

func (e *Endpoint) Weight() uint32 {
	return e.Load
}

func (e *Endpoint) IsAvailable() bool {
	return e.Available
}

func New(options config.Options, adminHandlers ...AdminHandler) (*Gateway, error) {
	snapshot, err := BuildSnapshot(options)
	if err != nil {
		return nil, err
	}

	listen := ""
	for _, serverOptions := range options.Servers {
		listen = serverOptions.Listen
		break
	}
	if listen == "" {
		return nil, errors.New("server listen cannot be empty")
	}

	h := server.Default(server.WithHostPorts(listen))
	gw := &Gateway{
		options: options,
		server:  h,
	}
	if len(adminHandlers) > 0 {
		gw.admin = adminHandlers[0]
	}
	gw.snapshot.Store(snapshot)

	h.Any("/*path", gw.ServeHTTP)
	return gw, nil
}

func (g *Gateway) Run() error {
	slog.Info("lumen gateway is listening")
	g.server.Spin()
	return nil
}

func (g *Gateway) Shutdown() error {
	if g.server == nil {
		return nil
	}
	return g.server.Shutdown(context.Background())
}

func (g *Gateway) Reload(options config.Options) error {
	snapshot, err := BuildSnapshot(options)
	if err != nil {
		return err
	}
	g.snapshot.Store(snapshot)
	return nil
}

func (g *Gateway) ServeHTTP(ctx context.Context, c *app.RequestContext) {
	if g.admin != nil && g.admin.ServeHTTP(ctx, c) {
		return
	}
	if string(c.Path()) == "/metrics" {
		c.Response.Header.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		c.Response.SetStatusCode(200)
		c.Response.SetBodyString(observability.Default().RenderPrometheus())
		return
	}

	snapshot := g.snapshot.Load()
	if snapshot == nil || snapshot.Router == nil {
		c.SetStatusCode(503)
		return
	}

	route, ok := snapshot.Router.Match(string(c.Method()), string(c.Host()), string(c.Path()))
	if !ok {
		c.SetStatusCode(404)
		return
	}
	plugin.SetRouteID(c, route.ID)
	plugin.SetServiceID(c, route.Service)
	if service := snapshot.Services[route.Service]; service != nil && service.Upstream != nil {
		plugin.SetUpstreamID(c, service.Upstream.ID)
	}
	plugin.SetPhase(c, "request")

	handlers := make([]app.HandlerFunc, 0)
	handlers = append(handlers, snapshot.GlobalHandlers...)
	handlers = append(handlers, snapshot.ServerHandlers...)
	handlers = append(handlers, snapshot.RouteHandlers[route.ID]...)
	handlers = append(handlers, func(nextCtx context.Context, next *app.RequestContext) {
		service := snapshot.Services[route.Service]
		if service == nil || service.Upstream == nil || service.Proxy == nil {
			next.SetStatusCode(503)
			return
		}
		plugin.SetUpstreamID(next, service.Upstream.ID)
		serviceHandlers := make([]app.HandlerFunc, 0, len(service.Handlers)+len(service.Upstream.Handlers)+1)
		serviceHandlers = append(serviceHandlers, service.Handlers...)
		serviceHandlers = append(serviceHandlers, service.Upstream.Handlers...)
		serviceHandlers = append(serviceHandlers, func(_ context.Context, terminal *app.RequestContext) {
			endpoint, err := service.Upstream.Balancer.Pick(nextCtx)
			if err != nil {
				terminal.SetStatusCode(503)
				return
			}

			target := resolveTarget(service, service.Upstream, endpoint, terminal)
			plugin.SetEndpointAddress(terminal, endpoint.Address())
			plugin.SetUpstreamHost(terminal, target.Host)
			plugin.SetPhase(terminal, "proxy")
			if err := service.Proxy.ServeHTTP(nextCtx, terminal, target); err != nil {
				plugin.SetGatewayError(terminal, err)
				switch {
				case errors.Is(err, proxy.ErrTimeout):
					terminal.SetStatusCode(504)
				case errors.Is(err, proxy.ErrInvalidTarget):
					terminal.SetStatusCode(502)
				default:
					terminal.SetStatusCode(502)
				}
			}
			plugin.SetPhase(terminal, "response")
		})
		next.SetIndex(-1)
		next.SetHandlers(serviceHandlers)
		next.Next(nextCtx)
	})

	c.SetIndex(-1)
	c.SetHandlers(handlers)
	c.Next(ctx)
	c.Abort()
}

func BuildSnapshot(options config.Options) (*RuntimeSnapshot, error) {
	registry := plugin.NewRegistry()
	if err := builtin.Register(registry); err != nil {
		return nil, err
	}

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
		upstreamBalancer, err := buildBalancer(upstreamOptions, balancerEndpoints)
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
		services[id] = &Service{
			ID:       id,
			Options:  serviceOptions,
			Handlers: handlers,
			Upstream: upstreams[serviceOptions.Upstream],
			Proxy: proxy.NewHTTP(proxy.HTTPOptions{
				Timeout: mergedTimeout(serviceOptions, upstreams[serviceOptions.Upstream]),
			}),
		}
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
		routeHandlers[id] = handlers
	}

	return &RuntimeSnapshot{
		Router:         r,
		GlobalHandlers: globalHandlers,
		ServerHandlers: serverHandlers,
		RouteHandlers:  routeHandlers,
		Services:       services,
		Upstreams:      upstreams,
	}, nil
}

func mergedTimeout(service config.ServiceOptions, upstream *Upstream) config.TimeoutOptions {
	timeout := service.Timeout
	if upstream == nil {
		return timeout
	}
	if timeout.Connect == 0 {
		timeout.Connect = upstream.Options.Timeout.Connect
	}
	if timeout.Read == 0 {
		timeout.Read = upstream.Options.Timeout.Read
	}
	if timeout.Write == 0 {
		timeout.Write = upstream.Options.Timeout.Write
	}
	return timeout
}

func buildBalancer(options config.UpstreamOptions, endpoints []balancer.Endpoint) (balancer.Balancer, error) {
	switch options.Balancer.Type {
	case "", "round_robin":
		return roundrobin.New(endpoints, options.Balancer.Params)
	default:
		return nil, fmt.Errorf("balancer type %q is not supported", options.Balancer.Type)
	}
}

func resolveTarget(service *Service, upstream *Upstream, endpoint balancer.Endpoint, c *app.RequestContext) proxy.Target {
	scheme := upstream.Options.Scheme
	if scheme == "" {
		scheme = service.Options.Protocol
	}
	if scheme == "" {
		scheme = "http"
	}

	return proxy.Target{
		Scheme:  scheme,
		Address: endpoint.Address(),
		Host:    resolveUpstreamHost(upstream.Options, endpoint, c),
	}
}

func resolveUpstreamHost(options config.UpstreamOptions, endpoint balancer.Endpoint, c *app.RequestContext) string {
	switch options.PassHost {
	case "", "pass":
		return string(c.Host())
	case "rewrite":
		return options.UpstreamHost
	case "node":
		host, _, err := net.SplitHostPort(endpoint.Address())
		if err == nil {
			return host
		}
		return endpoint.Address()
	default:
		return string(c.Host())
	}
}

func buildPluginHandlers(
	registry *plugin.Registry,
	options config.Options,
	scope plugin.Scope,
	refs []config.PluginRef,
) ([]app.HandlerFunc, error) {
	type builtPlugin struct {
		index    int
		priority int
		handler  app.HandlerFunc
	}

	built := make([]builtPlugin, 0, len(refs))
	for index, ref := range refs {
		name := ref.Name
		params := ref.Params
		if ref.Use != "" {
			definition := options.Plugins[ref.Use]
			name = definition.Name
			params = definition.Params
		}
		definition, ok := registry.Definition(name)
		if !ok {
			return nil, fmt.Errorf("plugin %q is not registered", name)
		}
		if !definition.Supports(scope) {
			return nil, fmt.Errorf("plugin %q does not support %s scope", name, scope)
		}
		handler, err := definition.Factory()(params)
		if err != nil {
			return nil, fmt.Errorf("plugin %q build failed: %w", name, err)
		}
		handler = wrapObservedPluginHandler(name, scope, handler)
		built = append(built, builtPlugin{
			index:    index,
			priority: definition.Metadata().Priority,
			handler:  handler,
		})
	}

	slices.SortStableFunc(built, func(left, right builtPlugin) int {
		if left.priority == right.priority {
			switch {
			case left.index < right.index:
				return -1
			case left.index > right.index:
				return 1
			default:
				return 0
			}
		}
		if left.priority > right.priority {
			return -1
		}
		return 1
	})

	handlers := make([]app.HandlerFunc, 0, len(built))
	for _, plugin := range built {
		handlers = append(handlers, plugin.handler)
	}
	return handlers, nil
}

func wrapObservedPluginHandler(name string, scope plugin.Scope, next app.HandlerFunc) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		pc := plugin.FromRequestContext(c)
		startPhase := pc.Phase()
		start := time.Now()
		next(ctx, c)

		result := "ok"
		if pc.GatewayError() != nil {
			result = "error"
		} else if plugin.IsAborted(c) {
			result = "aborted"
		}

		observability.Default().ObservePlugin(observability.PluginLabels{
			Plugin:     name,
			Scope:      string(scope),
			Phase:      startPhase,
			RouteID:    pc.RouteID(),
			ServiceID:  pc.ServiceID(),
			UpstreamID: pc.UpstreamID(),
			Result:     result,
		}, time.Since(start))
	}
}
