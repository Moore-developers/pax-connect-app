package controller_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
)

type recencyFixture struct {
	t   *testing.T
	c   *controller.Controller
	seq uint64
	now time.Time
}

func newRecencyFixture(t *testing.T) *recencyFixture {
	t.Helper()
	ctx := context.Background()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	c := controller.New("test", "test")
	if e = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := controller.CloseHistoryFixture(c, ctx); e != nil {
			t.Error(e)
		}
	})
	return &recencyFixture{t: t, c: c, now: time.Now()}
}

// pinCalendar fixes the "now" of calendar windows (today/week/month) to a fixed local noon,
// so seeded traffic stays inside the window at any wall-clock time. Rolling windows
// (hour/day) keep the real clock and need no pin.
func (f *recencyFixture) pinCalendar() *recencyFixture {
	f.now = controller.SetHistoryFixtureClock(f.c)
	return f
}

// emit records one started flow with bytes, `ago` before now.
func (f *recencyFixture) emit(fact config.FlowFact, ago time.Duration, up, down uint64) {
	f.t.Helper()
	at := f.now.Add(-ago)
	fact.StartedAt = at
	for _, kind := range []string{"started", "sample"} {
		f.seq++
		u, d := uint64(0), uint64(0)
		if kind == "sample" {
			u, d = up, down
		}
		b := config.ObservationBatch{Session: "recency", Collecting: true, Records: []config.Observation{{Session: "recency", Sequence: f.seq, At: at, Kind: kind, Flow: &fact, Upload: u, Download: d}}}
		if e := controller.FeedHistoryFixture(f.t, f.c, context.Background(), b); e != nil {
			f.t.Fatal(e)
		}
	}
}

func appFlow(id, direction string) config.FlowFact {
	f := config.FlowFact{Key: "recency/" + id + direction, Domain: id + ".example.com", Direction: direction, ApplicationID: "fixture." + id, Application: id}
	if direction == "proxy" {
		f.RouteID, f.RouteName = "pax-node-1", "线路"
	}
	return f
}

// Rows are newest-first by last activity; bytes only break ties. Two pages of the
// frozen read keep that order, and last_active_at is the 5 minute bucket end.
func TestAggregateTrafficOrderedByLastActivityAcrossPages(t *testing.T) {
	ctx := context.Background()
	f := newRecencyFixture(t).pinCalendar()
	f.emit(appFlow("big-old", "proxy"), 5*time.Hour, 1000, 9000)
	f.emit(appFlow("mid", "proxy"), 3*time.Hour, 100, 900)
	f.emit(appFlow("small-new", "proxy"), time.Hour, 1, 9)
	first, e := f.c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today", Limit: 2})
	if e != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("page1 err=%v rows=%d", e, len(first.GetItems()))
	}
	second, e := f.c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today", Limit: 2, ReadToken: first.ReadToken, Cursor: first.NextCursor})
	if e != nil || len(second.Items) != 1 {
		t.Fatalf("page2 err=%v rows=%d", e, len(second.GetItems()))
	}
	names := []string{first.Items[0].Name, first.Items[1].Name, second.Items[0].Name}
	if names[0] != "small-new" || names[1] != "mid" || names[2] != "big-old" {
		t.Fatalf("order=%v", names)
	}
	prev := first.Items[0].LastActiveAt
	if want := f.now.Add(-time.Hour).UTC().Truncate(5 * time.Minute).Add(5 * time.Minute).UnixMilli(); prev != want {
		t.Fatalf("last_active_at=%d want=%d", prev, want)
	}
	if first.Items[1].LastActiveAt >= prev || second.Items[0].LastActiveAt >= first.Items[1].LastActiveAt {
		t.Fatalf("not strictly newest first: %d %d %d", prev, first.Items[1].LastActiveAt, second.Items[0].LastActiveAt)
	}
}

