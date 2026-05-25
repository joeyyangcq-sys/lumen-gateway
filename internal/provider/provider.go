package provider

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/joey/lumen-gateway/internal/apisix"
	"github.com/joey/lumen-gateway/internal/bootstrap"
	"github.com/joey/lumen-gateway/internal/config"
	"github.com/joey/lumen-gateway/internal/translate"
)

type Update struct {
	Err     error
	Options config.Options
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

type etcdKVClient interface {
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
	Watch(ctx context.Context, key string, opts ...clientv3.OpOption) clientv3.WatchChan
	Close() error
}

type etcdApisixSource struct {
	client     etcdKVClient
	prefix     string
	listen     string
	retryDelay time.Duration
}

func newEtcdApisixSource(boot bootstrap.Options) (Source, error) {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   boot.Etcd.Endpoints,
		DialTimeout: boot.Etcd.DialTimeout,
		Username:    boot.Etcd.Username,
		Password:    boot.Etcd.Password,
	})
	if err != nil {
		return nil, err
	}

	return &etcdApisixSource{
		client: client,
		prefix: normalizePrefix(boot.Etcd.Prefix),
		listen: boot.Gateway.Listen,
	}, nil
}

func (s *etcdApisixSource) Load(ctx context.Context) (config.Options, error) {
	options, err := s.loadOptions(ctx)
	if err != nil {
		return config.Options{}, err
	}
	applyListenOverride(&options, s.listen)
	return options, nil
}

func (s *etcdApisixSource) Watch(ctx context.Context, onUpdate func(Update)) error {
	watchPath := s.resourceRoot()
	for {
		watchCh := s.client.Watch(ctx, watchPath, clientv3.WithPrefix())
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case resp, ok := <-watchCh:
				if !ok {
					if err := sleepContext(ctx, s.watchRetryDelay()); err != nil {
						return err
					}
					goto restart
				}
				if err := resp.Err(); err != nil {
					if onUpdate != nil {
						onUpdate(Update{Err: err})
					}
					if err := sleepContext(ctx, s.watchRetryDelay()); err != nil {
						return err
					}
					goto restart
				}
				if len(resp.Events) == 0 {
					continue
				}

				options, err := s.Load(ctx)
				if onUpdate == nil {
					if err != nil {
						return err
					}
					continue
				}
				if err != nil {
					onUpdate(Update{Err: err})
					continue
				}
				onUpdate(Update{Options: options})
			}
		}
	restart:
		continue
	}
}

func (s *etcdApisixSource) watchRetryDelay() time.Duration {
	if s.retryDelay > 0 {
		return s.retryDelay
	}
	return time.Second
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *etcdApisixSource) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *etcdApisixSource) loadOptions(ctx context.Context) (config.Options, error) {
	snapshot, err := s.loadSnapshot(ctx)
	if err != nil {
		return config.Options{}, err
	}
	return translate.ApisixSnapshotToConfig(snapshot, translate.ApisixToConfigOptions{
		Listen: s.listen,
	})
}

func (s *etcdApisixSource) loadSnapshot(ctx context.Context) (apisix.Snapshot, error) {
	resp, err := s.client.Get(ctx, s.resourceRoot(), clientv3.WithPrefix())
	if err != nil {
		return apisix.Snapshot{}, err
	}

	snapshot := apisix.NewSnapshot()
	for _, kv := range resp.Kvs {
		if kv == nil {
			continue
		}
		if err := s.applyKV(&snapshot, string(kv.Key), kv.Value); err != nil {
			return apisix.Snapshot{}, err
		}
	}
	return snapshot, nil
}

func (s *etcdApisixSource) applyKV(snapshot *apisix.Snapshot, key string, value []byte) error {
	kind, id, ok := parseResourceKey(s.prefix, key)
	if !ok {
		return nil
	}

	switch kind {
	case "routes":
		resource := apisix.Route{}
		if err := apisix.UnmarshalEtcdValue(value, &resource); err != nil {
			return fmt.Errorf("decode route %q: %w", id, err)
		}
		if resource.ID == "" {
			resource.ID = apisix.ID(id)
		}
		snapshot.Routes[id] = resource
	case "services":
		resource := apisix.Service{}
		if err := apisix.UnmarshalEtcdValue(value, &resource); err != nil {
			return fmt.Errorf("decode service %q: %w", id, err)
		}
		if resource.ID == "" {
			resource.ID = apisix.ID(id)
		}
		snapshot.Services[id] = resource
	case "upstreams":
		resource := apisix.Upstream{}
		if err := apisix.UnmarshalEtcdValue(value, &resource); err != nil {
			return fmt.Errorf("decode upstream %q: %w", id, err)
		}
		if resource.ID == "" {
			resource.ID = apisix.ID(id)
		}
		snapshot.Upstreams[id] = resource
	case "plugin_configs":
		resource := apisix.PluginConfig{}
		if err := apisix.UnmarshalEtcdValue(value, &resource); err != nil {
			return fmt.Errorf("decode plugin config %q: %w", id, err)
		}
		if resource.ID == "" {
			resource.ID = apisix.ID(id)
		}
		snapshot.PluginConfig[id] = resource
	case "global_rules":
		resource := apisix.GlobalRule{}
		if err := apisix.UnmarshalEtcdValue(value, &resource); err != nil {
			return fmt.Errorf("decode global rule %q: %w", id, err)
		}
		if resource.ID == "" {
			resource.ID = apisix.ID(id)
		}
		snapshot.GlobalRules[id] = resource
	}

	return nil
}

func (s *etcdApisixSource) resourceRoot() string {
	return path.Join(s.prefix, "/") + "/"
}

func parseResourceKey(prefix, key string) (kind string, id string, ok bool) {
	root := path.Join(prefix, "/") + "/"
	if !strings.HasPrefix(key, root) {
		return "", "", false
	}

	relative := strings.TrimPrefix(key, root)
	parts := strings.SplitN(relative, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "/apisix"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimRight(prefix, "/")
}

func applyListenOverride(opts *config.Options, listen string) {
	if opts == nil || listen == "" {
		return
	}
	if len(opts.Servers) == 0 {
		return
	}

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
