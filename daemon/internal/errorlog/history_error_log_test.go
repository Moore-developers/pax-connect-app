package errorlog

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryErrorLogExcludesProductEvidenceAndRotatesAtOneMiB(t *testing.T) {
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "logs", "paxd.log")
	sink, err := OpenHistoryErrorLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close() }()
	logger := slog.New(WithHistoryErrorLog(slog.NewTextHandler(io.Discard, nil), sink))
	logger.WarnContext(context.Background(), "history traffic degraded", "library", "traffic", "reason", "history_traffic_write_failed", "domain", "private.example", "process", "/Users/private/app", "elapsed_ms", int64(12))
	for range 15000 {
		logger.Warn("history logs degraded", "reason", "history_logs_write_failed", "library", "logs", "elapsed_ms", int64(1))
	}
	for _, name := range []string{path, path + ".1"} {
		data, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if len(data) > historyErrorLogLimit {
			t.Fatalf("rotation exceeded limit: %d", len(data))
		}
		if strings.Contains(string(data), "private") || !strings.Contains(string(data), "library=") {
			t.Fatal("unsafe or missing operational fields")
		}
	}
	if _, e := os.Stat(path + ".2"); !os.IsNotExist(e) {
		t.Fatal("retained more than two files")
	}
}

func TestHistoryErrorLogResumesAfterRotationFailure(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "paxd.log")
	sink, e := OpenHistoryErrorLog(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = sink.Close() }()
	sink.size = historyErrorLogLimit
	if e = os.Mkdir(path+".1", 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(path+".1", "block"), []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = sink.write("history_store_failed", "traffic", 1); e == nil {
		t.Fatal("rotation unexpectedly succeeded")
	}
	if e = os.Remove(filepath.Join(path+".1", "block")); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path + ".1"); e != nil {
		t.Fatal(e)
	}
	if e = sink.write("history_store_failed", "traffic", 2); e != nil {
		t.Fatal("failed rotation permanently disabled logging", e)
	}
	data, e := os.ReadFile(path)
	if e != nil || !strings.Contains(string(data), "elapsed_ms=2") {
		t.Fatal(string(data), e)
	}
}

func TestHistoryErrorLogUsesExplicitFieldsIndependentOfWording(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "logs", "paxd.log")
	sink, err := OpenHistoryErrorLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close() }()
	logger := slog.New(WithHistoryErrorLog(slog.NewTextHandler(io.Discard, nil), sink))
	for _, message := range []string{"history traffic failed", "renamed operation"} {
		logger.Warn(message, "library", "logs", "code", "history_logs_write_failed", "elapsed_ms", int64(17))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "library=logs elapsed_ms=17") != 2 {
		t.Fatalf("wording changed routing: %s", data)
	}
}

func TestHistoryErrorLogRetainsBoundLibraryAfterMessageChange(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "logs", "paxd.log")
	sink, err := OpenHistoryErrorLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close() }()
	logger := slog.New(WithHistoryErrorLog(slog.NewTextHandler(io.Discard, nil), sink)).With("library", "journal")
	logger.Warn("new wording", "code", "history_journal_rebuilt", "elapsed_ms", int64(9))
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "library=journal elapsed_ms=9") {
		t.Fatal(string(data), err)
	}
}
