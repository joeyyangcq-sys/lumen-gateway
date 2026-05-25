package roundrobin

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/joey/lumen-gateway/internal/balancer"
)

type Balancer struct {
	endpoints []balancer.Endpoint
	next      atomic.Uint64
	mu        sync.RWMutex
}

func New(endpoints []balancer.Endpoint, _ any) (balancer.Balancer, error) {
	b := &Balancer{}
	if err := b.Update(endpoints); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Balancer) Pick(_ context.Context) (balancer.Endpoint, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

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
	b.mu.Lock()
	defer b.mu.Unlock()
	b.endpoints = append([]balancer.Endpoint(nil), endpoints...)
	return nil
}
