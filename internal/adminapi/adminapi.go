package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/joey/lumen-gateway/internal/bootstrap"
	"github.com/joey/lumen-gateway/internal/controlplane"
)

const routePrefix = "/apisix/admin/"
const controlPrefix = "/apisix/admin/control/"

type service interface {
	List(ctx context.Context, kind controlplane.ResourceKind) ([]controlplane.Envelope, error)
	Get(ctx context.Context, kind controlplane.ResourceKind, id string) (controlplane.Envelope, error)
	Put(ctx context.Context, kind controlplane.ResourceKind, id string, body json.RawMessage) (controlplane.Envelope, error)
	Post(ctx context.Context, kind controlplane.ResourceKind, body json.RawMessage) (controlplane.Envelope, error)
	Patch(ctx context.Context, kind controlplane.ResourceKind, id string, body json.RawMessage) (controlplane.Envelope, error)
	Delete(ctx context.Context, kind controlplane.ResourceKind, id string) (controlplane.DeleteResult, error)
	ValidateBundle(ctx context.Context, bundle controlplane.FileBundle, options controlplane.ValidateOptions) (controlplane.ValidationResult, error)
	ValidateResource(ctx context.Context, kind controlplane.ResourceKind, id string, body json.RawMessage) (controlplane.ValidationResult, error)
	PreviewBundle(ctx context.Context, bundle controlplane.FileBundle, options controlplane.PreviewOptions) (controlplane.ApplyPlan, error)
	ApplyBundle(ctx context.Context, bundle controlplane.FileBundle, options controlplane.ApplyOptions) (controlplane.ApplyResult, error)
	ExportBundle(ctx context.Context, options controlplane.ExportOptions) (controlplane.FileBundle, error)
	SaveHistorySnapshot(ctx context.Context, source string) (controlplane.HistoryEntry, error)
	ListHistory(ctx context.Context, limit int) ([]controlplane.HistoryEntry, error)
	RollbackHistory(ctx context.Context, id string) (controlplane.ApplyResult, controlplane.HistoryEntry, error)
	Close() error
}

type Handler struct {
	service  service
	adminKey string
}

func New(boot bootstrap.Options) (*Handler, error) {
	if boot.Gateway.Source != "etcd_apisix" {
		return nil, nil
	}
	store, err := controlplane.NewEtcdStore(boot)
	if err != nil {
		return nil, err
	}
	historyStore, err := controlplane.NewEtcdHistoryStore(boot)
	if err != nil {
		return nil, err
	}
	return NewWithService(controlplane.New(
		store,
		controlplane.WithHistory(historyStore, 10, controlplane.ExportOptions{EtcdPrefix: boot.Etcd.Prefix}),
	), boot.Admin.Key), nil
}

func NewWithService(svc service, adminKey string) *Handler {
	return &Handler{
		service:  svc,
		adminKey: adminKey,
	}
}

func (h *Handler) Close() error {
	if h == nil || h.service == nil {
		return nil
	}
	return h.service.Close()
}

func (h *Handler) ServeHTTP(ctx context.Context, c *app.RequestContext) bool {
	if h == nil {
		return false
	}
	pathValue := string(c.Path())
	if strings.HasPrefix(pathValue, controlPrefix) {
		if !h.authorized(c) {
			writeControlError(c, http.StatusUnauthorized, "unauthorized", "missing or invalid X-API-KEY", nil)
			return true
		}
		h.handleControl(ctx, c, pathValue)
		return true
	}
	if !strings.HasPrefix(pathValue, routePrefix) {
		return false
	}
	if !h.authorized(c) {
		writeError(c, http.StatusUnauthorized, "missing or invalid X-API-KEY")
		return true
	}

	resource, id, ok := parsePath(pathValue)
	if !ok {
		writeError(c, http.StatusNotFound, "unsupported admin path")
		return true
	}
	kind, ok := controlplane.ParseKind(resource)
	if !ok {
		writeError(c, http.StatusNotFound, "unsupported resource")
		return true
	}

	switch string(c.Method()) {
	case http.MethodGet:
		if id == "" {
			h.handleList(ctx, c, kind)
			return true
		}
		h.handleGet(ctx, c, kind, id)
		return true
	case http.MethodPost:
		if id != "" {
			writeError(c, http.StatusBadRequest, "resource id must not be in path for POST")
			return true
		}
		h.handlePost(ctx, c, kind)
		return true
	case http.MethodPut:
		h.handlePut(ctx, c, kind, id)
		return true
	case http.MethodPatch:
		h.handlePatch(ctx, c, kind, id)
		return true
	case http.MethodDelete:
		h.handleDelete(ctx, c, kind, id)
		return true
	default:
		writeError(c, http.StatusMethodNotAllowed, "unsupported method")
		return true
	}
}

