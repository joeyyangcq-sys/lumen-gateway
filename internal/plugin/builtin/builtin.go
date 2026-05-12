package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"

	"github.com/joey/lumen-gateway/internal/plugin"
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
		registerAccessLog,
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
	return plugin.RegisterTypedContext(registry, plugin.Metadata{
		Name:     "request_transformer",
		Priority: 100,
		Scopes:   plugin.AllScopes(),
	}, func(cfg requestTransformerConfig) (plugin.ContextHandler, error) {
		return func(ctx context.Context, pc plugin.PluginContext) {
			if cfg.Method != "" {
				pc.SetRequestMethod(strings.ToUpper(strings.TrimSpace(renderRequestTemplate(pc, cfg.Method))))
			}
			if cfg.Host != "" {
				pc.SetRequestHost(renderRequestTemplate(pc, cfg.Host))
			}
			for key, value := range cfg.Add.Headers {
				if key != "" {
					pc.AddRequestHeader(key, renderRequestTemplate(pc, value))
				}
			}
			for key, value := range cfg.Add.Query {
				if key != "" {
					pc.AddRequestQuery(key, renderRequestTemplate(pc, value))
				}
			}
			for _, header := range cfg.Remove.Headers {
				if header != "" {
					pc.DelRequestHeader(header)
				}
			}
			for _, key := range cfg.Remove.Query {
				if key != "" {
					pc.DelRequestQuery(key)
				}
			}
			for key, value := range cfg.Set.Headers {
				if key != "" {
					pc.SetRequestHeader(key, renderRequestTemplate(pc, value))
				}
			}
			for key, value := range cfg.Set.Query {
				if key != "" {
					pc.SetRequestQuery(key, renderRequestTemplate(pc, value))
				}
			}
			pc.Next(ctx)
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
	return plugin.RegisterTypedContext(registry, plugin.Metadata{
		Name:     "rewrite_path_regex",
		Priority: 0,
		Scopes:   plugin.AllScopes(),
	}, func(cfg rewritePathRegexConfig) (plugin.ContextHandler, error) {
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

		return func(ctx context.Context, pc plugin.PluginContext) {
			path := pc.RequestPath()
			for _, rule := range rules {
				matches := rule.regex.FindStringSubmatch(path)
				if len(matches) == 0 {
					continue
				}
				pc.SetRegexCaptures(matches[1:])
				rewritten := renderRequestTemplate(pc, rule.regex.ReplaceAllString(path, rule.replacement))
				if rewritten != "" && !strings.HasPrefix(rewritten, "/") {
					rewritten = "/" + rewritten
				}
				pc.SetRequestPath(rewritten)
				break
			}
			pc.Next(ctx)
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
	return plugin.RegisterTypedContext(registry, plugin.Metadata{
		Name:     "response_transformer",
		Priority: 0,
		Scopes:   plugin.AllScopes(),
	}, func(cfg responseTransformerConfig) (plugin.ContextHandler, error) {
		body := cfg.Body
		if cfg.BodyBase64 && body != "" {
			decoded, err := base64.StdEncoding.DecodeString(body)
			if err != nil {
				return nil, err
			}
			body = string(decoded)
		}
		return func(ctx context.Context, pc plugin.PluginContext) {
			pc.Next(ctx)
			for _, header := range cfg.Remove.Headers {
				if header != "" {
					pc.DelResponseHeader(header)
				}
			}
			for key, value := range cfg.Add.Headers {
				if key != "" {
					pc.AddResponseHeader(key, renderRequestTemplate(pc, value))
				}
			}
			for key, value := range cfg.Set.Headers {
				if key != "" {
					pc.SetResponseHeader(key, renderRequestTemplate(pc, value))
				}
			}
			if cfg.Status > 0 {
				pc.SetResponseStatus(cfg.Status)
			}
			if body != "" {
				if cfg.ContentType != "" {
					pc.SetResponseHeader("Content-Type", cfg.ContentType)
				}
				pc.SetResponseBody([]byte(renderRequestTemplate(pc, body)))
			}
		}, nil
	})
}

type replacePathConfig struct {
	Path string `yaml:"path"`
}

func registerReplacePath(registry *plugin.Registry) error {
	return plugin.RegisterTypedContext(registry, plugin.Metadata{
		Name:     "replace_path",
		Priority: 0,
		Scopes:   plugin.AllScopes(),
	}, func(cfg replacePathConfig) (plugin.ContextHandler, error) {
		if cfg.Path == "" {
			return nil, errors.New("replace_path requires path")
		}
		return func(ctx context.Context, pc plugin.PluginContext) {
			path := renderRequestTemplate(pc, cfg.Path)
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			pc.SetRequestPath(path)
			pc.Next(ctx)
		}, nil
	})
}

type stripPrefixConfig struct {
	Prefixes []string `yaml:"prefixes"`
	Prefix   string   `yaml:"prefix"`
}

func registerStripPrefix(registry *plugin.Registry) error {
	return plugin.RegisterTypedContext(registry, plugin.Metadata{
		Name:     "strip_prefix",
		Priority: 0,
		Scopes:   plugin.AllScopes(),
	}, func(cfg stripPrefixConfig) (plugin.ContextHandler, error) {
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
		return func(ctx context.Context, pc plugin.PluginContext) {
			raw := pc.Raw()
			for _, prefix := range rawPrefixes {
				if after, ok := bytes.CutPrefix(raw.Request.Path(), prefix); ok {
					if len(after) == 0 {
						after = []byte("/")
					}
					raw.Request.URI().SetPathBytes(after)
					break
				}
			}
			pc.Next(ctx)
		}, nil
	})
}

type addPrefixConfig struct {
	Prefix string `yaml:"prefix"`
}

func registerAddPrefix(registry *plugin.Registry) error {
	return plugin.RegisterTypedContext(registry, plugin.Metadata{
		Name:     "add_prefix",
		Priority: 0,
		Scopes:   plugin.AllScopes(),
	}, func(cfg addPrefixConfig) (plugin.ContextHandler, error) {
		if cfg.Prefix == "" {
			return nil, errors.New("add_prefix requires prefix")
		}
		if !strings.HasPrefix(cfg.Prefix, "/") {
			cfg.Prefix = "/" + cfg.Prefix
		}
		return func(ctx context.Context, pc plugin.PluginContext) {
			path := pc.RequestPath()
			if path == "/" {
				pc.SetRequestPath(renderRequestTemplate(pc, cfg.Prefix))
			} else {
				pc.SetRequestPath(renderRequestTemplate(pc, cfg.Prefix) + path)
			}
			pc.Next(ctx)
		}, nil
	})
}
