package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/observability"
	"github.com/joey/lumen-gateway/internal/runtimectx"
)

type PluginContext interface {
	Raw() *app.RequestContext
	Next(context.Context)
	Abort()

	Value(string) (any, bool)
	SetValue(string, any)

	RouteID() string
	ServiceID() string
	UpstreamID() string
	UpstreamHost() string
	EndpointAddress() string
	Phase() string
	SetPhase(string)

	RequestID() string
	SetRequestID(string)
	RegexCaptures() []string
	SetRegexCaptures([]string)

	RequestMethod() string
	SetRequestMethod(string)
	RequestHost() string
	SetRequestHost(string)
	RequestPath() string
	SetRequestPath(string)
	RequestURI() string
	RequestQuery(string) string
	AddRequestQuery(string, string)
	SetRequestQuery(string, string)
	DelRequestQuery(string)
	RequestHeader(string) string
	AddRequestHeader(string, string)
	SetRequestHeader(string, string)
	DelRequestHeader(string)
	RequestBody() []byte
	SetRequestBody([]byte)

	ResponseStatus() int
	SetResponseStatus(int)
	ResponseHeader(string) string
	AddResponseHeader(string, string)
	SetResponseHeader(string, string)
	DelResponseHeader(string)
	ResponseBody() []byte
	SetResponseBody([]byte)

	ClientIP() string
	GatewayError() error
	SetGatewayError(error)
	UpstreamStatusCode() int
	SetUpstreamStatusCode(int)
	ProxyInfo() observability.ProxyInfo
	SetProxyInfo(observability.ProxyInfo)
}

type ContextHandler func(context.Context, PluginContext)

type hertzPluginContext struct {
	raw             *app.RequestContext
	routeID         string
	serviceID       string
	upstreamID      string
	upstreamHost    string
	endpointAddress string
	phase           string
	requestID       string
	regexCaptures   []string
	gatewayError    error
	upstreamStatus  int
	proxyInfo       observability.ProxyInfo
	aborted         bool
}

func WrapContextHandler(handler ContextHandler) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		handler(ctx, FromRequestContext(c))
	}
}

func RegisterContext(r *Registry, metadata Metadata, factory func(params any) (ContextHandler, error)) error {
	return r.Register(metadata, func(params any) (app.HandlerFunc, error) {
		handler, err := factory(params)
		if err != nil {
			return nil, err
		}
		return WrapContextHandler(handler), nil
	})
}

func RegisterTypedContext[T any](r *Registry, metadata Metadata, factory func(T) (ContextHandler, error)) error {
	return RegisterContext(r, metadata, func(params any) (ContextHandler, error) {
		var cfg T
		if err := Decode(params, &cfg); err != nil {
			return nil, err
		}
		return factory(cfg)
	})
}

func RegisterTypedContextWithCloser[T any](
	r *Registry,
	metadata Metadata,
	factory func(T) (ContextHandler, io.Closer, error),
) error {
	return RegisterContext(r, metadata, func(params any) (ContextHandler, error) {
		var cfg T
		if err := Decode(params, &cfg); err != nil {
			return nil, err
		}

		handler, closer, err := factory(cfg)
		if err != nil {
			if closer != nil {
				_ = closer.Close()
			}
			return nil, err
		}
		r.addCloser(closer)
		return handler, nil
	})
}

const pluginContextKey = "_lumen_plugin_context"

func FromRequestContext(c *app.RequestContext) PluginContext {
	if val, ok := c.Get(pluginContextKey); ok {
		if pc, ok := val.(PluginContext); ok {
			return pc
		}
	}
	pc := &hertzPluginContext{raw: c}
	c.Set(pluginContextKey, pc)
	return pc
}

func SetRouteID(c *app.RequestContext, routeID string) {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.routeID = routeID
	} else {
		pc.SetValue(runtimectx.RouteIDKey, routeID)
	}
}

func SetServiceID(c *app.RequestContext, serviceID string) {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.serviceID = serviceID
	} else {
		pc.SetValue(runtimectx.ServiceIDKey, serviceID)
	}
}

func SetUpstreamID(c *app.RequestContext, upstreamID string) {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.upstreamID = upstreamID
	} else {
		pc.SetValue(runtimectx.UpstreamIDKey, upstreamID)
	}
}

func SetUpstreamHost(c *app.RequestContext, upstreamHost string) {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.upstreamHost = upstreamHost
	} else {
		pc.SetValue(runtimectx.UpstreamHostKey, upstreamHost)
	}
}

func SetEndpointAddress(c *app.RequestContext, address string) {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.endpointAddress = address
	} else {
		pc.SetValue(runtimectx.EndpointAddrKey, address)
	}
}

