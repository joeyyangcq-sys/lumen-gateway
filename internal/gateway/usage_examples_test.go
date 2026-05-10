package gateway

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/config"
)

func TestUsageGuideFileModeQuickstart(t *testing.T) {
	options, err := config.Load(filepath.Join("testdata", "usage", "quickstart.yaml"))
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}

	gw, err := New(options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var gotHost string
	var gotPath string
	var gotQuery string

	installTransport(gw, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.Host
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-Upstream": []string{"ok"}},
			Body:       io.NopCloser(strings.NewReader("proxied from usage guide")),
		}, nil
	}))

	c := app.NewContext(0)
	c.Request.SetMethod("GET")
	c.Request.SetHost("public.example.com")
	c.Request.URI().SetPath("/api/users")
	c.Request.URI().SetQueryString("page=1")

	gw.ServeHTTP(context.Background(), c)

	if got := c.Response.StatusCode(); got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
	if got := string(c.Response.Body()); got != "proxied from usage guide" {
		t.Fatalf("body = %q, want proxied response", got)
	}
	if gotHost != "users.internal" {
		t.Fatalf("upstream host = %q, want users.internal", gotHost)
	}
	if gotPath != "/users" {
		t.Fatalf("upstream path = %q, want /users", gotPath)
	}
	if !strings.Contains(gotQuery, "page=1") || !strings.Contains(gotQuery, "route=users") || !strings.Contains(gotQuery, "service=users-service") || !strings.Contains(gotQuery, "upstream=users-upstream") {
		t.Fatalf("upstream query = %q, want page, route, service, upstream markers", gotQuery)
	}
	if got := c.Response.Header.Get("X-Lumen-Global"); got != "true" {
		t.Fatalf("X-Lumen-Global = %q, want true", got)
	}
	if got := c.Response.Header.Get("X-Lumen-Service"); got != "users-service" {
		t.Fatalf("X-Lumen-Service = %q, want users-service", got)
	}
}

func TestUsageGuideRequestIDAndLimitCount(t *testing.T) {
	options, err := config.Load(filepath.Join("testdata", "usage", "request_id_limit.yaml"))
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}

	gw, err := New(options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var upstreamRequestIDs []string
	installTransport(gw, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		upstreamRequestIDs = append(upstreamRequestIDs, r.Header.Get("X-Request-Id"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	}))

	first := app.NewContext(0)
	first.Request.SetMethod("GET")
	first.Request.SetHost("gateway.example.com")
	first.Request.URI().SetPath("/limited/users")
	gw.ServeHTTP(context.Background(), first)

	generatedID := first.Response.Header.Get("X-Request-Id")
	if generatedID == "" {
		t.Fatal("generated request id is empty")
	}
	if got := first.Response.StatusCode(); got != http.StatusOK {
		t.Fatalf("first status = %d, want 200", got)
	}

	second := app.NewContext(0)
	second.Request.SetMethod("GET")
	second.Request.SetHost("gateway.example.com")
	second.Request.URI().SetPath("/limited/users")
	second.Request.Header.Set("X-Request-Id", "external-id")
	gw.ServeHTTP(context.Background(), second)

	if got := second.Response.Header.Get("X-Request-Id"); got != "external-id" {
		t.Fatalf("preserved request id = %q, want external-id", got)
	}
	if got := second.Response.StatusCode(); got != http.StatusOK {
		t.Fatalf("second status = %d, want 200", got)
	}

	third := app.NewContext(0)
	third.Request.SetMethod("GET")
	third.Request.SetHost("gateway.example.com")
	third.Request.URI().SetPath("/limited/users")
	gw.ServeHTTP(context.Background(), third)

	if got := third.Response.StatusCode(); got != 429 {
		t.Fatalf("third status = %d, want 429", got)
	}
	if got := string(third.Response.Body()); got != "rate limited" {
		t.Fatalf("third body = %q, want rate limited", got)
	}
	if got := third.Response.Header.Get("X-RateLimit-Limit"); got != "2" {
		t.Fatalf("X-RateLimit-Limit = %q, want 2", got)
	}
	if len(upstreamRequestIDs) != 2 {
		t.Fatalf("upstream request count = %d, want 2 before limit rejection", len(upstreamRequestIDs))
	}
	if upstreamRequestIDs[0] == "" {
		t.Fatal("first upstream request id is empty")
	}
	if upstreamRequestIDs[1] != "external-id" {
		t.Fatalf("second upstream request id = %q, want external-id", upstreamRequestIDs[1])
	}
}
