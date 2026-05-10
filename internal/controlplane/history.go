package controlplane

import (
	"context"
)

type HistoryEntry struct {
	ID        string     `json:"id"`
	CreatedAt string     `json:"created_at"`
	Source    string     `json:"source"`
	Bundle    FileBundle `json:"bundle,omitempty"`
}

type HistoryStore interface {
	Save(ctx context.Context, entry HistoryEntry, limit int) (HistoryEntry, error)
	List(ctx context.Context, limit int) ([]HistoryEntry, error)
	Get(ctx context.Context, id string) (HistoryEntry, error)
	Close() error
}
