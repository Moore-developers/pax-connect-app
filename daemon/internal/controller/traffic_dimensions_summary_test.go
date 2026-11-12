package controller_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
)

func TestTrafficAllDimensionsSplitExitsAndKeepSourcesExclusive(t *testing.T) {
	f := newRecencyFixture(t)
	paths := []string{"/opt/homebrew/Cellar/gh/2.89.0/bin/gh", "/Users/test/.nvm/versions/node/v24.18.1/bin/node", "/usr/libexec/trustd"}
	for i, path := range paths {
		var observations []config.Observation
		for j, route := range []string{"route-a", "direct", "route-b"} {
			at := f.now.Add(-time.Duration(10+i*3+j) * time.Second)
			fact := config.FlowFact{Key: fmt.Sprintf("dimensions/%d/%d", i, j), Process: path, StartedAt: at, Domain: "api.example.test", RouteID: route, RouteName: route, Direction: "proxy", Rule: "builtin=局域网与私有网络地址", RuleSource: "Pax 包：Geo 基础包", ResponseKnown: true, ResponseMS: uint64(j + 1)}
			if route == "direct" {
				fact.Direction = "direct"
				fact.Rule, fact.RuleSource = "source=中国域名 / IP 规则", "Pax 包：geosite-cn"
			}
			f.seq++
			observations = append(observations, config.Observation{Session: "dimensions", Sequence: f.seq, At: at, Kind: "started", Flow: &fact, Upload: 10, Download: 20})
		}
		if err := controller.FeedHistoryFixture(t, f.c, context.Background(), config.ObservationBatch{Session: "dimensions", HighWater: f.seq, Collecting: true, Records: observations}); err != nil {
			t.Fatal(err)
		}
	}
	for _, window := range []string{"hour", "day", "today", "week", "month"} {
		var sourceCount uint64
		for _, dimension := range []string{"application", "system", "domain"} {
			page, err := f.c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: dimension, Window: window})
			if err != nil {
				t.Fatal(err)
			}
			wantRows, wantCount := 3, uint64(3)
			if dimension == "system" {
				wantRows, wantCount = 6, 6
			}
			if dimension == "domain" {
				wantCount = 9
			} else {
				sourceCount += page.Connections
			}
			if len(page.Items) != wantRows || page.Connections != wantCount || page.OtherProcessConnections != 0 {
				t.Fatalf("%s/%s row/count/source partition mismatch", window, dimension)
			}
			for _, row := range page.Items {
				if len(row.Exits) != 1 || len(row.Bases) == 0 {
					t.Fatalf("%s/%s exits or package summary missing", window, dimension)
				}
				detail, err := f.c.QueryTrafficDetail(context.Background(), &pb.TrafficDetailRequest{RowId: row.Id, ReadToken: page.ReadToken})
				if err != nil || uint64(len(detail.GetItems())) != row.Connections {
					t.Fatalf("%s/%s detail members lost: %v", window, dimension, err)
				}
			}
		}
		if sourceCount != 9 {
			t.Fatalf("%s application/system overlap or lost source", window)
		}
		filtered, err := f.c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "domain", Window: window, Direction: "proxy", Search: "route-a"})
		var trendCount uint64
		for _, point := range filtered.GetTrend() {
			trendCount += point.Upload
		}
		if trendCount != 30 {
			t.Fatalf("%s merged target trend changed filter: %d", window, trendCount)
		}
		if err != nil || len(filtered.GetItems()) != 1 || filtered.Items[0].Connections != 3 || len(filtered.Items[0].Exits) != 1 {
			t.Fatalf("%s merged target filter changed members: %v", window, err)
		}
	}
}
