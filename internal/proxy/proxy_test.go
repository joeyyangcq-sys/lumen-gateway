package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/observability"
	"github.com/joey/lumen-gateway/internal/plugin"
)

func TestHTTPProxyCapturesUpstreamTimings(t *testing.T) {
	previous := observability.Default()
	recorder := observability.NewMemoryRecorder()
	observability.SetDefault(recorder)
	defer observability.SetDefault(previous)

	proxy := NewHTTP(HTTPOptions{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			trace := httptrace.ContextClientTrace(req.Context())
			if trace != nil {
				trace.ConnectStart("tcp", "upstream.internal:443")
				time.Sleep(5 * time.Millisecond)
				trace.ConnectDone("tcp", "upstream.internal:443", nil)
				trace.TLSHandshakeStart()
				time.Sleep(5 * time.Millisecond)
				trace.TLSHandshakeDone(tls.ConnectionState{}, nil)
				trace.GotConn(httptrace.GotConnInfo{Conn: nil, Reused: false})
				time.Sleep(2 * time.Millisecond)
				trace.WroteRequest(httptrace.WroteRequestInfo{})
				time.Sleep(10 * time.Millisecond)
				trace.GotFirstResponseByte()
			}

			return &http.Response{
				StatusCode: http.StatusAccepted,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       &delayedReadCloser{reader: strings.NewReader("hello-world"), delay: 10 * time.Millisecond},
			}, nil
		}),
	})

	reqCtx := app.NewContext(0)
	reqCtx.Request.SetMethod("POST")
	reqCtx.Request.SetHost("client.example.com")
	reqCtx.Request.URI().SetPath("/demo")
	reqCtx.Request.URI().SetQueryString("id=1")
	reqCtx.Request.SetBodyString(strings.Repeat("payload", 32))
	plugin.SetRouteID(reqCtx, "route-a")
	plugin.SetServiceID(reqCtx, "service-a")
	plugin.SetUpstreamID(reqCtx, "upstream-a")

	if err := proxy.ServeHTTP(context.Background(), reqCtx, Target{
		Scheme:  "https",
		Address: "upstream.internal:443",
		Host:    "upstream.internal",
	}); err != nil {
		t.Fatalf("ServeHTTP() error = %v", err)
	}

	info := plugin.FromRequestContext(reqCtx).ProxyInfo()
	if got := reqCtx.Response.StatusCode(); got != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", got, http.StatusAccepted)
	}
	if info.TotalTime <= 0 {
		t.Fatalf("TotalTime = %s, want > 0", info.TotalTime)
	}
	if info.FirstByteTime <= 0 {
		t.Fatalf("FirstByteTime = %s, want > 0", info.FirstByteTime)
	}
	if info.ResponseReadTime <= 0 {
		t.Fatalf("ResponseReadTime = %s, want > 0", info.ResponseReadTime)
	}
	if got := plugin.FromRequestContext(reqCtx).UpstreamStatusCode(); got != http.StatusAccepted {
		t.Fatalf("UpstreamStatusCode = %d, want %d", got, http.StatusAccepted)
	}

	metrics := recorder.RenderPrometheus()
	if !strings.Contains(metrics, `phase="tls_handshake"`) {
		t.Fatalf("metrics missing tls_handshake phase:\n%s", metrics)
	}
	if !strings.Contains(metrics, `phase="total"`) {
		t.Fatalf("metrics missing total phase:\n%s", metrics)
	}
}

