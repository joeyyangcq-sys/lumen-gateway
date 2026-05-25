package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/joey/lumen-gateway/internal/config"
)

func TestConfigureWithWriterAppliesJSONDebugLogging(t *testing.T) {
	var buf bytes.Buffer
	if err := ConfigureWithWriter(config.LoggingOptions{
		Level:  "debug",
		Format: "json",
	}, &buf); err != nil {
		t.Fatalf("ConfigureWithWriter() error = %v", err)
	}

	slog.Debug("debug enabled", "route_id", "users")

	out := buf.String()
	if !strings.Contains(out, `"level":"DEBUG"`) {
		t.Fatalf("log output missing debug level: %q", out)
	}
	if !strings.Contains(out, `"route_id":"users"`) {
		t.Fatalf("log output missing structured field: %q", out)
	}
}

func TestParseRejectsUnsupportedOptions(t *testing.T) {
	if _, err := ParseLevel("trace"); err == nil {
		t.Fatal("ParseLevel() error = nil, want unsupported level")
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Fatal("ParseFormat() error = nil, want unsupported format")
	}
}

func TestConfigureAndErrors(t *testing.T) {
	// 1. 测试 Configure 接口
	err := Configure(config.LoggingOptions{
		Level:  "info",
		Format: "text",
	})
	if err != nil {
		t.Errorf("Configure failed: %v", err)
	}

	// 2. 测试 writer == nil 的情况
	err = ConfigureWithWriter(config.LoggingOptions{
		Level:  "info",
		Format: "text",
	}, nil)
	if err != nil {
		t.Errorf("ConfigureWithWriter with nil writer failed: %v", err)
	}

	// 3. 测试 UnsupportedOptionError.Error() 方法
	errOption := &UnsupportedOptionError{
		Field: "logging.level",
		Value: "trace",
	}
	expectedMsg := `logging.level "trace" is not supported`
	if errOption.Error() != expectedMsg {
		t.Errorf("expected error message %q, got %q", expectedMsg, errOption.Error())
	}

	// 4. 测试 ParseLevel 所有的有效级别
	levels := []string{"info", "", "debug", "warn", "warning", "error"}
	for _, l := range levels {
		_, err := ParseLevel(l)
		if err != nil {
			t.Errorf("ParseLevel(%q) failed: %v", l, err)
		}
	}

	// 5. 测试 ParseFormat 所有的有效格式
	formats := []string{"text", "", "json"}
	for _, f := range formats {
		_, err := ParseFormat(f)
		if err != nil {
			t.Errorf("ParseFormat(%q) failed: %v", f, err)
		}
	}
}

