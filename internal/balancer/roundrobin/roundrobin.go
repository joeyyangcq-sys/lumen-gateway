package roundrobin

import (
	"context"
	"sync/atomic"

	"github.com/joey/lumen-gateway/internal/balancer"
)

type Balancer struct {
	next      atomic.Uint64
	endpoints []balancer.Endpoint
}

func New(endpoints []balancer.Endpoint, _ any) (balancer.Balancer, error) {
	b := &Balancer{}
	if err := b.Update(endpoints); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Balancer) Pick(_ context.Context) (balancer.Endpoint, error) {
	if len(b.endpoints) == 0 {
		return nil, balancer.ErrNotAvailable
	}

	start := int(b.next.Add(1)-1) % len(b.endpoints)
	for i := range b.endpoints {
		idx := (start + i) % len(b.endpoints)
		endpoint := b.endpoints[idx]
		if endpoint.IsAvailable() {
			return endpoint, nil
		}
	}

	return nil, balancer.ErrNotAvailable
}

func (b *Balancer) Update(endpoints []balancer.Endpoint) error {
	b.endpoints = append([]balancer.Endpoint(nil), endpoints...)
	return nil
}
