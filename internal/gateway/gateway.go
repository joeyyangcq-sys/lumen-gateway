package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/joey/lumen-gateway/internal/config"
	"github.com/joey/lumen-gateway/internal/plugin"
	"github.com/joey/lumen-gateway/internal/plugin/builtin"
	"github.com/joey/lumen-gateway/internal/router"
)

type Gateway struct {
	options  config.Options
	server   *server.Hertz
	snapshot atomic.Pointer[RuntimeSnapshot]
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
}

type Upstream struct {
	ID        string
	Options   config.UpstreamOptions
	Handlers  []app.HandlerFunc
	Endpoints []*Endpoint
}

type Endpoint struct {
	Address string
	Weight  uint32
	Tags    map[string]string
}

func New(options config.Options) (*Gateway, error) {
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

func (g *Gateway) ServeHTTP(ctx context.Context, c *app.RequestContext) {
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

	handlers := make([]app.HandlerFunc, 0)
	handlers = append(handlers, snapshot.GlobalHandlers...)
	handlers = append(handlers, snapshot.ServerHandlers...)
	handlers = append(handlers, snapshot.RouteHandlers[route.ID]...)
	handlers = append(handlers, func(nextCtx context.Context, next *app.RequestContext) {
		service := snapshot.Services[route.Service]
		if service == nil || service.Upstream == nil {
			next.SetStatusCode(503)
			return
		}
		serviceHandlers := make([]app.HandlerFunc, 0, len(service.Handlers)+len(service.Upstream.Handlers)+1)
		serviceHandlers = append(serviceHandlers, service.Handlers...)
		serviceHandlers = append(serviceHandlers, service.Upstream.Handlers...)
		serviceHandlers = append(serviceHandlers, func(_ context.Context, terminal *app.RequestContext) {
			terminal.String(
				200,
				"lumen route=%s service=%s upstream=%s host=%s path=%s query=%s",
				route.ID,
				service.ID,
				service.Upstream.ID,
				string(terminal.Host()),
				string(terminal.Path()),
				string(terminal.Request.URI().QueryArgs().QueryString()),
			)
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

	globalHandlers, err := buildPluginHandlers(registry, options, options.GlobalPlugins)
	if err != nil {
		return nil, err
	}

	serverHandlers := make([]app.HandlerFunc, 0)
	for _, serverOptions := range options.Servers {
		handlers, err := buildPluginHandlers(registry, options, serverOptions.Plugins)
		if err != nil {
			return nil, fmt.Errorf("server %q plugins: %w", serverOptions.ID, err)
		}
		serverHandlers = handlers
		break
	}

	upstreams := make(map[string]*Upstream, len(options.Upstreams))
	for id, upstreamOptions := range options.Upstreams {
		handlers, err := buildPluginHandlers(registry, options, upstreamOptions.Plugins)
		if err != nil {
			return nil, fmt.Errorf("upstream %q plugins: %w", id, err)
		}
		endpoints := make([]*Endpoint, 0, len(upstreamOptions.Endpoints))
		for _, endpointOptions := range upstreamOptions.Endpoints {
			weight := endpointOptions.Weight
			if weight == 0 {
				weight = 1
			}
			endpoints = append(endpoints, &Endpoint{
				Address: endpointOptions.Address,
				Weight:  weight,
				Tags:    endpointOptions.Tags,
			})
		}
		upstreams[id] = &Upstream{
			ID:        id,
			Options:   upstreamOptions,
			Handlers:  handlers,
			Endpoints: endpoints,
		}
	}

	services := make(map[string]*Service, len(options.Services))
	for id, serviceOptions := range options.Services {
		handlers, err := buildPluginHandlers(registry, options, serviceOptions.Plugins)
		if err != nil {
			return nil, fmt.Errorf("service %q plugins: %w", id, err)
		}
		services[id] = &Service{
			ID:       id,
			Options:  serviceOptions,
			Handlers: handlers,
			Upstream: upstreams[serviceOptions.Upstream],
		}
	}

	r := router.New()
	routeHandlers := make(map[string][]app.HandlerFunc, len(options.Routes))
	for id, routeOptions := range options.Routes {
		routeOptions.ID = id
		if err := r.Add(routeOptions); err != nil {
			return nil, err
		}
		handlers, err := buildPluginHandlers(registry, options, routeOptions.Plugins)
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

func buildPluginHandlers(
	registry *plugin.Registry,
	options config.Options,
	refs []config.PluginRef,
) ([]app.HandlerFunc, error) {
	handlers := make([]app.HandlerFunc, 0, len(refs))
	for _, ref := range refs {
		name := ref.Name
		params := ref.Params
		if ref.Use != "" {
			definition := options.Plugins[ref.Use]
			name = definition.Name
			params = definition.Params
		}
		factory := registry.Factory(name)
		if factory == nil {
			return nil, fmt.Errorf("plugin %q is not registered", name)
		}
		handler, err := factory(params)
		if err != nil {
			return nil, fmt.Errorf("plugin %q build failed: %w", name, err)
		}
		handlers = append(handlers, handler)
	}
	return handlers, nil
}
