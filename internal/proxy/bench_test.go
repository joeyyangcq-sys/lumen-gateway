package proxy

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/observability"
	"github.com/joey/lumen-gateway/internal/plugin"
)

// mockTransport returns a fixed 200 JSON response without any network I/O.
var mockTransport roundTripFunc = func(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
	}, nil
}

func newBenchRequestContext() *app.RequestContext {
	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.SetHost("client.example.com")
	c.Request.URI().SetPath("/api/users/42")
	c.Request.URI().SetQueryString("page=1&limit=20")
	plugin.SetRouteID(c, "bench-route")
	plugin.SetServiceID(c, "bench-service")
	plugin.SetUpstreamID(c, "bench-upstream")
	return c
}

var benchTarget = Target{Scheme: "http", Address: "upstream.internal:8080"}

func BenchmarkProxyServeHTTP(b *testing.B) {
	prev := observability.Default()
	observability.SetDefault(observability.NewMemoryRecorder())
	defer observability.SetDefault(prev)

	p := NewHTTP(HTTPOptions{Transport: mockTransport})
	b.ReportAllocs()
	for b.Loop() {
		c := newBenchRequestContext()
		_ = p.ServeHTTP(context.Background(), c, benchTarget)
	}
}

func BenchmarkProxyServeHTTPWithBody(b *testing.B) {
	prev := observability.Default()
	observability.SetDefault(observability.NewMemoryRecorder())
	defer observability.SetDefault(prev)

	p := NewHTTP(HTTPOptions{Transport: mockTransport})
	payload := strings.Repeat("x", 4096) // 4 KB body
	b.ReportAllocs()
	for b.Loop() {
		c := newBenchRequestContext()
		c.Request.SetMethod("POST")
		c.Request.SetBodyString(payload)
		_ = p.ServeHTTP(context.Background(), c, benchTarget)
	}
}

func BenchmarkNewUpstreamRequest(b *testing.B) {
	c := newBenchRequestContext()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = newUpstreamRequest(context.Background(), c, benchTarget)
	}
}

func BenchmarkIsHopByHopHeader(b *testing.B) {
	headers := []string{
		"Connection", "Keep-Alive", "Content-Type", "Authorization",
		"Transfer-Encoding", "X-Custom-Header", "Upgrade", "Accept",
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, h := range headers {
			_ = isHopByHopHeader(h)
		}
	}
}