type bundleRequest struct {
	Bundle           json.RawMessage `json:"bundle"`
	Content          string          `json:"content"`
	Prune            bool            `json:"prune"`
	PruneKinds       []string        `json:"prune_kinds"`
	IncludeUnchanged bool            `json:"include_unchanged"`
}

type validateRequest struct {
	Kind       string          `json:"kind"`
	ID         string          `json:"id"`
	Resource   json.RawMessage `json:"resource"`
	Bundle     json.RawMessage `json:"bundle"`
	Content    string          `json:"content"`
	Prune      bool            `json:"prune"`
	PruneKinds []string        `json:"prune_kinds"`
}

func (h *Handler) handleControl(ctx context.Context, c *app.RequestContext, pathValue string) {
	trimmed := strings.Trim(strings.TrimPrefix(pathValue, controlPrefix), "/")
	switch {
	case trimmed == "validate" && string(c.Method()) == http.MethodPost:
		h.handleValidate(ctx, c)
	case trimmed == "imports/preview" && string(c.Method()) == http.MethodPost:
		h.handleImportPreview(ctx, c)
	case trimmed == "imports/apply" && string(c.Method()) == http.MethodPost:
		h.handleImportApply(ctx, c)
	case trimmed == "exports" && string(c.Method()) == http.MethodGet:
		h.handleExport(ctx, c)
	case trimmed == "history" && string(c.Method()) == http.MethodGet:
		h.handleHistoryList(ctx, c)
	case strings.HasPrefix(trimmed, "history/") && strings.HasSuffix(trimmed, "/rollback") && string(c.Method()) == http.MethodPost:
		h.handleHistoryRollback(ctx, c, trimmed)
	default:
		writeControlError(c, http.StatusNotFound, "not_found", "unsupported control path", nil)
	}
}

func (h *Handler) handleValidate(ctx context.Context, c *app.RequestContext) {
	request, err := decodeValidateRequest(c.Request.Body())
	if err != nil {
		writeControlMappedError(c, err)
		return
	}

	if request.Kind != "" || len(request.Resource) > 0 {
		kind, ok := controlplane.ParseKind(request.Kind)
		if !ok {
			writeControlError(c, http.StatusBadRequest, "invalid_request", "unsupported resource kind", nil)
			return
		}
		result, err := h.service.ValidateResource(ctx, kind, request.ID, request.Resource)
		if err != nil {
			writeControlMappedError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, result)
		return
	}

	pruneKinds, err := parseKinds(request.PruneKinds)
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	bundle, err := decodeBundleFromValidateRequest(request)
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	result, err := h.service.ValidateBundle(ctx, bundle, controlplane.ValidateOptions{
		Prune:      request.Prune,
		PruneKinds: pruneKinds,
	})
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, result)
}

func (h *Handler) handleImportPreview(ctx context.Context, c *app.RequestContext) {
	request, bundle, err := decodeBundleRequest(c.Request.Body())
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	pruneKinds, err := parseKinds(request.PruneKinds)
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	plan, err := h.service.PreviewBundle(ctx, bundle, controlplane.PreviewOptions{
		Prune:            request.Prune,
		PruneKinds:       pruneKinds,
		IncludeUnchanged: request.IncludeUnchanged,
	})
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, plan)
}

func (h *Handler) handleImportApply(ctx context.Context, c *app.RequestContext) {
	request, bundle, err := decodeBundleRequest(c.Request.Body())
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	pruneKinds, err := parseKinds(request.PruneKinds)
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	result, err := h.service.ApplyBundle(ctx, bundle, controlplane.ApplyOptions{
		Prune:      request.Prune,
		PruneKinds: pruneKinds,
	})
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	history, err := h.service.SaveHistorySnapshot(ctx, "control_import_apply")
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{
		"result":  result,
		"history": history,
	})
}

