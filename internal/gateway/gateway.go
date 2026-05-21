package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/joey/lumen-gateway/internal/balancer"
	"github.com/joey/lumen-gateway/internal/config"
	"github.com/joey/lumen-gateway/internal/observability"
	"github.com/joey/lumen-gateway/internal/plugin"
	"github.com/joey/lumen-gateway/internal/proxy"
	"github.com/joey/lumen-gateway/internal/router"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Gateway struct {
	options  config.Options
	server   *server.Hertz
	admin    AdminHandler
	compiler RuntimeCompiler
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
	return NewWithCompiler(options, NewRuntimeCompiler(), adminHandlers...)
}

func NewWithCompiler(options config.Options, compiler RuntimeCompiler, adminHandlers ...AdminHandler) (*Gateway, error) {
	if compiler == nil {
		compiler = NewRuntimeCompiler()
	}

	snapshot, err := compiler.Compile(options)
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
		options:  options,
		server:   h,
		compiler: compiler,
	}
	if len(adminHandlers) > 0 {
		gw.admin = adminHandlers[0]
	}
	gw.snapshot.Store(snapshot)

	// Health check endpoint — used by Docker and load balancers.
	h.GET("/health", func(_ context.Context, c *app.RequestContext) {
		startedAt := time.Now()
		c.Response.SetStatusCode(200)
		c.Response.SetBodyRaw([]byte("ok"))
		observability.Default().ObserveGateway(observability.GatewayLabels{
			Handler:     "health",
			Method:      string(c.Method()),
			StatusClass: statusClass(c.Response.StatusCode()),
		}, time.Since(startedAt))
	})
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
	compiler := g.compiler
	if compiler == nil {
		compiler = NewRuntimeCompiler()
	}
	snapshot, err := compiler.Compile(options)
	if err != nil {
		return err
	}
	g.snapshot.Store(snapshot)
	return nil
}

func (g *Gateway) ServeHTTP(ctx context.Context, c *app.RequestContext) {
	startedAt := time.Now()
	handler := "unmatched"
	routeID := ""
	defer func() {
		observability.Default().ObserveGateway(observability.GatewayLabels{
			Handler:     handler,
			Method:      string(c.Method()),
			RouteID:     routeID,
			StatusClass: statusClass(c.Response.StatusCode()),
		}, time.Since(startedAt))
	}()

	if g.admin != nil && g.admin.ServeHTTP(ctx, c) {
		handler = "admin"
		return
	}
	if string(c.Path()) == "/metrics" {
		handler = "metrics"
		c.Response.Header.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		c.Response.SetStatusCode(200)
		lumenMetrics := observability.Default().RenderPrometheus()
		stdMetrics := collectStdPromMetrics()
		c.Response.SetBodyString(lumenMetrics + stdMetrics)
		return
	}

	snapshot := g.snapshot.Load()
	if snapshot == nil || snapshot.Router == nil {
		handler = "unavailable"
		c.SetStatusCode(503)
		return
	}

	route, ok := snapshot.Router.Match(string(c.Method()), string(c.Host()), string(c.Path()))
	if !ok {
		c.SetStatusCode(404)
		return
	}
	handler = "proxy"
	routeID = route.ID
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

func statusClass(status int) string {
	if status <= 0 {
		return ""
	}
	return fmt.Sprintf("%dxx", status/100)
}

func BuildSnapshot(options config.Options) (*RuntimeSnapshot, error) {
	return NewRuntimeCompiler().Compile(options)
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
			slog.Warn("skipping unregistered plugin", "name", name, "scope", scope)
			continue
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

func collectStdPromMetrics() string {
	rec := http.NewServeMux()
	rec.Handle("/metrics", promhttp.Handler())
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	w := &metricsResponseWriter{}
	rec.ServeHTTP(w, req)
	return w.body.String()
}

type metricsResponseWriter struct {
	body       bytes.Buffer
	statusCode int
}

func (w *metricsResponseWriter) Header() http.Header         { return http.Header{} }
func (w *metricsResponseWriter) WriteHeader(statusCode int)  { w.statusCode = statusCode }
func (w *metricsResponseWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
