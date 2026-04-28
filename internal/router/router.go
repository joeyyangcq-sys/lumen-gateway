package router

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/joey/lumen-gateway/internal/config"
)

type Router struct {
	routes []config.RouteOptions
}

func New() *Router {
	return &Router{
		routes: make([]config.RouteOptions, 0),
	}
}

func (r *Router) Add(route config.RouteOptions) error {
	if route.ID == "" {
		return fmt.Errorf("route id cannot be empty")
	}
	r.routes = append(r.routes, route)
	return nil
}

func (r *Router) Match(method, host, path string) (config.RouteOptions, bool) {
	method = strings.ToUpper(method)
	for _, route := range r.routes {
		if !matchMethod(route.Methods, method) {
			continue
		}
		if !matchHost(route.Hosts, host) {
			continue
		}
		if !matchPath(route.Paths, path) {
			continue
		}
		return route, true
	}
	return config.RouteOptions{}, false
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
	host = strings.ToLower(host)
	for _, h := range hosts {
		if strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

func matchPath(paths []string, path string) bool {
	for _, p := range paths {
		switch {
		case strings.HasPrefix(p, "="):
			if strings.TrimSpace(p[1:]) == path {
				return true
			}
		case p == path || strings.HasPrefix(path, p):
			return true
		}
	}
	return false
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
