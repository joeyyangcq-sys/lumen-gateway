package builtin

import (
	"bufio"
	"context"
	"errors"
	"io"
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
	mu       sync.Mutex
	file     *os.File
	writer   *bufio.Writer
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
	closed   bool
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
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}, nil
}

func (w *accessLogWriter) writeLine(line string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	if _, err := w.writer.WriteString(line); err != nil {
		return err
	}
	return w.writer.WriteByte('\n')
}

func (w *accessLogWriter) flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	return w.writer.Flush()
}

func (w *accessLogWriter) startFlusher(interval time.Duration) {
	go func() {
		defer close(w.doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = w.flush()
			case <-w.stopCh:
				return
			}
		}
	}()
}

func (w *accessLogWriter) Close() error {
	w.stopOnce.Do(func() {
		close(w.stopCh)
		<-w.doneCh
	})

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true

	var err error
	if flushErr := w.writer.Flush(); flushErr != nil {
		err = flushErr
	}
	if closeErr := w.file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func registerAccessLog(registry *plugin.Registry) error {
	return plugin.RegisterTypedContextWithCloser(registry, plugin.Metadata{
		Name:     "access_log",
		Priority: -100,
		Scopes:   plugin.AllScopes(),
	}, func(cfg accessLogConfig) (plugin.ContextHandler, io.Closer, error) {
		path := strings.TrimSpace(cfg.Path)
		if path == "" {
			return nil, nil, errors.New("access_log requires path")
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
				return nil, nil, err
			}
			if parsed > 0 {
				flushInterval = parsed
			}
		}

		w, err := newAccessLogWriter(path, bufferSize)
		if err != nil {
			return nil, nil, err
		}
		w.startFlusher(flushInterval)

		return func(ctx context.Context, pc plugin.PluginContext) {
			pc.Next(ctx)
			line := renderRequestTemplate(pc, format)
			_ = w.writeLine(line)
		}, w, nil
	})
}
