package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/joey/lumen-gateway/internal/bootstrap"
	"github.com/joey/lumen-gateway/internal/config"
)

type Update struct {
	Options config.Options
	Err     error
}

type Source interface {
	Load(ctx context.Context) (config.Options, error)
	Watch(ctx context.Context, onUpdate func(Update)) error
	Close() error
}

func NewSource(boot bootstrap.Options) (Source, error) {
	switch boot.Gateway.Source {
	case "file":
		return &fileSource{path: boot.File.Path, listen: boot.Gateway.Listen}, nil
	case "etcd_apisix":
		return newEtcdApisixSource(boot)
	default:
		return nil, fmt.Errorf("unsupported gateway.source %q", boot.Gateway.Source)
	}
}

type fileSource struct {
	path   string
	listen string
}

func (s *fileSource) Load(_ context.Context) (config.Options, error) {
	opts, err := config.Load(s.path)
	if err != nil {
		return config.Options{}, err
	}
	applyListenOverride(&opts, s.listen)
	return opts, nil
}

func (s *fileSource) Watch(ctx context.Context, _ func(Update)) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s *fileSource) Close() error {
	return nil
}

type etcdApisixSource struct{}

func newEtcdApisixSource(_ bootstrap.Options) (Source, error) {
	return &etcdApisixSource{}, nil
}

func (s *etcdApisixSource) Load(_ context.Context) (config.Options, error) {
	return config.Options{}, errors.New("etcd_apisix source is not implemented yet")
}

func (s *etcdApisixSource) Watch(ctx context.Context, onUpdate func(Update)) error {
	if onUpdate != nil {
		onUpdate(Update{Err: errors.New("etcd_apisix watch is not implemented yet")})
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s *etcdApisixSource) Close() error {
	return nil
}

func applyListenOverride(opts *config.Options, listen string) {
	if opts == nil || listen == "" {
		return
	}
	if len(opts.Servers) == 0 {
		return
	}

	// Prefer "main" if present to keep deterministic behavior.
	if server, ok := opts.Servers["main"]; ok {
		server.Listen = listen
		opts.Servers["main"] = server
		return
	}

	for id, server := range opts.Servers {
		server.Listen = listen
		opts.Servers[id] = server
		break
	}
}
