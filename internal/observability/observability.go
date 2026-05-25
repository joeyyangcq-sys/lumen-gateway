package observability

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type PluginLabels struct {
	Plugin     string
	Scope      string
	Phase      string
	RouteID    string
	ServiceID  string
	UpstreamID string
	Result     string
}

type UpstreamLabels struct {
	RouteID     string
	ServiceID   string
	UpstreamID  string
	Endpoint    string
	Scheme      string
	Method      string
	StatusClass string
	ErrorType   string
	ReusedConn  bool
}

type GatewayLabels struct {
	Handler     string
	Method      string
	RouteID     string
	StatusClass string
}

type ProxyInfo struct {
	Scheme             string
	Address            string
	Host               string
	ConnectTime        time.Duration
	TLSHandshakeTime   time.Duration
	RequestWriteTime   time.Duration
	FirstByteTime      time.Duration
	ResponseReadTime   time.Duration
	TotalTime          time.Duration
	ProcessingEstimate time.Duration
	ReusedConnection   bool
}

type histogramSnapshot struct {
	Buckets map[float64]uint64
	Count   uint64
	Sum     float64
}

type Snapshot struct {
	PluginExecutions  map[string]uint64
	PluginDurations   map[string]histogramSnapshot
	UpstreamRequests  map[string]uint64
	UpstreamDurations map[string]histogramSnapshot
	GatewayRequests   map[string]uint64
	GatewayDurations  map[string]histogramSnapshot
}

// GatewayStats is a pre-computed summary derived from the current snapshot.
// Intended for the admin-UI overview — no Prometheus client required.
type GatewayStats struct {
	TopRoutes     []RouteStats `json:"top_routes"`
	RequestsTotal uint64       `json:"requests_total"`
	Errors4xx     uint64       `json:"errors_4xx"`
	Errors5xx     uint64       `json:"errors_5xx"`
	ErrorRate     float64      `json:"error_rate"`
}

// RouteStats is per-route request and error counts.
type RouteStats struct {
	RouteID  string `json:"route_id"`
	Requests uint64 `json:"requests"`
	Errors   uint64 `json:"errors"`
}

type Recorder interface {
	ObserveGateway(GatewayLabels, time.Duration)
	ObservePlugin(PluginLabels, time.Duration)
	ObserveUpstream(UpstreamLabels, ProxyInfo)
	RenderPrometheus() string
	Snapshot() Snapshot
	Stats() GatewayStats
	Reset()
}

type MemoryRecorder struct {
	pluginExecutions  map[string]uint64
	pluginDurations   map[string]*histogram
	upstreamRequests  map[string]uint64
	upstreamDurations map[string]*histogram
	gatewayRequests   map[string]uint64
	gatewayDurations  map[string]*histogram
	mu                sync.Mutex
}

type histogram struct {
	bins   map[float64]uint64
	bounds []float64
	count  uint64
	sum    float64
}

var (
	defaultRecorder Recorder = NewMemoryRecorder()
	defaultMu       sync.RWMutex
)

func NewMemoryRecorder() *MemoryRecorder {
	return &MemoryRecorder{
		pluginExecutions:  make(map[string]uint64),
		pluginDurations:   make(map[string]*histogram),
		upstreamRequests:  make(map[string]uint64),
		upstreamDurations: make(map[string]*histogram),
		gatewayRequests:   make(map[string]uint64),
		gatewayDurations:  make(map[string]*histogram),
	}
}

func Default() Recorder {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultRecorder
}

func SetDefault(recorder Recorder) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if recorder == nil {
		recorder = NewMemoryRecorder()
	}
	defaultRecorder = recorder
}

func (r *MemoryRecorder) ObserveGateway(labels GatewayLabels, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := renderGatewayKey(labels)
	r.gatewayRequests[key]++
	r.observeHistogram(r.gatewayDurations, key, duration.Seconds(), gatewayDurationBuckets())
}

func (r *MemoryRecorder) ObservePlugin(labels PluginLabels, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	executionKey := renderPluginKey(labels)
	r.pluginExecutions[executionKey]++
	r.observeHistogram(r.pluginDurations, executionKey, duration.Seconds(), pluginDurationBuckets())
}

