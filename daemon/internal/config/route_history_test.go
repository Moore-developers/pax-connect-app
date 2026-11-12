package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func historySample(route, metric string, at time.Time, value float64) HistorySample {
	return HistorySample{RouteID: route, Metric: metric, At: at, ConfigRevision: 1, Value: value, Success: true}
}

func countMetric(h RouteHistory, route, metric string) int {
	n := 0
	for _, s := range h.Items {
		if s.RouteID == route && s.Metric == metric {
			n++
		}
	}
	return n
}

func TestHistoryLimitPerMetric(t *testing.T) {
	for metric, want := range map[string]int{"latency_auto": 120, "latency": 50, "connect": 50, "exit": 50, "download": 50} {
		if got := HistoryLimit(metric); got != want {
			t.Fatalf("%s limit = %d, want %d", metric, got, want)
		}
	}
}

func TestAutoLatencyKeeps120AndEvictsOldest(t *testing.T) {
	now := time.Now()
	h := NewRouteHistory()
	for i := 0; i < 121; i++ {
		h.Append(historySample("r", "latency_auto", now.Add(time.Duration(i-200)*time.Minute), float64(i+1)), now)
	}
	if got := countMetric(h, "r", "latency_auto"); got != 120 {
		t.Fatalf("kept %d, want 120", got)
	}
	oldest := h.Items[len(h.Items)-1]
	if oldest.Value != 2 {
		t.Fatalf("oldest kept value = %v, want 2 (sample 1 evicted)", oldest.Value)
	}
}

func TestAutoAndManualLatencyDoNotCrowdEachOther(t *testing.T) {
	now := time.Now()
	h := NewRouteHistory()
	for i := 0; i < 130; i++ {
		at := now.Add(time.Duration(i-200) * time.Minute)
		h.Append(historySample("r", "latency_auto", at, 1), now)
		h.Append(historySample("r", "latency", at, 2), now)
	}
	if auto, manual := countMetric(h, "r", "latency_auto"), countMetric(h, "r", "latency"); auto != 120 || manual != 50 {
		t.Fatalf("auto=%d manual=%d, want 120 and 50", auto, manual)
	}
	// Another route has its own budget.
	h.Append(historySample("other", "latency_auto", now.Add(-time.Minute), 1), now)
	if got := countMetric(h, "other", "latency_auto"); got != 1 {
		t.Fatalf("other route auto = %d", got)
	}
}

func TestAutoLatencyExpiresAfterSevenDays(t *testing.T) {
	now := time.Now()
	h := NewRouteHistory()
	h.Append(historySample("r", "latency_auto", now.Add(-7*24*time.Hour-time.Minute), 1), now)
	h.Append(historySample("r", "latency_auto", now.Add(-time.Hour), 2), now)
	if got := countMetric(h, "r", "latency_auto"); got != 1 {
		t.Fatalf("kept %d, want only the recent sample", got)
	}
}

func TestRouteHistoryStoreRoundTripsAutoLatency(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewRouteHistoryStore(filepath.Join(dir, "route-history.json"), uint32(os.Getuid()))
	now := time.Now()
	h := NewRouteHistory()
	h.Append(historySample("r", "latency_auto", now.Add(-time.Minute), 120), now)
	h.Append(historySample("r", "latency", now.Add(-time.Minute), 80), now)
	if err = store.Save(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if countMetric(loaded, "r", "latency_auto") != 1 || countMetric(loaded, "r", "latency") != 1 {
		t.Fatalf("reloaded %+v", loaded.Items)
	}
}

func TestRouteHistoryStoreStillRejectsUnknownMetric(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewRouteHistoryStore(filepath.Join(dir, "route-history.json"), uint32(os.Getuid()))
	now := time.Now()
	h := NewRouteHistory()
	h.Items = append(h.Items, historySample("r", "latency_other", now.Add(-time.Minute), 1))
	if err = store.Save(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(); err == nil || err.Error() != "history_corrupt" {
		t.Fatalf("err = %v, want history_corrupt", err)
	}
}

func TestRouteHistoryRevisionMovesOncePerChange(t *testing.T) {
	now := time.Now()
	h := NewRouteHistory()
	start := h.Revision
	h.Append(historySample("r", "latency_auto", now.Add(-time.Minute), 1), now)
	if h.Revision != start+1 {
		t.Fatalf("a new point moves the revision once: %d -> %d", start, h.Revision)
	}
	// An append that also trims the oldest point is still one change.
	for i := 0; i < 130; i++ {
		h.Append(historySample("r", "latency_auto", now.Add(time.Duration(i-200)*time.Minute), 1), now)
	}
	before := h.Revision
	h.Append(historySample("r", "latency_auto", now, 2), now)
	if h.Revision != before+1 {
		t.Fatalf("append with trim: %d -> %d", before, h.Revision)
	}
	// Reads of an unchanged history never move it.
	h.Prune(now)
	if h.Revision != before+1 {
		t.Fatalf("idle prune moved the revision to %d", h.Revision)
	}
}

func TestRouteHistoryPruneByAgeMovesRevisionAndSurvivesReload(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewRouteHistoryStore(filepath.Join(dir, "route-history.json"), uint32(os.Getuid()))
	now := time.Now()
	h := NewRouteHistory()
	h.Append(historySample("r", "latency_auto", now.Add(-time.Hour), 1), now)
	before := h.Revision
	h.Prune(now.Add(8 * 24 * time.Hour))
	if len(h.Items) != 0 || h.Revision != before+1 {
		t.Fatalf("expired item pruned without moving the revision: items=%d revision %d -> %d", len(h.Items), before, h.Revision)
	}
	if err = store.Save(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Revision < h.Revision {
		t.Fatalf("revision went back across a reload: %d < %d (%v)", loaded.Revision, h.Revision, err)
	}
}
