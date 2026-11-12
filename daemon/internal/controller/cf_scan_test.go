package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/network"
)

func scanFixture(t *testing.T, repo CFRepository, probe cf.Probe) *Controller {
	t.Helper()
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "", "", "" })
	c.ConfigureCFCandidates()
	c.ConfigureCFWorkspace(repo, func(p network.Physical) cf.Probe {
		if p.Name != "en0" || p.Epoch != "A" {
			t.Error("wrong physical egress")
		}
		return probe
	})
	return c
}
func oldScanData(t *testing.T) []byte {
	t.Helper()
	d := cf.NewDocument()
	now := time.Now().Add(-time.Hour)
	for _, r := range []cf.Record{
		{IP: "104.16.0.1", Network: "A", TCP: cf.Sample{Value: 1, Attempt: now, Rounds: 3}, Download: cf.Sample{Value: 1000, Attempt: now}, Validation: cf.Sample{Value: 1, Attempt: now}, ValidationKey: "old", Location: cf.Location{Country: "old", At: now}},
		{IP: "104.16.0.2", Network: "old", TCP: cf.Sample{Value: 2, Attempt: now, Rounds: 3}},
	} {
		d.Merge(r, time.Now())
	}
	raw, err := cf.Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func successfulScanProbe() cf.Probe {
	return cf.Probe{TCP: func(_ context.Context, ip string) (time.Duration, error) {
		if ip == "104.16.0.1" {
			return 90 * time.Millisecond, nil
		}
		return 0, errors.New("unreachable")
	}}
}
func runScan(t *testing.T, c *Controller) {
	t.Helper()
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "scan"})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, sink, ack.OperationId).Ok {
		t.Fatal("scan failed")
	}
}
func TestCFScanReplacesAllOldDataAndSurvivesReopen(t *testing.T) {
	repo := &memoryCFFile{data: oldScanData(t)}
	c := scanFixture(t, repo, successfulScanProbe())
	runScan(t, c)
	for i := 0; i < 2; i++ {
		if i == 1 {
			c.ConfigureCFWorkspace(repo, func(network.Physical) cf.Probe { return successfulScanProbe() })
		}
		w, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if len(w.Records) != 1 {
			t.Fatalf("scan did not replace old candidates: %d", len(w.Records))
		}
		r := w.Records[0]
		if r.Ip != "104.16.0.1" || r.Tcp.Value != 90 || r.Download.Value != 0 || r.Validation.Value != 0 || r.Location != nil || r.ProofCurrent {
			t.Fatal("old stage data survived", r)
		}
	}
}
func TestCFCompletedEmptyScanReplacesOldData(t *testing.T) {
	repo := &memoryCFFile{data: oldScanData(t)}
	c := scanFixture(t, repo, cf.Probe{TCP: func(context.Context, string) (time.Duration, error) { return 0, errors.New("unreachable") }})
	runScan(t, c)
	c.ConfigureCFWorkspace(repo, func(network.Physical) cf.Probe { return successfulScanProbe() })
	w, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Records) != 0 || w.Phase != "scan_complete" {
		t.Fatal("old rows revived after empty scan", w)
	}
}
