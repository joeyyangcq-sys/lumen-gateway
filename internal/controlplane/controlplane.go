package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Deleted int64  `json:"deleted"`
}

type ApplyOptions struct {
	Prune bool
}

type Store interface {
	List(ctx context.Context, kind ResourceKind) ([]Envelope, error)
	Get(ctx context.Context, kind ResourceKind, id string) (Envelope, error)
	Put(ctx context.Context, kind ResourceKind, id string, body json.RawMessage) (Envelope, error)
	Delete(ctx context.Context, kind ResourceKind, id string) (DeleteResult, error)
	Close() error
}

type Service struct {
	store Store
}

func New(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Close()
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
	normalized, err := NormalizeResourceBody(body, id)
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
	normalized, err := NormalizeResourceBody(body, id)
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
	normalized, err := NormalizeResourceBody(merged, id)
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

func SupportedKinds() []ResourceKind {
	return append([]ResourceKind(nil), supportedKinds...)
}

func ParseKind(raw string) (ResourceKind, bool) {
	kind := ResourceKind(raw)
	return kind, slices.Contains(supportedKinds, kind)
}

func NormalizeResourceBody(data []byte, id string) (json.RawMessage, error) {
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBody, err)
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
