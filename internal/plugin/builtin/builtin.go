package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/plugin"
	"github.com/joey/lumen-gateway/internal/runtimectx"
)

func Register(registry *plugin.Registry) error {
	registers := []func(*plugin.Registry) error{
		registerRequestID,
		registerLimitCount,
		registerRewritePathRegex,
		registerRequestTransformer,
		registerResponseTransformer,
		registerReplacePath,
		registerStripPrefix,
		registerAddPrefix,
	}
	for _, register := range registers {
		if err := register(registry); err != nil {
			return err
		}
	}
	return nil
}

type requestTransformerConfig struct {
	Method string `yaml:"method"`
	Host   string `yaml:"host"`
	Add    struct {
		Headers map[string]string `yaml:"headers"`
		Query   map[string]string `yaml:"query"`
	} `yaml:"add"`
	Set struct {
		Headers map[string]string `yaml:"headers"`
		Query   map[string]string `yaml:"query"`
	} `yaml:"set"`
	Remove struct {
		Headers []string `yaml:"headers"`
		Query   []string `yaml:"query"`
	} `yaml:"remove"`
}

func registerRequestTransformer(registry *plugin.Registry) error {
	return plugin.RegisterTyped(registry, plugin.Metadata{
		Name:     "request_transformer",
		Priority: 100,
		Scopes:   plugin.AllScopes(),
	}, func(cfg requestTransformerConfig) (app.HandlerFunc, error) {
		return func(ctx context.Context, c *app.RequestContext) {
			if cfg.Method != "" {
				c.Request.SetMethod(strings.ToUpper(strings.TrimSpace(renderRequestTemplate(c, cfg.Method))))
			}
			if cfg.Host != "" {
				host := renderRequestTemplate(c, cfg.Host)
				c.Request.SetHost(host)
				c.Request.Header.SetHost(host)
			}
			for key, value := range cfg.Add.Headers {
				if key != "" {
					c.Request.Header.Add(key, renderRequestTemplate(c, value))
				}
			}
			for key, value := range cfg.Add.Query {
				if key != "" {
					c.Request.URI().QueryArgs().Add(key, renderRequestTemplate(c, value))
				}
			}
			for _, header := range cfg.Remove.Headers {
				if header != "" {
					c.Request.Header.Del(header)
				}
			}
			for _, key := range cfg.Remove.Query {
				if key != "" {
					c.Request.URI().QueryArgs().Del(key)
				}
			}
			for key, value := range cfg.Set.Headers {
				if key != "" {
					c.Request.Header.Set(key, renderRequestTemplate(c, value))
				}
			}
			for key, value := range cfg.Set.Query {
				if key != "" {
					c.Request.URI().QueryArgs().Set(key, renderRequestTemplate(c, value))
				}
			}
			c.Next(ctx)
		}, nil
	})
}

type rewritePathRegexConfig struct {
	Rules []rewritePathRegexRule `yaml:"rules"`
}

type rewritePathRegexRule struct {
	Pattern     string `yaml:"pattern"`
	Replacement string `yaml:"replacement"`
}

func registerRewritePathRegex(registry *plugin.Registry) error {
	return plugin.RegisterTyped(registry, plugin.Metadata{
		Name:     "rewrite_path_regex",
		Priority: 0,
		Scopes:   plugin.AllScopes(),
	}, func(cfg rewritePathRegexConfig) (app.HandlerFunc, error) {
		if len(cfg.Rules) == 0 {
			return nil, errors.New("rewrite_path_regex requires at least one rule")
		}

		type compiledRule struct {
			regex       *regexp.Regexp
			replacement string
		}

		rules := make([]compiledRule, 0, len(cfg.Rules))
		for _, rule := range cfg.Rules {
			if strings.TrimSpace(rule.Pattern) == "" {
				return nil, errors.New("rewrite_path_regex pattern cannot be empty")
			}
			re, err := regexp.Compile(rule.Pattern)
			if err != nil {
				return nil, err
			}
			rules = append(rules, compiledRule{
				regex:       re,
				replacement: rule.Replacement,
			})
		}

		return func(ctx context.Context, c *app.RequestContext) {
			path := string(c.Path())
			for _, rule := range rules {
				matches := rule.regex.FindStringSubmatch(path)
				if len(matches) == 0 {
					continue
				}
				c.Set(runtimectx.RegexCapturesKey, matches[1:])
				rewritten := renderRequestTemplate(c, rule.regex.ReplaceAllString(path, rule.replacement))
				if rewritten != "" && !strings.HasPrefix(rewritten, "/") {
					rewritten = "/" + rewritten
				}
				c.Request.URI().SetPath(rewritten)
				break
			}
			c.Next(ctx)
		}, nil
	})
}

