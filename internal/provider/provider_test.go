package provider

import (
	"context"
	"testing"

	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestEtcdApisixSourceLoadBuildsGatewayOptions(t *testing.T) {
	source := &etcdApisixSource{
		client: &fakeEtcdClient{
			getResponses: []*clientv3.GetResponse{
				{Kvs: []*mvccpb.KeyValue{
					kv("/apisix/upstreams/1", `{"value":{"id":"1","nodes":{"127.0.0.1:9001":1},"scheme":"http","pass_host":"rewrite","upstream_host":"users.internal"}}`),
					kv("/apisix/services/1", `{"value":{"id":"1","upstream_id":"1"}}`),
					kv("/apisix/routes/1", `{"value":{"id":"1","uri":"/users","service_id":"1"}}`),
				}},
			},
		},
		prefix: "/apisix",
		listen: ":18080",
	}

	options, err := source.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := options.Servers["main"].Listen; got != ":18080" {
		t.Fatalf("listen = %q, want :18080", got)
	}
	upstream := options.Upstreams["1"]
	if upstream.Scheme != "http" {
		t.Fatalf("scheme = %q, want http", upstream.Scheme)
	}
	if upstream.PassHost != "rewrite" {
		t.Fatalf("pass_host = %q, want rewrite", upstream.PassHost)
	}
	if upstream.UpstreamHost != "users.internal" {
		t.Fatalf("upstream_host = %q, want users.internal", upstream.UpstreamHost)
	}
	if len(upstream.Endpoints) != 1 || upstream.Endpoints[0].Address != "127.0.0.1:9001" {
		t.Fatalf("endpoints = %#v, want one 127.0.0.1:9001 endpoint", upstream.Endpoints)
	}
	if got := options.Routes["1"].Service; got != "1" {
		t.Fatalf("route service = %q, want 1", got)
	}
}

func TestEtcdApisixSourceWatchReloadsOnChanges(t *testing.T) {
	watchCh := make(chan clientv3.WatchResponse, 1)
	source := &etcdApisixSource{
		client: &fakeEtcdClient{
			getResponses: []*clientv3.GetResponse{
				{Kvs: []*mvccpb.KeyValue{
					kv("/apisix/upstreams/1", `{"value":{"id":"1","nodes":{"127.0.0.1:9001":1}}}`),
					kv("/apisix/services/1", `{"value":{"id":"1","upstream_id":"1"}}`),
					kv("/apisix/routes/1", `{"value":{"id":"1","uri":"/v2/users","service_id":"1"}}`),
				}},
			},
			watchCh: watchCh,
		},
		prefix: "/apisix",
		listen: ":18080",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var got Update
	done := make(chan struct{})
	go func() {
		watchCh <- clientv3.WatchResponse{
			Events: []*clientv3.Event{
				{Type: clientv3.EventTypePut},
			},
		}
	}()

	err := source.Watch(ctx, func(update Update) {
		got = update
		cancel()
		close(done)
	})
	if err != context.Canceled {
		t.Fatalf("Watch() error = %v, want context.Canceled", err)
	}

	<-done
	if got.Err != nil {
		t.Fatalf("update error = %v, want nil", got.Err)
	}
	if got.Options.Routes["1"].Paths[0] != "= /v2/users" {
		t.Fatalf("route path = %#v, want = /v2/users", got.Options.Routes["1"].Paths)
	}
}

type fakeEtcdClient struct {
	getResponses []*clientv3.GetResponse
	watchCh      chan clientv3.WatchResponse
	getCalls     int
}

func (c *fakeEtcdClient) Get(_ context.Context, _ string, _ ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	if len(c.getResponses) == 0 {
		return &clientv3.GetResponse{}, nil
	}
	idx := c.getCalls
	if idx >= len(c.getResponses) {
		idx = len(c.getResponses) - 1
	}
	c.getCalls++
	return c.getResponses[idx], nil
}

func (c *fakeEtcdClient) Watch(_ context.Context, _ string, _ ...clientv3.OpOption) clientv3.WatchChan {
	if c.watchCh == nil {
		c.watchCh = make(chan clientv3.WatchResponse)
	}
	return c.watchCh
}

func (c *fakeEtcdClient) Close() error {
	return nil
}

func kv(key, value string) *mvccpb.KeyValue {
	return &mvccpb.KeyValue{Key: []byte(key), Value: []byte(value)}
}
