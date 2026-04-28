package builtin

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/plugin"
)

func Register(registry *plugin.Registry) error {
	registers := []func(*plugin.Registry) error{
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
	Host string `yaml:"host"`
	Add  struct {
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
	return registry.Register("request_transformer", func(params any) (app.HandlerFunc, error) {
		cfg := requestTransformerConfig{}
		if err := plugin.Decode(params, &cfg); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *app.RequestContext) {
			if cfg.Host != "" {
				c.Request.SetHost(cfg.Host)
				c.Request.Header.SetHost(cfg.Host)
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
			for key, value := range cfg.Add.Headers {
				if key != "" && c.Request.Header.Get(key) == "" {
					c.Request.Header.Set(key, value)
				}
			}
			for key, value := range cfg.Add.Query {
				if key != "" && string(c.Query(key)) == "" {
					c.Request.URI().QueryArgs().Add(key, value)
				}
			}
			for key, value := range cfg.Set.Headers {
				if key != "" {
					c.Request.Header.Set(key, value)
				}
			}
			for key, value := range cfg.Set.Query {
				if key != "" {
					c.Request.URI().QueryArgs().Set(key, value)
				}
			}
			c.Next(ctx)
		}, nil
	})
}

type responseTransformerConfig struct {
	Status      int    `yaml:"status"`
	Body        string `yaml:"body"`
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
	return registry.Register("response_transformer", func(params any) (app.HandlerFunc, error) {
		cfg := responseTransformerConfig{}
		if err := plugin.Decode(params, &cfg); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *app.RequestContext) {
			c.Next(ctx)
			for _, header := range cfg.Remove.Headers {
				if header != "" {
					c.Response.Header.Del(header)
				}
			}
			for key, value := range cfg.Add.Headers {
				if key != "" && c.Response.Header.Get(key) == "" {
					c.Response.Header.Set(key, value)
				}
			}
			for key, value := range cfg.Set.Headers {
				if key != "" {
					c.Response.Header.Set(key, value)
				}
			}
			if cfg.Status > 0 {
				c.Response.SetStatusCode(cfg.Status)
			}
			if cfg.Body != "" {
				if cfg.ContentType != "" {
					c.Response.Header.Set("Content-Type", cfg.ContentType)
				}
				c.Response.SetBodyString(cfg.Body)
			}
		}, nil
	})
}

type replacePathConfig struct {
	Path string `yaml:"path"`
}

func registerReplacePath(registry *plugin.Registry) error {
	return registry.Register("replace_path", func(params any) (app.HandlerFunc, error) {
		cfg := replacePathConfig{}
		if err := plugin.Decode(params, &cfg); err != nil {
			return nil, err
		}
		if cfg.Path == "" {
			return nil, errors.New("replace_path requires path")
		}
		if !strings.HasPrefix(cfg.Path, "/") {
			cfg.Path = "/" + cfg.Path
		}
		return func(ctx context.Context, c *app.RequestContext) {
			c.Request.URI().SetPath(cfg.Path)
			c.Next(ctx)
		}, nil
	})
}

type stripPrefixConfig struct {
	Prefixes []string `yaml:"prefixes"`
	Prefix   string   `yaml:"prefix"`
}

func registerStripPrefix(registry *plugin.Registry) error {
	return registry.Register("strip_prefix", func(params any) (app.HandlerFunc, error) {
		cfg := stripPrefixConfig{}
		if err := plugin.Decode(params, &cfg); err != nil {
			return nil, err
		}
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
	return registry.Register("add_prefix", func(params any) (app.HandlerFunc, error) {
		cfg := addPrefixConfig{}
		if err := plugin.Decode(params, &cfg); err != nil {
			return nil, err
		}
		if cfg.Prefix == "" {
			return nil, errors.New("add_prefix requires prefix")
		}
		if !strings.HasPrefix(cfg.Prefix, "/") {
			cfg.Prefix = "/" + cfg.Prefix
		}
		return func(ctx context.Context, c *app.RequestContext) {
			path := string(c.Request.Path())
			if path == "/" {
				c.Request.URI().SetPath(cfg.Prefix)
			} else {
				c.Request.URI().SetPath(cfg.Prefix + path)
			}
			c.Next(ctx)
		}, nil
	})
}
