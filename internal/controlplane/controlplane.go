package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"
)

type ResourceKind string

const (
	KindRoute        ResourceKind = "routes"
	KindService      ResourceKind = "services"
	KindUpstream     ResourceKind = "upstreams"
	KindPluginConfig ResourceKind = "plugin_configs"
	KindGlobalRule   ResourceKind = "global_rules"
)

var (
	ErrUnsupportedKind = errors.New("unsupported resource kind")
	ErrNotFound        = errors.New("resource not found")
	ErrInvalidBody     = errors.New("invalid resource body")
)

var supportedKinds = []ResourceKind{
	KindRoute,
	KindService,
	KindUpstream,
	KindPluginConfig,
	KindGlobalRule,
}

type Envelope struct {
	Key           string          `json:"key"`
	Value         json.RawMessage `json:"value"`
	CreatedIndex  int64           `json:"createdIndex,omitempty"`
	ModifiedIndex int64           `json:"modifiedIndex,omitempty"`
}

type DeleteResult struct {
	Key     string `json:"key"`
	Deleted int64  `json:"deleted,string"`
}

type ApplyOptions struct {
	Prune      bool
	PruneKinds []ResourceKind
}

type Store interface {
	List(ctx context.Context, kind ResourceKind) ([]Envelope, error)
	Get(ctx context.Context, kind ResourceKind, id string) (Envelope, error)
	Put(ctx context.Context, kind ResourceKind, id string, body json.RawMessage) (Envelope, error)
	Delete(ctx context.Context, kind ResourceKind, id string) (DeleteResult, error)
	Close() error
}

type Option func(*Service)

type Service struct {
	store         Store
	history       HistoryStore
	historyLimit  int
	exportOptions ExportOptions
}

