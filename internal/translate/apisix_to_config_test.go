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
