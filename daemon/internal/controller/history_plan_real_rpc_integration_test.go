//go:build integration

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// TestPlanRealRPC measures the same private-dataset requests used by the App.
// A single sample is a probe check; two rounds of 50 are the planned benchmark.
func TestPlanRealRPC(t *testing.T) {
	dir, inventoryPath := os.Getenv("PAX_PLAN_R_DIR"), os.Getenv("PAX_PLAN_INVENTORY")
	if dir == "" || inventoryPath == "" {
		t.Skip("explicit private R dataset and inventory required")
	}
	at, err := time.Parse(time.RFC3339, os.Getenv("PAX_PLAN_AT"))
	if err != nil {
		t.Fatal("PAX_PLAN_AT must be the recorded RFC3339 cutoff", err)
	}
	samples := 1
	if raw := os.Getenv("PAX_PLAN_SAMPLES"); raw != "" {
		samples, err = strconv.Atoi(raw)
		if err != nil || samples != 1 && samples != 5 && samples != 50 {
			t.Fatal("PAX_PLAN_SAMPLES must be 1 for probe, 5 for diagnosis or 50 for benchmark")
		}
	}
	rounds := 1
	if samples == 50 {
		rounds = 2
	} else {
		t.Log("probe/diagnosis only: this run is not a performance acceptance")
	}
	ctx := context.Background()
	logs, _, err := historystore.OpenLogs(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logs.Close() })
	logs.ConfigureColumnProjector(projectColumnBatch)
	if err = logs.Ready(ctx); err != nil {
		t.Fatal("R log projection is not ready", err)
	}
	var latestNS int64
	if err = logs.Reader().QueryRowContext(ctx, "SELECT max(at_ns) FROM col_events").Scan(&latestNS); err != nil {
		t.Fatal("R log watermark", err)
	}
	if at.Before(time.Unix(0, latestNS)) {
		t.Fatal("R dataset contains events after the cutoff; prepare an as-of snapshot before benchmarking")
	}
	traffic, _, err := historystore.OpenTraffic(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = traffic.Close() })
	c := New("history-plan", "history-plan")
	c.logs = &historyLogs{logs: logs, reads: c.readTokens}
	c.traffic = &historyTraffic{store: traffic}
	c.history = &historyOwner{clock: func() time.Time { return at }}
	meta, err := logs.SnapshotMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.readTokens.observeLogGenerations(meta.LogDeletionGeneration, meta.SummaryGeneration, meta.JournalGeneration, meta.LogRemovalBeforeMs)
	apps := planInventory(t, c, inventoryPath)
	c.history.applicationInventory = &historyApplicationInventory{At: time.Now(), Apps: apps}
	c.routing = &routingLibraryOwner{applicationQuery: func(context.Context) []config.RoutingApplication { return apps }}
	client := columnPerformanceClient(t, c)
	local := at.In(time.Local)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	t.Logf("PLAN_RPC_SETUP cutoff=%s samples_per_round=%d rounds=%d inventory=%d", at.Format(time.RFC3339), samples, rounds, len(apps))

	for _, window := range []struct {
		name string
		span time.Duration
	}{{"30m", 30 * time.Minute}, {"24h", 24 * time.Hour}} {
		if !planCaseSelected("logs/" + window.name) {
			continue
		}
		for round := 0; round < rounds; round++ {
			var directory, list, detail []time.Duration
			var total, records int
			for i := 0; i < samples; i++ {
				q := &pb.LogQuery{View: "activity", Dimension: "domain", Scope: "access", Limit: 100,
					From: timestamppb.New(at.Add(-window.span)), Until: timestamppb.New(at)}
				start := time.Now()
				objects, e := client.QueryLogObjects(ctx, q)
				if e != nil {
					t.Fatal("directory", window.name, e)
				}
				directory = append(directory, time.Since(start))
				q.ReadToken = objects.ReadToken
				start = time.Now()
				page, e := client.QueryLogs(ctx, q)
				if e != nil {
					t.Fatal("logs", window.name, e)
				}
				list = append(list, time.Since(start))
				total, records = int(page.Total), len(page.Records)
				if len(page.Records) > 0 {
					q.ReadToken = page.ReadToken
					start = time.Now()
					_, e = client.GetLogDetail(ctx, &pb.LogDetailRequest{RecordId: page.Records[0].RecordId, Query: q})
					if e != nil {
						t.Fatal("log detail", window.name, e)
					}
					detail = append(detail, time.Since(start))
				}
			}
			t.Logf("PLAN_LOG window=%s round=%d total=%d loaded=%d directory=%s list=%s detail=%s", window.name, round+1, total, records, planTimings(directory), planTimings(list), planTimings(detail))
			if total == 0 || records == 0 {
				t.Errorf("logs/%s has no records; cannot count as nonempty acceptance", window.name)
			}
			planThreshold(t, samples, "logs/"+window.name+"/directory", directory, 200*time.Millisecond)
			planThreshold(t, samples, "logs/"+window.name+"/list", list, 200*time.Millisecond)
			planThreshold(t, samples, "logs/"+window.name+"/detail", detail, 200*time.Millisecond)
		}
	}

	for _, test := range []struct{ window, dimension string }{
		{"today", "application"}, {"today", "domain"}, {"today", "system"},
		{"yesterday", "application"}, {"week", "application"}, {"month", "application"}, {"today", "network"},
	} {
		name := "traffic/" + test.window + "/" + test.dimension
		if !planCaseSelected(name) {
			continue
		}
		limit := 15
		if test.dimension == "network" {
			limit = 100 // TrafficFilter default used by the dashboard today card.
		}
		for round := 0; round < rounds; round++ {
			var list, detail, dns []time.Duration
			var total, records int
			var upload, download uint64
			for i := 0; i < samples; i++ {
				q := &pb.TrafficQuery{Window: test.window, Dimension: test.dimension, Direction: "all", Limit: uint32(limit)}
				if test.dimension == "network" {
					q.Until = timestamppb.New(at)
				}
				start := time.Now()
				page, e := client.QueryTraffic(ctx, q)
				if e != nil {
					t.Fatal(name, e)
				}
				list = append(list, time.Since(start))
				total, records = int(page.TotalRows), len(page.Items)
				upload, download = page.Upload, page.Download
				if len(page.Items) > 0 {
					start = time.Now()
					_, e = client.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: page.Items[0].Id, Limit: 20})
					if e != nil {
						t.Fatal(name, "detail", e)
					}
					detail = append(detail, time.Since(start))
				}
				if page.DnsSummary != nil {
					start = time.Now()
					_, e = client.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: page.DnsSummary.Id, Limit: 20})
					if e != nil {
						t.Fatal(name, "dns", e)
					}
					dns = append(dns, time.Since(start))
				}
			}
			t.Logf("PLAN_TRAFFIC case=%s round=%d total=%d loaded=%d upload=%d download=%d list=%s detail=%s dns=%s", name, round+1, total, records, upload, download, planTimings(list), planTimings(detail), planTimings(dns))
			if test.dimension == "network" && upload+download == 0 {
				t.Errorf("%s has no recorded traffic; nonempty acceptance remains unverified", name)
			}
			threshold := 200 * time.Millisecond
			if test.window == "week" || test.window == "month" {
				threshold = 500 * time.Millisecond
			}
			planThreshold(t, samples, name+"/list", list, threshold)
			if test.dimension != "network" && len(detail) == 0 {
				t.Errorf("%s has no ordinary detail; cannot count it as nonempty acceptance", name)
			} else if len(detail) > 0 {
				planThreshold(t, samples, name+"/detail", detail, threshold)
			}
			if test.dimension == "system" && len(dns) == 0 {
				t.Errorf("%s has no DNS summary; DNS detail remains unverified", name)
			} else if len(dns) > 0 {
				planThreshold(t, samples, name+"/dns", dns, threshold)
			}
		}
	}

	for _, view := range []string{"problems", "operations"} {
		name := "dashboard/" + view
		if !planCaseSelected(name) {
			continue
		}
		for round := 0; round < rounds; round++ {
			var times []time.Duration
			total := 0
			for i := 0; i < samples; i++ {
				q := &pb.LogQuery{View: view, From: timestamppb.New(today), Until: timestamppb.New(at), Limit: 20}
				start := time.Now()
				page, e := client.QueryLogs(ctx, q)
				if e != nil {
					t.Fatal(name, e)
				}
				times = append(times, time.Since(start))
				total = int(page.Total)
			}
			t.Logf("PLAN_DASHBOARD view=%s round=%d total=%d rpc=%s", view, round+1, total, planTimings(times))
			if total == 0 {
				t.Errorf("%s has no candidates; nonempty acceptance remains unverified", name)
			}
			planThreshold(t, samples, name, times, 200*time.Millisecond)
		}
	}
}

