package translate

import (
	"encoding/base64"
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
	case "request-id":
		return translateRequestID(raw)
	case "limit-count":
		return translateLimitCount(raw)
	default:
		return nil, nil
	}
}

func translateProxyRewrite(raw json.RawMessage) ([]config.PluginRef, error) {
	type proxyRewrite struct {
		Host                    string          `json:"host"`
		URI                     string          `json:"uri"`
		Method                  string          `json:"method"`
		RegexURI                []string        `json:"regex_uri"`
		Headers                 json.RawMessage `json:"headers"`
		UseRealRequestURIUnsafe bool            `json:"use_real_request_uri_unsafe"`
	}

	cfg := proxyRewrite{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("decode proxy-rewrite: %w", err)
	}

	headers, err := decodeProxyRewriteHeaders(cfg.Headers)
	if err != nil {
		return nil, err
	}

	refs := make([]config.PluginRef, 0, 3)
	if cfg.URI == "" && len(cfg.RegexURI) > 0 {
		if len(cfg.RegexURI)%2 != 0 {
			return nil, errors.New("regex_uri requires pattern/replacement pairs")
		}

		rules := make([]map[string]any, 0, len(cfg.RegexURI)/2)
		for index := 0; index < len(cfg.RegexURI); index += 2 {
			rules = append(rules, map[string]any{
				"pattern":     cfg.RegexURI[index],
				"replacement": cfg.RegexURI[index+1],
			})
		}

		refs = append(refs, config.PluginRef{
			Name: "rewrite_path_regex",
			Params: map[string]any{
				"rules": rules,
			},
		})
	}

	if cfg.Method != "" || cfg.Host != "" || len(headers.Add) > 0 || len(headers.Set) > 0 || len(headers.Remove) > 0 {
		params := map[string]any{
			"method": cfg.Method,
			"host":   cfg.Host,
		}
		if len(headers.Add) > 0 {
			params["add"] = map[string]any{
				"headers": headers.Add,
			}
		}
		if len(headers.Remove) > 0 {
			params["remove"] = map[string]any{
				"headers": headers.Remove,
			}
		}
		if len(headers.Set) > 0 {
			params["set"] = map[string]any{
				"headers": headers.Set,
			}
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

func decodeProxyRewriteHeaders(raw json.RawMessage) (struct {
	Add    map[string]string
	Set    map[string]string
	Remove []string
}, error) {
	out := struct {
		Add    map[string]string
		Set    map[string]string
		Remove []string
	}{}

	if len(raw) == 0 {
		return out, nil
	}

	var actionHeaders struct {
		Add    map[string]string `json:"add"`
		Set    map[string]string `json:"set"`
		Remove []string          `json:"remove"`
	}
	if err := json.Unmarshal(raw, &actionHeaders); err != nil {
		return out, fmt.Errorf("decode proxy-rewrite headers: %w", err)
	}
	if len(actionHeaders.Add) > 0 || len(actionHeaders.Set) > 0 || len(actionHeaders.Remove) > 0 {
		out.Add = actionHeaders.Add
		out.Set = actionHeaders.Set
		out.Remove = actionHeaders.Remove
		return out, nil
	}

	plainSet := make(map[string]string)
	if err := json.Unmarshal(raw, &plainSet); err != nil {
		return out, fmt.Errorf("decode proxy-rewrite headers: %w", err)
	}
	out.Set = plainSet
	return out, nil
}

func translateResponseRewrite(raw json.RawMessage) ([]config.PluginRef, error) {
	type responseRewrite struct {
		StatusCode int             `json:"status_code"`
		Body       string          `json:"body"`
		BodyBase64 bool            `json:"body_base64"`
		Headers    json.RawMessage `json:"headers"`
	}

	cfg := responseRewrite{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("decode response-rewrite: %w", err)
	}

	headers, err := decodeResponseRewriteHeaders(cfg.Headers)
	if err != nil {
		return nil, err
	}

	if cfg.StatusCode == 0 && cfg.Body == "" && !cfg.BodyBase64 && len(headers.Add) == 0 && len(headers.Set) == 0 && len(headers.Remove) == 0 {
		return nil, nil
	}

	params := map[string]any{
		"status": cfg.StatusCode,
		"body":   cfg.Body,
	}
	if cfg.BodyBase64 {
		if _, err := base64.StdEncoding.DecodeString(cfg.Body); err != nil {
			return nil, fmt.Errorf("decode response-rewrite body_base64: %w", err)
		}
		params["body_base64"] = true
	}
	if len(headers.Add) > 0 {
		params["add"] = map[string]any{
			"headers": headers.Add,
		}
	}
	if len(headers.Set) > 0 {
		params["set"] = map[string]any{
			"headers": headers.Set,
		}
	}
	if len(headers.Remove) > 0 {
		params["remove"] = map[string]any{
			"headers": headers.Remove,
		}
	}

	return []config.PluginRef{{
		Name:   "response_transformer",
		Params: params,
	}}, nil
}

func translateRequestID(raw json.RawMessage) ([]config.PluginRef, error) {
	params := make(map[string]any)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, fmt.Errorf("decode request-id: %w", err)
		}
	}
	return []config.PluginRef{{
		Name:   "request_id",
		Params: params,
	}}, nil
}

func translateLimitCount(raw json.RawMessage) ([]config.PluginRef, error) {
	params := make(map[string]any)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, fmt.Errorf("decode limit-count: %w", err)
		}
	}
	return []config.PluginRef{{
		Name:   "limit_count",
		Params: params,
	}}, nil
}

func decodeResponseRewriteHeaders(raw json.RawMessage) (struct {
	Add    map[string]string
	Set    map[string]string
	Remove []string
}, error) {
	out := struct {
		Add    map[string]string
		Set    map[string]string
		Remove []string
	}{}

	if len(raw) == 0 {
		return out, nil
	}

	var actionHeaders struct {
		Add    map[string]string `json:"add"`
		Set    map[string]string `json:"set"`
		Remove []string          `json:"remove"`
	}
	if err := json.Unmarshal(raw, &actionHeaders); err != nil {
		return out, fmt.Errorf("decode response-rewrite headers: %w", err)
	}
	if len(actionHeaders.Add) > 0 || len(actionHeaders.Set) > 0 || len(actionHeaders.Remove) > 0 {
		out.Add = actionHeaders.Add
		out.Set = actionHeaders.Set
		out.Remove = actionHeaders.Remove
		return out, nil
	}

	plainSet := make(map[string]string)
	if err := json.Unmarshal(raw, &plainSet); err != nil {
		return out, fmt.Errorf("decode response-rewrite headers: %w", err)
	}
	out.Set = plainSet
	return out, nil
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