func New(store Store, opts ...Option) *Service {
	svc := &Service{
		store:        store,
		historyLimit: 10,
		exportOptions: ExportOptions{
			IncludeMeta: true,
		},
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func WithHistory(store HistoryStore, limit int, exportOptions ExportOptions) Option {
	return func(s *Service) {
		s.history = store
		if limit > 0 {
			s.historyLimit = limit
		}
		exportOptions.IncludeMeta = true
		s.exportOptions = exportOptions
	}
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	var err error
	if s.store != nil {
		err = s.store.Close()
	}
	if s.history != nil {
		if closeErr := s.history.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func (s *Service) List(ctx context.Context, kind ResourceKind) ([]Envelope, error) {
	if err := validateKind(kind); err != nil {
		return nil, err
	}
	return s.store.List(ctx, kind)
}

func (s *Service) Get(ctx context.Context, kind ResourceKind, id string) (Envelope, error) {
	if err := validateKind(kind); err != nil {
		return Envelope{}, err
	}
	if id == "" {
		return Envelope{}, fmt.Errorf("%w: resource id is required", ErrInvalidBody)
	}
	return s.store.Get(ctx, kind, id)
}

func (s *Service) Put(ctx context.Context, kind ResourceKind, id string, body json.RawMessage) (Envelope, error) {
	if err := validateKind(kind); err != nil {
		return Envelope{}, err
	}
	if id == "" {
		return Envelope{}, fmt.Errorf("%w: resource id is required", ErrInvalidBody)
	}
	normalized, err := s.normalizeForWrite(ctx, kind, id, body)
	if err != nil {
		return Envelope{}, err
	}
	return s.store.Put(ctx, kind, id, normalized)
}

func (s *Service) Post(ctx context.Context, kind ResourceKind, body json.RawMessage) (Envelope, error) {
	if err := validateKind(kind); err != nil {
		return Envelope{}, err
	}
	id, err := ExtractResourceID(body)
	if err != nil {
		return Envelope{}, err
	}
	if id == "" {
		id = generateResourceID()
	}
	normalized, err := s.normalizeForWrite(ctx, kind, id, body)
	if err != nil {
		return Envelope{}, err
	}
	return s.store.Put(ctx, kind, id, normalized)
}

func (s *Service) Patch(ctx context.Context, kind ResourceKind, id string, patch json.RawMessage) (Envelope, error) {
	if err := validateKind(kind); err != nil {
		return Envelope{}, err
	}
	if id == "" {
		return Envelope{}, fmt.Errorf("%w: resource id is required", ErrInvalidBody)
	}
	current, err := s.store.Get(ctx, kind, id)
	if err != nil {
		return Envelope{}, err
	}
	merged, err := mergeJSON(current.Value, patch)
	if err != nil {
		return Envelope{}, err
	}
	normalized, err := s.normalizeForWrite(ctx, kind, id, merged)
	if err != nil {
		return Envelope{}, err
	}
	return s.store.Put(ctx, kind, id, normalized)
}

func (s *Service) Delete(ctx context.Context, kind ResourceKind, id string) (DeleteResult, error) {
	if err := validateKind(kind); err != nil {
		return DeleteResult{}, err
	}
	if id == "" {
		return DeleteResult{}, fmt.Errorf("%w: resource id is required", ErrInvalidBody)
	}
	return s.store.Delete(ctx, kind, id)
}

func (s *Service) PreviewBundle(ctx context.Context, bundle FileBundle, options PreviewOptions) (ApplyPlan, error) {
	return BuildApplyPlan(ctx, s, bundle, options)
}

func (s *Service) ApplyBundle(ctx context.Context, bundle FileBundle, options ApplyOptions) (ApplyResult, error) {
	return ApplyBundleWithOptions(ctx, s, bundle, options)
}

func (s *Service) ExportBundle(ctx context.Context, options ExportOptions) (FileBundle, error) {
	return ExportBundleWithOptions(ctx, s, options)
}

func (s *Service) SaveHistorySnapshot(ctx context.Context, source string) (HistoryEntry, error) {
	return s.saveHistorySnapshot(ctx, source, "", "", "")
}

func (s *Service) saveHistorySnapshot(ctx context.Context, source, actor, note, rollbackOf string) (HistoryEntry, error) {
	if s.history == nil {
		return HistoryEntry{}, nil
	}
	bundle, err := ExportBundleWithOptions(ctx, s, s.exportOptions)
	if err != nil {
		return HistoryEntry{}, err
	}
	entry := HistoryEntry{
		ID:         generateResourceID(),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Source:     source,
		Summary:    summarizeBundle(bundle),
		Actor:      actor,
		Note:       note,
		RollbackOf: rollbackOf,
		Bundle:     bundle,
	}
	return s.history.Save(ctx, entry, s.historyLimit)
}

func (s *Service) ListHistory(ctx context.Context, limit int) ([]HistoryEntry, error) {
	if s.history == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = s.historyLimit
	}
	return s.history.List(ctx, limit)
}

func (s *Service) RollbackHistory(ctx context.Context, id string) (ApplyResult, HistoryEntry, error) {
	if s.history == nil {
		return ApplyResult{}, HistoryEntry{}, ErrNotFound
	}
	target, err := s.history.Get(ctx, id)
	if err != nil {
		return ApplyResult{}, HistoryEntry{}, err
	}
	result, err := ApplyBundleWithOptions(ctx, s, target.Bundle, ApplyOptions{
		Prune:      true,
		PruneKinds: target.Bundle.Meta.ManagedKinds,
	})
	if err != nil {
		return ApplyResult{}, HistoryEntry{}, err
	}
	entry, saveErr := s.saveHistorySnapshot(ctx, "history_rollback", "", "", id)
	return result, entry, saveErr
}

func SupportedKinds() []ResourceKind {
	return append([]ResourceKind(nil), supportedKinds...)
}

func ParseKind(raw string) (ResourceKind, bool) {
	kind := ResourceKind(raw)
	return kind, slices.Contains(supportedKinds, kind)
}

func NormalizeResourceBody(data []byte, id string) (json.RawMessage, error) {
	decoded, err := decodeJSONObject(data)
	if err != nil {
		return nil, err
	}
	decoded["id"] = id
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	return normalized, nil
}

func ExtractResourceID(data []byte) (string, error) {
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	switch value := decoded["id"].(type) {
	case string:
		return value, nil
	case nil:
		return "", nil
	case float64:
		return strconv.FormatInt(int64(value), 10), nil
	default:
		return fmt.Sprint(value), nil
	}
}

func validateKind(kind ResourceKind) error {
	if slices.Contains(supportedKinds, kind) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrUnsupportedKind, kind)
}

func (s *Service) normalizeForWrite(ctx context.Context, kind ResourceKind, id string, body json.RawMessage) (json.RawMessage, error) {
	decoded, err := decodeJSONObject(body)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	decoded["id"] = id

	existing, getErr := s.store.Get(ctx, kind, id)
	switch {
	case getErr == nil:
		existingDecoded, err := decodeJSONObject(existing.Value)
		if err != nil {
			return nil, err
		}
		if created, ok := existingDecoded["create_time"]; ok {
			decoded["create_time"] = created
		} else {
			decoded["create_time"] = now
		}
	case errors.Is(getErr, ErrNotFound):
		decoded["create_time"] = now
	case getErr != nil:
		return nil, getErr
	}
	decoded["update_time"] = now

	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	return normalized, nil
}

func decodeJSONObject(data []byte) (map[string]any, error) {
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	if decoded == nil {
		decoded = map[string]any{}
	} else {
		decoded = maps.Clone(decoded)
	}
	return decoded, nil
}

func mergeJSON(baseData, patchData []byte) (json.RawMessage, error) {
	var baseValue map[string]any
	if err := json.Unmarshal(baseData, &baseValue); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	var patchValue map[string]any
	if err := json.Unmarshal(patchData, &patchValue); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	merged := mergeObject(baseValue, patchValue)
	normalized, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
	}
	return normalized, nil
}

func mergeObject(base, patch map[string]any) map[string]any {
	out := make(map[string]any, len(base))
	for key, value := range base {
		out[key] = value
	}
	for key, patchValue := range patch {
		if patchValue == nil {
			delete(out, key)
			continue
		}
		baseValue, ok := out[key]
		if ok {
			baseMap, baseOK := baseValue.(map[string]any)
			patchMap, patchOK := patchValue.(map[string]any)
			if baseOK && patchOK {
				out[key] = mergeObject(baseMap, patchMap)
				continue
			}
		}
		out[key] = patchValue
	}
	return out
}

func generateResourceID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}
