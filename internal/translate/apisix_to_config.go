package translate

import (
	"encoding/json"
	"errors"
	"fmt"
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

	for id, upstream := range s.Upstreams {
		up, err := apisixUpstreamToConfig(upstream)
		if err != nil {
			return config.Options{}, fmt.Errorf("upstream %q: %w", id, err)
		}
		out.Upstreams[id] = up
	}

	for id, service := range s.Services {
		svc, err := apisixServiceToConfig(service, out.Upstreams)
		if err != nil {
			return config.Options{}, fmt.Errorf("service %q: %w", id, err)
		}
		out.Services[id] = svc
	}

	for id, route := range s.Routes {
		rt, err := apisixRouteToConfig(route)
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

func apisixRouteToConfig(route apisix.Route) (config.RouteOptions, error) {
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

	return config.RouteOptions{
		ID:      id,
		Hosts:   route.Hosts,
		Methods: route.Methods,
		Paths:   paths,
		Service: "",
	}, nil
}

func apisixServiceToConfig(service apisix.Service, upstreams map[string]config.UpstreamOptions) (config.ServiceOptions, error) {
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

	return config.ServiceOptions{
		ID:       id,
		Protocol: "http",
		Upstream: upstreamID,
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
