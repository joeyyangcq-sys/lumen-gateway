package builtin

import (
	"net"
	"strings"

	"github.com/joey/lumen-gateway/internal/plugin"
)

func renderRequestTemplate(pc plugin.PluginContext, value string) string {
	if value == "" || !strings.Contains(value, "$") {
		return value
	}

	var builder strings.Builder
	builder.Grow(len(value))

	for index := 0; index < len(value); index++ {
		if value[index] != '$' {
			builder.WriteByte(value[index])
			continue
		}

		if index+1 >= len(value) {
			builder.WriteByte(value[index])
			continue
		}

		next := value[index+1]
		switch {
		case next >= '0' && next <= '9':
			end := index + 2
			for end < len(value) && value[end] >= '0' && value[end] <= '9' {
				end++
			}
			builder.WriteString(resolveRequestVariable(pc, value[index+1:end]))
			index = end - 1
		case isVariableChar(next):
			end := index + 2
			for end < len(value) && isVariableChar(value[end]) {
				end++
			}
			builder.WriteString(resolveRequestVariable(pc, value[index+1:end]))
			index = end - 1
		default:
			builder.WriteByte(value[index])
		}
	}

	return builder.String()
}

func resolveRequestVariable(pc plugin.PluginContext, variable string) string {
	if variable == "" {
		return ""
	}

	if isNumeric(variable) {
		index := atoi(variable)
		captures := pc.RegexCaptures()
		if index >= 1 && index <= len(captures) {
			return captures[index-1]
		}
		return ""
	}

	switch variable {
	case "host":
		return pc.RequestHost()
	case "uri":
		return pc.RequestPath()
	case "request_uri":
		return pc.RequestURI()
	case "remote_addr":
		if ip := pc.ClientIP(); ip != "" {
			return ip
		}
		if addr := pc.Raw().RemoteAddr(); addr != nil {
			host, _, err := net.SplitHostPort(addr.String())
			if err == nil {
				return host
			}
			return addr.String()
		}
		return ""
	case "request_id":
		return pc.RequestID()
	case "route_id":
		return pc.RouteID()
	case "service_id":
		return pc.ServiceID()
	case "upstream_id":
		return pc.UpstreamID()
	case "upstream_host":
		return pc.UpstreamHost()
	case "endpoint_addr":
		return pc.EndpointAddress()
	}

	if strings.HasPrefix(variable, "arg_") {
		return pc.RequestQuery(strings.TrimPrefix(variable, "arg_"))
	}
	if strings.HasPrefix(variable, "http_") {
		headerName := strings.ReplaceAll(strings.TrimPrefix(variable, "http_"), "_", "-")
		return pc.RequestHeader(headerName)
	}

	return ""
}

func isVariableChar(ch byte) bool {
	return ch == '_' ||
		(ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9')
}

func isNumeric(value string) bool {
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return value != ""
}

func atoi(value string) int {
	result := 0
	for _, ch := range value {
		result = result*10 + int(ch-'0')
	}
	return result
}
