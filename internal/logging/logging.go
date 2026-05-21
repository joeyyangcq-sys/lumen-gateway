package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/joey/lumen-gateway/internal/config"
)

func Configure(options config.LoggingOptions) error {
	return ConfigureWithWriter(options, os.Stderr)
}

func ConfigureWithWriter(options config.LoggingOptions, writer io.Writer) error {
	level, err := ParseLevel(options.Level)
	if err != nil {
		return err
	}
	format, err := ParseFormat(options.Format)
	if err != nil {
		return err
	}
	if writer == nil {
		writer = io.Discard
	}

	handlerOptions := &slog.HandlerOptions{Level: level}
	switch format {
	case "json":
		slog.SetDefault(slog.New(slog.NewJSONHandler(writer, handlerOptions)))
	default:
		slog.SetDefault(slog.New(slog.NewTextHandler(writer, handlerOptions)))
	}
	return nil
}

func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, &UnsupportedOptionError{Field: "logging.level", Value: value}
	}
}

func ParseFormat(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "text":
		return "text", nil
	case "json":
		return "json", nil
	default:
		return "", &UnsupportedOptionError{Field: "logging.format", Value: value}
	}
}

type UnsupportedOptionError struct {
	Field string
	Value string
}

func (e *UnsupportedOptionError) Error() string {
	return fmt.Sprintf("%s %q is not supported", e.Field, e.Value)
}
