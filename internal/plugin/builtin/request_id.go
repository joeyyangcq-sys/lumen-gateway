package builtin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mathrand "math/rand"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/plugin"
	"github.com/joey/lumen-gateway/internal/runtimectx"
)

type requestIDConfig struct {
	HeaderName        string `yaml:"header_name"`
	IncludeInResponse *bool  `yaml:"include_in_response"`
	Algorithm         string `yaml:"algorithm"`
	RangeID           struct {
		CharSet string `yaml:"char_set"`
		Length  int    `yaml:"length"`
	} `yaml:"range_id"`
}

func registerRequestID(registry *plugin.Registry) error {
	return plugin.RegisterTyped(registry, plugin.Metadata{
		Name:     "request_id",
		Priority: 1200,
		Scopes:   plugin.AllScopes(),
	}, func(cfg requestIDConfig) (app.HandlerFunc, error) {
		headerName := strings.TrimSpace(cfg.HeaderName)
		if headerName == "" {
			headerName = "X-Request-Id"
		}

		includeInResponse := true
		if cfg.IncludeInResponse != nil {
			includeInResponse = *cfg.IncludeInResponse
		}

		algorithm := strings.TrimSpace(cfg.Algorithm)
		if algorithm == "" {
			algorithm = "uuid"
		}

		generate, err := buildRequestIDGenerator(algorithm, cfg)
		if err != nil {
			return nil, err
		}

		return func(ctx context.Context, c *app.RequestContext) {
			requestID := strings.TrimSpace(c.Request.Header.Get(headerName))
			if requestID == "" {
				requestID = generate()
				c.Request.Header.Set(headerName, requestID)
			}

			c.Set(runtimectx.RequestIDKey, requestID)
			c.Next(ctx)

			if includeInResponse {
				c.Response.Header.Set(headerName, requestID)
			}
		}, nil
	})
}

func buildRequestIDGenerator(algorithm string, cfg requestIDConfig) (func() string, error) {
	switch algorithm {
	case "uuid":
		return generateUUID, nil
	case "nanoid":
		return func() string {
			return generateRandomString(21, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_-")
		}, nil
	case "range_id":
		charset := cfg.RangeID.CharSet
		if charset == "" {
			charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIGKLMNOPQRSTUVWXYZ0123456789"
		}
		length := cfg.RangeID.Length
		if length == 0 {
			length = 16
		}
		if len(charset) < 6 {
			return nil, fmt.Errorf("request_id range_id.char_set length must be at least 6")
		}
		if length < 6 {
			return nil, fmt.Errorf("request_id range_id.length must be at least 6")
		}
		return func() string {
			return generateRandomString(length, charset)
		}, nil
	default:
		return nil, fmt.Errorf("request_id algorithm %q is not supported", algorithm)
	}
}

func generateUUID() string {
	buffer := randomBytes(16)
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		hex.EncodeToString(buffer[0:4]),
		hex.EncodeToString(buffer[4:6]),
		hex.EncodeToString(buffer[6:8]),
		hex.EncodeToString(buffer[8:10]),
		hex.EncodeToString(buffer[10:16]),
	)
}

func generateRandomString(length int, charset string) string {
	if length <= 0 || charset == "" {
		return ""
	}
	bytes := randomBytes(length)
	out := make([]byte, length)
	for index := range out {
		out[index] = charset[int(bytes[index])%len(charset)]
	}
	return string(out)
}

func randomBytes(length int) []byte {
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		fallback := mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
		for index := range buffer {
			buffer[index] = byte(fallback.Intn(256))
		}
	}
	return buffer
}
