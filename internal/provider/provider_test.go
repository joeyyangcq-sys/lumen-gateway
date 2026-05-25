package provider

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/joey/lumen-gateway/internal/apisix"
	"github.com/joey/lumen-gateway/internal/bootstrap"
	"github.com/joey/lumen-gateway/internal/config"
)

type mockEtcdKVClient struct {
	getFunc   func(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
	watchFunc func(ctx context.Context, key string, opts ...clientv3.OpOption) clientv3.WatchChan
	closeFunc func() error
}

func (m *mockEtcdKVClient) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, key, opts...)
	}
	return &clientv3.GetResponse{}, nil
}

func (m *mockEtcdKVClient) Watch(ctx context.Context, key string, opts ...clientv3.OpOption) clientv3.WatchChan {
	if m.watchFunc != nil {
		return m.watchFunc(ctx, key, opts...)
	}
	return nil
}

func (m *mockEtcdKVClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func TestNewSource(t *testing.T) {
	// File source
	s, err := NewSource(bootstrap.Options{
		Gateway: bootstrap.GatewayOptions{Source: "file"},
		File:    bootstrap.FileOptions{Path: "configs/bootstrap.yaml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.(*fileSource); !ok {
		t.Errorf("expected *fileSource, got %T", s)
	}

	// Unsupported source
	_, err = NewSource(bootstrap.Options{
		Gateway: bootstrap.GatewayOptions{Source: "invalid"},
	})
	if err == nil {
		t.Error("expected error for invalid source")
	}
}

func TestFileSourceLoadAndWatch(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	_, _ = tmpFile.WriteString(`
servers:
  main:
    listen: :8080
`)
	_ = tmpFile.Close()

	s := &fileSource{path: tmpFile.Name(), listen: ":9090"}
	opts, err := s.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if opts.Servers["main"].Listen != ":9090" {
		t.Errorf("expected listen override to :9090, got %q", opts.Servers["main"].Listen)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err = s.Watch(ctx, nil)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("unexpected watch error: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Errorf("unexpected close error: %v", err)
	}
}

func TestEtcdApisixSourceLoad(t *testing.T) {
	mockClient := &mockEtcdKVClient{
		getFunc: func(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
			return &clientv3.GetResponse{
				Kvs: []*mvccpb.KeyValue{
					{
						Key:   []byte("/apisix/routes/1"),
						Value: []byte(`{"id":"1","uri":"/users","upstream_id":"1"}`),
					},
					{
						Key:   []byte("/apisix/services/1"),
						Value: []byte(`{"id":"1","upstream_id":"1"}`),
					},
					{
						Key:   []byte("/apisix/upstreams/1"),
						Value: []byte(`{"id":"1","type":"roundrobin","nodes":{"127.0.0.1:8080":1}}`),
					},
					{
						Key:   []byte("/apisix/plugin_configs/1"),
						Value: []byte(`{"id":"1","plugins":{"request-id":{}}}`),
					},
					{
						Key:   []byte("/apisix/global_rules/1"),
						Value: []byte(`{"id":"1","plugins":{"request-id":{}}}`),
					},
				},
			}, nil
		},
	}

	s := &etcdApisixSource{
		client: mockClient,
		prefix: "/apisix",
		listen: ":8080",
	}

	opts, err := s.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(opts.Routes) != 1 {
		t.Errorf("expected 1 route, got %d", len(opts.Routes))
	}
	if _, ok := opts.Services["1"]; !ok {
		t.Errorf("expected service 1 to exist")
	}
	if len(opts.Upstreams) != 1 {
		t.Errorf("expected 1 upstream, got %d", len(opts.Upstreams))
	}

	// Test load error on invalid json
	failClient := &mockEtcdKVClient{
		getFunc: func(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
			return &clientv3.GetResponse{
				Kvs: []*mvccpb.KeyValue{
					{
						Key:   []byte("/apisix/routes/1"),
						Value: []byte(`{invalid`),
					},
				},
			}, nil
		},
	}
	sFail := &etcdApisixSource{
		client: failClient,
		prefix: "/apisix",
	}
	if _, err := sFail.Load(context.Background()); err == nil {
		t.Error("expected error when loading invalid json")
	}

	// Test Get error
	errClient := &mockEtcdKVClient{
		getFunc: func(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
			return nil, errors.New("get error")
		},
	}
	sErr := &etcdApisixSource{
		client: errClient,
		prefix: "/apisix",
	}
	if _, err := sErr.Load(context.Background()); err == nil {
		t.Error("expected error when etcd Get fails")
	}
}

func TestEtcdApisixSourceWatch(t *testing.T) {
	watchCh := make(chan clientv3.WatchResponse, 5)
	mockClient := &mockEtcdKVClient{
		getFunc: func(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
			return &clientv3.GetResponse{}, nil
		},
		watchFunc: func(ctx context.Context, key string, opts ...clientv3.OpOption) clientv3.WatchChan {
			return watchCh
		},
	}

	s := &etcdApisixSource{
		client:     mockClient,
		prefix:     "/apisix",
		listen:     ":8080",
		retryDelay: 1 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updateCh := make(chan Update, 5)
	go func() {
		_ = s.Watch(ctx, func(u Update) {
			updateCh <- u
		})
	}()

	// 1. Simulate standard event
	watchCh <- clientv3.WatchResponse{
		Events: []*clientv3.Event{
			{
				Type: mvccpb.PUT,
				Kv: &mvccpb.KeyValue{
					Key:   []byte("/apisix/routes/1"),
					Value: []byte(`{"id":"1","uri":"/users"}`),
				},
			},
		},
	}

	select {
	case u := <-updateCh:
		if u.Err != nil {
			t.Errorf("unexpected update error: %v", u.Err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for watch update")
	}

	// 2. Simulate watch error
	watchCh <- clientv3.WatchResponse{
		CompactRevision: 1, // trigger response error
	}

	select {
	case u := <-updateCh:
		if u.Err == nil {
			t.Error("expected update error")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for watch error update")
	}

	// 3. Simulate close of watch channel (ok is false) to trigger retry delay
	close(watchCh)
	time.Sleep(10 * time.Millisecond)

	// Test sleepContext
	if err := sleepContext(ctx, 0); err != nil {
		t.Errorf("expected no error for 0 sleep, got %v", err)
	}
	
	// Test close
	cancel()
	_ = s.Close()
}

func TestNewSourceEtcd(t *testing.T) {
	// Should initialize etcd source successfully
	s, err := NewSource(bootstrap.Options{
		Gateway: bootstrap.GatewayOptions{Source: "etcd_apisix"},
		Etcd: bootstrap.EtcdOptions{
			Endpoints:   []string{"localhost:2379"},
			DialTimeout: 1 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok := s.(*etcdApisixSource); !ok {
		t.Errorf("expected *etcdApisixSource, got %T", s)
	}
}


func TestNormalizePrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "/apisix"},
		{"  ", "/apisix"},
		{"apisix", "/apisix"},
		{"/apisix/", "/apisix"},
		{"/custom/prefix/", "/custom/prefix"},
	}

	for _, tt := range tests {
		got := normalizePrefix(tt.input)
		if got != tt.want {
			t.Errorf("normalizePrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseResourceKey(t *testing.T) {
	tests := []struct {
		prefix string
		key    string
		wantK  string
		wantI  string
		wantOk bool
	}{
		{"/apisix", "/apisix/routes/1", "routes", "1", true},
		{"/apisix", "/apisix/services/abc", "services", "abc", true},
		{"/apisix", "/apisix/invalid", "", "", false},
		{"/apisix", "/other/routes/1", "", "", false},
	}

	for _, tt := range tests {
		k, id, ok := parseResourceKey(tt.prefix, tt.key)
		if ok != tt.wantOk || k != tt.wantK || id != tt.wantI {
			t.Errorf("parseResourceKey(%q, %q) = (%q, %q, %t), want (%q, %q, %t)",
				tt.prefix, tt.key, k, id, ok, tt.wantK, tt.wantI, tt.wantOk)
		}
	}
}

func TestApplyListenOverride(t *testing.T) {
	opts := &config.Options{
		Servers: map[string]config.ServerOptions{
			"other": {Listen: ":9090"},
		},
	}
	applyListenOverride(opts, ":9999")
	if opts.Servers["other"].Listen != ":9999" {
		t.Errorf("expected listen to be overridden to :9999, got %q", opts.Servers["other"].Listen)
	}

	applyListenOverride(nil, ":1234") // should not panic
}

func TestEtcdApisixSourceEdgeCases(t *testing.T) {
	// 1. watchRetryDelay 默认回退
	s := &etcdApisixSource{}
	if s.watchRetryDelay() != time.Second {
		t.Errorf("expected 1s default retry delay, got %v", s.watchRetryDelay())
	}

	// 2. sleepContext with canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sleepContext(ctx, 1*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// 3. sleepContext with zero delay and canceled context
	err = sleepContext(ctx, 0)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled for zero delay, got %v", err)
	}

	// 4. etcdApisixSource Close with nil client
	sNil := &etcdApisixSource{client: nil}
	if err := sNil.Close(); err != nil {
		t.Errorf("unexpected error on nil client Close: %v", err)
	}
}

func TestEtcdApisixSourceApplyKVErrorPaths(t *testing.T) {
	s := &etcdApisixSource{prefix: "/apisix"}
	snap := apisix.NewSnapshot()

	// 1. parseResourceKey 返回 ok=false 分支
	err := s.applyKV(&snap, "/apisix/invalid_key", []byte(`{}`))
	if err != nil {
		t.Errorf("expected nil error when parseResourceKey fails, got %v", err)
	}

	// 2. 各资源反序列化失败分支
	resources := []struct {
		key   string
		value []byte
	}{
		{"/apisix/services/1", []byte(`{invalid`)},
		{"/apisix/upstreams/1", []byte(`{invalid`)},
		{"/apisix/plugin_configs/1", []byte(`{invalid`)},
		{"/apisix/global_rules/1", []byte(`{invalid`)},
	}

	for _, tc := range resources {
		err := s.applyKV(&snap, tc.key, tc.value)
		if err == nil {
			t.Errorf("expected error when unmarshalling failed for key %q", tc.key)
		}
	}

	// 3. 走 default kind 分支
	err = s.applyKV(&snap, "/apisix/invalid_kind/1", []byte(`{}`))
	if err != nil {
		t.Errorf("expected nil error for invalid kind, got %v", err)
	}
}


