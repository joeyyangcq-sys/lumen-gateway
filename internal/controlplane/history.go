package controlplane

import (
	"context"
)

type HistoryEntry struct {
	ID         string         `json:"id"`
	CreatedAt  string         `json:"created_at"`
	Source     string         `json:"source"`
	Summary    HistorySummary `json:"summary,omitempty"`
	Actor      string         `json:"actor,omitempty"`
	Note       string         `json:"note,omitempty"`
	RollbackOf string         `json:"rollback_of,omitempty"`
	Bundle     FileBundle     `json:"bundle,omitempty"`
}

type HistorySummary struct {
	Counts       map[ResourceKind]int `json:"counts,omitempty"`
	ManagedKinds []ResourceKind       `json:"managed_kinds,omitempty"`
}

type HistoryStore interface {
	Save(ctx context.Context, entry HistoryEntry, limit int) (HistoryEntry, error)
	List(ctx context.Context, limit int) ([]HistoryEntry, error)
	Get(ctx context.Context, id string) (HistoryEntry, error)
	Close() error
}
