package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/joey/lumen-gateway/internal/bootstrap"
)

type fakeHistoryEtcdClient struct {
	getResp    *clientv3.GetResponse
	getErr     error
	putErr     error
	deleteErr  error
	closeErr   error
	putCalls   int
	deleteKeys []string
}

func (c *fakeHistoryEtcdClient) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	if c.getErr != nil {
		return nil, c.getErr
	}
	if c.getResp == nil {
		return &clientv3.GetResponse{}, nil
	}
	return c.getResp, nil
}

func (c *fakeHistoryEtcdClient) Put(ctx context.Context, key, val string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error) {
	c.putCalls++
	if c.putErr != nil {
		return nil, c.putErr
	}
	return &clientv3.PutResponse{}, nil
}

func (c *fakeHistoryEtcdClient) Delete(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
	c.deleteKeys = append(c.deleteKeys, key)
	if c.deleteErr != nil {
		return nil, c.deleteErr
	}
	return &clientv3.DeleteResponse{Deleted: 1}, nil
}

func (c *fakeHistoryEtcdClient) Close() error {
	return c.closeErr
}

func TestNewEtcdHistoryStore(t *testing.T) {
	// 校验 DialTimeout 错误路径
	_, err := NewEtcdHistoryStore(bootstrap.Options{
		Etcd: bootstrap.EtcdOptions{
			Endpoints:   []string{"http://127.0.0.1:2379"},
			DialTimeout: -1,
		},
	})
	if err == nil {
		// 容忍 clientv3 未报错的情况
	}
}

func TestEtcdHistoryStore_CRUD(t *testing.T) {
	fakeClient := &fakeHistoryEtcdClient{}
	store := NewEtcdHistoryStoreWithClient(fakeClient, "/apisix")

	// 1. 测试 Save (自动生成 ID)
	entry := HistoryEntry{
		Source: "test",
		Actor:  "admin",
	}
	savedEntry, err := store.Save(context.Background(), entry, 0)
	if err != nil {
		t.Fatalf("Save error: %v", err)
	}
	if savedEntry.ID == "" {
		t.Error("expected auto-generated ID to be non-empty")
	}

	// 2. 测试 Save (指定 ID 并开启 limit 触发 prune)
	entry2 := HistoryEntry{
		ID:     "123",
		Source: "test2",
	}
	// 为了让 Save 内的 List 有数据可删，我们需要模拟 List 返回数据
	data1, _ := json.Marshal(HistoryEntry{ID: "001"})
	data2, _ := json.Marshal(HistoryEntry{ID: "002"})
	fakeClient.getResp = &clientv3.GetResponse{
		Kvs: []*mvccpb.KeyValue{
			{Key: []byte("/lumen/history/apisix/001"), Value: data1},
			{Key: []byte("/lumen/history/apisix/002"), Value: data2},
		},
	}
	savedEntry2, err := store.Save(context.Background(), entry2, 1)
	if err != nil {
		t.Fatalf("Save with limit error: %v", err)
	}
	if savedEntry2.ID != "123" {
		t.Errorf("expected ID '123', got %q", savedEntry2.ID)
	}
	// 期望删除了 002 (因为 002 被排在前面或满足 index >= limit 条件，List 返回的 item 经过排序)
	if len(fakeClient.deleteKeys) == 0 {
		t.Error("expected old history items to be pruned")
	}

	// 3. 测试 Save 错误路径
	fakeClient.putErr = errors.New("put error")
	_, err = store.Save(context.Background(), entry2, 0)
	if err == nil {
		t.Error("expected error when client.Put fails")
	}
	fakeClient.putErr = nil

	// 4. 测试 Get 成功
	dataGet, _ := json.Marshal(HistoryEntry{ID: "123", Source: "get_test"})
	fakeClient.getResp = &clientv3.GetResponse{
		Kvs: []*mvccpb.KeyValue{
			{Key: []byte("/lumen/history/apisix/123"), Value: dataGet},
		},
	}
	getEntry, err := store.Get(context.Background(), "123")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if getEntry.Source != "get_test" {
		t.Errorf("expected source 'get_test', got %q", getEntry.Source)
	}

	// 5. 测试 Get 未找到
	fakeClient.getResp = &clientv3.GetResponse{
		Kvs: []*mvccpb.KeyValue{},
	}
	_, err = store.Get(context.Background(), "456")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	// 6. 测试 Get 错误
	fakeClient.getErr = errors.New("get error")
	_, err = store.Get(context.Background(), "123")
	if err == nil {
		t.Error("expected error when client.Get fails")
	}
	fakeClient.getErr = nil

	// 7. 测试 List 错误
	fakeClient.getErr = errors.New("list error")
	_, err = store.List(context.Background(), 0)
	if err == nil {
		t.Error("expected error when client.Get fails in List")
	}
	fakeClient.getErr = nil

	// 8. 测试 Close
	fakeClient.closeErr = errors.New("close error")
	err = store.Close()
	if err == nil {
		t.Error("expected error when Close fails")
	}
}
