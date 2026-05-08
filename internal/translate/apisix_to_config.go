package translate

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/joey/lumen-gateway/internal/apisix"
	"github.com/joey/lumen-gateway/internal/config"
)

type ApisixToConfigOptions struct {
	Listen string
}

// ApisixSnapshotToConfig converts a subset of the APISIX etcd model into Lumen's internal config.Options.
// This is intentionally narrow: it only maps hosts/methods/uri(s) + upstream nodes, leaving plugin
// translation to later phases.
func ApisixSnapshotToConfig(s apisix.Snapshot, opts ApisixToConfigOptions) (config.Options, error) {
	if opts.Listen == "" {
		return config.Options{}, errors.New("listen cannot be empty")
	}

	out := config.Options{
		Servers: map[string]config.ServerOptions{
			"main": {ID: "main", Listen: opts.Listen},
		},
		Routes:    make(map[string]config.RouteOptions, len(s.Routes)),
		Services:  make(map[string]config.ServiceOptions),
		Upstreams: make(map[string]config.UpstreamOptions),
	}

	globalPlugins, err := apisixGlobalRulesToPlugins(s.GlobalRules)
	if err != nil {
		return config.Options{}, fmt.Errorf("global rules: %w", err)
	}
	out.GlobalPlugins = globalPlugins

	for id, upstream := range s.Upstreams {
		up, err := apisixUpstreamToConfig(upstream)
		if err != nil {
			return config.Options{}, fmt.Errorf("upstream %q: %w", id, err)
		}
		out.Upstreams[id] = up
	}

	for id, service := range s.Services {
		svc, err := apisixServiceToConfig(service, s.PluginConfig, out.Upstreams)
		if err != nil {
			return config.Options{}, fmt.Errorf("service %q: %w", id, err)
		}
		out.Services[id] = svc
	}

	for id, route := range s.Routes {
		rt, err := apisixRouteToConfig(route, s.PluginConfig)
		if err != nil {
			return config.Options{}, fmt.Errorf("route %q: %w", id, err)
		}

		// Resolve service reference.
		if route.ServiceID != "" {
			rt.Service = route.ServiceID.String()
		} else if route.UpstreamID != "" {
			upstreamID := route.UpstreamID.String()
			serviceID := "service-" + upstreamID
			if _, ok := out.Services[serviceID]; !ok {
				out.Services[serviceID] = config.ServiceOptions{
					ID:       serviceID,
					Protocol: "http",
					Upstream: upstreamID,
				}
			}
			rt.Service = serviceID
		} else if route.Upstream != nil {
			upstreamID := "upstream-route-" + id
			up, err := apisixUpstreamToConfig(*route.Upstream)
			if err != nil {
				return config.Options{}, fmt.Errorf("route %q inline upstream: %w", id, err)
			}
			up.ID = upstreamID
			out.Upstreams[upstreamID] = up
			serviceID := "service-" + upstreamID
			out.Services[serviceID] = config.ServiceOptions{
				ID:       serviceID,
				Protocol: "http",
				Upstream: upstreamID,
			}
			rt.Service = serviceID
		}

		out.Routes[id] = rt
	}

	if err := out.Validate(); err != nil {
		return config.Options{}, err
	}

	return out, nil
}

func apisixRouteToConfig(route apisix.Route, pluginConfigs map[string]apisix.PluginConfig) (config.RouteOptions, error) {
	id := route.ID.String()
	if id == "" {
		id = "route"
	}

	paths := make([]string, 0, 1+len(route.URIs))
	if route.URI != "" {
		paths = append(paths, normalizeApisixURI(route.URI))
	}
	for _, uri := range route.URIs {
		if uri == "" {
			continue
		}
		paths = append(paths, normalizeApisixURI(uri))
	}
	if len(paths) == 0 {
		return config.RouteOptions{}, errors.New("uri/uris cannot be empty")
	}

	plugins, err := apisixMergePlugins(route.PluginConfigID, route.Plugins, pluginConfigs)
	if err != nil {
		return config.RouteOptions{}, fmt.Errorf("plugins: %w", err)
	}

	return config.RouteOptions{
		ID:      id,
		Hosts:   route.Hosts,
		Methods: route.Methods,
		Paths:   paths,
		Service: "",
		Plugins: plugins,
	}, nil
}