func (r *MemoryRecorder) ObserveUpstream(labels UpstreamLabels, info ProxyInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()

	baseKey := renderUpstreamKey(labels, "")
	r.upstreamRequests[baseKey]++

	r.observeUpstreamPhase(labels, "connect", info.ConnectTime)
	r.observeUpstreamPhase(labels, "tls_handshake", info.TLSHandshakeTime)
	r.observeUpstreamPhase(labels, "request_write", info.RequestWriteTime)
	r.observeUpstreamPhase(labels, "first_byte", info.FirstByteTime)
	r.observeUpstreamPhase(labels, "response_read", info.ResponseReadTime)
	r.observeUpstreamPhase(labels, "processing_estimate", info.ProcessingEstimate)
	r.observeUpstreamPhase(labels, "total", info.TotalTime)
}

func (r *MemoryRecorder) RenderPrometheus() string {
	snapshot := r.Snapshot()
	var builder strings.Builder

	builder.WriteString("# HELP lumen_gateway_requests_total Total gateway requests by bounded handler and route.\n")
	builder.WriteString("# TYPE lumen_gateway_requests_total counter\n")
	for _, key := range sortedKeys(snapshot.GatewayRequests) {
		builder.WriteString(fmt.Sprintf("lumen_gateway_requests_total%s %d\n", key, snapshot.GatewayRequests[key]))
	}

	builder.WriteString("# HELP lumen_gateway_request_duration_seconds Gateway request duration by bounded handler and route.\n")
	builder.WriteString("# TYPE lumen_gateway_request_duration_seconds histogram\n")
	renderHistogramMetric(&builder, "lumen_gateway_request_duration_seconds", snapshot.GatewayDurations)

	builder.WriteString("# HELP lumen_plugin_executions_total Total plugin executions.\n")
	builder.WriteString("# TYPE lumen_plugin_executions_total counter\n")
	for _, key := range sortedKeys(snapshot.PluginExecutions) {
		builder.WriteString(fmt.Sprintf("lumen_plugin_executions_total%s %d\n", key, snapshot.PluginExecutions[key]))
	}

	builder.WriteString("# HELP lumen_plugin_duration_seconds Plugin execution duration.\n")
	builder.WriteString("# TYPE lumen_plugin_duration_seconds histogram\n")
	renderHistogramMetric(&builder, "lumen_plugin_duration_seconds", snapshot.PluginDurations)

	builder.WriteString("# HELP lumen_upstream_requests_total Total upstream requests.\n")
	builder.WriteString("# TYPE lumen_upstream_requests_total counter\n")
	for _, key := range sortedKeys(snapshot.UpstreamRequests) {
		builder.WriteString(fmt.Sprintf("lumen_upstream_requests_total%s %d\n", key, snapshot.UpstreamRequests[key]))
	}

	builder.WriteString("# HELP lumen_upstream_phase_duration_seconds Upstream phase durations.\n")
	builder.WriteString("# TYPE lumen_upstream_phase_duration_seconds histogram\n")
	renderHistogramMetric(&builder, "lumen_upstream_phase_duration_seconds", snapshot.UpstreamDurations)

	return builder.String()
}

func (r *MemoryRecorder) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	return Snapshot{
		PluginExecutions:  cloneCounterMap(r.pluginExecutions),
		PluginDurations:   cloneHistogramMap(r.pluginDurations),
		UpstreamRequests:  cloneCounterMap(r.upstreamRequests),
		UpstreamDurations: cloneHistogramMap(r.upstreamDurations),
		GatewayRequests:   cloneCounterMap(r.gatewayRequests),
		GatewayDurations:  cloneHistogramMap(r.gatewayDurations),
	}
}

