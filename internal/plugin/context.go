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
	raw *app.RequestContext
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

func FromRequestContext(c *app.RequestContext) PluginContext {
	return &hertzPluginContext{raw: c}
}

func SetRouteID(c *app.RequestContext, routeID string) {
	c.Set(runtimectx.RouteIDKey, routeID)
}

func SetServiceID(c *app.RequestContext, serviceID string) {
	c.Set(runtimectx.ServiceIDKey, serviceID)
}

func SetUpstreamID(c *app.RequestContext, upstreamID string) {
	c.Set(runtimectx.UpstreamIDKey, upstreamID)
}

func SetUpstreamHost(c *app.RequestContext, upstreamHost string) {
	c.Set(runtimectx.UpstreamHostKey, upstreamHost)
}

func SetEndpointAddress(c *app.RequestContext, address string) {
	c.Set(runtimectx.EndpointAddrKey, address)
}

func SetRequestID(c *app.RequestContext, requestID string) {
	c.Set(runtimectx.RequestIDKey, requestID)
}

func SetPhase(c *app.RequestContext, phase string) {
	c.Set(runtimectx.PhaseKey, phase)
}

func SetRegexCaptures(c *app.RequestContext, captures []string) {
	c.Set(runtimectx.RegexCapturesKey, captures)
}

func SetGatewayError(c *app.RequestContext, err error) {
	if err == nil {
		return
	}
	c.Set(runtimectx.GatewayErrorKey, err)
}

func SetUpstreamStatusCode(c *app.RequestContext, status int) {
	c.Set(runtimectx.UpstreamStatusKey, status)
}

func SetProxyInfo(c *app.RequestContext, info observability.ProxyInfo) {
	c.Set(runtimectx.ProxyInfoKey, info)
}

func (p *hertzPluginContext) Raw() *app.RequestContext {
	return p.raw
}

func (p *hertzPluginContext) Next(ctx context.Context) {
	p.raw.Next(ctx)
}

func (p *hertzPluginContext) Abort() {
	p.raw.Set(runtimectx.PluginAbortKey, true)
	p.raw.Abort()
}

func (p *hertzPluginContext) Value(key string) (any, bool) {
	return p.raw.Get(key)
}

func (p *hertzPluginContext) SetValue(key string, value any) {
	p.raw.Set(key, value)
}

func (p *hertzPluginContext) RouteID() string {
	return getStringValue(p.raw, runtimectx.RouteIDKey)
}

func (p *hertzPluginContext) ServiceID() string {
	return getStringValue(p.raw, runtimectx.ServiceIDKey)
}

func (p *hertzPluginContext) UpstreamID() string {
	return getStringValue(p.raw, runtimectx.UpstreamIDKey)
}

func (p *hertzPluginContext) UpstreamHost() string {
	return getStringValue(p.raw, runtimectx.UpstreamHostKey)
}

func (p *hertzPluginContext) EndpointAddress() string {
	return getStringValue(p.raw, runtimectx.EndpointAddrKey)
}

func (p *hertzPluginContext) Phase() string {
	phase := getStringValue(p.raw, runtimectx.PhaseKey)
	if phase == "" {
		return "request"
	}
	return phase
}

func (p *hertzPluginContext) SetPhase(phase string) {
	SetPhase(p.raw, phase)
}

func (p *hertzPluginContext) RequestID() string {
	return getStringValue(p.raw, runtimectx.RequestIDKey)
}

func (p *hertzPluginContext) SetRequestID(requestID string) {
	SetRequestID(p.raw, requestID)
}

func (p *hertzPluginContext) RegexCaptures() []string {
	value, ok := p.raw.Get(runtimectx.RegexCapturesKey)
	if !ok {
		return nil
	}
	captures, ok := value.([]string)
	if !ok {
		return nil
	}
	return captures
}

func (p *hertzPluginContext) SetRegexCaptures(captures []string) {
	SetRegexCaptures(p.raw, captures)
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
	value, ok := p.raw.Get(runtimectx.GatewayErrorKey)
	if !ok {
		return nil
	}
	err, ok := value.(error)
	if !ok {
		return nil
	}
	return err
}

func (p *hertzPluginContext) SetGatewayError(err error) {
	SetGatewayError(p.raw, err)
}

func (p *hertzPluginContext) UpstreamStatusCode() int {
	value, ok := p.raw.Get(runtimectx.UpstreamStatusKey)
	if !ok {
		return 0
	}
	status, ok := value.(int)
	if !ok {
		return 0
	}
	return status
}

func (p *hertzPluginContext) SetUpstreamStatusCode(status int) {
	SetUpstreamStatusCode(p.raw, status)
}

func (p *hertzPluginContext) ProxyInfo() observability.ProxyInfo {
	value, ok := p.raw.Get(runtimectx.ProxyInfoKey)
	if !ok {
		return observability.ProxyInfo{}
	}
	info, ok := value.(observability.ProxyInfo)
	if !ok {
		return observability.ProxyInfo{}
	}
	return info
}

func (p *hertzPluginContext) SetProxyInfo(info observability.ProxyInfo) {
	SetProxyInfo(p.raw, info)
}

func getStringValue(c *app.RequestContext, key string) string {
	value, ok := c.Get(key)
	if !ok {
		return ""
	}
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return s
}

func IsAborted(c *app.RequestContext) bool {
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
