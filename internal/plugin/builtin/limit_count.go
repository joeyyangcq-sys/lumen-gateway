package builtin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/plugin"
	"github.com/joey/lumen-gateway/internal/runtimectx"
)

type limitCountConfig struct {
	Count                int    `yaml:"count"`
	TimeWindow           int    `yaml:"time_window"`
	KeyType              string `yaml:"key_type"`
	Key                  string `yaml:"key"`
	RejectedCode         int    `yaml:"rejected_code"`
	RejectedMsg          string `yaml:"rejected_msg"`
	Policy               string `yaml:"policy"`
	ShowLimitQuotaHeader *bool  `yaml:"show_limit_quota_header"`
	Group                string `yaml:"group"`
}

type limitCountBucket struct {
	window int64
	count  int
}

func registerLimitCount(registry *plugin.Registry) error {
	return plugin.RegisterTyped(registry, plugin.Metadata{
		Name:     "limit_count",
		Priority: 1100,
		Scopes:   plugin.AllScopes(),
	}, func(cfg limitCountConfig) (app.HandlerFunc, error) {
		if cfg.Count <= 0 {
			return nil, fmt.Errorf("limit_count count must be greater than 0")
		}
		if cfg.TimeWindow <= 0 {
			return nil, fmt.Errorf("limit_count time_window must be greater than 0")
		}

		policy := cfg.Policy
		if policy == "" {
			policy = "local"
		}
		if policy != "local" {
			return nil, fmt.Errorf("limit_count policy %q is not supported", policy)
		}

		keyType := cfg.KeyType
		if keyType == "" {
			keyType = "var"
		}
		switch keyType {
		case "var", "var_combination", "constant":
		default:
			return nil, fmt.Errorf("limit_count key_type %q is not supported", keyType)
		}

		key := cfg.Key
		if key == "" {
			key = "remote_addr"
		}

		rejectedCode := cfg.RejectedCode
		if rejectedCode == 0 {
			rejectedCode = 503
		}

		showHeaders := true
		if cfg.ShowLimitQuotaHeader != nil {
			showHeaders = *cfg.ShowLimitQuotaHeader
		}

		counters := make(map[string]*limitCountBucket)
		var mu sync.Mutex

		return func(ctx context.Context, c *app.RequestContext) {
			scope := cfg.Group
			if scope == "" {
				scope = routeIDFromContext(c)
			}

			derivedKey := deriveLimitCountKey(c, keyType, key)
			counterKey := scope + "|" + derivedKey
			now := time.Now()
			window := now.Unix() / int64(cfg.TimeWindow)
			reset := cfg.TimeWindow - int(now.Unix()%int64(cfg.TimeWindow))

			mu.Lock()
			bucket := counters[counterKey]
			if bucket == nil {
				bucket = &limitCountBucket{}
				counters[counterKey] = bucket
			}
			if bucket.window != window {
				bucket.window = window
				bucket.count = 0
			}

			allowed := bucket.count < cfg.Count
			if allowed {
				bucket.count++
			}
			remaining := cfg.Count - bucket.count
			if remaining < 0 {
				remaining = 0
			}
			mu.Unlock()

			if !allowed {
				if showHeaders {
					setLimitCountHeaders(c, cfg.Count, 0, reset)
				}
				c.Response.SetStatusCode(rejectedCode)
				if cfg.RejectedMsg != "" {
					c.Response.SetBodyString(cfg.RejectedMsg)
				}
				c.Abort()
				return
			}

			c.Next(ctx)
			if showHeaders {
				setLimitCountHeaders(c, cfg.Count, remaining, reset)
			}
		}, nil
	})
}

func routeIDFromContext(c *app.RequestContext) string {
	value, ok := c.Get(runtimectx.RouteIDKey)
	if !ok {
		return ""
	}
	routeID, ok := value.(string)
	if !ok {
		return ""
	}
	return routeID
}

func deriveLimitCountKey(c *app.RequestContext, keyType, key string) string {
	switch keyType {
	case "constant":
		return key
	case "var":
		return resolveRequestVariable(c, key)
	case "var_combination":
		return renderRequestTemplate(c, key)
	default:
		return ""
	}
}

func setLimitCountHeaders(c *app.RequestContext, limit, remaining, reset int) {
	c.Response.Header.Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	c.Response.Header.Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
	c.Response.Header.Set("X-RateLimit-Reset", fmt.Sprintf("%d", reset))
}
