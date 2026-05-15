package router

import (
	"fmt"
	"testing"

	"github.com/joey/lumen-gateway/internal/config"
)

// buildRouter creates a router with n prefix routes (/api/route-0, /api/route-1, …).
// The last route always matches the probe path so every bench iteration hits the
// full linear scan (worst case).
func buildRouter(b *testing.B, n int) *Router {
	b.Helper()
	r := New()
	for i := range n {
		if err := r.Add(config.RouteOptions{
			ID:      fmt.Sprintf("route-%d", i),
			Methods: []string{"GET"},
			Paths:   []string{fmt.Sprintf("/api/route-%d", i)},
			Service: "svc",
		}); err != nil {
			b.Fatalf("Add route %d: %v", i, err)
		}
	}
	return r
}

func BenchmarkRouterMatch100(b *testing.B) {
	r := buildRouter(b, 100)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = r.Match("GET", "", "/api/route-99/resource")
	}
}

func BenchmarkRouterMatch1000(b *testing.B) {
	r := buildRouter(b, 1000)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = r.Match("GET", "", "/api/route-999/resource")
	}
}

func BenchmarkRouterMatchExact(b *testing.B) {
	r := New()
	if err := r.Add(config.RouteOptions{
		ID:      "exact",
		Methods: []string{"GET"},
		Paths:   []string{"= /status"},
		Service: "svc",
	}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = r.Match("GET", "", "/status")
	}
}

func BenchmarkRouterMatchRegex(b *testing.B) {
	r := New()
	if err := r.Add(config.RouteOptions{
		ID:      "regex",
		Methods: []string{"GET"},
		Paths:   []string{"~ ^/api/v[0-9]+/"},
		Service: "svc",
	}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = r.Match("GET", "", "/api/v3/users")
	}
}
