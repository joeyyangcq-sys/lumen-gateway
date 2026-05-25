package config

import (
	"os"
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

func TestValidateRejectsUnsupportedLoggingOptions(t *testing.T) {
	tests := []struct {
		name    string
		logging LoggingOptions
		want    string
	}{
		{
			name:    "level",
			logging: LoggingOptions{Level: "trace"},
			want:    `logging.level "trace" is not supported`,
		},
		{
			name:    "format",
			logging: LoggingOptions{Format: "xml"},
			want:    `logging.format "xml" is not supported`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := minimalOptions()
			options.Logging = tt.logging

			err := options.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want logging validation error")
			}
			if err.Error() != tt.want {
				t.Fatalf("Validate() error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidateRejectsInvalidConfigurations(t *testing.T) {
	t.Run("no servers", func(t *testing.T) {
		opts := minimalOptions()
		opts.Servers = nil
		if err := opts.Validate(); err == nil || !strings.Contains(err.Error(), "at least one server is required") {
			t.Errorf("expected server requirement error, got %v", err)
		}
	})

	t.Run("empty server listen", func(t *testing.T) {
		opts := minimalOptions()
		s := opts.Servers["main"]
		s.Listen = ""
		opts.Servers["main"] = s
		if err := opts.Validate(); err == nil || !strings.Contains(err.Error(), "listen cannot be empty") {
			t.Errorf("expected server listen error, got %v", err)
		}
	})

	t.Run("empty route paths", func(t *testing.T) {
		opts := minimalOptions()
		r := opts.Routes["user-api"]
		r.Paths = nil
		opts.Routes["user-api"] = r
		if err := opts.Validate(); err == nil || !strings.Contains(err.Error(), "paths cannot be empty") {
			t.Errorf("expected route paths error, got %v", err)
		}
	})

	t.Run("empty route service", func(t *testing.T) {
		opts := minimalOptions()
		r := opts.Routes["user-api"]
		r.Service = ""
		opts.Routes["user-api"] = r
		if err := opts.Validate(); err == nil || !strings.Contains(err.Error(), "service cannot be empty") {
			t.Errorf("expected route service error, got %v", err)
		}
	})

	t.Run("empty service upstream", func(t *testing.T) {
		opts := minimalOptions()
		s := opts.Services["user-service"]
		s.Upstream = ""
		opts.Services["user-service"] = s
		if err := opts.Validate(); err == nil || !strings.Contains(err.Error(), "upstream cannot be empty") {
			t.Errorf("expected service upstream error, got %v", err)
		}
	})

	t.Run("empty upstream endpoints", func(t *testing.T) {
		opts := minimalOptions()
		u := opts.Upstreams["user-upstream"]
		u.Endpoints = nil
		opts.Upstreams["user-upstream"] = u
		if err := opts.Validate(); err == nil || !strings.Contains(err.Error(), "endpoints cannot be empty") {
			t.Errorf("expected upstream endpoints error, got %v", err)
		}
	})

	t.Run("unsupported upstream pass_host", func(t *testing.T) {
		opts := minimalOptions()
		u := opts.Upstreams["user-upstream"]
		u.PassHost = "invalid"
		opts.Upstreams["user-upstream"] = u
		if err := opts.Validate(); err == nil || !strings.Contains(err.Error(), "pass_host \"invalid\" is not supported") {
			t.Errorf("expected pass_host error, got %v", err)
		}
	})

	t.Run("empty endpoint address", func(t *testing.T) {
		opts := minimalOptions()
		u := opts.Upstreams["user-upstream"]
		u.Endpoints = []EndpointOptions{{Address: ""}}
		opts.Upstreams["user-upstream"] = u
		if err := opts.Validate(); err == nil || !strings.Contains(err.Error(), "endpoint address cannot be empty") {
			t.Errorf("expected endpoint address error, got %v", err)
		}
	})

	t.Run("empty plugin ref name", func(t *testing.T) {
		opts := minimalOptions()
		r := opts.Routes["user-api"]
		r.Plugins = []PluginRef{{Name: ""}}
		opts.Routes["user-api"] = r
		if err := opts.Validate(); err == nil || !strings.Contains(err.Error(), "plugin name cannot be empty") {
			t.Errorf("expected plugin name error, got %v", err)
		}
	})
}

func TestLoadConfiguration(t *testing.T) {
	t.Run("non-existent file", func(t *testing.T) {
		_, err := Load("non-existent.yaml")
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("invalid yaml", func(t *testing.T) {
		// Create a temporary invalid yaml file
		tmpFile, err := os.CreateTemp("", "invalid-*.yaml")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())
		_, _ = tmpFile.WriteString("servers: [invalid")
		_ = tmpFile.Close()

		_, err = Load(tmpFile.Name())
		if err == nil {
			t.Error("expected error for invalid yaml")
		}
	})

	t.Run("success", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "valid-*.yaml")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())
		_, _ = tmpFile.WriteString(`
servers:
  main:
    listen: :8080
routes:
  user-api:
    paths: [/api/users]
    service: user-service
services:
  user-service:
    upstream: user-upstream
upstreams:
  user-upstream:
    endpoints:
      - address: 127.0.0.1:9001
`)
		_ = tmpFile.Close()

		opts, err := Load(tmpFile.Name())
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if opts.Servers["main"].ID != "main" {
			t.Errorf("expected server ID to be main, got %q", opts.Servers["main"].ID)
		}
	})
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
