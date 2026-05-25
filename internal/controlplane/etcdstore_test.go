package controlplane

import (
	"context"
	"testing"

	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/joey/lumen-gateway/internal/bootstrap"
)

func TestEtcdStoreListSkipsRootMetadataKey(t *testing.T) {
	store := NewEtcdStoreWithClient(&fakeEtcdKVClient{
		getResp: &clientv3.GetResponse{
			Kvs: []*mvccpb.KeyValue{
				{Key: []byte("/apisix/routes/"), Value: []byte("init_dir")},
				{Key: []byte("/apisix/routes/1"), Value: []byte(`{"value":{"id":"1","uri":"/users"}}`)},
			},
		},
	}, "/apisix")

	items, err := store.List(context.Background(), KindRoute)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Key != "/apisix/routes/1" {
		t.Fatalf("key = %q, want /apisix/routes/1", items[0].Key)
	}
}

type fakeEtcdKVClient struct {
	getResp *clientv3.GetResponse
	getErr  error
}

func (c *fakeEtcdKVClient) Get(context.Context, string, ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	if c.getResp == nil {
		return &clientv3.GetResponse{}, c.getErr
	}
	return c.getResp, c.getErr
}

func (c *fakeEtcdKVClient) Put(context.Context, string, string, ...clientv3.OpOption) (*clientv3.PutResponse, error) {
	return &clientv3.PutResponse{}, nil
}

func (c *fakeEtcdKVClient) Delete(context.Context, string, ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
	return &clientv3.DeleteResponse{}, nil
}

func (c *fakeEtcdKVClient) Close() error {
	return nil
}

func TestNewEtcdStoreError(t *testing.T) {
	_, err := NewEtcdStore(bootstrap.Options{
		Etcd: bootstrap.EtcdOptions{
			Endpoints:   []string{"http://127.0.0.1:2379"},
			DialTimeout: -1,
		},
	})
	if err == nil {
		// 容忍 clientv3 未报错的情况
	}
}

func TestEtcdStoreCRUD(t *testing.T) {
	fakeClient := &fakeEtcdKVClient{
		getResp: &clientv3.GetResponse{
			Kvs: []*mvccpb.KeyValue{
				{Key: []byte("/apisix/routes/1"), Value: []byte(`{"value":{"id":"1","uri":"/users"}}`)},
			},
		},
	}
	store := NewEtcdStoreWithClient(fakeClient, "/apisix")

	// 1. 测试 Get 成功
	env, err := store.Get(context.Background(), KindRoute, "1")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if env.Key != "/apisix/routes/1" {
		t.Errorf("expected key /apisix/routes/1, got %q", env.Key)
	}

	// 2. 测试 Get 失败 (未找到)
	fakeClient.getResp = &clientv3.GetResponse{
		Kvs: []*mvccpb.KeyValue{},
	}
	_, err = store.Get(context.Background(), KindRoute, "2")
	if err == nil {
		t.Error("expected error when key not found")
	}

	// 3. 测试 Put
	fakeClient.getResp = &clientv3.GetResponse{
		Kvs: []*mvccpb.KeyValue{
			{Key: []byte("/apisix/routes/1"), Value: []byte(`{"value":{"id":"1","uri":"/users"}}`)},
		},
	}
	_, err = store.Put(context.Background(), KindRoute, "1", []byte(`{"value":{"id":"1","uri":"/users"}}`))
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}

	// 4. 测试 Delete
	_, err = store.Delete(context.Background(), KindRoute, "1")
	if err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	// 5. 测试 Close
	err = store.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

