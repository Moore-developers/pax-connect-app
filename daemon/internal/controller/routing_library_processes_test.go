package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func TestRoutingCandidatesRankTrafficInBothViews(t *testing.T) {
	ctx := context.Background()
	c, pipeline := newLogsTestController(t, newLogsTestDir(t))
	const app = "/Applications/Example.app"
	const main = app + "/Contents/MacOS/Example"
	const helper = app + "/Contents/Helpers/worker"
	const cli = "/opt/another/worker"
	const other = "/Applications/Other.app/Contents/MacOS/Other"
	now := time.Now()
	c.history.applicationInventory = &historyApplicationInventory{At: now, Apps: []config.RoutingApplication{
		{ID: "example.app", Path: app, Bundle: true},
		{ID: "other.app", Path: "/Applications/Other.app", Bundle: true},
		{ID: "quiet.app", Path: "/Applications/Quiet.app", Bundle: true},
	}}
	batch := config.ObservationBatch{Session: "rule-process", Collecting: true}
	for i, sample := range []struct {
		path     string
		age      time.Duration
		dns      bool
		up, down uint64
	}{
		{main, 2 * time.Hour, false, 1000, 0},
		{cli, 40 * time.Minute, false, 0, 100},
		{helper, 30 * time.Minute, false, 50, 0},
		{other, 25 * time.Minute, false, 0, 60},
		{main, 20 * time.Minute, false, 10, 40},
		{helper, 15 * time.Minute, false, 15, 15},
		{app + "/Contents/MacOS/dns-helper", 10 * time.Minute, true, 1000, 1000},
		{"unknown-worker", 5 * time.Minute, false, 1000, 0},
		{"/Applications/Quiet.app/Contents/MacOS/Quiet", 2 * time.Minute, false, 0, 0},
	} {
		at := now.Add(-sample.age)
		fact := config.FlowFact{
			Key: fmt.Sprintf("rule-process/%d", i), Process: sample.path, StartedAt: at,
			Direction: "direct", Domain: "fixture.test", IP: "192.0.2.1", Transport: "tcp",
		}
		if sample.dns {
			fact.Purpose = "dns"
			fact.DNS = &config.DNSFact{QueryType: "A", Result: "NOERROR"}
		}
		batch.Records = append(batch.Records, config.Observation{
			Session: batch.Session, Sequence: uint64(i + 1), At: at, Kind: "started", Flow: &fact, Upload: sample.up, Download: sample.down,
		})
	}
	batch.HighWater = uint64(len(batch.Records))
	pipeline.observe(batch)
	logs, err := c.logJob()
	if err != nil {
		t.Fatal(err)
	}
	if err = logs.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		return tx.SetMeta(ctx, historystore.MetaRetentionHours, "1")
	}); err != nil {
		t.Fatal(err)
	}
	result, err := c.QueryRoutingProcesses(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.GetPaths(), []string{cli, helper, other, main}) || result.GetLimited() {
		t.Fatalf("processes must be ranked by recorded bytes: %v", result)
	}
	processes, apps := result.GetProcesses(), result.GetApplications()
	if len(processes) != 4 || processes[0].Bytes != 100 || processes[1].Bytes != 80 || processes[3].Bytes != 50 {
		t.Fatalf("wrong per-path traffic: %v", processes)
	}
	if len(apps) != 2 || apps[0].Id != "example.app" || apps[0].Bytes != 130 ||
		apps[1].Id != "other.app" || apps[1].Bytes != 60 {
		t.Fatalf("wrong installed application traffic: %v", apps)
	}
}

func TestRoutingProcessRulePersistsWithoutCandidate(t *testing.T) {
	ctx := context.Background()
	c, _ := routingController(t)
	repo := config.NewRoutingLibraryStore(filepath.Join(newLogsTestDir(t), "rules.json"), uint32(os.Geteuid()))
	c.ConfigureRoutingLibrary(repo, nil)
	c.routing.applicationQuery = func(context.Context) []config.RoutingApplication { return nil }
	const path = "/tmp/Exited Tools/Same Name "
	entry := &pb.RoutingEntry{Id: "exact-process", Method: "process", Target: path, Destination: "block", Enabled: true}
	q := &pb.RoutingMutationRequest{ExpectedRevision: 1, Action: "replace_rules", PackageId: "priority", Rules: []*pb.RoutingEntry{entry}}
	result, err := c.MutateRoutingLibrary(ctx, q)
	if err != nil || len(result.GetPackages()) == 0 || len(result.GetPackages()[0].GetRules()) != 1 ||
		result.GetPackages()[0].GetRules()[0].GetTarget() != path {
		t.Fatalf("process rule did not preserve the exact path: %v (%v)", result, err)
	}
	loaded, exists, err := repo.Load()
	if err != nil || !exists || loaded.Packages[0].Rules[0].Target != path {
		t.Fatalf("process rule did not round-trip: %v (%v)", exists, err)
	}
	entry.Target = "Same Name"
	q.ExpectedRevision = result.Revision
	if _, err = c.MutateRoutingLibrary(ctx, q); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("relative process name accepted: %v", err)
	}
}
