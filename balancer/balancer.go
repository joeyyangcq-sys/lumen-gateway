// Package balancer exposes the public API for implementing custom load
// balancers in lumen-gateway.
//
// Implement the Balancer interface, then register your type with
// lumen.WithBalancerType:
//
//	lumen.Run(
//	    lumen.WithBalancerType("least_conn", func(endpoints []balancer.Endpoint, params any) (balancer.Balancer, error) {
//	        return leastconn.New(endpoints, params)
//	    }),
//	)
//
// In the upstream config, set balancer.type to the registered name:
//
//	upstreams:
//	  my-upstream:
//	    balancer:
//	      type: least_conn
//	    endpoints:
//	      - address: "127.0.0.1:9001"
//	        weight: 1
package balancer

import (
	internalbalancer "github.com/joey/lumen-gateway/internal/balancer"
)

type (
	// Endpoint is a single upstream server node. Implement or wrap this to
	// describe addresses and availability.
	Endpoint = internalbalancer.Endpoint

	// Balancer selects an endpoint for each incoming request.
	Balancer = internalbalancer.Balancer

	// Factory is the constructor signature for a balancer: receives the
	// initial endpoint list and the raw params from the upstream config.
	Factory = internalbalancer.Factory
)

// ErrNotAvailable is returned by Pick when no healthy endpoint is found.
var ErrNotAvailable = internalbalancer.ErrNotAvailable
