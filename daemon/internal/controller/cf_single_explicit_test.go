package controller

import (
	"context"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/network"
)

// A route's own CF IP is rarely a member of the last scan. Its explicit latency test must
// still be stored and readable, while scan membership keeps protecting late stage updates.
func TestCFSingleLatencyStoresResultForIPOutsideLastScan(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	c.ConfigureCFWorkspace(&memoryCFFile{}, func(network.Physical) cf.Probe {
		return cf.Probe{TCP: func(context.Context, string) (time.Duration, error) { return 42 * time.Millisecond, nil }}
	})
	c.cf.mu.Lock()
	c.cf.workspace.document.ScanNetwork = "A"
	c.cf.workspace.document.ScanAt = time.Now()
	c.cf.workspace.document.Records = []cf.Record{{IP: "104.16.0.9", Network: "A"}}
	c.cf.mu.Unlock()
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "latency_one", Ips: []string{"104.16.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	workspace, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range workspace.Records {
		if r.Ip == "104.16.0.1" && r.Tcp.Value == 42 {
			found = true
		}
	}
	if !found {
		t.Fatal("explicit single latency result was dropped", workspace)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
}

// The scan is bound to the old network A, but the route's own test runs on the current
// network B. The result is tagged B, stays readable, survives reload, and A is untouched.
func TestCFSingleLatencyOnCurrentNetworkLeavesOtherNetworkScanUntouched(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "B"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	file := &memoryCFFile{}
	probe := func(network.Physical) cf.Probe {
		return cf.Probe{TCP: func(context.Context, string) (time.Duration, error) { return 42 * time.Millisecond, nil }}
	}
	c.ConfigureCFWorkspace(file, probe)
	scanned := cf.Record{IP: "104.16.0.9", Prefix: cf.Prefix("104.16.0.9"), Network: "A", Reachable: true, CheckedAt: time.Now(), TCP: cf.Sample{Value: 7, At: time.Now(), Attempt: time.Now(), Rounds: 3}}
	c.cf.mu.Lock()
	c.cf.workspace.document.ScanNetwork = "A"
	c.cf.workspace.document.ScanAt = time.Now()
	c.cf.workspace.document.Records = []cf.Record{scanned}
	c.cf.mu.Unlock()
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.RunCFTask(context.Background(), &pb.CFTaskRequest{Action: "latency_one", Ips: []string{"104.16.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	workspace, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{Network: "B"})
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Records) != 1 || workspace.Records[0].Ip != "104.16.0.1" || workspace.Records[0].Tcp.Value != 42 || workspace.Records[0].Network != "B" {
		t.Fatal("current-network result missing", workspace)
	}
	c.cf.mu.Lock()
	document := c.cf.workspace.document
	c.cf.mu.Unlock()
	var kept bool
	for _, r := range document.Records {
		kept = kept || (r.Network == "A" && r.IP == "104.16.0.9" && r.TCP.Value == 7)
	}
	if document.ScanNetwork != "A" || !kept {
		t.Fatal("other network scan was rebound or changed", document)
	}
	c.ConfigureCFWorkspace(file, probe)
	restored, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{Network: "B"})
	if err != nil || len(restored.Records) != 1 || restored.Records[0].Tcp.Value != 42 {
		t.Fatal("result did not survive reload", restored, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = c.CloseProxy(ctx); err != nil {
		t.Fatal(err)
	}
}
