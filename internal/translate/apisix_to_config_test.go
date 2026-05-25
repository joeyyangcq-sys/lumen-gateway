package translate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/joey/lumen-gateway/internal/apisix"
	"github.com/joey/lumen-gateway/internal/config"
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

func TestApisixSnapshotToConfigMapsUpstreamTimeoutToService(t *testing.T) {
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
				ID:    "upstream-1",
				Nodes: nodes,
				Timeout: &apisix.UpstreamTimeout{
					Connect: "0.1",
					Send:    "0.2",
					Read:    "0.3",
				},
			},
		},
	}, ApisixToConfigOptions{Listen: ":8080"})
	if err != nil {
		t.Fatalf("ApisixSnapshotToConfig() error = %v", err)
	}

	service := options.Services["service-1"]
	if service.Timeout.Connect != 100*time.Millisecond {
		t.Fatalf("connect timeout = %s, want 100ms", service.Timeout.Connect)
	}
	if service.Timeout.Write != 200*time.Millisecond {
		t.Fatalf("write timeout = %s, want 200ms", service.Timeout.Write)
	}
	if service.Timeout.Read != 300*time.Millisecond {
		t.Fatalf("read timeout = %s, want 300ms", service.Timeout.Read)
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

func TestTranslateProxyRewriteMapsMethodRegexAndHeaderActions(t *testing.T) {
	refs, err := translateProxyRewrite(json.RawMessage(`{
		"method": "PATCH",
		"regex_uri": ["^/orders/(\\d+)$", "/internal/orders/$1"],
		"headers": {
			"add": {"X-Trace": "trace-$1"},
			"remove": ["X-Remove"],
			"set": {"X-Mode": "rewrite"}
		}
	}`))
	if err != nil {
		t.Fatalf("translateProxyRewrite() error = %v", err)
	}

	if len(refs) != 2 {
		t.Fatalf("refs = %d, want 2", len(refs))
	}
	if got := refs[0].Name; got != "rewrite_path_regex" {
		t.Fatalf("first ref = %q, want rewrite_path_regex", got)
	}
	if got := refs[1].Name; got != "request_transformer" {
		t.Fatalf("second ref = %q, want request_transformer", got)
	}

	params, ok := refs[1].Params.(map[string]any)
	if !ok {
		t.Fatalf("request transformer params = %#v, want map", refs[1].Params)
	}
	if got := params["method"]; got != "PATCH" {
		t.Fatalf("method = %#v, want PATCH", got)
	}
	addBlock, ok := params["add"].(map[string]any)
	if !ok {
		t.Fatalf("add block = %#v, want map", params["add"])
	}
	addHeaders, ok := addBlock["headers"].(map[string]string)
	if !ok {
		t.Fatalf("add headers = %#v, want map[string]string", addBlock["headers"])
	}
	if got := addHeaders["X-Trace"]; got != "trace-$1" {
		t.Fatalf("X-Trace add header = %q, want trace-$1", got)
	}
}

func TestTranslateRequestIDAndLimitCount(t *testing.T) {
	requestID, err := translateRequestID(json.RawMessage(`{
		"header_name": "X-Req-Id",
		"include_in_response": false,
		"algorithm": "uuid"
	}`))
	if err != nil {
		t.Fatalf("translateRequestID() error = %v", err)
	}
	if len(requestID) != 1 || requestID[0].Name != "request_id" {
		t.Fatalf("request-id refs = %#v, want request_id plugin", requestID)
	}

	limitCount, err := translateLimitCount(json.RawMessage(`{
		"count": 2,
		"time_window": 60,
		"key_type": "var",
		"key": "remote_addr",
		"rejected_code": 429
	}`))
	if err != nil {
		t.Fatalf("translateLimitCount() error = %v", err)
	}
	if len(limitCount) != 1 || limitCount[0].Name != "limit_count" {
		t.Fatalf("limit-count refs = %#v, want limit_count plugin", limitCount)
	}
}

func TestTranslateResponseRewriteMapsHeaderActionsAndBase64Body(t *testing.T) {
	refs, err := translateResponseRewrite(json.RawMessage(`{
		"status_code": 202,
		"body": "eyJvayI6dHJ1ZX0=",
		"body_base64": true,
		"headers": {
			"add": {"X-Trace":"1"},
			"remove": ["X-Delete"],
			"set": {"X-Mode":"rewrite"}
		}
	}`))
	if err != nil {
		t.Fatalf("translateResponseRewrite() error = %v", err)
	}
	if len(refs) != 1 || refs[0].Name != "response_transformer" {
		t.Fatalf("refs = %#v, want response_transformer", refs)
	}
	params, ok := refs[0].Params.(map[string]any)
	if !ok {
		t.Fatalf("params = %#v, want map", refs[0].Params)
	}
	if got := params["body_base64"]; got != true {
		t.Fatalf("body_base64 = %#v, want true", got)
	}
}

func TestTranslateEdgeAndErrorCases(t *testing.T) {
	// 1. parseAPISIXTimeoutSeconds
	t.Run("parse timeout seconds", func(t *testing.T) {
		if parseAPISIXTimeoutSeconds("0.5") != 500*time.Millisecond {
			t.Error("expected 500ms")
		}
		if parseAPISIXTimeoutSeconds("invalid") != 0 {
			t.Error("expected 0 on invalid")
		}
		if parseAPISIXTimeoutSeconds("") != 0 {
			t.Error("expected 0 on empty")
		}
	})

	// 2. apisixNodesToEndpoints error
	t.Run("nodes to endpoints error", func(t *testing.T) {
		endpoints, err := apisixNodesToEndpoints(json.RawMessage(`{invalid`))
		if err == nil || len(endpoints) != 0 {
			t.Error("expected error and empty endpoints on invalid json")
		}
	})

	// 3. apisixWildcardToRegex
	t.Run("wildcard to regex", func(t *testing.T) {
		res := apisixWildcardToRegex("/users/*")
		if res != "^/users/[^/]+$" {
			t.Errorf("expected ^/users/[^/]+$, got %s", res)
		}
	})

	// 4. decodeResponseRewriteHeaders error
	t.Run("decode response rewrite headers error", func(t *testing.T) {
		res, _ := decodeResponseRewriteHeaders(json.RawMessage(`{invalid`))
		if len(res.Remove) != 0 {
			t.Error("expected empty remove on error")
		}
	})

	// 5. decodeProxyRewriteHeaders error
	t.Run("decode proxy rewrite headers error", func(t *testing.T) {
		res, _ := decodeProxyRewriteHeaders(json.RawMessage(`{invalid`))
		if len(res.Remove) != 0 {
			t.Error("expected empty remove on error")
		}
	})

	// 6. translateLimitCount error
	t.Run("translate limit count error", func(t *testing.T) {
		_, err := translateLimitCount(json.RawMessage(`{invalid`))
		if err == nil {
			t.Error("expected error on invalid limit count json")
		}
	})

	// 7. translateRequestID error
	t.Run("translate request id error", func(t *testing.T) {
		_, err := translateRequestID(json.RawMessage(`{invalid`))
		if err == nil {
			t.Error("expected error on invalid request id json")
		}
	})

	// 8. translateProxyRewrite error
	t.Run("translate proxy rewrite error", func(t *testing.T) {
		_, err := translateProxyRewrite(json.RawMessage(`{invalid`))
		if err == nil {
			t.Error("expected error on invalid proxy rewrite json")
		}
	})

	// 9. translateResponseRewrite error
	t.Run("translate response rewrite error", func(t *testing.T) {
		_, err := translateResponseRewrite(json.RawMessage(`{invalid`))
		if err == nil {
			t.Error("expected error on invalid response rewrite json")
		}
	})

	// 10. isDisabledApisixPlugin unmarshal error
	t.Run("is disabled plugin error", func(t *testing.T) {
		if isDisabledApisixPlugin(json.RawMessage(`{invalid`)) {
			t.Error("expected false on unmarshal error")
		}
	})

	// 11. translateApisixPlugin default json unmarshal error
	t.Run("translate custom plugin unmarshal error", func(t *testing.T) {
		_, err := translateApisixPlugin("custom-plugin", json.RawMessage(`{invalid`))
		if err == nil {
			t.Error("expected error when custom plugin params is invalid json")
		}
	})

	// 12. translateApisixPlugin default null/empty params
	t.Run("translate custom plugin null params", func(t *testing.T) {
		refs, err := translateApisixPlugin("custom-plugin", json.RawMessage(`null`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 || refs[0].Name != "custom-plugin" {
			t.Errorf("unexpected refs: %v", refs)
		}
	})
}

func TestApisixSnapshotToConfigEdgeCases(t *testing.T) {
	// 1. empty listen options
	t.Run("empty listen options", func(t *testing.T) {
		_, err := ApisixSnapshotToConfig(apisix.Snapshot{}, ApisixToConfigOptions{})
		if err == nil {
			t.Error("expected error for empty listen")
		}
	})

	// 2. route inline upstream and service inline upstream
	t.Run("inline upstreams", func(t *testing.T) {
		nodes, _ := json.Marshal(map[string]uint32{"127.0.0.1:80": 1})
		snap := apisix.Snapshot{
			Routes: map[string]apisix.Route{
				"r1": {
					ID:  "r1",
					URI: "/users",
					Upstream: &apisix.Upstream{
						Type:  "roundrobin",
						Nodes: nodes,
					},
				},
			},
			Services: map[string]apisix.Service{
				"s1": {
					ID: "s1",
					Upstream: &apisix.Upstream{
						Type:  "roundrobin",
						Nodes: nodes,
					},
				},
			},
		}
		opts, err := ApisixSnapshotToConfig(snap, ApisixToConfigOptions{Listen: ":8080"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := opts.Upstreams["upstream-route-r1"]; !ok {
			t.Error("expected synthesized route upstream")
		}
		if _, ok := opts.Upstreams["upstream-service-s1"]; !ok {
			t.Error("expected synthesized service upstream")
		}
	})

	// 3. invalid regex_uri pairs in proxy-rewrite
	t.Run("invalid regex_uri pairs", func(t *testing.T) {
		_, err := translateProxyRewrite(json.RawMessage(`{"regex_uri": ["^/a"]}`))
		if err == nil {
			t.Error("expected error for odd regex_uri count")
		}
	})

	// 4. invalid base64 in response-rewrite
	t.Run("invalid base64 body", func(t *testing.T) {
		_, err := translateResponseRewrite(json.RawMessage(`{"body": "invalid-base64", "body_base64": true}`))
		if err == nil {
			t.Error("expected error for invalid base64 body")
		}
	})

	// 5. plugin_config not found error
	t.Run("plugin config not found", func(t *testing.T) {
		snap := apisix.Snapshot{
			Routes: map[string]apisix.Route{
				"r1": {
					ID:             "r1",
					URI:            "/users",
					PluginConfigID: "missing-cfg",
					ServiceID:      "s1",
				},
			},
			Services: map[string]apisix.Service{
				"s1": {
					ID:         "s1",
					UpstreamID: "u1",
				},
			},
			Upstreams: map[string]apisix.Upstream{
				"u1": {
					ID:    "u1",
					Nodes: json.RawMessage(`{"127.0.0.1:80": 1}`),
				},
			},
		}
		opts, err := ApisixSnapshotToConfig(snap, ApisixToConfigOptions{Listen: ":8080"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if _, ok := opts.Routes["r1"]; ok {
			t.Error("expected route r1 to be skipped")
		}
	})

	// 6. missing upstream in service
	t.Run("missing upstream in service", func(t *testing.T) {
		snap := apisix.Snapshot{
			Services: map[string]apisix.Service{
				"s1": {ID: "s1"},
			},
		}
		opts, err := ApisixSnapshotToConfig(snap, ApisixToConfigOptions{Listen: ":8080"})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if _, ok := opts.Services["s1"]; ok {
			t.Error("expected service s1 to be skipped")
		}
	})

	// 7. empty service ID and empty route ID fallback
	t.Run("fallback IDs", func(t *testing.T) {
		r, err := apisixRouteToConfig(apisix.Route{URI: "/users"}, nil)
		if err != nil || r.ID != "route" {
			t.Errorf("expected route ID fallback, got ID %q", r.ID)
		}

		s, err := apisixServiceToConfig(apisix.Service{UpstreamID: "u1"}, nil, map[string]config.UpstreamOptions{"u1": {}})
		if err != nil || s.ID != "service" {
			t.Errorf("expected service ID fallback, got ID %q", s.ID)
		}
	})
}

func TestTranslateEdgeFormats(t *testing.T) {
	// 1. alternate nodes format (list)
	t.Run("alternate nodes list", func(t *testing.T) {
		nodes := json.RawMessage(`[{"host":"127.0.0.1","port":1980,"weight":5}]`)
		endpoints, err := apisixNodesToEndpoints(nodes)
		if err != nil {
			t.Fatal(err)
		}
		if len(endpoints) != 1 || endpoints[0].Address != "127.0.0.1:1980" || endpoints[0].Weight != 5 {
			t.Errorf("unexpected endpoints: %v", endpoints)
		}
	})

	// 2. plain set header format in proxy rewrite
	t.Run("plain header set", func(t *testing.T) {
		res, err := decodeProxyRewriteHeaders(json.RawMessage(`{"X-Custom": "val"}`))
		if err != nil {
			t.Fatal(err)
		}
		if res.Set["X-Custom"] != "val" {
			t.Errorf("expected Set X-Custom=val, got %v", res.Set)
		}
	})

	// 3. mid-path wildcard in URI normalization
	t.Run("mid-path wildcard", func(t *testing.T) {
		res := normalizeApisixURI("/users/*/profile")
		if !strings.HasPrefix(res, "~ ") {
			t.Errorf("expected regex path starting with '~ ', got %q", res)
		}
	})

	// 4. preformatted prefix URI
	t.Run("preformatted prefix URI", func(t *testing.T) {
		for _, uri := range []string{"= /exact", "~ ^/regex", "~* ^/case"} {
			if normalizeApisixURI(uri) != uri {
				t.Errorf("expected unchanged URI %q, got %q", uri, normalizeApisixURI(uri))
			}
		}
	})
}



