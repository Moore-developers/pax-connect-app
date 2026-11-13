package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/network"
)

// The toolbar names its own displayed IPs. Neither an empty/replaced scan nor a
// failed scan's temporary view may swallow these explicitly requested results.
func TestCFDisplayedTestsPersistOutsideScan(t *testing.T) {
	for _, state := range []string{"empty_scan", "outside_scan", "old_network", "failed_scan"} {
		for _, action := range []string{"check", "locate", "download"} {
			t.Run(state+"/"+action, func(t *testing.T) {
				now := time.Now()
				old := now.Add(-48 * time.Hour)
				document := cf.ScanDocument("A", nil, old)
				if state != "empty_scan" {
					document.ScanNetwork = "old"
					document.Merge(cf.Record{IP: "104.16.0.9", Network: "old", TCP: cf.Sample{Value: 7, Attempt: old, Rounds: 3}}, now)
				}
				if state == "outside_scan" {
					document.ScanNetwork = "A"
				}
				if state == "old_network" {
					document.Merge(cf.Record{IP: "104.16.0.1", Network: "A", TCP: cf.Sample{Value: 8, Attempt: old, Rounds: 3}, Location: cf.Location{City: "old", At: old}}, now)
				}
				raw, err := cf.Encode(document)
				if err != nil {
					t.Fatal(err)
				}
				file := &memoryCFFile{data: raw}
				var failScan atomic.Bool
				failScan.Store(state == "failed_scan")
				var tcp, download, location atomic.Int32
				probe := cf.Probe{
					Validate: func(context.Context) error {
						if failScan.Load() {
							return cf.ErrEnvironment
						}
						return nil
					},
					TCP:      func(context.Context, string) (time.Duration, error) { tcp.Add(1); return 42 * time.Millisecond, nil },
					Download: func(context.Context, string) (float64, error) { download.Add(1); return 1234567, nil },
					Locate: func(context.Context, string) (cf.Location, error) {
						location.Add(1)
						return cf.Location{Country: "Test", City: "TestCity", Source: "fixture", At: time.Now()}, nil
					},
				}
				c := scanFixture(t, file, probe)
				sink, stop := operationSink(c)
				defer stop()
				if state == "failed_scan" {
					ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "scan"})
					if err != nil {
						t.Fatal(err)
					}
					if awaitResult(t, sink, ack.OperationId).Ok {
						t.Fatal("fixture scan unexpectedly succeeded")
					}
					failScan.Store(false)
				}
				started := time.Now()
				ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: action, Ips: []string{"104.16.0.1"}})
				if err != nil {
					t.Fatal(err)
				}
				if result := awaitResult(t, sink, ack.OperationId); !result.Ok {
					t.Fatal(result)
				}
				for _, reopen := range []bool{false, true} {
					if reopen {
						c.ConfigureCFWorkspace(file, func(network.Physical) cf.Probe { return probe })
					}
					w, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
					if err != nil {
						t.Fatal(err)
					}
					var row *pb.CFRecord
					for _, r := range w.Records {
						if r.Ip == "104.16.0.1" {
							row = r
						}
					}
					if row == nil {
						t.Fatalf("%s succeeded but result invisible (reopen=%t)", action, reopen)
					}
					if row.Network != "A" || row.ProofCurrent {
						t.Fatal("wrong network or invented route proof", row)
					}
					switch action {
					case "check":
						if row.GetTcp().GetValue() != 42 || row.Tcp.AttemptedAt.AsTime().Before(started) {
							t.Fatal("latency result dropped", row)
						}
					case "download":
						if row.GetTcp().GetValue() != 42 || row.GetDownload().GetValue() != 1234567 || row.Download.AttemptedAt.AsTime().Before(started) {
							t.Fatal("download result dropped", row)
						}
					case "locate":
						if row.GetLocation().GetCity() != "TestCity" || row.Location.At.AsTime().Before(started) {
							t.Fatal("region result dropped", row)
						}
					}
				}
				saved, _, err := file.Read()
				if err != nil {
					t.Fatal(err)
				}
				got, err := cf.Decode(saved)
				if err != nil {
					t.Fatal(err)
				}
				if got.ScanNetwork != document.ScanNetwork {
					t.Fatal("explicit test rebound scan network")
				}
				for _, r := range document.Records {
					if r.Network != "old" {
						continue
					}
					kept := false
					for _, v := range got.Records {
						if v.IP == r.IP && v.Network == r.Network && v.TCP.Value == r.TCP.Value && v.TCP.Error == r.TCP.Error && v.TCP.Rounds == r.TCP.Rounds && v.TCP.At.Equal(r.TCP.At) && v.TCP.Attempt.Equal(r.TCP.Attempt) {
							kept = true
						}
					}
					if !kept {
						t.Fatal("explicit test changed old network evidence")
					}
				}
				if action == "check" && (tcp.Load() != 3 || location.Load() != 0 || download.Load() != 0) || action == "locate" && (location.Load() != 1 || tcp.Load() != 0 || download.Load() != 0) || action == "download" && (tcp.Load() != 3 || download.Load() != 1 || location.Load() != 0) {
					t.Fatal("wrong test scope", tcp.Load(), download.Load(), location.Load())
				}
			})
		}
	}
}

func TestCFDisplayedDownloadRecordsUnreachableOutsideScan(t *testing.T) {
	file := &memoryCFFile{}
	c := scanFixture(t, file, cf.Probe{
		TCP:      func(context.Context, string) (time.Duration, error) { return 0, errors.New("unreachable") },
		Download: func(context.Context, string) (float64, error) { t.Error("unreachable IP downloaded"); return 0, nil },
	})
	c.cf.mu.Lock()
	c.cf.workspace.document = cf.ScanDocument("old", nil, time.Now())
	c.cf.mu.Unlock()
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "download", Ips: []string{"104.16.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if awaitResult(t, sink, ack.OperationId).Ok {
		t.Fatal("unreachable IP reported success")
	}
	w, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil || len(w.GetRecords()) != 1 {
		t.Fatal("failure result invisible", w, err)
	}
	row := w.Records[0]
	if row.GetTcp().GetErrorCode() == "" || row.GetDownload().GetErrorCode() != "cf_tcp_unreachable" {
		t.Fatal("failure feedback missing", row)
	}
}

func TestCFDisplayedLocationReusesOtherNetworkCache(t *testing.T) {
	document := cf.ScanDocument("old", nil, time.Now())
	cached := cf.Location{City: "CachedCity", Source: "fixture", At: time.Now().Add(-time.Hour)}
	document.Merge(cf.Record{IP: "104.16.0.1", Network: "old", Location: cached}, time.Now())
	raw, err := cf.Encode(document)
	if err != nil {
		t.Fatal(err)
	}
	file := &memoryCFFile{data: raw}
	c := scanFixture(t, file, cf.Probe{Locate: func(context.Context, string) (cf.Location, error) {
		t.Error("valid region cache caused another HTTP lookup")
		return cf.Location{}, nil
	}})
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "locate", Ips: []string{"104.16.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, sink, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	c.ConfigureCFWorkspace(file, func(network.Physical) cf.Probe { return cf.Probe{} })
	w, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil || len(w.GetRecords()) != 1 {
		t.Fatal("cached location not published on current network", w, err)
	}
	row := w.Records[0]
	if row.Network != "A" || row.GetLocation().GetCity() != cached.City || !row.Location.At.AsTime().Equal(cached.At) || row.GetTcp().GetValue() != 0 {
		t.Fatal("cache lost its source time or inherited old network metrics", row)
	}
}
