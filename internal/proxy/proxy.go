package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/config"
	"github.com/joey/lumen-gateway/internal/observability"
	"github.com/joey/lumen-gateway/internal/plugin"
)

var (
	ErrInvalidTarget = errors.New("invalid upstream target")
	ErrBadGateway    = errors.New("upstream request failed")
	ErrTimeout       = errors.New("upstream request timed out")
)

// hopByHopHeaders contains headers that must not be forwarded to the upstream
// or downstream. Both Title-Case (canonical net/http form) and lowercase
// variants are included so no per-call EqualFold is needed.
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

type Target struct {
	Scheme  string
	Address string
	Host    string
}

type Proxy interface {
	ServeHTTP(ctx context.Context, c *app.RequestContext, target Target) error
}

type HTTPOptions struct {
	Transport http.RoundTripper
	Timeout   config.TimeoutOptions
}

type HTTPProxy struct {
	client *http.Client
}

func NewHTTP(options HTTPOptions) *HTTPProxy {
	transport := options.Transport
	if transport == nil {
		defaultTransport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          1024,
			MaxIdleConnsPerHost:   512,
			MaxConnsPerHost:       0,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: options.Timeout.Read,
			TLSHandshakeTimeout:   options.Timeout.Connect,
		}
		if options.Timeout.Connect > 0 {
			dialer := &net.Dialer{Timeout: options.Timeout.Connect}
			defaultTransport.DialContext = dialer.DialContext
		}
		transport = defaultTransport
	}

	return &HTTPProxy{
		client: &http.Client{
			Timeout:   requestTimeout(options.Timeout),
			Transport: transport,
		},
	}
}

func (p *HTTPProxy) ServeHTTP(ctx context.Context, c *app.RequestContext, target Target) error {
	req, err := newUpstreamRequest(ctx, c, target)
	if err != nil {
		return err
	}

	timings := newTraceTimings(target)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), timings.clientTrace()))
	resp, err := p.client.Do(req)
	timings.finishRoundTrip(err)
	if err != nil {
		recordUpstreamObservation(c, target, timings.snapshot(), 0, classifyErrorType(err, timings))
		return classifyRequestError(err)
	}
	defer resp.Body.Close()

	c.Response.Reset()
	c.Response.SetStatusCode(resp.StatusCode)
	for key, values := range resp.Header {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			c.Response.Header.Add(key, value)
		}
	}
	_, err = io.Copy(c.Response.BodyWriter(), resp.Body)
	timings.finishBodyRead()
	if err != nil {
		recordUpstreamObservation(c, target, timings.snapshot(), resp.StatusCode, classifyErrorType(err, timings))
		return classifyRequestError(err)
	}
	recordUpstreamObservation(c, target, timings.snapshot(), resp.StatusCode, "")
	return nil
}

func newUpstreamRequest(ctx context.Context, c *app.RequestContext, target Target) (*http.Request, error) {
	if target.Address == "" {
		return nil, ErrInvalidTarget
	}

	scheme := target.Scheme
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return nil, ErrInvalidTarget
	}

	uri := &url.URL{
		Scheme:   scheme,
		Host:     target.Address,
		Path:     string(c.Path()),
		RawQuery: string(c.Request.URI().QueryArgs().QueryString()),
	}

	body := io.Reader(nil)
	contentLength := int64(0)
	if c.Request.IsBodyStream() {
		body = c.Request.BodyStream()
		if length := c.Request.Header.ContentLength(); length >= 0 {
			contentLength = int64(length)
		} else {
			contentLength = -1
		}
	} else if raw := c.Request.Body(); len(raw) > 0 {
		body = bytes.NewReader(raw)
		contentLength = int64(len(raw))
	}

	req, err := http.NewRequestWithContext(ctx, string(c.Method()), uri.String(), body)
	if err != nil {
		return nil, ErrInvalidTarget
	}

	if target.Host != "" {
		req.Host = target.Host
	}

	c.Request.Header.VisitAll(func(key, value []byte) {
		headerKey := string(key)
		if headerKey == "" || strings.EqualFold(headerKey, "Host") || isHopByHopHeader(headerKey) {
			return
		}
		req.Header.Add(headerKey, string(value))
	})

	if req.Body != nil {
		req.ContentLength = contentLength
	}
	return req, nil
}

func classifyRequestError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrTimeout
	}
	return ErrBadGateway
}

func requestTimeout(timeout config.TimeoutOptions) time.Duration {
	total := timeout.Connect + timeout.Read + timeout.Write
	if total > 0 {
		return total
	}
	if timeout.Read > 0 {
		return timeout.Read
	}
	if timeout.Write > 0 {
		return timeout.Write
	}
	return 0
}

