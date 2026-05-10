package controlplane

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/joey/lumen-gateway/internal/bootstrap"
)

type EtcdHistoryStore struct {
	client etcdKVClient
	root   string
}

func NewEtcdHistoryStore(boot bootstrap.Options) (*EtcdHistoryStore, error) {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   boot.Etcd.Endpoints,
		DialTimeout: boot.Etcd.DialTimeout,
		Username:    boot.Etcd.Username,
		Password:    boot.Etcd.Password,
	})
	if err != nil {
		return nil, err
	}
	return NewEtcdHistoryStoreWithClient(client, boot.Etcd.Prefix), nil
}

func NewEtcdHistoryStoreWithClient(client etcdKVClient, prefix string) *EtcdHistoryStore {
	namespace := strings.Trim(normalizePrefix(prefix), "/")
	namespace = strings.ReplaceAll(namespace, "/", "__")
	return &EtcdHistoryStore{
		client: client,
		root:   path.Join("/lumen/history", namespace) + "/",
	}
}

func (s *EtcdHistoryStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *EtcdHistoryStore) Save(ctx context.Context, entry HistoryEntry, limit int) (HistoryEntry, error) {
	if entry.ID == "" {
		entry.ID = generateResourceID()
	}
	body, err := json.Marshal(entry)
	if err != nil {
		return HistoryEntry{}, err
	}
	if _, err := s.client.Put(ctx, s.key(entry.ID), string(body)); err != nil {
		return HistoryEntry{}, err
	}
	if limit > 0 {
		items, err := s.List(ctx, 0)
		if err != nil {
			return HistoryEntry{}, err
		}
		for index, item := range items {
			if index < limit {
				continue
			}
			if _, err := s.client.Delete(ctx, s.key(item.ID)); err != nil {
				return HistoryEntry{}, err
			}
		}
	}
	return entry, nil
}

func (s *EtcdHistoryStore) List(ctx context.Context, limit int) ([]HistoryEntry, error) {
	resp, err := s.client.Get(ctx, s.root, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	items := make([]HistoryEntry, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		if kv == nil || string(kv.Key) == s.root || string(kv.Key) == strings.TrimSuffix(s.root, "/") {
			continue
		}
		var entry HistoryEntry
		if err := json.Unmarshal(kv.Value, &entry); err != nil {
			return nil, err
		}
		if entry.ID == "" {
			entry.ID = path.Base(string(kv.Key))
		}
		items = append(items, entry)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ID > items[j].ID
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *EtcdHistoryStore) Get(ctx context.Context, id string) (HistoryEntry, error) {
	resp, err := s.client.Get(ctx, s.key(id))
	if err != nil {
		return HistoryEntry{}, err
	}
	if len(resp.Kvs) == 0 {
		return HistoryEntry{}, ErrNotFound
	}
	var entry HistoryEntry
	if err := json.Unmarshal(resp.Kvs[0].Value, &entry); err != nil {
		return HistoryEntry{}, err
	}
	if entry.ID == "" {
		entry.ID = id
	}
	return entry, nil
}

func (s *EtcdHistoryStore) key(id string) string {
	return path.Join(s.root, id)
}
