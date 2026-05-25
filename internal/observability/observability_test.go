package observability

import (
	"strings"
	"testing"
	"time"
)

func TestMemoryRecorderRendersPluginAndUpstreamMetrics(t *testing.T) {
	recorder := NewMemoryRecorder()

	recorder.ObserveGateway(GatewayLabels{
		Handler:     "proxy",
		Method:      "GET",
		RouteID:     "route-a",
		StatusClass: "2xx",
	}, 33*time.Millisecond)

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
	if !strings.Contains(metrics, `lumen_gateway_requests_total{handler="proxy",method="GET",route_id="route-a",status_class="2xx"} 1`) {
		t.Fatalf("gateway requests metric missing:\n%s", metrics)
	}
	if !strings.Contains(metrics, `lumen_gateway_request_duration_seconds_count{handler="proxy",method="GET",route_id="route-a",status_class="2xx"} 1`) {
		t.Fatalf("gateway duration metric missing:\n%s", metrics)
	}
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

func TestMemoryRecorderStatsAndHelpers(t *testing.T) {
	recorder := NewMemoryRecorder()

	// 模拟一些 upstream 观测，使其有不同的 status class 和 route id
	// 4xx error on route-1
	recorder.ObserveUpstream(UpstreamLabels{
		RouteID:     "route-1",
		StatusClass: "404",
	}, ProxyInfo{TotalTime: 10 * time.Millisecond})

	// 5xx error on route-1
	recorder.ObserveUpstream(UpstreamLabels{
		RouteID:     "route-1",
		StatusClass: "500",
	}, ProxyInfo{TotalTime: 15 * time.Millisecond})

	// 2xx success on route-2
	recorder.ObserveUpstream(UpstreamLabels{
		RouteID:     "route-2",
		StatusClass: "200",
	}, ProxyInfo{TotalTime: 5 * time.Millisecond})

	// 模拟一些没有 route_id 的请求
	recorder.ObserveUpstream(UpstreamLabels{
		StatusClass: "200",
	}, ProxyInfo{TotalTime: 5 * time.Millisecond})

	// 计算 Stats
	stats := recorder.Stats()
	if stats.RequestsTotal != 4 {
		t.Errorf("expected 4 total requests, got %d", stats.RequestsTotal)
	}
	if stats.Errors4xx != 1 {
		t.Errorf("expected 1 4xx error, got %d", stats.Errors4xx)
	}
	if stats.Errors5xx != 1 {
		t.Errorf("expected 1 5xx error, got %d", stats.Errors5xx)
	}
	if stats.ErrorRate != 50.0 {
		t.Errorf("expected 50%% error rate, got %f", stats.ErrorRate)
	}

	// 验证 TopRoutes
	if len(stats.TopRoutes) != 2 {
		t.Errorf("expected 2 top routes, got %d", len(stats.TopRoutes))
	}
	// route-1 有 2 个请求，route-2 有 1 个，所以 route-1 应该排在第 1
	if stats.TopRoutes[0].RouteID != "route-1" {
		t.Errorf("expected route-1 to be top, got %q", stats.TopRoutes[0].RouteID)
	}
	if stats.TopRoutes[0].Requests != 2 || stats.TopRoutes[0].Errors != 2 {
		t.Errorf("expected 2 requests/errors on route-1, got req=%d, err=%d", stats.TopRoutes[0].Requests, stats.TopRoutes[0].Errors)
	}
}

func TestDefaultAndHelpers(t *testing.T) {
	// 1. Default & SetDefault & Reset
	orig := Default()
	defer SetDefault(orig)

	newRecorder := NewMemoryRecorder()
	SetDefault(newRecorder)
	if Default() != newRecorder {
		t.Error("SetDefault failed")
	}

	SetDefault(nil) // 应该回退到新的 memory recorder
	if Default() == nil {
		t.Error("expected default recorder to be non-nil after setting nil")
	}

	newRecorder.ObserveGateway(GatewayLabels{
		Handler: "proxy",
	}, time.Millisecond)
	newRecorder.Reset()
	if len(newRecorder.Snapshot().GatewayRequests) != 0 {
		t.Error("Reset failed to clear snapshot")
	}

	// 2. renderLabelKey
	resEmpty := renderLabelKey(nil)
	if resEmpty != "" {
		t.Errorf("expected empty string, got %q", resEmpty)
	}

	resVal := renderLabelKey(map[string]string{
		"a": "1",
		"b": "2",
		"c": "", // 空值应该被跳过
	})
	expected := `{a="1",b="2"}`
	if resVal != expected {
		t.Errorf("expected %q, got %q", expected, resVal)
	}

	// 3. parseLabelKey edge cases
	pEmpty := parseLabelKey("")
	if len(pEmpty) != 0 {
		t.Errorf("expected empty map, got %v", pEmpty)
	}
}

