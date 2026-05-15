package roundrobin

import (
	"context"
	"fmt"
	"testing"

	"github.com/joey/lumen-gateway/internal/balancer"
)

type benchEndpoint struct {
	addr string
}

func (e *benchEndpoint) Address() string  { return e.addr }
func (e *benchEndpoint) Weight() uint32   { return 1 }
func (e *benchEndpoint) IsAvailable() bool { return true }

func buildEndpoints(n int) []balancer.Endpoint {
	eps := make([]balancer.Endpoint, n)
	for i := range n {
		eps[i] = &benchEndpoint{addr: fmt.Sprintf("10.0.0.%d:8080", i%254+1)}
	}
	return eps
}

func BenchmarkRoundRobinPick10(b *testing.B) {
	lb, _ := New(buildEndpoints(10), nil)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = lb.Pick(ctx)
	}
}

func BenchmarkRoundRobinPick100(b *testing.B) {
	lb, _ := New(buildEndpoints(100), nil)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = lb.Pick(ctx)
	}
}

func BenchmarkRoundRobinPickParallel10(b *testing.B) {
	lb, _ := New(buildEndpoints(10), nil)
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = lb.Pick(ctx)
		}
	})
}

func BenchmarkRoundRobinPickParallel100(b *testing.B) {
	lb, _ := New(buildEndpoints(100), nil)
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = lb.Pick(ctx)
		}
	})
}

func BenchmarkRoundRobinUpdate(b *testing.B) {
	lb, _ := New(buildEndpoints(10), nil)
	eps := buildEndpoints(10)
	b.ReportAllocs()
	for b.Loop() {
		_ = lb.Update(eps)
	}
}