// Same activity time falls back to bytes (desc), then id.
func TestAggregateTrafficTieBreaksByBytes(t *testing.T) {
	f := newRecencyFixture(t).pinCalendar()
	f.emit(appFlow("small", "proxy"), time.Hour, 1, 1)
	f.emit(appFlow("large", "proxy"), time.Hour, 50, 50)
	page, e := f.c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "application", Window: "today"})
	if e != nil || len(page.Items) != 2 || page.Items[0].Name != "large" || page.Items[0].LastActiveAt != page.Items[1].LastActiveAt {
		t.Fatalf("err=%v items=%v", e, page.GetItems())
	}
}

// The live (non-calendar) window uses transfer/start/close times and the same order.
func TestLiveTrafficOrderedByLastActivityAcrossPages(t *testing.T) {
	ctx := context.Background()
	f := newRecencyFixture(t)
	f.emit(appFlow("big-old", "proxy"), 50*time.Minute, 1000, 9000)
	f.emit(appFlow("mid", "proxy"), 30*time.Minute, 100, 900)
	f.emit(appFlow("small-new", "proxy"), 10*time.Minute, 1, 9)
	q := &pb.TrafficQuery{Dimension: "application", Window: "hour", Limit: 2}
	first, e := f.c.QueryTraffic(ctx, q)
	if e != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("page1 err=%v rows=%d", e, len(first.GetItems()))
	}
	q2 := &pb.TrafficQuery{Dimension: "application", Window: "hour", Limit: 2, ReadToken: first.ReadToken, Cursor: first.NextCursor}
	second, e := f.c.QueryTraffic(ctx, q2)
	if e != nil || len(second.Items) != 1 {
		t.Fatalf("page2 err=%v rows=%d", e, len(second.GetItems()))
	}
	if first.Items[0].Name != "small-new" || first.Items[1].Name != "mid" || second.Items[0].Name != "big-old" {
		t.Fatalf("order=%s %s %s", first.Items[0].Name, first.Items[1].Name, second.Items[0].Name)
	}
	want := f.now.Add(-10 * time.Minute).UnixMilli()
	if d := first.Items[0].LastActiveAt - want; d < -2000 || d > 2000 {
		t.Fatalf("last_active_at off by %dms", d)
	}
}

// Unknown-source traffic through both directions is one summary row, and system
// services are reported beside the application subtotal.
func TestAggregateUnknownMergesDirectionsAndReportsSystem(t *testing.T) {
	f := newRecencyFixture(t).pinCalendar()
	f.emit(appFlow("app", "proxy"), time.Hour, 10, 20)
	f.emit(config.FlowFact{Key: "recency/u-proxy", Domain: "u.example.com", Direction: "proxy", RouteID: "pax-node-1", RouteName: "线路"}, time.Hour, 100, 200)
	f.emit(config.FlowFact{Key: "recency/u-direct", Domain: "v.example.com", Direction: "direct", RouteID: "direct", RouteName: "直连"}, 2*time.Hour, 1000, 2000)
	f.emit(config.FlowFact{Key: "recency/sys", Domain: "s.example.com", Direction: "direct", RouteID: "direct", RouteName: "直连", Process: "/usr/libexec/trustd"}, time.Hour, 7, 8)
	page, e := f.c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "application", Direction: "all", Window: "today"})
	if e != nil {
		t.Fatal(e)
	}
	u := page.UnknownSource
	if u == nil || u.Upload != 1100 || u.Download != 2200 || u.Connections != 2 || u.Direction != "mixed" {
		t.Fatalf("unknown=%v", u)
	}
	if page.SystemUpload != 7 || page.SystemDownload != 8 || page.Upload != 10 || page.Download != 20 {
		t.Fatalf("system=%d/%d app=%d/%d", page.SystemUpload, page.SystemDownload, page.Upload, page.Download)
	}
	detail, e := f.c.QueryTrafficDetail(context.Background(), &pb.TrafficDetailRequest{RowId: u.Id, ReadToken: page.ReadToken, Limit: 10})
	if e != nil || len(detail.Items) != 2 {
		t.Fatalf("unknown detail err=%v items=%d", e, len(detail.GetItems()))
	}
}
