package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func TestRecoveredLibrariesPublishPartialAndOperationalCode(t *testing.T) {
	for _, name := range []string{"logs", "traffic"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			dir := newLogsTestDir(t)
			history := filepath.Join(dir, "history")
			if err := os.Mkdir(history, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(history, name+".db"), []byte("corrupt fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			c := New("test", "test")
			var err error
			if name == "logs" {
				err = c.ConfigureHistoryLogs(ctx, dir, uint32(os.Geteuid()))
				t.Cleanup(func() { _ = c.CloseHistoryLogs(ctx) })
			} else {
				err = c.ConfigureHistoryTraffic(ctx, dir, uint32(os.Geteuid()))
				t.Cleanup(func() { _ = c.CloseHistoryTraffic(ctx) })
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "logs" {
				l, _ := c.logJob()
				meta, err := l.logs.SnapshotMeta(ctx)
				if err != nil || !meta.Partial {
					t.Fatalf("lost recovery: %+v %v", meta, err)
				}
			} else {
				job := c.trafficJob()
				partial, _, err := job.store.Meta(ctx, historystore.MetaPartial)
				if err != nil || partial != "1" {
					t.Fatalf("lost recovery: %q %v", partial, err)
				}
			}
		})
	}
}

func TestLegacyRouteUsageBucketsRemainGapsWithBytes(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	at := SetHistoryFixtureClock(c).Add(-time.Hour).Truncate(5 * time.Minute)
	err := c.trafficJob().store.Update(ctx, func(tx *historystore.TrafficTx) error {
		return tx.PutTraffic5m(ctx, historystore.TrafficRow{TrafficKey: historystore.TrafficKey{BucketMs: at.UnixMilli(), Dimension: "route", Object: "old", Route: "old", Direction: "proxy"}, SourceKind: "legacy", UpBytes: 10, DownBytes: 90})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"", "today", "week", "month"} {
		out, err := c.QueryRouteUsage(ctx, &pb.RouteHistoryRequest{RouteId: "old", Window: window})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range out.Items {
			if row.Upload > 0 {
				found = true
				if !row.Gap {
					t.Fatalf("%s legacy bytes claimed coverage: %v", window, row)
				}
			}
		}
		if !found || out.Upload != 10 || out.Download != 90 {
			t.Fatalf("%s legacy data missing: %v", window, out)
		}
	}
}

func TestRecentConnectionScanKeepsNewestDomainsAtLimit(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	store, _, err := historystore.OpenLogs(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	err = store.Update(ctx, func(tx *historystore.LogsTx) error {
		for i := 0; i < 40; i++ {
			f := config.FlowFact{Key: fmt.Sprint(i), Domain: fmt.Sprintf("host-%02d.test", i)}
			body, _ := json.Marshal(config.LogConnectionSummary{Fact: f})
			if err := tx.PutConnection(ctx, historystore.LogConnection{FlowKey: f.Key, StartedMs: 1, LastActivityMs: int64(i + 1), Body: body}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var domains []string
	err = store.EachRecentConnection(ctx, 0, 100, 32, func(row historystore.LogConnection) error {
		var summary config.LogConnectionSummary
		if err := json.Unmarshal(row.Body, &summary); err != nil {
			return err
		}
		domains = append(domains, summary.Fact.Domain)
		return nil
	})
	if err != nil || len(domains) != 32 || domains[0] != "host-39.test" || domains[31] != "host-08.test" {
		t.Fatal(domains, err)
	}
}

func TestLogsStartupFailuresAreDegraded(t *testing.T) {
	h := newLogsHarness(t)
	ctx := context.Background()
	if err := h.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.SetMeta(ctx, historystore.MetaRetentionHours, "72") }); err != nil {
		t.Fatal(err)
	}
	store := &flakyLogs{Logs: h.logs, fail: 1}
	l, err := newHistoryLogs(ctx, store, h.journal, h.consumers)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.initialize(ctx); err == nil {
		t.Fatal("normalization failure swallowed")
	}
	l.fail(ctx, "history_logs_startup_failed")
	meta, err := h.logs.SnapshotMeta(ctx)
	if err != nil || !meta.Partial || meta.ErrorCode != logsErrorStore {
		t.Fatalf("startup not degraded: %+v %v", meta, err)
	}
}

func TestJournalReaderReportsDamageWithoutQuarantining(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	uid := uint32(os.Geteuid())
	j, err := historystore.OpenJournal(dir, uid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	at := time.Now().UTC().Truncate(time.Hour)
	path := filepath.Join(dir, "journal", at.Format("2006010215")+".db")
	if err = os.WriteFile(path, []byte("corrupt fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	consumers := newJournalConsumers()
	for range 2 {
		reader, err := historystore.OpenJournal(dir, uid)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := reader.ReadAfter(ctx, historystore.Cursor{}, 10)
		consumers.reportDamage(err)
		_ = reader.Close()
		if err == nil || len(rows) != 0 {
			t.Fatal("damaged file skipped", err)
		}
		if _, err = os.Stat(path); err != nil {
			t.Fatal("reader mutated damaged source", err)
		}
	}
	in := newHistoryIngest(j, nil)
	in.consumers = consumers
	in.started = true
	in.cursor = historystore.Cursor{Session: "s", Seq: 1}
	in.attemptStarted = time.Now()
	in.repairJournal(ctx)
	losses, e := j.PendingLosses(ctx)
	if e != nil || len(losses) != 1 {
		t.Fatalf("duplicate or missing durable loss: %d %v", len(losses), e)
	}
	if e = in.flushJournalLosses(ctx); e != nil {
		t.Fatal(e)
	}
	rows, err := j.ReadAfter(ctx, historystore.Cursor{}, 10)
	if err != nil || len(rows) != 1 || rows[0].Kind != journalGapKind {
		t.Fatalf("gap not persisted: %v %v", rows, err)
	}
}

func TestMinuteTrafficQueryHasTwelvePointsFiveSecondsApart(t *testing.T) {
	c := New("test", "test")
	live := newLiveState()
	c.liveView.Store(live)
	now := time.Now().UTC().Truncate(5 * time.Second)
	f := config.FlowFact{Key: "minute", StartedAt: now.Add(-time.Minute), ApplicationID: "fixture.app", Application: "Fixture", Direction: "direct", RouteID: "direct"}
	live.apply(config.ObservationBatch{Session: "s", Collecting: true, Records: []config.Observation{{Session: "s", Sequence: 1, Kind: "sample", At: now.Add(-5 * time.Second), Flow: &f, Upload: 10}}}, now)
	out, err := c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "application", Window: "minute"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Trend) != 12 {
		t.Fatalf("minute points=%d", len(out.Trend))
	}
	for i, p := range out.Trend {
		if p.Seconds != 5 {
			t.Fatalf("point seconds=%d", p.Seconds)
		}
		if i > 0 && p.At.AsTime().Sub(out.Trend[i-1].At.AsTime()) != 5*time.Second {
			t.Fatal("minute points not spaced 5 seconds")
		}
	}
}

func TestPendingSnapshotWaitingForCacheHonorsDeadline(t *testing.T) {
	h := newTrafficHarness(t)
	h.tr.pendingMu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			h.tr.pendingMu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err := h.tr.resolvePending(ctx, []pendingEntry{{up: 1}})
	if err != context.DeadlineExceeded || time.Since(began) > 100*time.Millisecond {
		t.Fatalf("cache lock ignored cancellation: %v elapsed=%s", err, time.Since(began))
	}
	h.tr.pendingMu.Unlock()
	unlocked = true
}

func TestLogsStartupRetriesNormalizationBeforePublishingSupportedRetention(t *testing.T) {
	h := newLogsHarness(t)
	ctx := context.Background()
	if err := h.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.SetMeta(ctx, historystore.MetaRetentionHours, "72") }); err != nil {
		t.Fatal(err)
	}
	store := &flakyLogs{Logs: h.logs, fail: 1}
	l, err := newHistoryLogs(ctx, store, h.journal, h.consumers)
	if err != nil {
		t.Fatal(err)
	}
	l.start(ctx)
	defer func() { _ = l.stop(context.Background()) }()
	// A wake makes the deterministic retry immediate after the failure, without waiting a backoff second.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		meta, err := h.logs.SnapshotMeta(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if meta.Partial && meta.RetentionHours == logsRetentionHours {
			return
		}
		l.poke()
		time.Sleep(time.Millisecond)
	}
	meta, _ := h.logs.SnapshotMeta(ctx)
	t.Fatalf("normalization never recovered: %+v", meta)
}

func TestRoutingApplicationDomainsKeepLatestObservedThirtyTwo(t *testing.T) {
	c, dir := newTrafficRPCFixture(t)
	ctx := context.Background()
	root := filepath.Join(dir, "Applications")
	bundle := filepath.Join(root, "Fixture.app")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0700); err != nil {
		t.Fatal(err)
	}
	plist := []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>fixture.latest</string><key>CFBundleName</key><string>Fixture</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`)
	if err := os.WriteFile(filepath.Join(bundle, "Contents/Info.plist"), plist, 0600); err != nil {
		t.Fatal(err)
	}
	job, _ := c.logJob()
	now := time.Now()
	if err := job.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		for i := 0; i < 40; i++ {
			f := config.FlowFact{Key: fmt.Sprintf("latest/%02d", i), StartedAt: now.Add(-time.Hour), ApplicationID: "fixture.latest", Application: "Fixture", Process: filepath.Join(bundle, "Contents/MacOS/pax-identity"), Domain: fmt.Sprintf("host-%02d.example.test", i)}
			body, _ := json.Marshal(config.LogConnectionSummary{Fact: f})
			if err := tx.PutConnection(ctx, historystore.LogConnection{FlowKey: f.Key, StartedMs: f.StartedAt.UnixMilli(), LastActivityMs: now.Add(-time.Duration(40-i) * time.Second).UnixMilli(), Body: body}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	apps := c.installedRoutingApplications(ctx, []string{root})
	if len(apps) != 1 || len(apps[0].Domains) != 32 {
		t.Fatalf("domains truncated incorrectly: %+v", apps)
	}
	for _, domain := range apps[0].Domains {
		var i int
		if _, err := fmt.Sscanf(domain, "host-%02d.example.test", &i); err != nil || i < 8 {
			t.Fatalf("old domain survived latest-first budget: %q", domain)
		}
	}
}
