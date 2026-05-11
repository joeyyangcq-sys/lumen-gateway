package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/joey/lumen-gateway/internal/bootstrap"
	"github.com/joey/lumen-gateway/internal/controlplane"
	"github.com/joey/lumen-gateway/internal/observability"
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

// PluginCatalogEntry describes a single plugin registered in the gateway.
// It is returned by GET /apisix/admin/control/plugins so the admin UI can
// present a selectable list instead of asking users to type plugin names.
type PluginCatalogEntry struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

type Handler struct {
	service       service
	adminKey      string
	pluginCatalog []PluginCatalogEntry
}

// SetPluginCatalog stores the list of registered plugins so the admin API
// can return them at GET /apisix/admin/control/plugins.
func (h *Handler) SetPluginCatalog(catalog []PluginCatalogEntry) {
	if h == nil {
		return
	}
	h.pluginCatalog = catalog
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
	isAdminPath := strings.HasPrefix(pathValue, routePrefix) || strings.HasPrefix(pathValue, controlPrefix)

	// Handle CORS preflight: OPTIONS must succeed without auth so the browser
	// will proceed with the actual request carrying X-API-KEY.
	if string(c.Method()) == http.MethodOptions && isAdminPath {
		setCORSHeaders(c)
		c.Response.SetStatusCode(http.StatusNoContent)
		return true
	}

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

type operationMetadata struct {
	OperationID string                      `json:"operation_id"`
	CreatedAt   string                      `json:"created_at,omitempty"`
	Source      string                      `json:"source,omitempty"`
	Summary     controlplane.HistorySummary `json:"summary,omitempty"`
	Actor       string                      `json:"actor,omitempty"`
	Note        string                      `json:"note,omitempty"`
	RollbackOf  string                      `json:"rollback_of,omitempty"`
}

type listQuery struct {
	Page     int
	PageSize int
	Keyword  string
}

type listResourceItem struct {
	controlplane.Envelope
	Summary controlplane.ResourceSummary `json:"summary"`
}

func (h *Handler) handleControl(ctx context.Context, c *app.RequestContext, pathValue string) {
	trimmed := strings.Trim(strings.TrimPrefix(pathValue, controlPrefix), "/")
	switch {
	case trimmed == "schema" && string(c.Method()) == http.MethodGet:
		h.handleSchema(c)
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
	case trimmed == "plugins" && string(c.Method()) == http.MethodGet:
		h.handlePluginCatalog(c)
	case trimmed == "stats" && string(c.Method()) == http.MethodGet:
		h.handleStats(c)
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

func (h *Handler) handleStats(c *app.RequestContext) {
	setCORSHeaders(c)
	writeJSON(c, http.StatusOK, observability.Default().Stats())
}

func (h *Handler) handlePluginCatalog(c *app.RequestContext) {
	setCORSHeaders(c)
	catalog := h.pluginCatalog
	if catalog == nil {
		catalog = []PluginCatalogEntry{}
	}
	writeJSON(c, http.StatusOK, catalog)
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
		"result":    result,
		"history":   history,
		"operation": operationFromHistory(history),
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
		"result":    result,
		"history":   history,
		"operation": operationFromHistory(history),
	})
}

func setCORSHeaders(c *app.RequestContext) {
	c.Response.Header.Set("Access-Control-Allow-Origin", "*")
	c.Response.Header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	c.Response.Header.Set("Access-Control-Allow-Headers", "Content-Type, X-API-KEY")
	c.Response.Header.Set("Access-Control-Max-Age", "86400")
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
	query := parseListQuery(c)
	filtered := filterEnvelopes(items, query.Keyword)
	paged := paginateEnvelopes(filtered, query.Page, query.PageSize)
	responseItems := make([]listResourceItem, 0, len(paged))
	for _, item := range paged {
		id, _ := controlplane.ExtractResourceID(item.Value)
		responseItems = append(responseItems, listResourceItem{
			Envelope: item,
			Summary:  controlplane.SummarizeResource(kind, id, item.Value),
		})
	}
	writeJSON(c, http.StatusOK, map[string]any{
		"list":      responseItems,
		"total":     len(filtered),
		"page":      query.Page,
		"page_size": query.PageSize,
		"keyword":   query.Keyword,
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
	body := c.Request.Body()

	// Validate cross-references before writing to etcd.
	if stop := h.validateBeforeWrite(ctx, c, kind, id, body); stop {
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
	item, err := h.service.Put(ctx, kind, id, body)
	if err != nil {
		writeMappedError(c, err)
		return
	}
	writeJSON(c, status, item)
}

func (h *Handler) handlePost(ctx context.Context, c *app.RequestContext, kind controlplane.ResourceKind) {
	body := c.Request.Body()
	id, _ := controlplane.ExtractResourceID(body)

	// Validate cross-references before writing to etcd.
	if stop := h.validateBeforeWrite(ctx, c, kind, id, body); stop {
		return
	}

	item, err := h.service.Post(ctx, kind, body)
	if err != nil {
		writeMappedError(c, err)
		return
	}
	writeJSON(c, http.StatusCreated, item)
}

// validateBeforeWrite runs ValidateResource and writes a 422 with a human-readable
// Chinese error message if the resource is invalid. Returns true if the handler
// should stop (i.e. we already wrote an error response).
func (h *Handler) validateBeforeWrite(
	ctx context.Context,
	c *app.RequestContext,
	kind controlplane.ResourceKind,
	id string,
	body []byte,
) bool {
	result, err := h.service.ValidateResource(ctx, kind, id, body)
	if err != nil {
		// Validation infrastructure failed (e.g. etcd unreachable) — let the
		// write proceed so we don't block users when etcd is temporarily slow.
		return false
	}
	if result.Valid {
		return false
	}

	msg := "配置验证失败"
	if len(result.Issues) > 0 {
		msg = result.Issues[0].Message
	}
	writeJSON(c, http.StatusUnprocessableEntity, map[string]any{
		"error_msg":  msg,
		"validation": result,
	})
	return true
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

func parseListQuery(c *app.RequestContext) listQuery {
	page := 1
	if raw := strings.TrimSpace(queryValue(c, "page")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			page = value
		}
	}

	pageSize := 50
	if raw := strings.TrimSpace(queryValue(c, "page_size")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			pageSize = min(value, 200)
		}
	}

	return listQuery{
		Page:     page,
		PageSize: pageSize,
		Keyword:  strings.TrimSpace(queryValue(c, "keyword")),
	}
}

func filterEnvelopes(items []controlplane.Envelope, keyword string) []controlplane.Envelope {
	if strings.TrimSpace(keyword) == "" {
		return items
	}
	keyword = strings.ToLower(keyword)
	filtered := make([]controlplane.Envelope, 0, len(items))
	for _, item := range items {
		candidates := []string{item.Key, string(item.Value)}
		if id, err := controlplane.ExtractResourceID(item.Value); err == nil && id != "" {
			candidates = append(candidates, id)
		}
		if slices.ContainsFunc(candidates, func(value string) bool {
			return strings.Contains(strings.ToLower(value), keyword)
		}) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func paginateEnvelopes(items []controlplane.Envelope, page int, pageSize int) []controlplane.Envelope {
	if len(items) == 0 {
		return []controlplane.Envelope{}
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []controlplane.Envelope{}
	}
	end := min(start+pageSize, len(items))
	return items[start:end]
}

func writeAPISIXBody(c *app.RequestContext, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	setCORSHeaders(c)
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

func operationFromHistory(entry controlplane.HistoryEntry) operationMetadata {
	return operationMetadata{
		OperationID: entry.ID,
		CreatedAt:   entry.CreatedAt,
		Source:      entry.Source,
		Summary:     entry.Summary,
		Actor:       entry.Actor,
		Note:        entry.Note,
		RollbackOf:  entry.RollbackOf,
	}
}

func writeMessage(c *app.RequestContext, status int, message string) {
	writeJSON(c, status, map[string]any{
		"message": message,
	})
}
