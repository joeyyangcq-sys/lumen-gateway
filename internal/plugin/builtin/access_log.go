package builtin

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joey/lumen-gateway/internal/plugin"
)

const (
	defaultAccessLogFormat      = `$remote_addr - [$time_local] "$request_method $request_uri" $status $body_bytes_sent $request_time`
	defaultAccessLogBufferSize  = 16384
	defaultAccessLogFlushPeriod = time.Second
)

type accessLogConfig struct {
	Path          string `yaml:"path"`
	Format        string `yaml:"format"`
	BufferSize    int    `yaml:"buffer_size"`
	FlushInterval string `yaml:"flush_interval"`
}

type accessLogWriter struct {
	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
}

func newAccessLogWriter(path string, bufferSize int) (*accessLogWriter, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	return &accessLogWriter{
		file:   file,
		writer: bufio.NewWriterSize(file, bufferSize),
	}, nil
}

func (w *accessLogWriter) writeLine(line string) {
	w.mu.Lock()
	w.writer.WriteString(line)
	w.writer.WriteByte('\n')
	w.mu.Unlock()
}

func (w *accessLogWriter) flush() {
	w.mu.Lock()
	w.writer.Flush()
	w.mu.Unlock()
}

func (w *accessLogWriter) startFlusher(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			w.flush()
		}
	}()
}

func registerAccessLog(registry *plugin.Registry) error {
	return plugin.RegisterTypedContext(registry, plugin.Metadata{
		Name:     "access_log",
		Priority: -100,
		Scopes:   plugin.AllScopes(),
	}, func(cfg accessLogConfig) (plugin.ContextHandler, error) {
		path := strings.TrimSpace(cfg.Path)
		if path == "" {
			return nil, errors.New("access_log requires path")
		}

		format := cfg.Format
		if format == "" {
			format = defaultAccessLogFormat
		}

		bufferSize := cfg.BufferSize
		if bufferSize <= 0 {
			bufferSize = defaultAccessLogBufferSize
		}

		flushInterval := defaultAccessLogFlushPeriod
		if cfg.FlushInterval != "" {
			parsed, err := time.ParseDuration(cfg.FlushInterval)
			if err != nil {
				return nil, err
			}
			if parsed > 0 {
				flushInterval = parsed
			}
		}

		w, err := newAccessLogWriter(path, bufferSize)
		if err != nil {
			return nil, err
		}
		w.startFlusher(flushInterval)

		return func(ctx context.Context, pc plugin.PluginContext) {
			pc.Next(ctx)
			line := renderRequestTemplate(pc, format)
			w.writeLine(line)
		}, nil
	})
}
