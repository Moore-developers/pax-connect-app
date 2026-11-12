package controller

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestDomainGroupingAndHourWindowUseRawHistory(t *testing.T) {
	ctx := context.Background()
	c, p := newLogsTestController(t, newLogsTestDir(t))
	now := time.Now().UTC().Truncate(time.Second)
	seq := uint64(0)
	for i, host := range []string{"wxs.x.com", "api.x.com", "shop.example.co.uk"} {
		at := now.Add(-5 * time.Minute)
		if i == 1 {
			at = now.Add(-90 * time.Minute)
		}
		closed := at.Add(time.Minute)
		f := config.FlowFact{Key: host, Domain: host, IP: "192.0.2.1", ApplicationID: "fixture", Application: "fixture", RouteID: "direct", Direction: "direct", StartedAt: at, ClosedAt: &closed}
		seq++
		p.observe(config.ObservationBatch{Session: "root", HighWater: seq, Collecting: true, Records: []config.Observation{{Session: "root", Sequence: seq, At: at, Kind: "started", Flow: &f, Upload: 10, Download: 100}}})
	}
	directory, err := c.QueryLogObjects(ctx, &pb.LogQuery{View: "activity", Dimension: "domain"})
	if err != nil || len(directory.Items) != 2 || directory.Items[0].Id != "x.com" || directory.Items[0].Count != 2 {
		t.Fatal(directory, err)
	}
	logs, err := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", Dimension: "domain", ObjectId: "x.com", ReadToken: directory.ReadToken})
	if err != nil || len(logs.Records) != 2 {
		t.Fatal(logs, err)
	}
	for _, r := range logs.Records {
		if r.Flow.PrimaryDomain != "x.com" || r.Flow.Domain == "x.com" {
			t.Fatal("raw host lost", r.Flow)
		}
	}
	day, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "day"})
	if err != nil || len(day.Items) != 2 || day.Items[0].Name != "x.com" || day.Items[0].Connections != 2 || day.Items[0].Download != 200 {
		t.Fatal(day, err)
	}
	detail, err := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{RowId: day.Items[0].Id, ReadToken: day.ReadToken})
	if err != nil || len(detail.Items) != 2 || detail.Items[0].Domain == "x.com" {
		t.Fatal(detail, err)
	}
	hour, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "hour"})
	if err != nil || hour.Connections != 2 || hour.Download != 200 || hour.AsOf.AsTime().Sub(hour.From.AsTime()) != time.Hour {
		t.Fatal(hour, err)
	}
	// Two sibling domains on different actual paths still stay separate rows.
	if hour.Items[0].Name != "x.com" && hour.Items[1].Name != "x.com" {
		t.Fatal(hour)
	}
}
