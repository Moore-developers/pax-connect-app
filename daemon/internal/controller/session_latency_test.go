package controller

import (
	"context"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"testing"
	"time"
)

type sessionMeasurementFixture struct {
	fakeTester
	stable bool
}

func (f *sessionMeasurementFixture) MeasureRoute(context.Context, config.RouteConfig, func(config.RouteMeasurement)) (config.RouteMeasurement, error) {
	value := config.RouteMeasurement{Cold: 1200 * time.Millisecond, Duration: 1200 * time.Millisecond, Method: measurement.SessionLatencyMethod, StableSamples: 2, StableError: "latency_samples_incomplete", TestTarget: "google", ExitError: "exit_timeout"}
	if f.stable {
		value.Duration = 190 * time.Millisecond
		value.Stable = value.Duration
		value.StableSamples = 3
		value.StableError = ""
	}
	return value, nil
}
func TestSessionLatencyRPCPersistsFirstStableWithoutExit(t *testing.T) {
	for _, full := range []bool{true, false} {
		t.Run(map[bool]string{true: "full", false: "partial"}[full], func(t *testing.T) {
			c := newTestController(t, &sessionMeasurementFixture{stable: full})
			node := seedLocalRoute(t, c)
			directory, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(directory, 0700); err != nil {
				t.Fatal(err)
			}
			c.ConfigureRouteData(directory, uint32(os.Getuid()))
			repository := config.NewMeasurementStore(directory+"/latest.json", uint32(os.Getuid()))
			c.ConfigureMeasurements(repository)
			events, stop := operationSink(c)
			defer stop()
			ack, err := c.TestRoute(context.Background(), &pb.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if r := awaitResult(t, events, ack.OperationId); !r.Ok {
				t.Fatal(r)
			}
			summary := waitComplete(t, c).Routes[0].TestSummary
			if summary.ColdMs != 1200 || summary.Method != measurement.SessionLatencyMethod || summary.ExitError != nil {
				t.Fatal(summary)
			}
			if full {
				if summary.StableMs != 190 || summary.StableSamples != 3 {
					t.Fatal(summary)
				}
			} else if summary.StableMs != 0 || summary.StableSamples != 2 || summary.StableError != "latency_samples_incomplete" {
				t.Fatal(summary)
			}
			history, err := c.QueryRouteHistory(context.Background(), &pb.RouteHistoryRequest{RouteId: node.Id})
			if err != nil {
				t.Fatal(err)
			}
			connect, latency := false, false
			for _, item := range history.Items {
				if item.Metric == "connect" {
					connect = item.Success && item.Value == 1200 && item.Method == measurement.SessionLatencyMethod
				}
				if item.Metric == "latency" {
					latency = item.Success == full
					if full {
						latency = latency && item.Value == 190
					} else {
						latency = latency && item.Error.GetCode() == "latency_samples_incomplete"
					}
				}
			}
			if !connect || !latency {
				t.Fatal("history conflated first and stable", history)
			}
			doc, err := repository.Load()
			if err != nil {
				t.Fatal(err)
			}
			saved := doc.Records[node.Id].Latency
			if saved == nil || saved.ColdMS != 1200 || saved.StableSamples != summary.StableSamples || saved.StableMS != summary.StableMs {
				t.Fatal("latest not persisted", saved)
			}
			if err = c.query(context.Background(), func(ctx context.Context) { delete(c.routes.testResults, node.Id); c.publishCatalog(ctx) }); err != nil {
				t.Fatal(err)
			}
			c.ConfigureMeasurements(repository)
			if err = c.query(context.Background(), func(ctx context.Context) { c.publishCatalog(ctx) }); err != nil {
				t.Fatal(err)
			}
			reloaded := waitComplete(t, c).Routes[0].TestSummary
			if reloaded.ColdMs != 1200 || reloaded.StableSamples != summary.StableSamples {
				t.Fatal("restore lost independent sample", reloaded)
			}
		})
	}
}

type exitLocationFixture struct {
	fakeTester
	fail bool
}

func (f *exitLocationFixture) LookupRouteExit(context.Context, config.RouteConfig) (config.RouteMeasurement, error) {
	value := config.RouteMeasurement{Cold: time.Second, Duration: 20 * time.Millisecond, Stable: 20 * time.Millisecond, StableSamples: 3, Method: measurement.SessionLatencyMethod}
	if f.fail {
		value.ExitError = "exit_timeout"
	} else {
		value.ExitIP = "8.8.8.8"
		value.ExitLocation = &config.ExitLocation{IP: value.ExitIP, CountryCode: "US", Country: "United States", Region: "California", City: "Mountain View", ISP: "Google", ASN: "AS15169", Source: "ipwho.is", GeoSource: "ipwho.is", At: time.Now(), GeoAt: time.Now()}
	}
	return value, nil
}
func TestExitLocationRPCPersistsHistoryAndRetainsPreviousOnFailure(t *testing.T) {
	fixture := &exitLocationFixture{}
	c := newTestController(t, fixture)
	node := seedLocalRoute(t, c)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c.ConfigureRouteData(root, uint32(os.Getuid()))
	repository := config.NewMeasurementStore(root+"/latest.json", uint32(os.Getuid()))
	c.ConfigureMeasurements(repository)
	events, stop := operationSink(c)
	defer stop()
	measure := func() *pb.RouteExitSummary {
		ack, e := c.TestRoute(context.Background(), &pb.TestRouteRequest{RouteId: node.Id, ExpectedRouteRevision: node.Revision, Kind: "exit"})
		if e != nil {
			t.Fatal(e)
		}
		if awaitResult(t, events, ack.OperationId).Ok == fixture.fail {
			t.Fatal("exit result did not report its own failure")
		}
		return waitComplete(t, c).Routes[0].ExitSummary
	}
	first := measure()
	if first.Location == nil || first.Location.City != "Mountain View" || first.Location.Ip != first.Ip || first.Location.ConfigRevision == 0 {
		t.Fatal(first)
	}
	fixture.fail = true
	second := measure()
	if second.Ip != "" || second.Location != nil || second.LastLocation == nil || second.LastLocation.City != "Mountain View" || second.ErrorCode != "exit_timeout" {
		t.Fatal("old location became fresh or lost", second)
	}
	history, e := c.QueryRouteHistory(context.Background(), &pb.RouteHistoryRequest{RouteId: node.Id, Metric: "exit"})
	if e != nil {
		t.Fatal(e)
	}
	if len(history.Items) != 2 || history.Items[0].Success || history.Items[1].ExitLocation == nil {
		t.Fatal(history)
	}
	latest, e := repository.Load()
	if e != nil {
		t.Fatal(e)
	}
	if latest.Records[node.Id].Exit.LastExitLocation.City != "Mountain View" {
		t.Fatal("last location lost on disk")
	}
}
