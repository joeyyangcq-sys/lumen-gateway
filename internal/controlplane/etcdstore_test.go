package controlplane

import (
	"context"
	"testing"

	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
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
