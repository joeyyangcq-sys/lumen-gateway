package router

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/joey/lumen-gateway/internal/config"
)

type Router struct {
	routes []compiledRoute
}

func New() *Router {
	return &Router{
		routes: make([]compiledRoute, 0),
	}
}

func (r *Router) Add(route config.RouteOptions) error {
	if route.ID == "" {
		return fmt.Errorf("route id cannot be empty")
	}

	compiled, err := compileRoute(route)
	if err != nil {
		return err
	}

	r.routes = append(r.routes, compiled)
	return nil
}

func (r *Router) Match(method, host, path string) (config.RouteOptions, bool) {
	method = strings.ToUpper(method)
	var (
		bestRoute config.RouteOptions
		bestScore int
		found     bool
	)

	for _, entry := range r.routes {
		route := entry.route
		if !matchMethod(route.Methods, method) {
			continue
		}
		if !matchHost(route.Hosts, host) {
			continue
		}

		pathScore := entry.matchPath(path)
		if pathScore == 0 {
			continue
		}

		// Score order:
		// 1) route.Priority (higher wins)
		// 2) path match strength/specificity
		score := route.Priority*1_000_000 + pathScore
		if !found || score > bestScore {
			bestRoute = route
			bestScore = score
			found = true
		}
	}
	return bestRoute, found
}

func matchMethod(methods []string, method string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, m := range methods {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}

func matchHost(hosts []string, host string) bool {
	if len(hosts) == 0 {
		return true
	}
	for _, h := range hosts {
		if strings.EqualFold(h, host) {
			return true
		}
		// Very small wildcard support: "*.example.com"
		if strings.HasPrefix(h, "*.") {
			suffix := h[2:]
			if suffix != "" && len(host) > len(suffix) && host[len(host)-len(suffix)-1] == '.' && hasSuffixFold(host, suffix) {
				return true
			}
		}
		// Very small wildcard support: "example.*"
		if strings.HasSuffix(h, ".*") {
			prefix := h[:len(h)-2]
			if prefix != "" && len(host) > len(prefix) && host[len(prefix)] == '.' && hasPrefixFold(host, prefix) {
				return true
			}
		}
	}
	return false
}

func hasSuffixFold(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return strings.EqualFold(s[:len(prefix)], prefix)
}

func IsValidMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodTrace,
		http.MethodConnect:
		return true
	default:
		return false
	}
}

type pathKind int

const (
	pathPrefix pathKind = iota
	pathExact
	pathRegex
)

type compiledPath struct {
	regex       *regexp.Regexp
	exact       string
	prefix      string
	kind        pathKind
	specificity int
}

type compiledRoute struct {
	route config.RouteOptions
	paths []compiledPath
}

func compileRoute(route config.RouteOptions) (compiledRoute, error) {
	if len(route.Paths) == 0 {
		return compiledRoute{}, fmt.Errorf("route %q paths cannot be empty", route.ID)
	}

	paths := make([]compiledPath, 0, len(route.Paths))
	for _, raw := range route.Paths {
		cp, err := compilePath(raw)
		if err != nil {
			return compiledRoute{}, fmt.Errorf("route %q path %q: %w", route.ID, raw, err)
		}
		paths = append(paths, cp)
	}

	return compiledRoute{
		route: route,
		paths: paths,
	}, nil
}

func (r compiledRoute) matchPath(path string) int {
	best := 0
	for _, p := range r.paths {
		switch p.kind {
		case pathExact:
			if path == p.exact {
				score := 300_000 + p.specificity
				if score > best {
					best = score
				}
			}
		case pathRegex:
			if p.regex != nil && p.regex.MatchString(path) {
				score := 200_000 + p.specificity
				if score > best {
					best = score
				}
			}
		case pathPrefix:
			if path == p.prefix || strings.HasPrefix(path, p.prefix) {
				score := 100_000 + p.specificity
				if score > best {
					best = score
				}
			}
		}
	}
	return best
}

func compilePath(raw string) (compiledPath, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return compiledPath{}, fmt.Errorf("path cannot be empty")
	}

	switch {
	case strings.HasPrefix(raw, "= "):
		target := strings.TrimSpace(strings.TrimPrefix(raw, "= "))
		return compiledPath{kind: pathExact, exact: target, specificity: len(target)}, nil
	case strings.HasPrefix(raw, "="):
		target := strings.TrimSpace(strings.TrimPrefix(raw, "="))
		return compiledPath{kind: pathExact, exact: target, specificity: len(target)}, nil
	case strings.HasPrefix(raw, "~*"):
		expr := strings.TrimSpace(strings.TrimPrefix(raw, "~*"))
		if expr == "" {
			return compiledPath{}, fmt.Errorf("regex cannot be empty")
		}
		re, err := regexp.Compile("(?i)" + expr)
		if err != nil {
			return compiledPath{}, err
		}
		return compiledPath{kind: pathRegex, regex: re, specificity: len(expr)}, nil
	case strings.HasPrefix(raw, "~ "):
		expr := strings.TrimSpace(strings.TrimPrefix(raw, "~ "))
		if expr == "" {
			return compiledPath{}, fmt.Errorf("regex cannot be empty")
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return compiledPath{}, err
		}
		return compiledPath{kind: pathRegex, regex: re, specificity: len(expr)}, nil
	case strings.HasPrefix(raw, "~"):
		expr := strings.TrimSpace(strings.TrimPrefix(raw, "~"))
		if expr == "" {
			return compiledPath{}, fmt.Errorf("regex cannot be empty")
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return compiledPath{}, err
		}
		return compiledPath{kind: pathRegex, regex: re, specificity: len(expr)}, nil
	}

	// APISIX-style deep-prefix: "/foo/*" or "/foo*"
	if strings.HasSuffix(raw, "/*") {
		prefix := strings.TrimSuffix(raw, "*")
		return compiledPath{kind: pathPrefix, prefix: prefix, specificity: len(prefix)}, nil
	}
	if strings.HasSuffix(raw, "*") {
		prefix := strings.TrimSuffix(raw, "*")
		return compiledPath{kind: pathPrefix, prefix: prefix, specificity: len(prefix)}, nil
	}

	// Default: Lumen prefix matching (legacy behavior).
	return compiledPath{kind: pathPrefix, prefix: raw, specificity: len(raw)}, nil
}