func planInventory(t *testing.T, c *Controller, path string) []config.RoutingApplication {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		apps := c.localRoutingApplications(context.Background())
		data, err = json.Marshal(apps)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	var apps []config.RoutingApplication
	if err = json.Unmarshal(data, &apps); err != nil {
		t.Fatal(err)
	}
	if len(apps) == 0 {
		t.Fatal("actual application inventory is empty")
	}
	sum := sha256.Sum256(data)
	t.Logf("PLAN_INVENTORY file=%s sha256=%x entries=%d", filepath.Base(path), sum, len(apps))
	return apps
}

func planCaseSelected(name string) bool {
	selected := os.Getenv("PAX_PLAN_CASE")
	return selected == "" || strings.Contains(name, selected)
}

func planTimings(values []time.Duration) string {
	if len(values) == 0 {
		return "none"
	}
	copy := append([]time.Duration(nil), values...)
	sort.Slice(copy, func(i, j int) bool { return copy[i] < copy[j] })
	p95 := (95*len(copy)+99)/100 - 1
	return fmt.Sprintf("n=%d first_ms=%.3f p50_ms=%.3f p95_ms=%.3f max_ms=%.3f samples=%v", len(copy), float64(values[0].Microseconds())/1000, float64(copy[(len(copy)-1)/2].Microseconds())/1000, float64(copy[p95].Microseconds())/1000, float64(copy[len(copy)-1].Microseconds())/1000, values)
}

func planThreshold(t *testing.T, samples int, name string, values []time.Duration, limit time.Duration) {
	t.Helper()
	if samples != 50 {
		return
	}
	if len(values) != samples {
		t.Errorf("%s: collected %d of %d samples", name, len(values), samples)
		return
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	p95 := ordered[(95*len(ordered)+99)/100-1]
	if p95 > limit {
		t.Fatalf("%s P95=%s exceeds %s", name, p95, limit)
	}
}
