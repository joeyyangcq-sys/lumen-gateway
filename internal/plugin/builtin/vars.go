package builtin

import (
	"fmt"
	"net"
	"strings"
	"time"

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
	case "endpoint_addr", "upstream_addr":
		return pc.EndpointAddress()
	case "request_method":
		return pc.RequestMethod()
	case "status":
		if s := pc.ResponseStatus(); s > 0 {
			return fmt.Sprintf("%d", s)
		}
		return ""
	case "body_bytes_sent":
		return fmt.Sprintf("%d", len(pc.ResponseBody()))
	case "request_length":
		return fmt.Sprintf("%d", len(pc.RequestBody()))
	case "request_time":
		if t := pc.ProxyInfo().TotalTime; t > 0 {
			return fmt.Sprintf("%.3f", t.Seconds())
		}
		return ""
	case "upstream_status":
		if s := pc.UpstreamStatusCode(); s > 0 {
			return fmt.Sprintf("%d", s)
		}
		return ""
	case "upstream_response_time":
		if t := pc.ProxyInfo().TotalTime; t > 0 {
			return fmt.Sprintf("%.3f", t.Seconds())
		}
		return ""
	case "time_local":
		return time.Now().Format("02/Jan/2006:15:04:05 -0700")
	case "server_port":
		host := pc.RequestHost()
		if _, port, err := net.SplitHostPort(host); err == nil {
			return port
		}
		return ""
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