func (r *MemoryRecorder) Stats() GatewayStats {
	snap := r.Snapshot()

	routeTotals := make(map[string]uint64)
	routeErrors := make(map[string]uint64)

	var total, e4xx, e5xx uint64
	for key, count := range snap.UpstreamRequests {
		labels := parseLabelKey(key)
		total += count

		class := labels["status_class"]
		switch {
		case len(class) > 0 && class[0] == '4':
			e4xx += count
		case len(class) > 0 && class[0] == '5':
			e5xx += count
		}

		rid := labels["route_id"]
		if rid != "" {
			routeTotals[rid] += count
			if len(class) > 0 && (class[0] == '4' || class[0] == '5') {
				routeErrors[rid] += count
			}
		}
	}

	var errorRate float64
	if total > 0 {
		errorRate = float64(e4xx+e5xx) / float64(total) * 100
	}

	// top-5 routes by request volume
	type routeEntry struct {
		id   string
		reqs uint64
	}
	entries := make([]routeEntry, 0, len(routeTotals))
	for id, reqs := range routeTotals {
		entries = append(entries, routeEntry{id: id, reqs: reqs})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].reqs > entries[j].reqs })
	if len(entries) > 5 {
		entries = entries[:5]
	}
	topRoutes := make([]RouteStats, len(entries))
	for i, e := range entries {
		topRoutes[i] = RouteStats{
			RouteID:  e.id,
			Requests: e.reqs,
			Errors:   routeErrors[e.id],
		}
	}

	return GatewayStats{
		RequestsTotal: total,
		Errors4xx:     e4xx,
		Errors5xx:     e5xx,
		ErrorRate:     errorRate,
		TopRoutes:     topRoutes,
	}
}

func (r *MemoryRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.pluginExecutions = make(map[string]uint64)
	r.pluginDurations = make(map[string]*histogram)
	r.upstreamRequests = make(map[string]uint64)
	r.upstreamDurations = make(map[string]*histogram)
	r.gatewayRequests = make(map[string]uint64)
	r.gatewayDurations = make(map[string]*histogram)
}

func (r *MemoryRecorder) observeHistogram(target map[string]*histogram, key string, value float64, buckets []float64) {
	h, ok := target[key]
	if !ok {
		h = newHistogram(buckets)
		target[key] = h
	}
	h.observe(value)
}

func (r *MemoryRecorder) observeUpstreamPhase(labels UpstreamLabels, phase string, duration time.Duration) {
	key := renderUpstreamKey(labels, phase)
	r.observeHistogram(r.upstreamDurations, key, duration.Seconds(), upstreamDurationBuckets())
}

func newHistogram(bounds []float64) *histogram {
	bins := make(map[float64]uint64, len(bounds))
	for _, bound := range bounds {
		bins[bound] = 0
	}
	return &histogram{
		bounds: append([]float64(nil), bounds...),
		bins:   bins,
	}
}

func (h *histogram) observe(value float64) {
	h.count++
	h.sum += value
	for _, bound := range h.bounds {
		if value <= bound {
			h.bins[bound]++
		}
	}
}

func renderHistogramMetric(builder *strings.Builder, name string, metrics map[string]histogramSnapshot) {
	for _, key := range sortedHistogramKeys(metrics) {
		snapshot := metrics[key]
		bounds := sortedBounds(snapshot.Buckets)
		for _, bound := range bounds {
			builder.WriteString(fmt.Sprintf("%s_bucket%s,le=\"%s\"} %d\n", name, trimRightBrace(key), formatBound(bound), snapshot.Buckets[bound]))
		}
		builder.WriteString(fmt.Sprintf("%s_bucket%s,le=\"+Inf\"} %d\n", name, trimRightBrace(key), snapshot.Count))
		builder.WriteString(fmt.Sprintf("%s_sum%s %g\n", name, key, snapshot.Sum))
		builder.WriteString(fmt.Sprintf("%s_count%s %d\n", name, key, snapshot.Count))
	}
}

func trimRightBrace(key string) string {
	return strings.TrimSuffix(key, "}")
}

func formatBound(bound float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", bound), "0"), ".")
}

func pluginDurationBuckets() []float64 {
	return []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}
}

func upstreamDurationBuckets() []float64 {
	return []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
}

func gatewayDurationBuckets() []float64 {
	return []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
}

