//go:build integration

package controller

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/ipc"
)

// routingMatchUIData uses the shipping journal and SQLite processors, never a network runtime.
type routingMatchUIData struct {
	c        *Controller
	sequence uint64
	now      time.Time
}

func newRoutingMatchUIData(t testing.TB, ctx context.Context, dir string) *routingMatchUIData {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	dir = canonical
	c := New("routing-match-ui-fixture", "fixture")
	if err := ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseHistoryFixture(c, context.Background()) })
	snap, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateSettings(ctx, &pb.UpdateSettingsRequest{Settings: &pb.Settings{LogRetentionHours: 24}, ExpectedRevision: snap.Settings.Revision, FieldMask: []string{"log_retention_hours"}, OperationId: "fixture-retention", RequestedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	c.history.applicationInventory = &historyApplicationInventory{At: time.Now(), Apps: []config.RoutingApplication{
		{ID: "fixture.merged", Name: "虚构合并应用", Path: "/Applications/Fixture-Merged.app", Bundle: true},
		{ID: "fixture.legacy", Name: "虚构旧记录", Path: "/Applications/Fixture-Legacy.app", Bundle: true},
		{ID: "fixture.ipv6", Name: "虚构IPv6尝试", Path: "/Applications/Fixture-IPv6.app", Bundle: true},
		{ID: "fixture.userblock", Name: "虚构用户阻断", Path: "/Applications/Fixture-Block.app", Bundle: true},
	}}
	return &routingMatchUIData{c: c, now: time.Now().Add(-time.Minute)}
}
func (d *routingMatchUIData) feed(t testing.TB, ctx context.Context, facts []config.FlowFact, rejected bool) {
	t.Helper()
	batch := config.ObservationBatch{Session: "routing-match-fixture", Collecting: true}
	appendRecord := func(kind string, at time.Time, f config.FlowFact, up uint64) {
		d.sequence++
		batch.Records = append(batch.Records, config.Observation{Session: batch.Session, Sequence: d.sequence, At: at, Kind: kind, Flow: &f, Upload: up})
	}
	for _, f := range facts {
		if rejected {
			identity := f
			identity.Direction, identity.Rule, identity.RuleSource, identity.Failure, identity.EstablishmentState = "unknown", "", "", "", "not_measurable"
			identity.ClosedAt = nil
			appendRecord("identity", f.StartedAt.Add(-time.Millisecond), identity, 0)
			appendRecord("rejected", f.StartedAt, f, 0)
			appendRecord("closed", f.StartedAt.Add(time.Millisecond), f, 0)
		} else {
			appendRecord("started", f.StartedAt, f, 30)
		}
	}
	batch.HighWater = d.sequence
	if err := FeedHistoryFixture(t, d.c, ctx, batch); err != nil {
		t.Fatal(err)
	}
}
func (d *routingMatchUIData) rejection(index int, ordinary bool) config.FlowFact {
	app, name, rule, source := "fixture.ipv6", "虚构IPv6尝试", "builtin=IPv6 流量", "Pax 包：Geo 基础包"
	if ordinary {
		app, name, rule, source = "fixture.userblock", "虚构用户阻断", "domain=blocked.example.test", "Pax 包：用户规则"
	} else if index%2 == 1 {
		source = "Pax 包：Pax 内置规则"
	}
	at := d.now.Add(time.Duration(index) * time.Millisecond)
	return config.FlowFact{Key: fmt.Sprintf("routing-match-fixture/%s/%02d", app, index), ApplicationID: app, Application: name, Domain: "blocked.example.test", IP: "2001:db8::1", Port: 443, Transport: "tcp", StartedAt: at, RoutingMatchedAt: at, Direction: "blocked", Rule: rule, RuleSource: source, Failure: "rule_rejected", EstablishmentState: "failed", Evidence: "synthetic_routing_match_ui"}
}
func (d *routingMatchUIData) seed(t testing.TB, ctx context.Context) {
	t.Helper()
	older := config.FlowFact{Key: "routing-match-fixture/merged/old", ApplicationID: "fixture.merged", Application: "虚构合并应用", Domain: "merged.example.test", IP: "192.0.2.10", Port: 443, Transport: "tcp", StartedAt: d.now, RoutingMatchedAt: d.now, RouteID: "direct", RouteName: "直连", Direction: "direct", Rule: "source=中国域名 / IP 规则", RuleSource: "Pax 包：Geo 基础包", ResponseKnown: true, ResponseMS: 12, EstablishmentState: "ready", Evidence: "synthetic_routing_match_ui"}
	newer := older
	newer.Key, newer.StartedAt, newer.RoutingMatchedAt = "routing-match-fixture/merged/new", d.now.Add(time.Second), d.now.Add(time.Second)
	newer.Rule, newer.RuleSource = "application=fixture.merged", "Pax 包：优先规则"
	legacy := older
	legacy.Key, legacy.ApplicationID, legacy.Application = "routing-match-fixture/legacy", "fixture.legacy", "虚构旧记录"
	legacy.RoutingMatchedAt = time.Time{}
	d.feed(t, ctx, []config.FlowFact{older, newer, legacy}, false)
	var rejected []config.FlowFact
	for i := 0; i < 25; i++ {
		rejected = append(rejected, d.rejection(i, false))
	}
	rejected = append(rejected, d.rejection(0, true))
	d.feed(t, ctx, rejected, true)
}
func (d *routingMatchUIData) row(t testing.TB, ctx context.Context, app string) (*pb.TrafficPage, *pb.TrafficRow) {
	t.Helper()
	page, err := d.c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Items {
		if row.ObjectId == app {
			return page, row
		}
	}
	for _, row := range page.Items {
		t.Logf("synthetic row object=%q name=%q", row.ObjectId, row.Name)
	}
	t.Fatalf("synthetic app %s missing", app)
	return nil, nil
}
func (d *routingMatchUIData) validate(t testing.TB, ctx context.Context) (*pb.TrafficPage, *pb.TrafficRow) {
	t.Helper()
	_, merged := d.row(t, ctx, "fixture.merged")
	if merged.Connections != 2 || merged.Upload != 60 || len(merged.Bases) != 2 || merged.LatestMatch == nil || merged.LatestMatch.Rule != "application=fixture.merged" {
		t.Fatal("synthetic merged decision/totals mismatch")
	}
	_, legacy := d.row(t, ctx, "fixture.legacy")
	if legacy.LatestMatch != nil {
		t.Fatal("legacy fixture invented a decision time")
	}
	_, ordinary := d.row(t, ctx, "fixture.userblock")
	if ordinary.Direction != "blocked" || ordinary.FailedCount != 1 || ordinary.LatestMatch == nil || ordinary.LatestMatch.Source != "Pax 包：用户规则" {
		t.Fatal("ordinary rejection changed")
	}
	page, row := d.row(t, ctx, "fixture.ipv6")
	if row.Connections != 0 || row.FailedCount != 25 || row.Direction != "blocked" || len(row.Bases) != 2 {
		t.Fatal("synthetic rejection totals mismatch")
	}
	detail, err := d.c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id})
	if err != nil || detail.GetRetainedConnectionCount() != 25 || len(detail.GetItems()) != 20 || detail.GetNextCursor() == "" {
		t.Fatal("synthetic first rejection page mismatch", err)
	}
	next, err := d.c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id, Cursor: detail.NextCursor})
	if err != nil || len(next.GetItems()) != 5 {
		t.Fatal("synthetic second rejection page mismatch", err)
	}
	return page, row
}
func (d *routingMatchUIData) appendAndCheck(t testing.TB, ctx context.Context, page *pb.TrafficPage, row *pb.TrafficRow) {
	t.Helper()
	d.feed(t, ctx, []config.FlowFact{d.rejection(25, false)}, true)
	frozen, err := d.c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id})
	if err != nil || frozen.GetRetainedConnectionCount() != 25 || frozen.Summary.FailedCount != 25 {
		t.Fatal("old synthetic token changed", err)
	}
	fresh, next := d.row(t, ctx, "fixture.ipv6")
	detail, err := d.c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: fresh.ReadToken, RowId: next.Id})
	if err != nil || detail.GetRetainedConnectionCount() != 26 || next.FailedCount != 26 {
		t.Fatal("new synthetic token missed appended rejection", err)
	}
}

