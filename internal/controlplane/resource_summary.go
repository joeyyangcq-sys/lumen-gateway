package controlplane

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ResourceSummary struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Fields      map[string]any `json:"fields,omitempty"`
}

func SummarizeResource(kind ResourceKind, id string, raw json.RawMessage) ResourceSummary {
	title, fields := summarizeChangeResource(kind, id, raw)
	summary := ResourceSummary{
		Title:  title,
		Fields: fields,
	}

	switch kind {
	case KindRoute:
		descriptionParts := make([]string, 0, 2)
		if serviceID := summaryLookupString(fields, "service_id"); serviceID != "" {
			descriptionParts = append(descriptionParts, "service "+serviceID)
		}
		if upstreamID := summaryLookupString(fields, "upstream_id"); upstreamID != "" {
			descriptionParts = append(descriptionParts, "upstream "+upstreamID)
		}
		summary.Description = strings.Join(descriptionParts, " · ")
		summary.Tags = appendNonEmpty(summary.Tags,
			firstMethod(fields),
			routeMatchTag(fields),
		)
	case KindService:
		summary.Description = firstNonEmptyString(
			stringValueWithPrefix(fields, "upstream_id", "upstream "),
			stringValueWithPrefix(fields, "plugin_config_id", "plugin_config "),
		)
	case KindUpstream:
		parts := make([]string, 0, 3)
		if scheme := summaryLookupString(fields, "scheme"); scheme != "" {
			parts = append(parts, scheme)
		}
		if nodesCount, ok := fields["nodes_count"].(int); ok && nodesCount > 0 {
			parts = append(parts, fmt.Sprintf("%d nodes", nodesCount))
		}
		if passHost := summaryLookupString(fields, "pass_host"); passHost != "" {
			parts = append(parts, "pass_host="+passHost)
		}
		summary.Description = strings.Join(parts, " · ")
	case KindPluginConfig, KindGlobalRule:
		if pluginKeys, ok := fields["plugin_keys"].([]string); ok && len(pluginKeys) > 0 {
			summary.Description = "plugins: " + strings.Join(pluginKeys, ", ")
			summary.Tags = append(summary.Tags, pluginKeys...)
		}
	}

	return summary
}

func summaryLookupString(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	value, _ := fields[key].(string)
	return value
}

func stringValueWithPrefix(fields map[string]any, key string, prefix string) string {
	value := summaryLookupString(fields, key)
	if value == "" {
		return ""
	}
	return prefix + value
}

func appendNonEmpty(out []string, values ...string) []string {
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstMethod(fields map[string]any) string {
	raw, ok := fields["methods"]
	if !ok {
		return ""
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	value, _ := items[0].(string)
	return value
}

func routeMatchTag(fields map[string]any) string {
	if uri := summaryLookupString(fields, "uri"); uri != "" {
		return uri
	}
	raw, ok := fields["uris"]
	if !ok {
		return ""
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	value, _ := items[0].(string)
	return value
}
