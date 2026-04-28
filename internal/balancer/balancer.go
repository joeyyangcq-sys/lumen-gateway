package balancer

import (
	"context"
	"errors"
)

var ErrNotAvailable = errors.New("no available endpoint")

type Endpoint interface {
	Address() string
	Weight() uint32
	IsAvailable() bool
}

type Balancer interface {
	Pick(ctx context.Context) (Endpoint, error)
	Update(endpoints []Endpoint) error
}

type Factory func(endpoints []Endpoint, params any) (Balancer, error)
