package observability

import (
	"strings"
	"testing"
	"time"
)

func TestMemoryRecorderRendersPluginAndUpstreamMetrics(t *testing.T) {
	recorder := NewMemoryRecorder()

	recorder.ObservePlugin(PluginLabels{
		Plugin:     "request_transformer",
		Scope:      "route",
		Phase:      "request",
		RouteID:    "route-a",
		ServiceID:  "service-a",
		UpstreamID: "upstream-a",
		Result:     "ok",
	}, 25*time.Millisecond)

	recorder.ObserveUpstream(UpstreamLabels{
		RouteID:     "route-a",
		ServiceID:   "service-a",
		UpstreamID:  "upstream-a",
		Endpoint:    "127.0.0.1:8080",
		Scheme:      "https",
		Method:      "GET",
		StatusClass: "2xx",
		ErrorType:   "",
		ReusedConn:  false,
	}, ProxyInfo{
		ConnectTime:        5 * time.Millisecond,
		TLSHandshakeTime:   7 * time.Millisecond,
		RequestWriteTime:   2 * time.Millisecond,
		FirstByteTime:      11 * time.Millisecond,
		ResponseReadTime:   13 * time.Millisecond,
		ProcessingEstimate: 11 * time.Millisecond,
		TotalTime:          31 * time.Millisecond,
	})

	metrics := recorder.RenderPrometheus()
	if !strings.Contains(metrics, `lumen_plugin_executions_total{phase="request",plugin="request_transformer"`) {
		t.Fatalf("plugin executions metric missing:\n%s", metrics)
	}
	if !strings.Contains(metrics, `lumen_plugin_duration_seconds_count{phase="request",plugin="request_transformer"`) {
		t.Fatalf("plugin duration metric missing:\n%s", metrics)
	}
	if !strings.Contains(metrics, `lumen_upstream_requests_total{endpoint="127.0.0.1:8080"`) {
		t.Fatalf("upstream requests metric missing:\n%s", metrics)
	}
	if !strings.Contains(metrics, `phase="tls_handshake"`) {
		t.Fatalf("tls handshake phase metric missing:\n%s", metrics)
	}
	if !strings.Contains(metrics, `phase="processing_estimate"`) {
		t.Fatalf("processing estimate phase metric missing:\n%s", metrics)
	}
}