func isHopByHopHeader(key string) bool {
	_, ok := hopByHopHeaders[key]
	return ok
}

type traceTimings struct {
	gotConnAt         time.Time
	bodyReadDoneAt    time.Time
	connectStart      time.Time
	connectDone       time.Time
	tlsHandshakeStart time.Time
	tlsHandshakeDone  time.Time
	firstByteAt       time.Time
	wroteRequestAt    time.Time
	start             time.Time
	writeErr          error
	connectErr        error
	tlsErr            error
	target            observability.ProxyInfo
	reusedConn        bool
}

func newTraceTimings(target Target) *traceTimings {
	return &traceTimings{
		target: observability.ProxyInfo{
			Scheme:  target.Scheme,
			Address: target.Address,
			Host:    target.Host,
		},
		start: time.Now(),
	}
}

func (t *traceTimings) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			t.reusedConn = info.Reused
			t.gotConnAt = time.Now()
		},
		ConnectStart: func(_, _ string) {
			t.connectStart = time.Now()
		},
		ConnectDone: func(_, _ string, err error) {
			t.connectDone = time.Now()
			t.connectErr = err
		},
		TLSHandshakeStart: func() {
			t.tlsHandshakeStart = time.Now()
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			t.tlsHandshakeDone = time.Now()
			t.tlsErr = err
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			t.wroteRequestAt = time.Now()
			t.writeErr = info.Err
		},
		GotFirstResponseByte: func() {
			t.firstByteAt = time.Now()
		},
	}
}

func (t *traceTimings) finishRoundTrip(err error) {
	if err == nil && t.firstByteAt.IsZero() {
		t.firstByteAt = time.Now()
	}
}

func (t *traceTimings) finishBodyRead() {
	t.bodyReadDoneAt = time.Now()
}

func (t *traceTimings) snapshot() observability.ProxyInfo {
	info := t.target
	info.ReusedConnection = t.reusedConn

	if !t.connectStart.IsZero() && !t.connectDone.IsZero() {
		info.ConnectTime = t.connectDone.Sub(t.connectStart)
	}
	if !t.tlsHandshakeStart.IsZero() && !t.tlsHandshakeDone.IsZero() {
		info.TLSHandshakeTime = t.tlsHandshakeDone.Sub(t.tlsHandshakeStart)
	}
	writeStart := t.gotConnAt
	if writeStart.IsZero() {
		writeStart = t.start
	}
	if !t.tlsHandshakeDone.IsZero() {
		writeStart = t.tlsHandshakeDone
	}
	if !t.wroteRequestAt.IsZero() {
		info.RequestWriteTime = t.wroteRequestAt.Sub(writeStart)
	}
	if !t.wroteRequestAt.IsZero() && !t.firstByteAt.IsZero() {
		info.FirstByteTime = t.firstByteAt.Sub(t.wroteRequestAt)
		info.ProcessingEstimate = info.FirstByteTime
	}
	end := t.bodyReadDoneAt
	if end.IsZero() {
		end = time.Now()
	}
	if !t.firstByteAt.IsZero() {
		info.ResponseReadTime = end.Sub(t.firstByteAt)
	}
	info.TotalTime = end.Sub(t.start)
	return info
}

func classifyErrorType(err error, timings *traceTimings) string {
	switch {
	case timings.connectErr != nil:
		return "connect_error"
	case timings.tlsErr != nil:
		return "tls_error"
	case timings.writeErr != nil:
		return "write_error"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return "timeout"
		}
		if !timings.firstByteAt.IsZero() {
			return "read_error"
		}
		return "bad_gateway"
	}
}

func recordUpstreamObservation(c *app.RequestContext, target Target, info observability.ProxyInfo, status int, errorType string) {
	plugin.SetProxyInfo(c, info)
	if status > 0 {
		plugin.SetUpstreamStatusCode(c, status)
	}

	statusClass := ""
	if status > 0 {
		statusClass = fmt.Sprintf("%dxx", status/100)
	}

	pc := plugin.FromRequestContext(c)
	observability.Default().ObserveUpstream(observability.UpstreamLabels{
		RouteID:     pc.RouteID(),
		ServiceID:   pc.ServiceID(),
		UpstreamID:  pc.UpstreamID(),
		Endpoint:    info.Address,
		Scheme:      target.Scheme,
		Method:      pc.RequestMethod(),
		StatusClass: statusClass,
		ErrorType:   errorType,
		ReusedConn:  info.ReusedConnection,
	}, info)
}
