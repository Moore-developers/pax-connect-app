package errorlog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/storage"
	"strings"
	"sync"
	"syscall"
	"time"
)

const historyErrorLogLimit = 1 << 20

// HistoryErrorLog is a bounded operational error sink. It stores only controlled codes,
// library names and duration; product history and diagnostic text never enter this file.
type HistoryErrorLog struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

// OpenHistoryErrorLog creates a private two-file rotation at path.
func OpenHistoryErrorLog(path string) (*HistoryErrorLog, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return nil, err
	}
	if err := storage.PreparePrivateDirectory(dir, uint32(os.Geteuid())); err != nil {
		return nil, err
	}

	l := &HistoryErrorLog{path: path}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}
func (l *HistoryErrorLog) open() error {
	fd, err := syscall.Open(l.path, syscall.O_CREAT|syscall.O_APPEND|syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), l.path)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		_ = f.Close()
		return errors.New("history error log permissions invalid")
	}
	l.file, l.size = f, info.Size()
	return nil
}

// Close releases the file handle.
func (l *HistoryErrorLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *HistoryErrorLog) write(code, library string, elapsed int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := fmt.Sprintf("%s code=%s library=%s elapsed_ms=%d\n", time.Now().UTC().Format(time.RFC3339), code, library, elapsed)
	if l.file == nil {
		if err := l.open(); err != nil {
			return err
		}
	}
	if l.size+int64(len(line)) > historyErrorLogLimit {
		if err := os.Remove(l.path + ".1"); err != nil && !os.IsNotExist(err) {
			return err
		}
		// Keep the old descriptor writable until the rename succeeds; a failed rename
		// leaves it available for the next attempt.
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			return err
		}
		if err := l.file.Close(); err != nil {
			l.file = nil
			return err
		}
		l.file = nil
		if err := l.open(); err != nil {
			return err
		}
	}

	n, err := io.WriteString(l.file, line)
	l.size += int64(n)
	return err
}

type historyLogHandler struct {
	next    slog.Handler
	sink    *HistoryErrorLog
	attrs   []slog.Attr
	grouped bool
}

// WithHistoryErrorLog duplicates storage errors to the private sink while keeping the
// original handler (stderr). Attributes outside the explicit allowlist are discarded.
func WithHistoryErrorLog(next slog.Handler, sink *HistoryErrorLog) slog.Handler {
	return &historyLogHandler{next: next, sink: sink}
}
func (h *historyLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h *historyLogHandler) Handle(ctx context.Context, r slog.Record) error {
	code, library := "", ""
	elapsed := int64(-1)
	collect := func(a slog.Attr) bool {
		switch a.Key {
		case "library":
			library = a.Value.String()
		case "code", "reason":
			if safeHistoryCode(a.Value.String()) {
				code = a.Value.String()
			}
		case "elapsed_ms":
			if a.Value.Kind() == slog.KindInt64 {
				elapsed = a.Value.Int64()
			}
		}
		return true
	}
	for _, a := range h.attrs {
		collect(a)
	}
	if !h.grouped {
		r.Attrs(collect)
	}
	if r.Level >= slog.LevelWarn && code != "" && (library == "traffic" || library == "logs" || library == "journal" || library == "history") && elapsed >= 0 {
		if err := h.sink.write(code, library, elapsed); err != nil {
			return errors.Join(err, h.next.Handle(ctx, r))
		}
	}
	return h.next.Handle(ctx, r)
}
func (h *historyLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := append([]slog.Attr(nil), h.attrs...)
	if !h.grouped {
		bound = append(bound, attrs...)
	}
	return &historyLogHandler{next: h.next.WithAttrs(attrs), sink: h.sink, attrs: bound, grouped: h.grouped}
}
func (h *historyLogHandler) WithGroup(name string) slog.Handler {
	return &historyLogHandler{next: h.next.WithGroup(name), sink: h.sink, attrs: h.attrs, grouped: h.grouped || name != ""}
}
func safeHistoryCode(s string) bool {
	if !strings.HasPrefix(s, "history_") || len(s) > 96 {
		return false
	}
	for _, r := range s {
		if r != '_' && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}