func apisixServiceToConfig(service apisix.Service, pluginConfigs map[string]apisix.PluginConfig, upstreams map[string]config.UpstreamOptions) (config.ServiceOptions, error) {
	id := service.ID.String()
	if id == "" {
		id = "service"
	}

	upstreamID := service.UpstreamID.String()
	if upstreamID == "" && service.Upstream != nil {
		upstreamID = "upstream-service-" + id
		up, err := apisixUpstreamToConfig(*service.Upstream)
		if err != nil {
			return config.ServiceOptions{}, err
		}
		up.ID = upstreamID
		upstreams[upstreamID] = up
	}

	if upstreamID == "" {
		return config.ServiceOptions{}, errors.New("missing upstream_id/upstream")
	}

	plugins, err := apisixMergePlugins(service.PluginConfigID, service.Plugins, pluginConfigs)
	if err != nil {
		return config.ServiceOptions{}, fmt.Errorf("plugins: %w", err)
	}

	return config.ServiceOptions{
		ID:       id,
		Protocol: "http",
		Upstream: upstreamID,
		Plugins:  plugins,
	}, nil
}

func apisixUpstreamToConfig(up apisix.Upstream) (config.UpstreamOptions, error) {
	id := up.ID.String()
	if id == "" {
		id = "upstream"
	}

	endpoints, err := apisixNodesToEndpoints(up.Nodes)
	if err != nil {
		return config.UpstreamOptions{}, err
	}

	return config.UpstreamOptions{
		ID:           id,
		Scheme:       up.Scheme,
		PassHost:     up.PassHost,
		UpstreamHost: up.UpstreamHost,
		Endpoints:    endpoints,
	}, nil
}