func (h *Handler) handleExport(ctx context.Context, c *app.RequestContext) {
	kinds, err := parseKinds(headersOrQueryList(c, "kind"))
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	format := strings.ToLower(strings.TrimSpace(queryValue(c, "format")))
	if format == "" {
		format = "json"
	}
	bundle, err := h.service.ExportBundle(ctx, controlplane.ExportOptions{
		IncludeKinds: kinds,
		IncludeMeta:  true,
	})
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	switch format {
	case "json":
		payload, err := bundle.ToMap()
		if err != nil {
			writeControlMappedError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, payload)
	case "yaml":
		content, err := bundle.ToYAML()
		if err != nil {
			writeControlMappedError(c, err)
			return
		}
		writeJSON(c, http.StatusOK, map[string]any{
			"format":  "yaml",
			"content": string(content),
		})
	default:
		writeControlError(c, http.StatusBadRequest, "invalid_request", "unsupported export format", nil)
	}
}

func (h *Handler) handleHistoryList(ctx context.Context, c *app.RequestContext) {
	limit := 10
	if raw := strings.TrimSpace(queryValue(c, "limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			limit = value
		}
	}
	items, err := h.service.ListHistory(ctx, limit)
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{
		"list":  items,
		"total": len(items),
	})
}

func (h *Handler) handleHistoryRollback(ctx context.Context, c *app.RequestContext, trimmed string) {
	id := strings.TrimSuffix(strings.TrimPrefix(trimmed, "history/"), "/rollback")
	id = strings.Trim(id, "/")
	if id == "" {
		writeControlError(c, http.StatusBadRequest, "invalid_request", "history id is required", nil)
		return
	}
	result, history, err := h.service.RollbackHistory(ctx, id)
	if err != nil {
		writeControlMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{
		"result":  result,
		"history": history,
	})
}

func (h *Handler) authorized(c *app.RequestContext) bool {
	if h.adminKey == "" {
		return false
	}
	return string(c.Request.Header.Peek("X-API-KEY")) == h.adminKey
}

func (h *Handler) handleList(ctx context.Context, c *app.RequestContext, kind controlplane.ResourceKind) {
	items, err := h.service.List(ctx, kind)
	if err != nil {
		writeMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{
		"list":  items,
		"total": len(items),
	})
}

func (h *Handler) handleGet(ctx context.Context, c *app.RequestContext, kind controlplane.ResourceKind, id string) {
	item, err := h.service.Get(ctx, kind, id)
	if err != nil {
		writeMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, item)
}

func (h *Handler) handlePut(ctx context.Context, c *app.RequestContext, kind controlplane.ResourceKind, id string) {
	if id == "" {
		writeError(c, http.StatusBadRequest, "resource id is required")
		return
	}
	status := http.StatusOK
	if _, err := h.service.Get(ctx, kind, id); err != nil {
		if errors.Is(err, controlplane.ErrNotFound) {
			status = http.StatusCreated
		} else {
			writeMappedError(c, err)
			return
		}
	}
	item, err := h.service.Put(ctx, kind, id, c.Request.Body())
	if err != nil {
		writeMappedError(c, err)
		return
	}
	writeJSON(c, status, item)
}

func (h *Handler) handlePost(ctx context.Context, c *app.RequestContext, kind controlplane.ResourceKind) {
	item, err := h.service.Post(ctx, kind, c.Request.Body())
	if err != nil {
		writeMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusCreated, item)
}

func (h *Handler) handlePatch(ctx context.Context, c *app.RequestContext, kind controlplane.ResourceKind, id string) {
	item, err := h.service.Patch(ctx, kind, id, c.Request.Body())
	if err != nil {
		writeMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, item)
}

func (h *Handler) handleDelete(ctx context.Context, c *app.RequestContext, kind controlplane.ResourceKind, id string) {
	result, err := h.service.Delete(ctx, kind, id)
	if err != nil {
		writeMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, result)
}

func parsePath(pathValue string) (resource, id string, ok bool) {
	trimmed := strings.TrimPrefix(pathValue, routePrefix)
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return "", "", false
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) == 1 {
		return parts[0], "", true
	}
	if len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func writeMappedError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, controlplane.ErrNotFound):
		writeMessage(c, http.StatusNotFound, "Key not found")
	case errors.Is(err, controlplane.ErrInvalidBody):
		writeError(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, controlplane.ErrUnsupportedKind):
		writeMessage(c, http.StatusNotFound, "Key not found")
	default:
		writeError(c, http.StatusBadGateway, err.Error())
	}
}

