package builtin

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/joey/lumen-gateway/internal/runtimectx"
)

func regexCapturesFromContext(c *app.RequestContext) []string {
	value, ok := c.Get(runtimectx.RegexCapturesKey)
	if !ok {
		return nil
	}
	captures, ok := value.([]string)
	if !ok {
		return nil
	}
	return captures
}

func requestIDFromContext(c *app.RequestContext) string {
	value, ok := c.Get(runtimectx.RequestIDKey)
	if !ok {
		return ""
	}
	id, ok := value.(string)
	if !ok {
		return ""
	}
	return id
}

func renderRequestTemplate(c *app.RequestContext, value string) string {
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
			builder.WriteString(resolveRequestVariable(c, value[index+1:end]))
			index = end - 1
		case isVariableChar(next):
			end := index + 2
			for end < len(value) && isVariableChar(value[end]) {
				end++
			}
			builder.WriteString(resolveRequestVariable(c, value[index+1:end]))
			index = end - 1
		default:
			builder.WriteByte(value[index])
		}
	}

	return builder.String()
}

func resolveRequestVariable(c *app.RequestContext, variable string) string {
	if variable == "" {
		return ""
	}

	if index, err := strconv.Atoi(variable); err == nil {
		captures := regexCapturesFromContext(c)
		if index >= 1 && index <= len(captures) {
			return captures[index-1]
		}
		return ""
	}

	switch variable {
	case "host":
		return string(c.Host())
	case "uri":
		return string(c.Path())
	case "request_uri":
		query := string(c.Request.URI().QueryArgs().QueryString())
		if query == "" {
			return string(c.Path())
		}
		return fmt.Sprintf("%s?%s", c.Path(), query)
	case "remote_addr":
		if ip := c.ClientIP(); ip != "" {
			return ip
		}
		if addr := c.RemoteAddr(); addr != nil {
			host, _, err := net.SplitHostPort(addr.String())
			if err == nil {
				return host
			}
			return addr.String()
		}
		return ""
	case "request_id":
		return requestIDFromContext(c)
	}

	if strings.HasPrefix(variable, "arg_") {
		return string(c.Query(strings.TrimPrefix(variable, "arg_")))
	}
	if strings.HasPrefix(variable, "http_") {
		headerName := strings.ReplaceAll(strings.TrimPrefix(variable, "http_"), "_", "-")
		return c.Request.Header.Get(headerName)
	}

	return ""
}

func isVariableChar(ch byte) bool {
	return ch == '_' ||
		(ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9')
}
