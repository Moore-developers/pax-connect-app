package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

func TestIngestRunWaitsFivePollsAndExitsOnCancel(t *testing.T) {
	h := newIngestHarness(t)
	h.core.add(testEpoch, "started", flow("a"), 0, 0)
	var waits []time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	h.in.wait = func(ctx context.Context, d time.Duration) bool {
		waits = append(waits, d)
		if len(waits) == 3 {
			cancel()
		}
		return ctx.Err() == nil
	}
	h.in.done = make(chan struct{})
	go h.in.run(ctx)
	select {
	case <-h.in.done:
	case <-time.After(5 * time.Second):
		t.Fatal("ingest goroutine did not exit after cancel")
	}
	// Seed baseline drains immediately (0), then the loop idles at the 5 s cadence.
	want := []time.Duration{0, ingestPollInterval, ingestPollInterval}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits %v want %v", waits, want)
		}
	}
}

type pagerSession struct{ *fakeCore }

func (pagerSession) Apply(context.Context, config.RouteConfig) error { return nil }
func (pagerSession) Clear(context.Context) error                     { return nil }
func (pagerSession) Shutdown(context.Context) error                  { return nil }

func TestConfigureHistoryIngestCreatesJournalAndStopsCleanly(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	core := newFakeCore("S1")
	core.add(testEpoch, "started", flow("a"), 0, 0)
	c := New("test", "test")
	if err = c.ConfigureHistoryIngest(context.Background(), dir, uint32(os.Geteuid())); err == nil {
		t.Fatal("ingest must refuse to start without a core session")
	}
	c.ConfigureCoreSession(pagerSession{core})
	if err = c.ConfigureHistoryIngest(context.Background(), dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "history", "journal")); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("journal directory: %v %v", info, err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = c.CloseHistoryIngest(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestRetiredSourceDoesNotDelayCaptureStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := newLogsTestDir(t)
	old := filepath.Join(dir, "observations")
	if err := os.Mkdir(old, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "history.db"), []byte("retired invalid source"), 0600); err != nil {
		t.Fatal(err)
	}
	c := New("test", "test")
	c.ConfigureCoreSession(pagerSession{newFakeCore("capture")})
	began := time.Now()
	if err := c.ConfigureHistoryIngest(ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseHistoryIngest(context.Background()) }()
	if time.Since(began) > time.Second {
		t.Fatal("retired source delayed capture")
	}
}
