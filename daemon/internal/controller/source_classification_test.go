package controller_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"testing"
	"time"
)

func TestTrafficSourcesAreExclusiveAndKeepUnknownBytes(t *testing.T) {
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
	defer func() { _ = controller.CloseHistoryFixture(c, ctx) }()
	at := time.Now().Add(-time.Minute)
	paths := []string{"/Applications/Browser.app/Contents/MacOS/Browser", "/Applications/Browser.app/Contents/Frameworks/Helper.app/Contents/MacOS/Helper", "/System/Applications/Safari.app/Contents/MacOS/Safari", "/usr/bin/curl", "/usr/libexec/trustd", "/usr/sbin/networksetup", "", "id-only-system"}
	var records []config.Observation
	for i, path := range paths {
		f := config.FlowFact{Key: fmt.Sprintf("source/1/%d", i), Process: path, Domain: "example.test", Direction: "direct", RouteID: "direct", Rule: "final", StartedAt: at}
		if i < 2 {
			f.Rule = `{"Package":"工作应用","Method":"application","Target":"com.browser"}`
		}
		if path == "id-only-system" {
			f.Process = ""
			f.ApplicationID = "com.apple.trustd"
			f.Application = "trustd"
		}
		end := at.Add(time.Second)
		f.ClosedAt = &end
		records = append(records, config.Observation{Session: "source", Sequence: uint64(i + 1), Kind: "started", At: end, Flow: &f, Upload: 10, Download: 20})
	}
	if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "source", HighWater: 8, Collecting: true, Records: records}); e != nil {
		t.Fatal(e)
	}
	p, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "day"})
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range p.Items {
		want := "无"
		if r.Name == "Browser" {
			want = "工作应用"
		}
		if r.Basis != want {
			t.Fatalf("rule repeats outcome or lost package: %q want %q", r.Basis, want)
		}
	}
	for _, r := range p.Items {
		if r.ObjectId == "process:/usr/libexec/trustd" || r.Name == "trustd" {
			t.Fatal("system process leaked into application list")
		}
	}
	if p.UnknownSource == nil || p.UnknownSource.Connections != 2 || p.UnknownSource.Upload != 20 || p.UnknownSource.Download != 40 {
		t.Fatal("unknown data lost or counted as identified", p)
	}
	unknownDetail, e := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{RowId: p.UnknownSource.Id, ReadToken: p.ReadToken, Limit: 1})
	if e != nil || unknownDetail.Summary.Connections != 2 || len(unknownDetail.Items) != 1 || unknownDetail.NextCursor == "" {
		t.Fatal("unknown source cannot be inspected", unknownDetail, e)
	}
	if p.Upload != 30 || p.Download != 60 || p.OtherProcessUpload != 0 || p.OtherProcessDownload != 0 {
		t.Fatal("identified subtotal includes other sources", p)
	}
	browser := 0
	for _, r := range p.Items {
		if r.Name == "Browser" {
			browser++
			if r.Connections != 2 {
				t.Fatal("helper split from owning app", r)
			}
		}
	}
	if browser != 1 {
		t.Fatal("owning app identity missing")
	}
	if p.Connections != 3 || p.OtherProcessConnections != 0 {
		t.Fatalf("identified app count=%d want 3", p.Connections)
	}
	s, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "system", Window: "day"})
	if e != nil || s.Connections != 3 {
		t.Fatalf("system count=%v error=%v", s, e)
	}
	// DNS query observations do not become active website connections in the
	// shared network summary, even while a query is waiting for a result.
	activeAt := time.Now()
	service := config.FlowFact{Key: "source/1/live", Process: "/usr/libexec/trustd", StartedAt: activeAt, Direction: "direct", RouteID: "direct"}
	dns := config.FlowFact{Key: "source/1/live-dns", Purpose: "dns", StartedAt: activeAt, Domain: "pending.test"}
	if e = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "source", Collecting: true, HighWater: 10, Records: []config.Observation{
		{Session: "source", Sequence: 9, Kind: "started", At: activeAt, Flow: &service},
		{Session: "source", Sequence: 10, Kind: "started", At: activeAt, Flow: &dns},
	}}); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot, e := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
		if e != nil {
			t.Fatal(e)
		}
		if snapshot.GetTodayTraffic().GetActiveConnectionsKnown() {
			if snapshot.TodayTraffic.ActiveConnections != 1 {
				t.Fatalf("DNS query counted as active connection: %d", snapshot.TodayTraffic.ActiveConnections)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("network summary not published")
		}
		time.Sleep(10 * time.Millisecond)
	}

}