func TestRoutingMatchFixtureRecordedScenarios(t *testing.T) {
	ctx := context.Background()
	d := newRoutingMatchUIData(t, ctx, t.TempDir())
	d.seed(t, ctx)
	page, row := d.validate(t, ctx)
	d.appendAndCheck(t, ctx, page, row)
}

// TestRoutingMatchNativeUIFixture is an opt-in local App host. It never starts core/TUN.
// PAX_ROUTING_MATCH_UI_FIXTURE must name an existing empty private directory outside /tmp.
// Writing "append-26" to command appends one rejection; appended marks successful token checks.
func TestRoutingMatchNativeUIFixture(t *testing.T) {
	dir := os.Getenv("PAX_ROUTING_MATCH_UI_FIXTURE")
	if dir == "" {
		t.Skip("explicit synthetic local UI fixture only")
	}
	if os.Geteuid() == 0 {
		t.Fatal("fixture must run as the normal user")
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("fixture requires a private directory")
	}
	entries, err := os.ReadDir(canonical)
	if err != nil || len(entries) != 0 {
		t.Fatal("fixture directory must be empty")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d := newRoutingMatchUIData(t, ctx, canonical)
	d.seed(t, ctx)
	d.validate(t, ctx)
	listener, err := ipc.ListenUser(filepath.Join(canonical, "ui.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	server := grpc.NewServer()
	pb.RegisterPaxServiceServer(server, d.c)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	if err = os.WriteFile(filepath.Join(canonical, "ready"), []byte("protocol=39; synthetic applications=4; rejected=25; detail=20+5\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	appended := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if appended {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(canonical, "command"))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != "append-26" {
				t.Fatal("unsupported fixture command")
			}
			before, beforeRow := d.row(t, ctx, "fixture.ipv6")
			d.appendAndCheck(t, ctx, before, beforeRow)
			appended = true
			if err = os.WriteFile(filepath.Join(canonical, "appended"), []byte("frozen=25; latest=26\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
