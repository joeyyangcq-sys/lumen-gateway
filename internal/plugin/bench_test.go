package plugin

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

// buildHandlerChain produces n no-op app.HandlerFuncs and wires them into a
// Hertz request context so c.Next() drives the full chain.
func buildHandlerChain(n int) ([]app.HandlerFunc, *app.RequestContext) {
	handlers := make([]app.HandlerFunc, n)
	for i := range n {
		handlers[i] = func(_ context.Context, c *app.RequestContext) {
			c.Next(context.Background())
		}
	}
	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.URI().SetPath("/bench")
	SetRouteID(c, "bench-route")
	SetServiceID(c, "bench-service")
	SetUpstreamID(c, "bench-upstream")
	return handlers, c
}

func runChain(handlers []app.HandlerFunc, c *app.RequestContext) {
	c.SetIndex(-1)
	c.SetHandlers(handlers)
	c.Next(context.Background())
}

func BenchmarkPluginChain5(b *testing.B) {
	handlers, _ := buildHandlerChain(5)
	b.ReportAllocs()
	for b.Loop() {
		c := app.NewContext(0)
		c.SetIndex(-1)
		c.SetHandlers(handlers)
		c.Next(context.Background())
	}
}

func BenchmarkPluginChain10(b *testing.B) {
	handlers, _ := buildHandlerChain(10)
	b.ReportAllocs()
	for b.Loop() {
		c := app.NewContext(0)
		c.SetIndex(-1)
		c.SetHandlers(handlers)
		c.Next(context.Background())
	}
}

func BenchmarkFromRequestContext(b *testing.B) {
	c := app.NewContext(0)
	SetRouteID(c, "r1")
	SetServiceID(c, "s1")
	SetUpstreamID(c, "u1")
	b.ReportAllocs()
	for b.Loop() {
		pc := FromRequestContext(c)
		_ = pc.RouteID()
		_ = pc.ServiceID()
		_ = pc.UpstreamID()
	}
}

func BenchmarkSetContextValues(b *testing.B) {
	c := app.NewContext(0)
	b.ReportAllocs()
	for b.Loop() {
		SetRouteID(c, "bench-route")
		SetServiceID(c, "bench-service")
		SetUpstreamID(c, "bench-upstream")
		SetPhase(c, "request")
	}
}
