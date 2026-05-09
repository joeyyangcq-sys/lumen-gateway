package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/joey/lumen-gateway/internal/bootstrap"
	"github.com/joey/lumen-gateway/internal/controlplane"
)

const routePrefix = "/apisix/admin/"

type service interface {
	List(ctx context.Context, kind controlplane.ResourceKind) ([]controlplane.Envelope, error)
	Get(ctx context.Context, kind controlplane.ResourceKind, id string) (controlplane.Envelope, error)
	Put(ctx context.Context, kind controlplane.ResourceKind, id string, body json.RawMessage) (controlplane.Envelope, error)
	Post(ctx context.Context, kind controlplane.ResourceKind, body json.RawMessage) (controlplane.Envelope, error)
	Patch(ctx context.Context, kind controlplane.ResourceKind, id string, body json.RawMessage) (controlplane.Envelope, error)
	Delete(ctx context.Context, kind controlplane.ResourceKind, id string) (controlplane.DeleteResult, error)
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
	return NewWithService(controlplane.New(store), boot.Admin.Key), nil
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
	if h == nil || !strings.HasPrefix(string(c.Path()), routePrefix) {
		return false
	}
	if !h.authorized(c) {
		writeError(c, http.StatusUnauthorized, "missing or invalid X-API-KEY")
		return true
	}

	resource, id, ok := parsePath(string(c.Path()))
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
		writeError(c, http.StatusNotFound, err.Error())
	case errors.Is(err, controlplane.ErrInvalidBody):
		writeError(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, controlplane.ErrUnsupportedKind):
		writeError(c, http.StatusNotFound, err.Error())
	default:
		writeError(c, http.StatusBadGateway, err.Error())
	}
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
