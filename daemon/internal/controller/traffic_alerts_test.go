package controller

import (
	"context"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

func TestTrafficAlertEpisodeDirectionsEscalationAndRecovery(t *testing.T) {
	key := "app:example"
	window := func(seconds uint32, up, down uint64, partial bool) alertWindow {
		return alertWindow{seconds: seconds, rule: config.TrafficAlertRule{Enabled: true, Upload: 100, Download: 200}, until: trafficEpoch, partial: partial, sources: map[string]*alertSource{key: {name: "Example", kind: "app", up: up, down: down, routes: map[string]alertTransfer{"a": {up: up, down: down, name: "A"}}}}}
	}
	count := 0
	id := func() (string, error) { count++; return fmt.Sprint(count), nil }
	first := []alertWindow{window(300, 100, 199, false), window(3600, 100, 199, false), window(86400, 99, 199, false)}
	episodes, notices, err := nextAlertEpisodes(nil, first, id)
	if err != nil || count != 1 || len(notices) != 1 || notices[0].WindowSeconds != 300 || notices[0].Direction != "upload" {
		t.Fatalf("first: %v %v", notices, err)
	}
	original := notices[0].ID
	episodes, notices, err = nextAlertEpisodes(episodes, first, id)
	if err != nil || count != 1 || notices[0].ID != original {
		t.Fatal("continuous excess repeated")
	}
	first[2] = window(86400, 100, 200, false)
	episodes, notices, err = nextAlertEpisodes(episodes, first, id)
	if err != nil || count != 3 || len(notices) != 2 {
		t.Fatalf("daily escalation/direction: %v %v", notices, err)
	}
	for _, n := range notices {
		if n.WindowSeconds != 86400 {
			t.Fatalf("expected daily: %v", n)
		}
	}
	episodes, _, _ = nextAlertEpisodes(episodes, first, id)
	if count != 3 {
		t.Fatal("daily escalated twice")
	}
	partial := []alertWindow{window(300, 0, 0, true), window(3600, 0, 0, false), window(86400, 0, 0, false)}
	episodes, notices, _ = nextAlertEpisodes(episodes, partial, id)
	if len(episodes) != 2 || len(notices) != 0 {
		t.Fatal("incomplete data reset episode or exposed stale notice")
	}
	partial[0].partial = false
	episodes, _, _ = nextAlertEpisodes(episodes, partial, id)
	if len(episodes) != 0 {
		t.Fatal("complete recovery did not rearm")
	}
	_, notices, _ = nextAlertEpisodes(episodes, first, id)
	if count != 5 || len(notices) != 2 {
		t.Fatal("did not rearm")
	}
}

func TestTrafficAlertReadsExactShortAndSteppedLongAcrossRoutes(t *testing.T) {
	h := newTrafficHarness(t)
	h.clock = trafficEpoch.Add(2*time.Hour + 2*time.Minute)
	end := h.clock.Truncate(5 * time.Second)
	shortStart := end.Add(-5 * time.Minute)
	add := func(key, route, app string, at time.Time, up, down uint64) config.Observation {
		f := trafficFlow("s/"+key, func(f *config.FlowFact) {
			f.RouteID = route
			f.RouteName = route
			f.ApplicationID = app
			f.Application = app
		})
		o := h.obs(0, "sample", f, up, down)
		o.At = at
		return o
	}
	h.append(h.clock, true,
		add("outside", "a", "Example", shortStart.Add(-time.Second), 10, 20),
		add("boundary", "a", "Example", shortStart, 30, 40),
		add("other-route", "b", "Example", shortStart.Add(10*time.Second), 50, 60),
		add("other-app", "a", "Other", shortStart.Add(20*time.Second), 70, 80),
		add("tail", "b", "Example", end.Add(-10*time.Second), 90, 100))
	h.settle()
	c := New("test", "test")
	c.traffic = h.tr
	c.history = &historyOwner{clock: func() time.Time { return h.clock }}
	c.ingest = &historyIngest{}
	c.ingest.coverage.Store(&coverageSpan{valid: true, collecting: true, runFrom: end.Add(-25 * time.Hour), last: end})
	p := config.DefaultTrafficAlertSettings()
	p.Enabled = true
	cache := &alertWindowCache{}
	windows, err := c.readAlertWindows(context.Background(), p, cache)
	if err != nil {
		t.Fatal(err)
	}
	var short, hour *alertSource
	for _, w := range windows {
		if w.partial {
			t.Fatal("continuous poll proof lost")
		}
		if w.seconds == 300 {
			short = w.sources["app:Example"]
		}
		if w.seconds == 3600 {
			hour = w.sources["app:Example"]
			if !w.until.Equal(end.Truncate(5 * time.Minute)) {
				t.Fatal("hour not stepped")
			}
		}
	}
	if short == nil || short.up != 170 || short.down != 200 || len(short.routes) != 2 {
		t.Fatalf("short: %+v", short)
	}
	if hour == nil || hour.up != 90 || hour.down != 120 {
		t.Fatalf("hour: %+v", hour)
	}
	// Inspecting a route includes its latest tail in every period, independently
	// of the notification job's frozen, five-minute-stepped long windows.
	c.trafficAlerts = &trafficAlertOwner{settings: p}
	c.publishTrafficAlerts(&pb.TrafficAlertState{Settings: alertSettingsProto(p), MonitorState: "collecting"})
	c.current.Status = pb.ProtectionStatus_PROTECTION_STATUS_PROTECTED
	for _, want := range []struct {
		route                        string
		shortUp, shortDown, up, down uint64
	}{{"a", 100, 120, 110, 140}, {"b", 140, 160, 140, 160}, {"empty", 0, 0, 0, 0}} {
		page, e := c.QueryRouteUsage(context.Background(), &pb.RouteHistoryRequest{Window: "today", RouteId: want.route})
		if e != nil {
			t.Fatal(e)
		}
		progress := page.GetAlertProgress()
		if len(progress.GetWindows()) != 3 || progress.GetSettings().GetMediumSeconds() != 3600 {
			t.Fatalf("missing configured windows: %v", progress)
		}
		for i, sample := range progress.Windows {
			up, down := want.up, want.down
			if i == 0 {
				up, down = want.shortUp, want.shortDown
			}
			if sample.Upload != up || sample.Download != down || sample.Partial || !sample.Until.AsTime().Equal(end) {
				t.Fatalf("route %s: %v", want.route, sample)
			}
		}
	}
	batch, e := c.QueryRouteUsage(context.Background(), &pb.RouteHistoryRequest{Window: "today"})
	if e != nil || batch.GetAlertProgress() != nil {
		t.Fatalf("unexpected batch progress: %v", e)
	}
	// A prior interruption leaves older windows incomplete. The short window
	// still has continuous coverage, and all already recorded bytes remain visible.
	c.ingest.coverage.Store(&coverageSpan{valid: true, collecting: true, runFrom: end.Add(-10 * time.Minute), last: end})
	page, err := c.QueryRouteUsage(context.Background(), &pb.RouteHistoryRequest{Window: "today", RouteId: "b"})
	if err != nil {
		t.Fatal(err)
	}
	progress := page.GetAlertProgress()
	if progress.GetMonitorState() != "partial" || len(progress.GetWindows()) != 3 ||
		progress.Windows[0].Partial || !progress.Windows[1].Partial || !progress.Windows[2].Partial ||
		progress.Windows[1].Upload != 140 || progress.Windows[1].Download != 160 {
		t.Fatalf("mixed coverage: %v", progress)
	}
	c.ingest.coverage.Store(&coverageSpan{valid: true, collecting: true, runFrom: end.Add(-25 * time.Hour), last: end})
	again, err := c.readAlertWindows(context.Background(), p, cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range again {
		if w.seconds == 300 && w.sources["app:Example"].up != 170 {
			t.Fatal("repeat accumulated")
		}
		if w.seconds == 3600 && w.sources["app:Example"].up != 90 {
			t.Fatal("cache accumulated")
		}
	}
}

func TestTrafficAlertCacheSeesLateCommittedData(t *testing.T) {
	h := newTrafficHarness(t)
	h.clock = trafficEpoch.Add(2*time.Hour + 2*time.Minute)
	c := New("test", "test")
	c.traffic = h.tr
	c.history = &historyOwner{clock: func() time.Time { return h.clock }}
	c.ingest = &historyIngest{}
	c.ingest.coverage.Store(&coverageSpan{valid: true, collecting: true, runFrom: h.clock.Add(-25 * time.Hour), last: h.clock})
	p := config.DefaultTrafficAlertSettings()
	p.Short.Enabled = false
	p.Day.Enabled = false
	cache := &alertWindowCache{}
	first, e := c.readAlertWindows(context.Background(), p, cache)
	if e != nil || len(first[0].sources) != 0 {
		t.Fatal(first, e)
	}
	f := trafficFlow("s/late", func(f *config.FlowFact) { f.ApplicationID = "Late"; f.Application = "Late" })
	o := h.obs(0, "sample", f, 123, 456)
	o.At = h.clock.Add(-10 * time.Minute)
	h.append(h.clock, true, o)
	h.settle()
	second, e := c.readAlertWindows(context.Background(), p, cache)
	if e != nil || second[0].sources["app:Late"] == nil || second[0].sources["app:Late"].up != 123 {
		t.Fatal("late committed bytes hidden by cache", second, e)
	}
}

func TestTrafficAlertWindowUsesDurationsAcrossMidnightAndDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{time.Date(2026, 11, 24, 0, 2, 33, 0, loc), time.Date(2026, 11, 1, 3, 2, 33, 0, loc)} {
		p := config.DefaultTrafficAlertSettings()
		p.MediumSeconds = 7200
		for _, w := range alertWindows(p, at) {
			from := w.until.Add(-time.Duration(w.seconds) * time.Second)
			if w.until.Sub(from) != time.Duration(w.seconds)*time.Second {
				t.Fatal("calendar day used")
			}
			if w.seconds != 300 && w.until.Unix()%300 != 0 {
				t.Fatal("not stepped")
			}
		}
	}
}

func TestTrafficAlertSettingsCASFailureAndRestart(t *testing.T) {
	c, _ := routingController(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.ConfigureTrafficAlerts(ctx, dir, uint32(os.Geteuid()))
	defer func() { _ = c.CloseTrafficAlerts(context.Background()) }()
	p := config.DefaultTrafficAlertSettings()
	p.Enabled = true
	p.MediumSeconds = 7200
	saved, err := c.SaveTrafficAlertSettings(ctx, alertSettingsProto(p))
	if err != nil || saved.Settings.Revision != 2 {
		t.Fatal(saved, err)
	}
	unchanged, err := c.SaveTrafficAlertSettings(ctx, saved.Settings)
	if err != nil || unchanged.Settings.Revision != 2 {
		t.Fatal("no-op save changed revision", unchanged, err)
	}
	if _, err = c.SaveTrafficAlertSettings(ctx, alertSettingsProto(p)); status.Code(err) != codes.Aborted {
		t.Fatal("CAS conflict missing", err)
	}
	p.Revision = 2
	p.Short.Upload = 0
	if _, err = c.SaveTrafficAlertSettings(ctx, alertSettingsProto(p)); status.Code(err) != codes.InvalidArgument {
		t.Fatal("invalid external input accepted", err)
	}
	p.Short.Upload = 1
	if err = os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = c.SaveTrafficAlertSettings(ctx, alertSettingsProto(p)); err == nil {
		t.Fatal("save failure hidden")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	reopened, err := config.NewTrafficAlertStore(dir, uint32(os.Geteuid())).LoadSettings()
	if err != nil || reopened.Revision != 2 || reopened.Short.Upload != 100_000_000 {
		t.Fatal("failed write changed persisted intent", reopened, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		state, _ := c.GetTrafficAlerts(ctx, nil)
		if state.Settings.Revision == 2 && state.MonitorState == "proxy_off" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reread did not recover", state)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTrafficAlertUncommittedTailAndUnknownSources(t *testing.T) {
	h := newTrafficHarness(t)
	h.clock = trafficEpoch.Add(2 * time.Hour)
	at := h.clock.Add(-10 * time.Second)
	observation := func(key, route, app, process, purpose string, up uint64) config.Observation {
		f := trafficFlow("s/"+key, func(f *config.FlowFact) {
			f.RouteID = route
			f.ApplicationID = app
			f.Application = app
			f.Process = process
			f.Purpose = purpose
		})
		o := h.obs(0, "sample", f, up, 0)
		o.At = at
		return o
	}
	records := []config.Observation{
		observation("a", "a", "", "", "", 10), observation("b", "b", "", "", "", 20),
		observation("tool", "a", "", "/opt/homebrew/bin/curl", "", 30),
		observation("dns", "a", "", "", "dns", 1_000_000_000),
	}
	h.append(h.clock, true, records...)
	h.settle()
	c := New("test", "test")
	c.traffic = h.tr
	c.history = &historyOwner{clock: func() time.Time { return h.clock }}
	c.ingest = &historyIngest{}
	c.ingest.coverage.Store(&coverageSpan{valid: true, collecting: true, runFrom: h.clock.Add(-25 * time.Hour), last: h.clock})
	live := newLiveState()
	live.seeded = true
	c.liveView.Store(live)
	h.tr.live = c.liveView.Load
	tail := observation("pending", "a", "Pending", "", "", 40)
	live.apply(config.ObservationBatch{Session: "s", Collecting: true, Records: []config.Observation{tail}}, h.clock)
	p := config.DefaultTrafficAlertSettings()
	p.Day.Enabled = false
	p.Hour.Enabled = false
	read := func() map[string]*alertSource {
		w, e := c.readAlertWindows(context.Background(), p, &alertWindowCache{})
		if e != nil {
			t.Fatal(e)
		}
		return w[0].sources
	}
	before := read()
	if len(before) != 4 || before["unknown:a"].up != 10 || before["unknown:b"].up != 20 || before["app:Pending"].up != 40 {
		t.Fatalf("source/tail: %+v", before)
	}
	h.append(h.clock, true, tail)
	h.settle()
	after := read()
	if len(after) != 4 || after["app:Pending"].up != 40 {
		t.Fatalf("commit counted tail twice: %+v", after)
	}
}

func TestTrafficAlertPublishedRevisionTracksVisibleChangesOnly(t *testing.T) {
	c := New("test", "test")
	state := &pb.TrafficAlertState{Settings: alertSettingsProto(config.DefaultTrafficAlertSettings()), MonitorState: "disabled"}
	c.publishTrafficAlerts(state)
	first := c.current.TrafficAlerts.Revision
	seq := c.snapshotSeq
	c.publishTrafficAlerts(proto.Clone(state).(*pb.TrafficAlertState))
	if c.snapshotSeq != seq || c.current.TrafficAlerts.Revision != first {
		t.Fatal("idle check moved visible state")
	}
	next := proto.Clone(state).(*pb.TrafficAlertState)
	next.MonitorState = "checking"
	c.publishTrafficAlerts(next)
	if c.current.TrafficAlerts.Revision != first+1 || c.snapshotSeq != seq+1 {
		t.Fatal("visible change did not move state revision")
	}
}