func SetRequestID(c *app.RequestContext, requestID string) {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.requestID = requestID
	} else {
		pc.SetValue(runtimectx.RequestIDKey, requestID)
	}
}

func SetPhase(c *app.RequestContext, phase string) {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.phase = phase
	} else {
		pc.SetValue(runtimectx.PhaseKey, phase)
	}
}

func SetRegexCaptures(c *app.RequestContext, captures []string) {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.regexCaptures = captures
	} else {
		pc.SetValue(runtimectx.RegexCapturesKey, captures)
	}
}

func SetGatewayError(c *app.RequestContext, err error) {
	if err == nil {
		return
	}
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.gatewayError = err
	} else {
		pc.SetValue(runtimectx.GatewayErrorKey, err)
	}
}

func SetUpstreamStatusCode(c *app.RequestContext, status int) {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.upstreamStatus = status
	} else {
		pc.SetValue(runtimectx.UpstreamStatusKey, status)
	}
}

func SetProxyInfo(c *app.RequestContext, info observability.ProxyInfo) {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		hpc.proxyInfo = info
	} else {
		pc.SetValue(runtimectx.ProxyInfoKey, info)
	}
}

func (p *hertzPluginContext) Raw() *app.RequestContext {
	return p.raw
}

func (p *hertzPluginContext) Next(ctx context.Context) {
	p.raw.Next(ctx)
}

func (p *hertzPluginContext) Abort() {
	p.aborted = true
	p.raw.Set(runtimectx.PluginAbortKey, true)
	p.raw.Abort()
}

func (p *hertzPluginContext) Value(key string) (any, bool) {
	switch key {
	case runtimectx.RouteIDKey:
		return p.routeID, true
	case runtimectx.ServiceIDKey:
		return p.serviceID, true
	case runtimectx.UpstreamIDKey:
		return p.upstreamID, true
	case runtimectx.UpstreamHostKey:
		return p.upstreamHost, true
	case runtimectx.EndpointAddrKey:
		return p.endpointAddress, true
	case runtimectx.PhaseKey:
		return p.phase, true
	case runtimectx.RequestIDKey:
		return p.requestID, true
	case runtimectx.RegexCapturesKey:
		return p.regexCaptures, true
	case runtimectx.GatewayErrorKey:
		return p.gatewayError, true
	case runtimectx.UpstreamStatusKey:
		return p.upstreamStatus, true
	case runtimectx.ProxyInfoKey:
		return p.proxyInfo, true
	case runtimectx.PluginAbortKey:
		return p.aborted, true
	default:
		return p.raw.Get(key)
	}
}

func (p *hertzPluginContext) SetValue(key string, value any) {
	switch key {
	case runtimectx.RouteIDKey:
		if s, ok := value.(string); ok {
			p.routeID = s
		}
	case runtimectx.ServiceIDKey:
		if s, ok := value.(string); ok {
			p.serviceID = s
		}
	case runtimectx.UpstreamIDKey:
		if s, ok := value.(string); ok {
			p.upstreamID = s
		}
	case runtimectx.UpstreamHostKey:
		if s, ok := value.(string); ok {
			p.upstreamHost = s
		}
	case runtimectx.EndpointAddrKey:
		if s, ok := value.(string); ok {
			p.endpointAddress = s
		}
	case runtimectx.PhaseKey:
		if s, ok := value.(string); ok {
			p.phase = s
		}
	case runtimectx.RequestIDKey:
		if s, ok := value.(string); ok {
			p.requestID = s
		}
	case runtimectx.RegexCapturesKey:
		if s, ok := value.([]string); ok {
			p.regexCaptures = s
		}
	case runtimectx.GatewayErrorKey:
		if err, ok := value.(error); ok {
			p.gatewayError = err
		}
	case runtimectx.UpstreamStatusKey:
		if i, ok := value.(int); ok {
			p.upstreamStatus = i
		}
	case runtimectx.ProxyInfoKey:
		if info, ok := value.(observability.ProxyInfo); ok {
			p.proxyInfo = info
		}
	case runtimectx.PluginAbortKey:
		if b, ok := value.(bool); ok {
			p.aborted = b
		}
	default:
		p.raw.Set(key, value)
	}
}

func (p *hertzPluginContext) RouteID() string {
	return p.routeID
}

func (p *hertzPluginContext) ServiceID() string {
	return p.serviceID
}

func (p *hertzPluginContext) UpstreamID() string {
	return p.upstreamID
}

func (p *hertzPluginContext) UpstreamHost() string {
	return p.upstreamHost
}

func (p *hertzPluginContext) EndpointAddress() string {
	return p.endpointAddress
}

func (p *hertzPluginContext) Phase() string {
	if p.phase == "" {
		return "request"
	}
	return p.phase
}

func (p *hertzPluginContext) SetPhase(phase string) {
	p.phase = phase
}

