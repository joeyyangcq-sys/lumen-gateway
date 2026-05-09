package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/joey/lumen-gateway/internal/bootstrap"
)

type etcdKVClient interface {
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
	Put(ctx context.Context, key, val string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error)
	Delete(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.DeleteResponse, error)
	Close() error
}

type EtcdStore struct {
	client etcdKVClient
	prefix string
}

func NewEtcdStore(boot bootstrap.Options) (*EtcdStore, error) {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   boot.Etcd.Endpoints,
		DialTimeout: boot.Etcd.DialTimeout,
		Username:    boot.Etcd.Username,
		Password:    boot.Etcd.Password,
	})
	if err != nil {
		return nil, err
	}
	return NewEtcdStoreWithClient(client, boot.Etcd.Prefix), nil
}

func NewEtcdStoreWithClient(client etcdKVClient, prefix string) *EtcdStore {
	return &EtcdStore{
		client: client,
		prefix: normalizePrefix(prefix),
	}
}

func (s *EtcdStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *EtcdStore) List(ctx context.Context, kind ResourceKind) ([]Envelope, error) {
	root := s.resourceRoot(kind)
	resp, err := s.client.Get(ctx, root, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	items := make([]Envelope, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		if kv == nil {
			continue
		}
		key := string(kv.Key)
		if key == root || key == strings.TrimSuffix(root, "/") {
			continue
		}
		env, ok, err := kvToEnvelope(kv)
		if err != nil {
			return nil, err
		}
		if ok {
			items = append(items, env)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items, nil
}

func (s *EtcdStore) Get(ctx context.Context, kind ResourceKind, id string) (Envelope, error) {
	resp, err := s.client.Get(ctx, s.resourceKey(kind, id))
	if err != nil {
		return Envelope{}, err
	}
	if len(resp.Kvs) == 0 {
		return Envelope{}, ErrNotFound
	}
	env, ok, err := kvToEnvelope(resp.Kvs[0])
	if err != nil {
		return Envelope{}, err
	}
	if !ok {
		return Envelope{}, ErrNotFound
	}
	return env, nil
}

func (s *EtcdStore) Put(ctx context.Context, kind ResourceKind, id string, body json.RawMessage) (Envelope, error) {
	key := s.resourceKey(kind, id)
	if _, err := s.client.Put(ctx, key, string(body)); err != nil {
		return Envelope{}, err
	}
	return s.Get(ctx, kind, id)
}

func (s *EtcdStore) Delete(ctx context.Context, kind ResourceKind, id string) (DeleteResult, error) {
	key := s.resourceKey(kind, id)
	resp, err := s.client.Delete(ctx, key)
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{Key: key, Deleted: resp.Deleted}, nil
}

func (s *EtcdStore) resourceRoot(kind ResourceKind) string {
	return path.Join(s.prefix, string(kind)) + "/"
}

func (s *EtcdStore) resourceKey(kind ResourceKind, id string) string {
	return path.Join(s.prefix, string(kind), id)
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

func kvToEnvelope(kv *mvccpb.KeyValue) (Envelope, bool, error) {
	if kv == nil {
		return Envelope{}, false, nil
	}
	var wrapper struct {
		Value json.RawMessage `json:"value"`
	}
	value := json.RawMessage(kv.Value)
	if err := json.Unmarshal(kv.Value, &wrapper); err == nil && len(wrapper.Value) > 0 {
		value = wrapper.Value
	} else {
		var raw any
		if err := json.Unmarshal(kv.Value, &raw); err != nil {
			return Envelope{}, false, fmt.Errorf("decode etcd value %q: %w", string(kv.Key), err)
		}
	}
	if len(value) == 0 {
		return Envelope{}, false, nil
	}
	return Envelope{
		Key:           string(kv.Key),
		Value:         value,
		CreatedIndex:  kv.CreateRevision,
		ModifiedIndex: kv.ModRevision,
	}, true, nil
}
