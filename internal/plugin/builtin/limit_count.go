package builtin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/joey/lumen-gateway/internal/plugin"
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
	return plugin.RegisterTypedContext(registry, plugin.Metadata{
		Name:     "limit_count",
		Priority: 1100,
		Scopes:   plugin.AllScopes(),
	}, func(cfg limitCountConfig) (plugin.ContextHandler, error) {
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

		return func(ctx context.Context, pc plugin.PluginContext) {
			scope := cfg.Group
			if scope == "" {
				scope = pc.RouteID()
			}

			derivedKey := deriveLimitCountKey(pc, keyType, key)
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
					setLimitCountHeaders(pc, cfg.Count, 0, reset, cfg.TimeWindow, derivedKey)
				}
				pc.SetResponseStatus(rejectedCode)
				if cfg.RejectedMsg != "" {
					pc.SetResponseBody([]byte(cfg.RejectedMsg))
				}
				pc.Abort()
				return
			}

			pc.Next(ctx)
			if showHeaders {
				setLimitCountHeaders(pc, cfg.Count, remaining, reset, cfg.TimeWindow, derivedKey)
			}
		}, nil
	})
}

func deriveLimitCountKey(pc plugin.PluginContext, keyType, key string) string {
	switch keyType {
	case "constant":
		return key
	case "var":
		return resolveRequestVariable(pc, key)
	case "var_combination":
		return renderRequestTemplate(pc, key)
	default:
		return ""
	}
}

func setLimitCountHeaders(pc plugin.PluginContext, limit, remaining, reset, window int, key string) {
	pc.SetResponseHeader("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	pc.SetResponseHeader("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
	pc.SetResponseHeader("X-RateLimit-Reset", fmt.Sprintf("%d", reset))   // seconds until window resets
	pc.SetResponseHeader("X-RateLimit-Window", fmt.Sprintf("%d", window)) // total window size in seconds
	pc.SetResponseHeader("X-RateLimit-Key", key)                          // actual key used for this counter
}
