package adminapi

import (
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/joey/lumen-gateway/internal/controlplane"
)

type schemaResponse struct {
	Resources    []resourceSchema    `json:"resources"`
	Plugins      []pluginCapability  `json:"plugins"`
	Capabilities controlCapabilities `json:"capabilities"`
}

type resourceSchema struct {
	Kind        string            `json:"kind"`
	Label       string            `json:"label"`
	Description string            `json:"description"`
	Methods     []string          `json:"methods"`
	KeyFields   []fieldSchema     `json:"key_fields"`
	Examples    map[string]string `json:"examples,omitempty"`
}

type fieldSchema struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

type pluginCapability struct {
	Name         string   `json:"name"`
	Label        string   `json:"label"`
	Description  string   `json:"description"`
	Scopes       []string `json:"scopes"`
	TranslatedTo []string `json:"translated_to,omitempty"`
}

type controlCapabilities struct {
	BundleFormats        []string               `json:"bundle_formats"`
	ExportFormats        []string               `json:"export_formats"`
	HistoryLimit         int                    `json:"history_limit"`
	Supports             map[string]bool        `json:"supports"`
	PreviewActions       []string               `json:"preview_actions"`
	ValidationIssueShape []validationIssueField `json:"validation_issue_shape"`
}

type validationIssueField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

func (h *Handler) handleSchema(c *app.RequestContext) {
	writeJSON(c, http.StatusOK, buildSchemaResponse())
}

func buildSchemaResponse() schemaResponse {
	return schemaResponse{
		Resources: []resourceSchema{
			{
				Kind:        string(controlplane.KindRoute),
				Label:       "Route",
				Description: "Matches incoming traffic and attaches a service or upstream plus plugins.",
				Methods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
				KeyFields: []fieldSchema{
					{Name: "id", Type: "string", Required: false, Description: "Unique route identifier."},
					{Name: "uri", Type: "string", Required: false, Description: "Single APISIX URI matcher."},
					{Name: "uris", Type: "array[string]", Required: false, Description: "Multiple URI matchers; one of uri or uris is expected."},
					{Name: "hosts", Type: "array[string]", Required: false, Description: "Optional host matchers."},
					{Name: "methods", Type: "array[string]", Required: false, Description: "Optional HTTP method matchers."},
					{Name: "service_id", Type: "string", Required: false, Description: "References a service resource."},
					{Name: "upstream_id", Type: "string", Required: false, Description: "References an upstream when no service is used."},
					{Name: "plugin_config_id", Type: "string", Required: false, Description: "References a reusable APISIX plugin_config."},
					{Name: "plugins", Type: "object", Required: false, Description: "Inline APISIX plugin configuration."},
				},
				Examples: map[string]string{
					"uri": "/users/*",
				},
			},
			{
				Kind:        string(controlplane.KindService),
				Label:       "Service",
				Description: "Reusable upstream and plugin attachment target for routes.",
				Methods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
				KeyFields: []fieldSchema{
					{Name: "id", Type: "string", Required: false, Description: "Unique service identifier."},
					{Name: "upstream_id", Type: "string", Required: false, Description: "References an upstream resource."},
					{Name: "plugin_config_id", Type: "string", Required: false, Description: "References a reusable APISIX plugin_config."},
					{Name: "plugins", Type: "object", Required: false, Description: "Inline APISIX plugin configuration."},
				},
			},
			{
				Kind:        string(controlplane.KindUpstream),
				Label:       "Upstream",
				Description: "Backend target pool and proxy behavior configuration.",
				Methods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
				KeyFields: []fieldSchema{
					{Name: "id", Type: "string", Required: false, Description: "Unique upstream identifier."},
					{Name: "type", Type: "string", Required: false, Description: "Load balancer type; roundrobin is supported today."},
					{Name: "scheme", Type: "string", Required: false, Description: "http or https."},
					{Name: "nodes", Type: "object", Required: true, Description: "Map of host:port to weight."},
					{Name: "pass_host", Type: "string", Required: false, Description: "pass, node, or rewrite."},
					{Name: "upstream_host", Type: "string", Required: false, Description: "Required when pass_host=rewrite."},
					{Name: "timeout", Type: "object", Required: false, Description: "connect, send, and read timeout values."},
				},
			},
			{
				Kind:        string(controlplane.KindPluginConfig),
				Label:       "Plugin Config",
				Description: "Reusable APISIX plugin bundle referenced by routes or services.",
				Methods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
				KeyFields: []fieldSchema{
					{Name: "id", Type: "string", Required: false, Description: "Unique plugin config identifier."},
					{Name: "plugins", Type: "object", Required: true, Description: "Map of APISIX plugin name to plugin config."},
				},
			},
			{
				Kind:        string(controlplane.KindGlobalRule),
				Label:       "Global Rule",
				Description: "Globally applied APISIX plugins merged into every request.",
				Methods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
				KeyFields: []fieldSchema{
					{Name: "id", Type: "string", Required: false, Description: "Unique global rule identifier."},
					{Name: "plugins", Type: "object", Required: true, Description: "Map of APISIX plugin name to plugin config."},
				},
			},
		},
		Plugins: []pluginCapability{
			{
				Name:         "proxy-rewrite",
				Label:        "Proxy Rewrite",
				Description:  "Rewrites request path, method, host, and headers before proxying.",
				Scopes:       []string{"route", "service", "plugin_config", "global_rule"},
				TranslatedTo: []string{"rewrite_path_regex", "request_transformer", "replace_path"},
			},
			{
				Name:         "response-rewrite",
				Label:        "Response Rewrite",
				Description:  "Rewrites response status, headers, and body after upstream handling.",
				Scopes:       []string{"route", "service", "plugin_config", "global_rule"},
				TranslatedTo: []string{"response_transformer"},
			},
			{
				Name:         "request-id",
				Label:        "Request ID",
				Description:  "Generates or propagates request IDs to request and response headers.",
				Scopes:       []string{"route", "service", "plugin_config", "global_rule"},
				TranslatedTo: []string{"request_id"},
			},
			{
				Name:         "limit-count",
				Label:        "Limit Count",
				Description:  "Applies in-memory fixed-window request limiting by route and key.",
				Scopes:       []string{"route", "service", "plugin_config", "global_rule"},
				TranslatedTo: []string{"limit_count"},
			},
		},
		Capabilities: controlCapabilities{
			BundleFormats:  []string{"json", "yaml"},
			ExportFormats:  []string{"json", "yaml"},
			HistoryLimit:   10,
			PreviewActions: []string{"create", "update", "delete", "unchanged"},
			Supports: map[string]bool{
				"validate":          true,
				"preview":           true,
				"apply":             true,
				"export":            true,
				"history":           true,
				"rollback":          true,
				"selective_prune":   true,
				"bundle_yaml_input": true,
				"bundle_json_input": true,
			},
			ValidationIssueShape: []validationIssueField{
				{Name: "resource", Type: "string", Description: "APISIX resource kind such as routes or services."},
				{Name: "resource_id", Type: "string", Description: "Resource identifier when known."},
				{Name: "field", Type: "string", Description: "Best-effort field hint for UI focus and highlighting."},
				{Name: "message", Type: "string", Description: "Human-readable validation message."},
			},
		},
	}
}
