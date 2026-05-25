package router

import (
	"testing"

	"github.com/joey/lumen-gateway/internal/config"
)

func TestRouterMatchMethodHostAndPrefixPath(t *testing.T) {
	r := New()
	err := r.Add(config.RouteOptions{
		ID:      "users",
		Hosts:   []string{"api.example.test"},
		Methods: []string{"GET"},
		Paths:   []string{"/api/users"},
		Service: "user-service",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	route, ok := r.Match("GET", "api.example.test", "/api/users/42")
	if !ok {
		t.Fatal("Match() ok = false, want true")
	}
	if route.ID != "users" {
		t.Fatalf("Match() route.ID = %q, want users", route.ID)
	}
}

func TestRouterExactPathDoesNotMatchPrefix(t *testing.T) {
	r := New()
	err := r.Add(config.RouteOptions{
		ID:      "status",
		Methods: []string{"GET"},
		Paths:   []string{"= /status"},
		Service: "status-service",
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	if _, ok := r.Match("GET", "", "/status/extra"); ok {
		t.Fatal("Match() ok = true, want false for exact route prefix")
	}
	if route, ok := r.Match("GET", "", "/status"); !ok || route.ID != "status" {
		t.Fatalf("Match() = (%+v, %v), want status true", route, ok)
	}
}

func TestRouterRejectsEmptyRouteID(t *testing.T) {
	r := New()
	if err := r.Add(config.RouteOptions{Paths: []string{"/"}}); err == nil {
		t.Fatal("Add() error = nil, want error")
	}
}

func TestRouterWildcardHost(t *testing.T) {
	r := New()
	_ = r.Add(config.RouteOptions{
		ID:      "subdomain",
		Hosts:   []string{"*.example.com"},
		Paths:   []string{"/sub"},
		Service: "svc",
	})
	_ = r.Add(config.RouteOptions{
		ID:      "suffix",
		Hosts:   []string{"example.*"},
		Paths:   []string{"/suf"},
		Service: "svc",
	})

	if _, ok := r.Match("GET", "api.example.com", "/sub"); !ok {
		t.Error("expected match for *.example.com")
	}
	if _, ok := r.Match("GET", "example.com", "/sub"); ok {
		t.Error("expected no match for *.example.com on exact example.com")
	}
	if _, ok := r.Match("GET", "example.org", "/suf"); !ok {
		t.Error("expected match for example.*")
	}
}

func TestIsValidMethod(t *testing.T) {
	if !IsValidMethod("GET") || !IsValidMethod("post") || !IsValidMethod("DELETE") {
		t.Error("expected valid HTTP methods to pass")
	}
	if IsValidMethod("INVALID") || IsValidMethod("") {
		t.Error("expected invalid HTTP methods to fail")
	}
}

func TestRouterPathMatching(t *testing.T) {
	r := New()
	_ = r.Add(config.RouteOptions{
		ID:      "regex_case",
		Paths:   []string{"~* ^/users/[0-9]+$"},
		Service: "svc",
	})
	_ = r.Add(config.RouteOptions{
		ID:      "regex_exact",
		Paths:   []string{"~ ^/orders/[0-9]+$"},
		Service: "svc",
	})
	_ = r.Add(config.RouteOptions{
		ID:      "apisix_prefix",
		Paths:   []string{"/foo/*"},
		Service: "svc",
	})
	_ = r.Add(config.RouteOptions{
		ID:      "apisix_prefix_no_slash",
		Paths:   []string{"/bar*"},
		Service: "svc",
	})

	// Test regex case-insensitive
	if _, ok := r.Match("GET", "", "/USERS/123"); !ok {
		t.Error("expected match for case-insensitive regex")
	}
	// Test regex case-sensitive
	if _, ok := r.Match("GET", "", "/ORDERS/123"); ok {
		t.Error("expected no match for case-sensitive regex on uppercase path")
	}
	if _, ok := r.Match("GET", "", "/orders/123"); !ok {
		t.Error("expected match for case-sensitive regex")
	}
	// Test apisix prefix
	if _, ok := r.Match("GET", "", "/foo/bar"); !ok {
		t.Error("expected match for /foo/*")
	}
	if _, ok := r.Match("GET", "", "/bar/baz"); !ok {
		t.Error("expected match for /bar*")
	}
}

func TestRouterCompileErrors(t *testing.T) {
	r := New()
	// Empty path
	if err := r.Add(config.RouteOptions{ID: "r1", Paths: []string{""}}); err == nil {
		t.Error("expected error for empty path")
	}
	// Empty paths list
	if err := r.Add(config.RouteOptions{ID: "r1", Paths: []string{}}); err == nil {
		t.Error("expected error for empty paths list")
	}
	// Invalid regex
	if err := r.Add(config.RouteOptions{ID: "r1", Paths: []string{"~ ["}}); err == nil {
		t.Error("expected error for invalid regex")
	}
	if err := r.Add(config.RouteOptions{ID: "r1", Paths: []string{"~* ["}}); err == nil {
		t.Error("expected error for invalid regex case-insensitive")
	}
	// Empty regex
	if err := r.Add(config.RouteOptions{ID: "r1", Paths: []string{"~ "}}); err == nil {
		t.Error("expected error for empty regex")
	}
	if err := r.Add(config.RouteOptions{ID: "r1", Paths: []string{"~*"}}); err == nil {
		t.Error("expected error for empty regex 2")
	}
	if err := r.Add(config.RouteOptions{ID: "r1", Paths: []string{"~"}}); err == nil {
		t.Error("expected error for empty regex 3")
	}
}

