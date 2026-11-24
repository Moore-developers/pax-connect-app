package controller

import (
	"context"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func TestRouteUsageOverviewAndRankingRespectActualRouteAndHour(t *testing.T) {
	h := newTrafficHarness(t)
	h.clock = trafficEpoch.Add(2*time.Hour + 2*time.Minute)
	hour := routeUsageHourStart(h.clock)
	records := []config.Observation{}
	add := func(key, route, app, domain string, at time.Time, up, down uint64) {
		f := trafficFlow("s/"+key, func(f *config.FlowFact) {
			f.RouteID, f.RouteName, f.ApplicationID, f.Application = route, route, app, app
			f.Domain, f.IP = domain, "203.0.113.1"
		})
		o := h.obs(0, "started", f, up, down)
		o.At = at
		records = append(records, o)
	}
	add("old", "a", "Old", "old.example.com", hour.Add(-time.Second), 100, 900)
	add("one", "a", "Downloader", "jjdsn.com", hour.Add(time.Second), 50, 1_100_000_000)
	add("two", "a", "Browser", "example.com", hour.Add(time.Minute), 60, 300)
	add("three", "b", "Other", "jjdsn.com", hour.Add(2*time.Minute), 800, 8_000)
	h.append(h.clock, true, records...)
	h.settle()
	c := New("test", "test")
	c.traffic = h.tr
	c.history = &historyOwner{clock: func() time.Time { return h.clock }}
	got, err := c.QueryRouteUsage(context.Background(), &pb.RouteHistoryRequest{Window: "today"})
	if err != nil {
		t.Fatal(err)
	}
	sums := map[string]*pb.RouteUsageSummary{}
	for _, v := range got.Routes {
		sums[v.RouteId] = v
	}
	if got.Upload != 1010 || len(got.Items) == 0 {
		t.Fatalf("existing aggregate contract changed: %v", got)
	}
	today, err := c.QueryRouteUsage(context.Background(), &pb.RouteHistoryRequest{Window: "today", RouteId: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(today.Applications) != 3 || len(today.Domains) != 2 || len(today.Routes) != 1 {
		t.Fatalf("today ranking missing older traffic: apps=%v domains=%v routes=%v", today.Applications, today.Domains, today.Routes)
	}
	var dailyUpload uint64
	for _, app := range today.Applications {
		dailyUpload += app.Upload
	}
	if dailyUpload != today.Upload || today.Routes[0].TodayUpload != today.Upload || today.Routes[0].HourUpload != 110 {
		t.Fatalf("day totals/ranking and hour summary mixed: %v", today)
	}
	a, b := sums["a"], sums["b"]
	if a == nil || b == nil || a.TodayUpload != 210 || a.HourUpload != 110 || a.HourDownload != 1_100_000_300 || b.HourUpload != 800 {
		t.Fatalf("route overview: %v", got)
	}
	page, err := c.QueryRouteUsage(context.Background(), &pb.RouteHistoryRequest{Window: "hour", RouteId: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Upload != a.HourUpload || page.Download != a.HourDownload || !page.From.AsTime().Equal(hour) {
		t.Fatalf("hour mismatch: %v", page)
	}
	if len(page.Applications) != 2 || page.Applications[0].Name != "Downloader" || page.Applications[0].Download != 1_100_000_000 {
		t.Fatalf("ranking includes wrong route or old bucket: %v", page.Applications)
	}
	if len(page.Domains) != 2 || page.Domains[0].Name != "jjdsn.com" || len(page.Ips) != 1 || page.Ips[0].Upload != 110 {
		t.Fatalf("target ranking mismatch: domains=%v ips=%v", page.Domains, page.Ips)
	}
	// The batch is read-only and repeat reads must not accumulate bytes.
	again, err := c.QueryRouteUsage(context.Background(), &pb.RouteHistoryRequest{Window: "today"})
	if err != nil || again.TodayUpload != got.TodayUpload {
		t.Fatalf("repeat: %v %v", again, err)
	}
}

func TestRouteUsageHourDoesNotResetAtMidnight(t *testing.T) {
	h := newTrafficHarness(t)
	local := time.Now().In(time.Local)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
	h.clock = midnight.Add(12 * time.Minute)
	f := trafficFlow("s/midnight", func(f *config.FlowFact) {})
	one, two := h.obs(0, "started", f, 10, 90), h.obs(0, "sample", f, 20, 180)
	one.At, two.At = midnight.Add(-20*time.Minute), midnight.Add(5*time.Minute)
	h.append(h.clock, true, one, two)
	h.settle()
	c := New("test", "test")
	c.traffic = h.tr
	c.history = &historyOwner{clock: func() time.Time { return h.clock }}
	got, err := c.QueryRouteUsage(context.Background(), &pb.RouteHistoryRequest{Window: "today"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Routes) != 1 || got.Routes[0].TodayUpload != 20 || got.Routes[0].HourUpload != 30 {
		t.Fatalf("midnight split: %v", got)
	}
}