type responseTransformerConfig struct {
	Status      int    `yaml:"status"`
	Body        string `yaml:"body"`
	BodyBase64  bool   `yaml:"body_base64"`
	ContentType string `yaml:"content_type"`
	Add         struct {
		Headers map[string]string `yaml:"headers"`
	} `yaml:"add"`
	Set struct {
		Headers map[string]string `yaml:"headers"`
	} `yaml:"set"`
	Remove struct {
		Headers []string `yaml:"headers"`
	} `yaml:"remove"`
}

func registerResponseTransformer(registry *plugin.Registry) error {
	return plugin.RegisterTyped(registry, plugin.Metadata{
		Name:     "response_transformer",
		Priority: 0,
		Scopes:   plugin.AllScopes(),
	}, func(cfg responseTransformerConfig) (app.HandlerFunc, error) {
		body := cfg.Body
		if cfg.BodyBase64 && body != "" {
			decoded, err := base64.StdEncoding.DecodeString(body)
			if err != nil {
				return nil, err
			}
			body = string(decoded)
		}
		return func(ctx context.Context, c *app.RequestContext) {
			c.Next(ctx)
			for _, header := range cfg.Remove.Headers {
				if header != "" {
					c.Response.Header.Del(header)
				}
			}
			for key, value := range cfg.Add.Headers {
				if key != "" {
					c.Response.Header.Add(key, renderRequestTemplate(c, value))
				}
			}
			for key, value := range cfg.Set.Headers {
				if key != "" {
					c.Response.Header.Set(key, renderRequestTemplate(c, value))
				}
			}
			if cfg.Status > 0 {
				c.Response.SetStatusCode(cfg.Status)
			}
			if body != "" {
				if cfg.ContentType != "" {
					c.Response.Header.Set("Content-Type", cfg.ContentType)
				}
				c.Response.SetBodyString(renderRequestTemplate(c, body))
			}
		}, nil
	})
}

type replacePathConfig struct {
	Path string `yaml:"path"`
}

func registerReplacePath(registry *plugin.Registry) error {
	return plugin.RegisterTyped(registry, plugin.Metadata{
		Name:     "replace_path",
		Priority: 0,
		Scopes:   plugin.AllScopes(),
	}, func(cfg replacePathConfig) (app.HandlerFunc, error) {
		if cfg.Path == "" {
			return nil, errors.New("replace_path requires path")
		}
		return func(ctx context.Context, c *app.RequestContext) {
			path := renderRequestTemplate(c, cfg.Path)
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			c.Request.URI().SetPath(path)
			c.Next(ctx)
		}, nil
	})
}

type stripPrefixConfig struct {
	Prefixes []string `yaml:"prefixes"`
	Prefix   string   `yaml:"prefix"`
}

func registerStripPrefix(registry *plugin.Registry) error {
	return plugin.RegisterTyped(registry, plugin.Metadata{
		Name:     "strip_prefix",
		Priority: 0,
		Scopes:   plugin.AllScopes(),
	}, func(cfg stripPrefixConfig) (app.HandlerFunc, error) {
		prefixes := cfg.Prefixes
		if cfg.Prefix != "" {
			prefixes = append(prefixes, cfg.Prefix)
		}
		if len(prefixes) == 0 {
			return nil, errors.New("strip_prefix requires prefix or prefixes")
		}
		rawPrefixes := make([][]byte, 0, len(prefixes))
		for _, prefix := range prefixes {
			rawPrefixes = append(rawPrefixes, []byte(prefix))
		}
		return func(ctx context.Context, c *app.RequestContext) {
			for _, prefix := range rawPrefixes {
				if after, ok := bytes.CutPrefix(c.Request.Path(), prefix); ok {
					if len(after) == 0 {
						after = []byte("/")
					}
					c.Request.URI().SetPathBytes(after)
					break
				}
			}
			c.Next(ctx)
		}, nil
	})
}

type addPrefixConfig struct {
	Prefix string `yaml:"prefix"`
}

func registerAddPrefix(registry *plugin.Registry) error {
	return plugin.RegisterTyped(registry, plugin.Metadata{
		Name:     "add_prefix",
		Priority: 0,
		Scopes:   plugin.AllScopes(),
	}, func(cfg addPrefixConfig) (app.HandlerFunc, error) {
		if cfg.Prefix == "" {
			return nil, errors.New("add_prefix requires prefix")
		}
		if !strings.HasPrefix(cfg.Prefix, "/") {
			cfg.Prefix = "/" + cfg.Prefix
		}
		return func(ctx context.Context, c *app.RequestContext) {
			path := string(c.Request.Path())
			if path == "/" {
				c.Request.URI().SetPath(renderRequestTemplate(c, cfg.Prefix))
			} else {
				c.Request.URI().SetPath(renderRequestTemplate(c, cfg.Prefix) + path)
			}
			c.Next(ctx)
		}, nil
	})
}
