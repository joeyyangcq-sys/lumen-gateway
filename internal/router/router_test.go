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
