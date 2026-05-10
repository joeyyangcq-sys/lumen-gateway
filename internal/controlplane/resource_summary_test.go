package controlplane

import (
	"encoding/json"
	"testing"
)

func TestSummarizeResource(t *testing.T) {
	t.Run("route", func(t *testing.T) {
		summary := SummarizeResource(KindRoute, "route-1", json.RawMessage(`{"id":"route-1","uri":"/users","methods":["GET"],"service_id":"svc-1"}`))
		if summary.Title != "/users" {
			t.Fatalf("title = %q, want /users", summary.Title)
		}
		if summary.Description != "service svc-1" {
			t.Fatalf("description = %q, want service svc-1", summary.Description)
		}
		if got := summary.Fields["service_id"]; got != "svc-1" {
			t.Fatalf("fields.service_id = %#v, want svc-1", got)
		}
	})

	t.Run("upstream", func(t *testing.T) {
		summary := SummarizeResource(KindUpstream, "up-1", json.RawMessage(`{"id":"up-1","scheme":"https","pass_host":"rewrite","nodes":{"127.0.0.1:9001":1},"upstream_host":"api.internal"}`))
		if summary.Title != "up-1" {
			t.Fatalf("title = %q, want up-1", summary.Title)
		}
		if summary.Description != "https · 1 nodes · pass_host=rewrite" {
			t.Fatalf("description = %q", summary.Description)
		}
	})

	t.Run("plugin config", func(t *testing.T) {
		summary := SummarizeResource(KindPluginConfig, "pc-1", json.RawMessage(`{"id":"pc-1","plugins":{"request-id":{},"proxy-rewrite":{"uri":"/v2"}}}`))
		if summary.Description != "plugins: proxy-rewrite, request-id" {
			t.Fatalf("description = %q", summary.Description)
		}
		if len(summary.Tags) != 2 {
			t.Fatalf("tags = %#v, want 2", summary.Tags)
		}
	})
}