func (p *hertzPluginContext) RequestID() string {
	return p.requestID
}

func (p *hertzPluginContext) SetRequestID(requestID string) {
	p.requestID = requestID
}

func (p *hertzPluginContext) RegexCaptures() []string {
	return p.regexCaptures
}

func (p *hertzPluginContext) SetRegexCaptures(captures []string) {
	p.regexCaptures = captures
}

func (p *hertzPluginContext) RequestMethod() string {
	return string(p.raw.Method())
}

func (p *hertzPluginContext) SetRequestMethod(method string) {
	p.raw.Request.SetMethod(method)
}

func (p *hertzPluginContext) RequestHost() string {
	return string(p.raw.Host())
}

func (p *hertzPluginContext) SetRequestHost(host string) {
	p.raw.Request.SetHost(host)
	p.raw.Request.Header.SetHost(host)
}

func (p *hertzPluginContext) RequestPath() string {
	return string(p.raw.Path())
}

func (p *hertzPluginContext) SetRequestPath(path string) {
	p.raw.Request.URI().SetPath(path)
}

func (p *hertzPluginContext) RequestURI() string {
	query := string(p.raw.Request.URI().QueryArgs().QueryString())
	if query == "" {
		return p.RequestPath()
	}
	return fmt.Sprintf("%s?%s", p.RequestPath(), query)
}

func (p *hertzPluginContext) RequestQuery(key string) string {
	return string(p.raw.Query(key))
}

func (p *hertzPluginContext) AddRequestQuery(key, value string) {
	p.raw.Request.URI().QueryArgs().Add(key, value)
}

func (p *hertzPluginContext) SetRequestQuery(key, value string) {
	p.raw.Request.URI().QueryArgs().Set(key, value)
}

func (p *hertzPluginContext) DelRequestQuery(key string) {
	p.raw.Request.URI().QueryArgs().Del(key)
}

func (p *hertzPluginContext) RequestHeader(key string) string {
	return p.raw.Request.Header.Get(key)
}

func (p *hertzPluginContext) AddRequestHeader(key, value string) {
	p.raw.Request.Header.Add(key, value)
}

func (p *hertzPluginContext) SetRequestHeader(key, value string) {
	p.raw.Request.Header.Set(key, value)
}

func (p *hertzPluginContext) DelRequestHeader(key string) {
	p.raw.Request.Header.Del(key)
}

func (p *hertzPluginContext) RequestBody() []byte {
	return p.raw.Request.Body()
}

func (p *hertzPluginContext) SetRequestBody(body []byte) {
	p.raw.Request.SetBodyRaw(body)
}

func (p *hertzPluginContext) ResponseStatus() int {
	return p.raw.Response.StatusCode()
}

func (p *hertzPluginContext) SetResponseStatus(status int) {
	p.raw.Response.SetStatusCode(status)
}

func (p *hertzPluginContext) ResponseHeader(key string) string {
	return p.raw.Response.Header.Get(key)
}

func (p *hertzPluginContext) AddResponseHeader(key, value string) {
	p.raw.Response.Header.Add(key, value)
}

func (p *hertzPluginContext) SetResponseHeader(key, value string) {
	p.raw.Response.Header.Set(key, value)
}

func (p *hertzPluginContext) DelResponseHeader(key string) {
	p.raw.Response.Header.Del(key)
}

func (p *hertzPluginContext) ResponseBody() []byte {
	return p.raw.Response.Body()
}

func (p *hertzPluginContext) SetResponseBody(body []byte) {
	p.raw.Response.SetBodyRaw(body)
}

func (p *hertzPluginContext) ClientIP() string {
	return p.raw.ClientIP()
}

func (p *hertzPluginContext) GatewayError() error {
	return p.gatewayError
}

func (p *hertzPluginContext) SetGatewayError(err error) {
	p.gatewayError = err
}

func (p *hertzPluginContext) UpstreamStatusCode() int {
	return p.upstreamStatus
}

func (p *hertzPluginContext) SetUpstreamStatusCode(status int) {
	p.upstreamStatus = status
}

func (p *hertzPluginContext) ProxyInfo() observability.ProxyInfo {
	return p.proxyInfo
}

func (p *hertzPluginContext) SetProxyInfo(info observability.ProxyInfo) {
	p.proxyInfo = info
}

func IsAborted(c *app.RequestContext) bool {
	pc := FromRequestContext(c)
	if hpc, ok := pc.(*hertzPluginContext); ok {
		return hpc.aborted
	}
	value, ok := c.Get(runtimectx.PluginAbortKey)
	if !ok {
		return false
	}
	aborted, ok := value.(bool)
	return ok && aborted
}

func IsGatewayError(err error, target error) bool {
	return errors.Is(err, target)
}
