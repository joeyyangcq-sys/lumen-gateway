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