func writeControlMappedError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, controlplane.ErrNotFound):
		writeControlError(c, http.StatusNotFound, "not_found", "Key not found", nil)
	case errors.Is(err, controlplane.ErrInvalidBody):
		writeControlError(c, http.StatusBadRequest, "invalid_request", err.Error(), nil)
	case errors.Is(err, controlplane.ErrUnsupportedKind):
		writeControlError(c, http.StatusBadRequest, "invalid_request", err.Error(), nil)
	default:
		writeControlError(c, http.StatusBadGateway, "controlplane_error", err.Error(), nil)
	}
}

func decodeBundleRequest(body []byte) (bundleRequest, controlplane.FileBundle, error) {
	var request bundleRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return bundleRequest{}, controlplane.FileBundle{}, controlplane.ErrInvalidBody
	}
	switch {
	case len(request.Bundle) > 0:
		encoded, err := json.Marshal(request.Bundle)
		if err != nil {
			return bundleRequest{}, controlplane.FileBundle{}, controlplane.ErrInvalidBody
		}
		bundle, err := controlplane.ParseBundle(encoded)
		return request, bundle, err
	case strings.TrimSpace(request.Content) != "":
		bundle, err := controlplane.ParseBundle([]byte(request.Content))
		return request, bundle, err
	default:
		return bundleRequest{}, controlplane.FileBundle{}, controlplane.ErrInvalidBody
	}
}

func decodeValidateRequest(body []byte) (validateRequest, error) {
	var request validateRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return validateRequest{}, controlplane.ErrInvalidBody
	}
	if request.Kind == "" && len(request.Resource) == 0 && len(request.Bundle) == 0 && strings.TrimSpace(request.Content) == "" {
		return validateRequest{}, controlplane.ErrInvalidBody
	}
	if request.Kind != "" && len(request.Resource) == 0 {
		return validateRequest{}, controlplane.ErrInvalidBody
	}
	if request.Kind == "" && len(request.Resource) > 0 {
		return validateRequest{}, controlplane.ErrInvalidBody
	}
	return request, nil
}

func decodeBundleFromValidateRequest(request validateRequest) (controlplane.FileBundle, error) {
	switch {
	case len(request.Bundle) > 0:
		encoded, err := json.Marshal(request.Bundle)
		if err != nil {
			return controlplane.FileBundle{}, controlplane.ErrInvalidBody
		}
		return controlplane.ParseBundle(encoded)
	case strings.TrimSpace(request.Content) != "":
		return controlplane.ParseBundle([]byte(request.Content))
	default:
		return controlplane.FileBundle{}, controlplane.ErrInvalidBody
	}
}

func parseKinds(values []string) ([]controlplane.ResourceKind, error) {
	if len(values) == 0 {
		return nil, nil
	}
	kinds := make([]controlplane.ResourceKind, 0, len(values))
	for _, value := range values {
		kind, ok := controlplane.ParseKind(value)
		if !ok {
			return nil, controlplane.ErrInvalidBody
		}
		kinds = append(kinds, kind)
	}
	return kinds, nil
}

func headersOrQueryList(c *app.RequestContext, key string) []string {
	out := make([]string, 0, 1)
	c.QueryArgs().VisitAll(func(k, value []byte) {
		if string(k) == key {
			out = append(out, string(value))
		}
	})
	return out
}

func queryValue(c *app.RequestContext, key string) string {
	return string(c.QueryArgs().Peek(key))
}

func writeAPISIXBody(c *app.RequestContext, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Response.Header.Set("Content-Type", "application/json")
	c.Response.SetStatusCode(status)
	c.Response.SetBodyRaw(body)
}

func writeJSON(c *app.RequestContext, status int, payload any) {
	writeAPISIXBody(c, status, payload)
}

func writeError(c *app.RequestContext, status int, message string) {
	writeJSON(c, status, map[string]any{
		"error_msg": message,
	})
}

func writeControlError(c *app.RequestContext, status int, code string, message string, details any) {
	payload := map[string]any{
		"code":    code,
		"message": message,
	}
	if details != nil {
		payload["details"] = details
	}
	writeJSON(c, status, payload)
}

func writeMessage(c *app.RequestContext, status int, message string) {
	writeJSON(c, status, map[string]any{
		"message": message,
	})
}