// renderGatewayKey uses only bounded labels. Handler is one of health, metrics,
// admin, proxy, unmatched, or unavailable; route_id is empty except matched proxy routes.
func renderGatewayKey(labels GatewayLabels) string {
	var b strings.Builder
	b.Grow(120)
	b.WriteByte('{')
	first := true
	first = appendLabel(&b, first, "handler", labels.Handler)
	first = appendLabel(&b, first, "method", labels.Method)
	first = appendLabel(&b, first, "route_id", labels.RouteID)
	_ = appendLabel(&b, first, "status_class", labels.StatusClass)
	b.WriteByte('}')
	return b.String()
}

// renderPluginKey builds the Prometheus label key for plugin metrics.
// Keys are in sorted order: phase, plugin, result, route_id, scope, service_id, upstream_id.
func renderPluginKey(labels PluginLabels) string {
	var b strings.Builder
	b.Grow(160)
	b.WriteByte('{')
	first := true
	first = appendLabel(&b, first, "phase", labels.Phase)
	first = appendLabel(&b, first, "plugin", labels.Plugin)
	first = appendLabel(&b, first, "result", labels.Result)
	first = appendLabel(&b, first, "route_id", labels.RouteID)
	first = appendLabel(&b, first, "scope", labels.Scope)
	first = appendLabel(&b, first, "service_id", labels.ServiceID)
	_ = appendLabel(&b, first, "upstream_id", labels.UpstreamID)
	b.WriteByte('}')
	return b.String()
}

// renderUpstreamKey builds the Prometheus label key for upstream metrics.
// If phase is non-empty it is inserted in sorted position (between "method" and "reused_conn").
// Keys are in sorted order: endpoint, error_type, method, [phase,] reused_conn, route_id, scheme,
// service_id, status_class, upstream_id.
func renderUpstreamKey(labels UpstreamLabels, phase string) string {
	reusedConnStr := "false"
	if labels.ReusedConn {
		reusedConnStr = "true"
	}
	var b strings.Builder
	b.Grow(220)
	b.WriteByte('{')
	first := true
	first = appendLabel(&b, first, "endpoint", labels.Endpoint)
	first = appendLabel(&b, first, "error_type", labels.ErrorType)
	first = appendLabel(&b, first, "method", labels.Method)
	first = appendLabel(&b, first, "phase", phase)
	first = appendLabel(&b, first, "reused_conn", reusedConnStr)
	first = appendLabel(&b, first, "route_id", labels.RouteID)
	first = appendLabel(&b, first, "scheme", labels.Scheme)
	first = appendLabel(&b, first, "service_id", labels.ServiceID)
	first = appendLabel(&b, first, "status_class", labels.StatusClass)
	_ = appendLabel(&b, first, "upstream_id", labels.UpstreamID)
	b.WriteByte('}')
	return b.String()
}

// appendLabel writes key="value" to b in Prometheus label format, preceded by a comma
// if it is not the first label. Empty values are skipped. Returns whether the next
// label written will still be the first.
func appendLabel(b *strings.Builder, first bool, key, value string) bool {
	if value == "" {
		return first
	}
	if !first {
		b.WriteByte(',')
	}
	fmt.Fprintf(b, "%s=%q", key, value)
	return false
}

// renderLabelKey is the generic label renderer used by Stats() and tests.
func renderLabelKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key, value := range labels {
		if value == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", key, labels[key]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func parseLabelKey(key string) map[string]string {
	result := make(map[string]string)
	if key == "" {
		return result
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(key, "{"), "}")
	if trimmed == "" {
		return result
	}
	for _, part := range strings.Split(trimmed, ",") {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		result[name] = strings.Trim(value, "\"")
	}
	return result
}

func cloneCounterMap(source map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func cloneHistogramMap(source map[string]*histogram) map[string]histogramSnapshot {
	out := make(map[string]histogramSnapshot, len(source))
	for key, value := range source {
		buckets := make(map[float64]uint64, len(value.bins))
		for bound, count := range value.bins {
			buckets[bound] = count
		}
		out[key] = histogramSnapshot{
			Count:   value.count,
			Sum:     value.sum,
			Buckets: buckets,
		}
	}
	return out
}

func sortedKeys(values map[string]uint64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedHistogramKeys(values map[string]histogramSnapshot) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedBounds(values map[float64]uint64) []float64 {
	keys := make([]float64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Float64s(keys)
	return keys
}
