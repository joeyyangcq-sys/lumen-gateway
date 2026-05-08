package translate

import (
	"encoding/json"
	"testing"

	"github.com/joey/lumen-gateway/internal/apisix"
)

func TestApisixSnapshotToConfigMapsUpstreamProxyFields(t *testing.T) {
	nodes, err := json.Marshal(map[string]uint32{"127.0.0.1:9081": 1})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	options, err := ApisixSnapshotToConfig(apisix.Snapshot{
		Routes: map[string]apisix.Route{
			"route-1": {
				ID:        "route-1",
				URI:       "/users",
				ServiceID: "service-1",
			},
		},
		Services: map[string]apisix.Service{
			"service-1": {
				ID:         "service-1",
				UpstreamID: "upstream-1",
			},
		},
		Upstreams: map[string]apisix.Upstream{
			"upstream-1": {
				ID:           "upstream-1",
				Scheme:       "https",
				PassHost:     "rewrite",
				UpstreamHost: "users.internal",
				Nodes:        nodes,
			},
		},
	}, ApisixToConfigOptions{Listen: ":8080"})
	if err != nil {
		t.Fatalf("ApisixSnapshotToConfig() error = %v", err)
	}

	upstream := options.Upstreams["upstream-1"]
	if upstream.Scheme != "https" {
		t.Fatalf("scheme = %q, want https", upstream.Scheme)
	}
	if upstream.PassHost != "rewrite" {
		t.Fatalf("pass_host = %q, want rewrite", upstream.PassHost)
	}
	if upstream.UpstreamHost != "users.internal" {
		t.Fatalf("upstream_host = %q, want users.internal", upstream.UpstreamHost)
	}
	if len(upstream.Endpoints) != 1 || upstream.Endpoints[0].Address != "127.0.0.1:9081" {
		t.Fatalf("endpoints = %#v, want one endpoint 127.0.0.1:9081", upstream.Endpoints)
	}
}

func TestApisixSnapshotToConfigMergesGlobalRouteAndPluginConfigPlugins(t *testing.T) {
	nodes, err := json.Marshal(map[string]uint32{"127.0.0.1:9081": 1})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	options, err := ApisixSnapshotToConfig(apisix.Snapshot{
		Routes: map[string]apisix.Route{
			"route-1": {
				ID:             "route-1",
				URI:            "/users",
				ServiceID:      "service-1",
				PluginConfigID: "cfg-route",
				Plugins: json.RawMessage(`{
					"proxy-rewrite": {"host":"route.internal","uri":"/v2/users"}
				}`),
			},
		},
		Services: map[string]apisix.Service{
			"service-1": {
				ID:             "service-1",
				UpstreamID:     "upstream-1",
				PluginConfigID: "cfg-service",
				Plugins: json.RawMessage(`{
					"response-rewrite": {"status_code": 202}
				}`),
			},
		},
		Upstreams: map[string]apisix.Upstream{
			"upstream-1": {
				ID:    "upstream-1",
				Nodes: nodes,
			},
		},
		PluginConfig: map[string]apisix.PluginConfig{
			"cfg-route": {
				ID: "cfg-route",
				Plugins: json.RawMessage(`{
					"response-rewrite": {"status_code": 299, "headers": {"X-Route":"cfg"}}
				}`),
			},
			"cfg-service": {
				ID: "cfg-service",
				Plugins: json.RawMessage(`{
					"proxy-rewrite": {"host":"service.internal"}
				}`),
			},
		},
		GlobalRules: map[string]apisix.GlobalRule{
			"10": {
				ID: "10",
				Plugins: json.RawMessage(`{
					"response-rewrite": {"headers": {"X-Global":"true"}}
				}`),
			},
		},
	}, ApisixToConfigOptions{Listen: ":8080"})
	if err != nil {
		t.Fatalf("ApisixSnapshotToConfig() error = %v", err)
	}

	if len(options.GlobalPlugins) != 1 {
		t.Fatalf("global plugins = %d, want 1", len(options.GlobalPlugins))
	}
	if got := options.GlobalPlugins[0].Name; got != "response_transformer" {
		t.Fatalf("global plugin name = %q, want response_transformer", got)
	}

	routePlugins := options.Routes["route-1"].Plugins
	if len(routePlugins) != 3 {
		t.Fatalf("route plugins = %d, want 3", len(routePlugins))
	}
	if got := routePlugins[0].Name; got != "request_transformer" {
		t.Fatalf("first route plugin = %q, want request_transformer", got)
	}
	if got := routePlugins[1].Name; got != "replace_path" {
		t.Fatalf("second route plugin = %q, want replace_path", got)
	}
	if got := routePlugins[0].Params.(map[string]any)["host"]; got != "route.internal" {
		t.Fatalf("route host = %#v, want route.internal", got)
	}
	if got := routePlugins[2].Name; got != "response_transformer" {
		t.Fatalf("third route plugin = %q, want response_transformer", got)
	}

	servicePlugins := options.Services["service-1"].Plugins
	if len(servicePlugins) != 2 {
		t.Fatalf("service plugins = %d, want 2", len(servicePlugins))
	}
	if got := servicePlugins[0].Name; got != "request_transformer" {
		t.Fatalf("first service plugin = %q, want request_transformer", got)
	}
	if got := servicePlugins[1].Name; got != "response_transformer" {
		t.Fatalf("second service plugin = %q, want response_transformer", got)
	}
}

func TestApisixSnapshotToConfigIgnoresDisabledPlugins(t *testing.T) {
	nodes, err := json.Marshal(map[string]uint32{"127.0.0.1:9081": 1})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	options, err := ApisixSnapshotToConfig(apisix.Snapshot{
		Routes: map[string]apisix.Route{
			"route-1": {
				ID:        "route-1",
				URI:       "/users",
				ServiceID: "service-1",
				Plugins: json.RawMessage(`{
					"proxy-rewrite": {"host":"disabled.internal","_meta":{"disable":true}}
				}`),
			},
		},
		Services: map[string]apisix.Service{
			"service-1": {
				ID:         "service-1",
				UpstreamID: "upstream-1",
			},
		},
		Upstreams: map[string]apisix.Upstream{
			"upstream-1": {
				ID:    "upstream-1",
				Nodes: nodes,
			},
		},
	}, ApisixToConfigOptions{Listen: ":8080"})
	if err != nil {
		t.Fatalf("ApisixSnapshotToConfig() error = %v", err)
	}

	if len(options.Routes["route-1"].Plugins) != 0 {
		t.Fatalf("route plugins = %#v, want disabled plugin to be ignored", options.Routes["route-1"].Plugins)
	}
}
