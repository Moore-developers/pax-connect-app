package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func TestStoredTrafficIdentityUsesOnlyExactObjectEvidence(t *testing.T) {
	auth := "process:/System/Volumes/Preboot/Cryptexes/App/usr/libexec/AuthenticationServicesAgent"
	for _, tc := range []struct {
		in                    aggregateIdentity
		dimension, kind, name string
	}{
		{aggregateIdentity{Dimension: "application", Object: auth, Kind: "tool"}, "system", "system", "系统认证服务"},
		{aggregateIdentity{Dimension: "application", Object: "process:/Users/test/.local/share/claude/versions/2.1.301", Kind: "tool"}, "application", "app", "Claude Code"},
		{aggregateIdentity{Dimension: "application", Object: "process:/Users/test/.nvm/versions/node/v24.18.1/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex", Kind: "tool"}, "application", "app", "Codex CLI"},
		{aggregateIdentity{Dimension: "application", Object: "process:/usr/local/bin/node", Kind: "tool", Search: auth}, "system", "tool", "node"},
		{aggregateIdentity{Dimension: "domain", Object: "unknown", Kind: "app", Search: "192.0.2.1"}, "domain", "app", "历史未细分目标（域名未记录）"},
	} {
		got := projectStoredIdentity(tc.in)
		if got.Dimension != tc.dimension || got.Kind != tc.kind || got.Name != tc.name {
			t.Fatalf("%+v", got)
		}
	}
}

func TestTrafficClassificationWindowsTotalsSearchAndLegacy(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c, _ := routingController(t)
	if err = ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := CloseHistoryFixture(c, ctx); err != nil {
			t.Error(err)
		}
	}()
	at := time.Now().Add(-2 * time.Minute)
	paths := []string{"/Applications/Fixture.app/Contents/MacOS/Fixture", "/Users/test/.local/share/claude/versions/2.1.301", "/usr/local/bin/node", "/System/Volumes/Preboot/Cryptexes/App/usr/libexec/AuthenticationServicesAgent"}
	var records []config.Observation
	for i, path := range paths {
		f := config.FlowFact{Key: path, Process: path, StartedAt: at, IP: "2001:db8::1", Port: 443, RouteID: "direct", Direction: "direct", ResponseKnown: true, ResponseMS: 20}
		end := at.Add(time.Second)
		f.ClosedAt = &end
		records = append(records, config.Observation{Session: "classify", Sequence: uint64(i + 1), Kind: "started", At: end, Flow: &f, Upload: 10, Download: 20})
	}
	// An older retained connection still overlaps this window but contributes no new start.
	earlier := config.FlowFact{Key: "earlier", Process: paths[0], StartedAt: at.Add(-26 * time.Hour), IP: "2001:db8::1", Port: 443, RouteID: "direct", Direction: "direct"}
	end := at.Add(time.Second)
	earlier.ClosedAt = &end
	records = append(records, config.Observation{Session: "classify", Sequence: 5, Kind: "closed", At: end, Flow: &earlier})
	if err = FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "classify", HighWater: 5, Collecting: true, Records: records}); err != nil {
		t.Fatal(err)
	}
	// Emulate exact identities recorded under the old tool classification, plus an
	// unsplittable domain/unknown bucket. Never touch the user's actual database.
	store := c.trafficJob().store.(*historystore.Traffic)
	old := readTrafficTable(t, store, "traffic_5m")
	err = store.Update(ctx, func(tx *historystore.TrafficTx) error {
		for _, r := range old {
			if r.Dimension == "application" && r.Object == "process:"+paths[1] {
				r.SourceKind = "tool"
				r.Name = "2.1.301"
				if e := tx.PutTraffic5m(ctx, r); e != nil {
					return e
				}
			}
		}
		legacy := historystore.TrafficRow{TrafficKey: historystore.TrafficKey{BucketMs: at.Add(-10 * time.Minute).Truncate(5 * time.Minute).UnixMilli(), Dimension: "domain", Object: "unknown", Route: "direct", Direction: "direct"}, Name: "未采集域名", SourceKind: "app", UpBytes: 7, DownBytes: 9, NewConns: 1}
		if err := tx.PutTraffic5m(ctx, legacy); err != nil {
			return err
		}
		// The same legacy connection also had a saved source bucket.
		legacy.Dimension, legacy.Object, legacy.Name = "application", "bundle:/Applications/Fixture.app", "Fixture"
		return tx.PutTraffic5m(ctx, legacy)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"hour", "day", "today", "week", "month"} {
		app, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: window})
		if e != nil {
			t.Fatal(e)
		}
		if app.Upload != 27 || app.Download != 49 || app.OtherProcessUpload != 0 || app.OtherProcessDownload != 0 || app.SystemUpload != 20 || app.SystemDownload != 40 {
			t.Fatalf("%s totals: %v", window, app)
		}
		filtered, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "system", Window: window, Search: "node"})
		if e != nil {
			t.Fatal(e)
		}
		if len(filtered.Items) != 1 || filtered.Items[0].SourceKind != "tool" || filtered.Upload != 10 || filtered.OtherProcessUpload != 0 {
			t.Fatalf("%s filtered: %v", window, filtered)
		}
		system, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "system", Window: window})
		if e != nil || len(system.GetItems()) != 2 {
			t.Fatalf("%s system: %v %v", window, system, e)
		}
		domain, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: window})
		if e != nil {
			t.Fatal(e)
		}
		if app.Connections+app.OtherProcessConnections+system.Connections != domain.Connections || domain.Connections != 5 {
			t.Fatalf("%s new connections mismatch: app=%d tool=%d system=%d domain=%d", window, app.Connections, app.OtherProcessConnections, system.Connections, domain.Connections)
		}
		if domain.Upload != 47 || domain.Download != 89 {
			t.Fatalf("%s target totals %d/%d", window, domain.Upload, domain.Download)
		}
		for _, row := range domain.Items {
			detail, e := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: domain.ReadToken, RowId: row.Id})
			if e != nil {
				t.Fatal(e)
			}
			if row.ObjectId == "unknown" && (len(detail.Items) != 0 || len(detail.RecentResponses) != 0) {
				t.Fatal("legacy membership fabricated")
			}
			if row.ObjectId == "target:ip-only" && (window == "day" || window == "hour") && len(detail.Items) != 4 {
				t.Fatalf("IP-only lost sources: %d", len(detail.Items))
			}
		}
	}
}
