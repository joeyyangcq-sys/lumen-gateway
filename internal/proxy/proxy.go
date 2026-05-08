package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/config"
)

var (
	ErrInvalidTarget = errors.New("invalid upstream target")
	ErrBadGateway    = errors.New("upstream request failed")
	ErrTimeout       = errors.New("upstream request timed out")
)

type Target struct {
	Scheme  string
	Address string
	Host    string
}

type Proxy interface {
	ServeHTTP(ctx context.Context, c *app.RequestContext, target Target) error
}

type HTTPOptions struct {
	Timeout   config.TimeoutOptions
	Transport http.RoundTripper
}

type HTTPProxy struct {
	client *http.Client
}

func NewHTTP(options HTTPOptions) *HTTPProxy {
	transport := options.Transport
	if transport == nil {
		defaultTransport := &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			ForceAttemptHTTP2: true,
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

	resp, err := p.client.Do(req)
	if err != nil {
		return classifyRequestError(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return classifyRequestError(err)
	}

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
	c.Response.SetBodyRaw(body)
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
	if c.Request.IsBodyStream() {
		body = c.Request.BodyStream()
	} else if raw := c.Request.Body(); len(raw) > 0 {
		body = bytes.NewReader(raw)
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
		req.ContentLength = int64(len(c.Request.Body()))
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
	switch {
	case strings.EqualFold(key, "Connection"):
		return true
	case strings.EqualFold(key, "Keep-Alive"):
		return true
	case strings.EqualFold(key, "Proxy-Authenticate"):
		return true
	case strings.EqualFold(key, "Proxy-Authorization"):
		return true
	case strings.EqualFold(key, "Te"):
		return true
	case strings.EqualFold(key, "Trailer"):
		return true
	case strings.EqualFold(key, "Transfer-Encoding"):
		return true
	case strings.EqualFold(key, "Upgrade"):
		return true
	default:
		return false
	}
}