func apisixNodesToEndpoints(raw json.RawMessage) ([]config.EndpointOptions, error) {
	if len(raw) == 0 {
		return nil, errors.New("nodes cannot be empty")
	}

	// Common APISIX form: {"127.0.0.1:1980": 1, "foo.com:80": 2}
	asMap := map[string]uint32{}
	if err := json.Unmarshal(raw, &asMap); err == nil && len(asMap) > 0 {
		endpoints := make([]config.EndpointOptions, 0, len(asMap))
		for addr, weight := range asMap {
			if addr == "" {
				continue
			}
			if weight == 0 {
				weight = 1
			}
			endpoints = append(endpoints, config.EndpointOptions{Address: addr, Weight: weight})
		}
		if len(endpoints) == 0 {
			return nil, errors.New("nodes map is empty")
		}
		return endpoints, nil
	}

	// Alternate form: [{"host":"127.0.0.1","port":1980,"weight":1}]
	var asList []struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Weight   uint32 `json:"weight"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(raw, &asList); err != nil {
		return nil, fmt.Errorf("unsupported nodes format: %w", err)
	}

	endpoints := make([]config.EndpointOptions, 0, len(asList))
	for _, node := range asList {
		if node.Host == "" || node.Port == 0 {
			continue
		}
		weight := node.Weight
		if weight == 0 {
			weight = 1
		}
		endpoints = append(endpoints, config.EndpointOptions{
			Address: fmt.Sprintf("%s:%d", node.Host, node.Port),
			Weight:  weight,
		})
	}
	if len(endpoints) == 0 {
		return nil, errors.New("nodes list is empty")
	}
	return endpoints, nil
}

func normalizeApisixURI(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return uri
	}
	if !strings.HasPrefix(uri, "/") {
		uri = "/" + uri
	}

	// Lumen's router supports:
	// - "= /exact" exact match
	// - plain prefix match (strings.HasPrefix)
	// - "~<regex>" regex match (added as part of skeleton)
	//
	// Map APISIX wildcard forms into a regex-like syntax:
	// - "/foo/*" => "/foo/" (prefix)
	// - "/foo*"  => "/foo"  (prefix)
	// - anything with "*" in the middle stays as-is and will be handled by regex matching later.
	if strings.Contains(uri, "*") {
		if strings.HasSuffix(uri, "/*") {
			return strings.TrimSuffix(uri, "*")
		}
		if strings.HasSuffix(uri, "*") {
			return strings.TrimSuffix(uri, "*")
		}
		return "~ " + apisixWildcardToRegex(uri)
	}

	// Default to exact match for APISIX full path match semantics.
	return "= " + uri
}

func apisixWildcardToRegex(uri string) string {
	// Very small subset:
	// - ":param" becomes a single segment wildcard
	// - "*" becomes a single segment wildcard unless it's trailing, in which case it becomes ".*"
	//
	// This intentionally does not try to clone libradixtree.
	escaped := make([]rune, 0, len(uri)+8)
	for i := 0; i < len(uri); i++ {
		ch := uri[i]
		switch ch {
		case '.', '+', '?', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\':
			escaped = append(escaped, '\\', rune(ch))
		default:
			escaped = append(escaped, rune(ch))
		}
	}
	s := string(escaped)
	s = strings.ReplaceAll(s, "/*", "/[^/]+")
	s = strings.ReplaceAll(s, "*", "[^/]+")

	// Parameters in radixtree_uri_with_parameter, example: /anything/user/:user_id/profile
	parts := strings.Split(s, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") && len(p) > 1 {
			parts[i] = "[^/]+"
		}
	}
	s = strings.Join(parts, "/")
	return "^" + s + "$"
}

func apisixGlobalRulesToPlugins(globalRules map[string]apisix.GlobalRule) ([]config.PluginRef, error) {
	if len(globalRules) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(globalRules))
	for id := range globalRules {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	merged := make(map[string]json.RawMessage)
	for _, id := range ids {
		plugins, err := decodeApisixPlugins(globalRules[id].Plugins)
		if err != nil {
			return nil, fmt.Errorf("global rule %q: %w", id, err)
		}
		mergeApisixPluginMaps(merged, plugins)
	}

	return apisixPluginsToRefs(merged)
}

func apisixMergePlugins(
	pluginConfigID apisix.ID,
	inline json.RawMessage,
	pluginConfigs map[string]apisix.PluginConfig,
) ([]config.PluginRef, error) {
	merged := make(map[string]json.RawMessage)

	if pluginConfigID != "" {
		pluginConfig, ok := pluginConfigs[pluginConfigID.String()]
		if !ok {
			return nil, fmt.Errorf("plugin_config %q not found", pluginConfigID)
		}
		plugins, err := decodeApisixPlugins(pluginConfig.Plugins)
		if err != nil {
			return nil, fmt.Errorf("plugin_config %q: %w", pluginConfigID, err)
		}
		mergeApisixPluginMaps(merged, plugins)
	}

	inlinePlugins, err := decodeApisixPlugins(inline)
	if err != nil {
		return nil, err
	}
	mergeApisixPluginMaps(merged, inlinePlugins)

	return apisixPluginsToRefs(merged)
}

func decodeApisixPlugins(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	plugins := make(map[string]json.RawMessage)
	if err := json.Unmarshal(raw, &plugins); err != nil {
		return nil, fmt.Errorf("decode plugins: %w", err)
	}
	return plugins, nil
}

func mergeApisixPluginMaps(dst map[string]json.RawMessage, src map[string]json.RawMessage) {
	for name, raw := range src {
		if isDisabledApisixPlugin(raw) {
			delete(dst, name)
			continue
		}
		dst[name] = raw
	}
}

func apisixPluginsToRefs(plugins map[string]json.RawMessage) ([]config.PluginRef, error) {
	if len(plugins) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(plugins))
	for name := range plugins {
		names = append(names, name)
	}
	sort.Strings(names)

	refs := make([]config.PluginRef, 0, len(names))
	for _, name := range names {
		translated, err := translateApisixPlugin(name, plugins[name])
		if err != nil {
			return nil, fmt.Errorf("plugin %q: %w", name, err)
		}
		refs = append(refs, translated...)
	}
	return refs, nil
}

func translateApisixPlugin(name string, raw json.RawMessage) ([]config.PluginRef, error) {
	switch name {
	case "proxy-rewrite":
		return translateProxyRewrite(raw)
	case "response-rewrite":
		return translateResponseRewrite(raw)
	default:
		return nil, nil
	}
}

func translateProxyRewrite(raw json.RawMessage) ([]config.PluginRef, error) {
	type proxyRewrite struct {
		Host    string            `json:"host"`
		URI     string            `json:"uri"`
		Headers map[string]string `json:"headers"`
	}

	cfg := proxyRewrite{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("decode proxy-rewrite: %w", err)
	}

	refs := make([]config.PluginRef, 0, 2)
	if cfg.Host != "" || len(cfg.Headers) > 0 {
		params := map[string]any{
			"host": cfg.Host,
			"set": map[string]any{
				"headers": cfg.Headers,
			},
		}
		refs = append(refs, config.PluginRef{
			Name:   "request_transformer",
			Params: params,
		})
	}
	if cfg.URI != "" {
		refs = append(refs, config.PluginRef{
			Name:   "replace_path",
			Params: map[string]any{"path": cfg.URI},
		})
	}
	return refs, nil
}

func translateResponseRewrite(raw json.RawMessage) ([]config.PluginRef, error) {
	type responseRewrite struct {
		StatusCode int               `json:"status_code"`
		Body       string            `json:"body"`
		Headers    map[string]string `json:"headers"`
	}

	cfg := responseRewrite{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("decode response-rewrite: %w", err)
	}

	if cfg.StatusCode == 0 && cfg.Body == "" && len(cfg.Headers) == 0 {
		return nil, nil
	}

	return []config.PluginRef{{
		Name: "response_transformer",
		Params: map[string]any{
			"status": cfg.StatusCode,
			"body":   cfg.Body,
			"set": map[string]any{
				"headers": cfg.Headers,
			},
		},
	}}, nil
}

func isDisabledApisixPlugin(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var meta struct {
		Meta struct {
			Disable bool `json:"disable"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return false
	}
	return meta.Meta.Disable
}
