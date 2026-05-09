package config

import (
	"strings"
	"testing"
)

func TestValidateAcceptsMinimalGatewayConfig(t *testing.T) {
	options := minimalOptions()

	if err := options.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAllowsEmptyRoutes(t *testing.T) {
	options := minimalOptions()
	options.Routes = map[string]RouteOptions{}

	if err := options.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsUnknownServiceReference(t *testing.T) {
	options := minimalOptions()
	route := options.Routes["user-api"]
	route.Service = "missing-service"
	options.Routes["user-api"] = route

	err := options.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want unknown service error")
	}
	if !strings.Contains(err.Error(), `unknown service "missing-service"`) {
		t.Fatalf("Validate() error = %q, want unknown service", err.Error())
	}
}

func TestValidateRejectsUnknownPluginReference(t *testing.T) {
	options := minimalOptions()
	server := options.Servers["main"]
	server.Plugins = []PluginRef{{Use: "missing-plugin"}}
	options.Servers["main"] = server

	err := options.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want unknown plugin error")
	}
	if !strings.Contains(err.Error(), `unknown plugin "missing-plugin"`) {
		t.Fatalf("Validate() error = %q, want unknown plugin", err.Error())
	}
}

func TestValidateRejectsUnsupportedUpstreamScheme(t *testing.T) {
	options := minimalOptions()
	upstream := options.Upstreams["user-upstream"]
	upstream.Scheme = "grpc"
	options.Upstreams["user-upstream"] = upstream

	err := options.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want scheme validation error")
	}
	if !strings.Contains(err.Error(), `scheme "grpc" is not supported`) {
		t.Fatalf("Validate() error = %q, want scheme validation error", err.Error())
	}
}

func TestValidateRejectsRewritePassHostWithoutUpstreamHost(t *testing.T) {
	options := minimalOptions()
	upstream := options.Upstreams["user-upstream"]
	upstream.PassHost = "rewrite"
	options.Upstreams["user-upstream"] = upstream

	err := options.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want upstream_host validation error")
	}
	if !strings.Contains(err.Error(), `upstream_host cannot be empty`) {
		t.Fatalf("Validate() error = %q, want upstream_host validation error", err.Error())
	}
}

func minimalOptions() Options {
	return Options{
		Servers: map[string]ServerOptions{
			"main": {
				ID:     "main",
				Listen: ":8080",
			},
		},
		Routes: map[string]RouteOptions{
			"user-api": {
				ID:      "user-api",
				Paths:   []string{"/api/users"},
				Service: "user-service",
			},
		},
		Services: map[string]ServiceOptions{
			"user-service": {
				ID:       "user-service",
				Protocol: "http",
				Upstream: "user-upstream",
			},
		},
		Upstreams: map[string]UpstreamOptions{
			"user-upstream": {
				ID: "user-upstream",
				Endpoints: []EndpointOptions{
					{Address: "127.0.0.1:9001"},
				},
			},
		},
	}
}
