package roundrobin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/joey/lumen-gateway/internal/balancer"
)

func TestBalancerUpdateCopiesEndpoints(t *testing.T) {
	endpoints := []balancer.Endpoint{
		testEndpoint{address: "10.0.0.1:80", available: true},
	}
	b, err := New(endpoints, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	endpoints[0] = testEndpoint{address: "10.0.0.2:80", available: true}

	got, err := b.Pick(context.Background())
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got.Address() != "10.0.0.1:80" {
		t.Fatalf("picked endpoint = %q, want original copied endpoint", got.Address())
	}
}

func TestBalancerAllowsConcurrentPickAndUpdate(t *testing.T) {
	b, err := New([]balancer.Endpoint{
		testEndpoint{address: "10.0.0.1:80", available: true},
	}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			if _, err := b.Pick(ctx); err != nil && !errors.Is(err, balancer.ErrNotAvailable) {
				errCh <- err
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		sets := [][]balancer.Endpoint{
			{testEndpoint{address: "10.0.0.1:80", available: true}},
			{testEndpoint{address: "10.0.0.2:80", available: false}},
			{
				testEndpoint{address: "10.0.0.3:80", available: true},
				testEndpoint{address: "10.0.0.4:80", available: true},
			},
		}
		for i := 0; ctx.Err() == nil; i++ {
			if err := b.Update(sets[i%len(sets)]); err != nil {
				errCh <- err
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent Pick/Update error = %v", err)
		}
	}
}

type testEndpoint struct {
	address   string
	available bool
}

func (e testEndpoint) Address() string {
	return e.address
}

func (e testEndpoint) Weight() uint32 {
	return 1
}

func (e testEndpoint) IsAvailable() bool {
	return e.available
}
