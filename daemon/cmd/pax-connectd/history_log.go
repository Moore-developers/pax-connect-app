package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/errorlog"
	"time"
)

func operationalLogger(level string) (*slog.Logger, func()) {
	logger := newLogger(level)
	started := time.Now()
	home, err := os.UserHomeDir()
	if err == nil {
		var sink *errorlog.HistoryErrorLog
		sink, err = errorlog.OpenHistoryErrorLog(filepath.Join(home, "Library", "Logs", "Pax Connect", "paxd.log"))
		if err == nil {
			return slog.New(errorlog.WithHistoryErrorLog(logger.Handler(), sink)), func() { _ = sink.Close() }
		}
	}
	logger.Warn("history error log unavailable", "library", "history", "code", "history_error_log_unavailable", "elapsed_ms", time.Since(started).Milliseconds(), "operation_id", "startup", "snapshot_version", 0)
	return logger, func() {}
}