func TestHTTPProxyClassifiesUpstreamErrors(t *testing.T) {
	testCases := []struct {
		name            string
		method          string
		target          Target
		roundTrip       roundTripFunc
		wantErr         error
		wantErrorType   string
		wantStatusClass string
	}{
		{
			name:   "connect error",
			method: "GET",
			target: Target{Scheme: "http", Address: "connect.internal:80"},
			roundTrip: func(req *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(req.Context())
				if trace != nil {
					trace.ConnectStart("tcp", "connect.internal:80")
					trace.ConnectDone("tcp", "connect.internal:80", errors.New("dial failed"))
				}
				return nil, errors.New("dial failed")
			},
			wantErr:       ErrBadGateway,
			wantErrorType: "connect_error",
		},
		{
			name:   "tls error",
			method: "GET",
			target: Target{Scheme: "https", Address: "tls.internal:443"},
			roundTrip: func(req *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(req.Context())
				if trace != nil {
					trace.ConnectStart("tcp", "tls.internal:443")
					trace.ConnectDone("tcp", "tls.internal:443", nil)
					trace.TLSHandshakeStart()
					trace.TLSHandshakeDone(tls.ConnectionState{}, errors.New("tls failed"))
				}
				return nil, errors.New("tls failed")
			},
			wantErr:       ErrBadGateway,
			wantErrorType: "tls_error",
		},
		{
			name:   "write error",
			method: "POST",
			target: Target{Scheme: "http", Address: "write.internal:80"},
			roundTrip: func(req *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(req.Context())
				if trace != nil {
					trace.ConnectStart("tcp", "write.internal:80")
					trace.ConnectDone("tcp", "write.internal:80", nil)
					trace.GotConn(httptrace.GotConnInfo{Reused: false})
					trace.WroteRequest(httptrace.WroteRequestInfo{Err: errors.New("write failed")})
				}
				return nil, errors.New("write failed")
			},
			wantErr:       ErrBadGateway,
			wantErrorType: "write_error",
		},
		{
			name:   "read error",
			method: "GET",
			target: Target{Scheme: "http", Address: "read.internal:80"},
			roundTrip: func(req *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(req.Context())
				if trace != nil {
					trace.ConnectStart("tcp", "read.internal:80")
					trace.ConnectDone("tcp", "read.internal:80", nil)
					trace.GotConn(httptrace.GotConnInfo{Reused: false})
					trace.WroteRequest(httptrace.WroteRequestInfo{})
					trace.GotFirstResponseByte()
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/plain"}},
					Body:       errorReadCloser{err: errors.New("read failed")},
				}, nil
			},
			wantErr:         ErrBadGateway,
			wantErrorType:   "read_error",
			wantStatusClass: "2xx",
		},
		{
			name:   "bad gateway",
			method: "GET",
			target: Target{Scheme: "http", Address: "bad-gateway.internal:80"},
			roundTrip: func(req *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(req.Context())
				if trace != nil {
					trace.ConnectStart("tcp", "bad-gateway.internal:80")
					trace.ConnectDone("tcp", "bad-gateway.internal:80", nil)
					trace.GotConn(httptrace.GotConnInfo{Reused: false})
					trace.WroteRequest(httptrace.WroteRequestInfo{})
				}
				return nil, errors.New("connection closed")
			},
			wantErr:       ErrBadGateway,
			wantErrorType: "bad_gateway",
		},
		{
			name:   "timeout",
			method: "GET",
			target: Target{Scheme: "http", Address: "timeout.internal:80"},
			roundTrip: func(req *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(req.Context())
				if trace != nil {
					trace.ConnectStart("tcp", "timeout.internal:80")
					trace.ConnectDone("tcp", "timeout.internal:80", nil)
					trace.GotConn(httptrace.GotConnInfo{Reused: false})
					trace.WroteRequest(httptrace.WroteRequestInfo{})
				}
				return nil, timeoutNetError{}
			},
			wantErr:       ErrTimeout,
			wantErrorType: "timeout",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			previous := observability.Default()
			recorder := observability.NewMemoryRecorder()
			observability.SetDefault(recorder)
			defer observability.SetDefault(previous)

			reqCtx := newProxyTestContext(tc.method)
			proxy := NewHTTP(HTTPOptions{Transport: tc.roundTrip})

			err := proxy.ServeHTTP(context.Background(), reqCtx, tc.target)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ServeHTTP() error = %v, want %v", err, tc.wantErr)
			}

			metrics := recorder.RenderPrometheus()
			if !strings.Contains(metrics, fmt.Sprintf(`error_type="%s"`, tc.wantErrorType)) {
				t.Fatalf("metrics missing error_type=%q:\n%s", tc.wantErrorType, metrics)
			}
			if !strings.Contains(metrics, `route_id="route-err"`) {
				t.Fatalf("metrics missing route_id label:\n%s", metrics)
			}
			if tc.wantStatusClass == "" {
				if strings.Contains(metrics, `status_class="2xx"`) || strings.Contains(metrics, `status_class="5xx"`) {
					t.Fatalf("unexpected status_class in metrics:\n%s", metrics)
				}
			} else if !strings.Contains(metrics, fmt.Sprintf(`status_class="%s"`, tc.wantStatusClass)) {
				t.Fatalf("metrics missing status_class=%q:\n%s", tc.wantStatusClass, metrics)
			}
		})
	}
}

func newProxyTestContext(method string) *app.RequestContext {
	reqCtx := app.NewContext(0)
	reqCtx.Request.SetMethod(method)
	reqCtx.Request.SetHost("client.example.com")
	reqCtx.Request.URI().SetPath("/demo")
	reqCtx.Request.URI().SetQueryString("id=1")
	reqCtx.Request.SetBodyString(strings.Repeat("payload", 32))
	plugin.SetRouteID(reqCtx, "route-err")
	plugin.SetServiceID(reqCtx, "service-err")
	plugin.SetUpstreamID(reqCtx, "upstream-err")
	return reqCtx
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type delayedReadCloser struct {
	reader io.Reader
	delay  time.Duration
}

func (d *delayedReadCloser) Read(p []byte) (int, error) {
	time.Sleep(d.delay)
	return d.reader.Read(p)
}

func (d *delayedReadCloser) Close() error {
	return nil
}

type errorReadCloser struct {
	err error
}

func (e errorReadCloser) Read([]byte) (int, error) {
	return 0, e.err
}

func (e errorReadCloser) Close() error {
	return nil
}

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

var _ net.Error = timeoutNetError{}
