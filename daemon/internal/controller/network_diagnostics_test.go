package controller

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
)

type fakeNetworkDiagnoser struct {
	fakeTester
	entered chan struct{}
	release chan struct{}
}

func (f *fakeNetworkDiagnoser) DiagnoseNetwork(ctx context.Context, _ config.RouteConfig, _ string) (measurement.NetworkDiagnosticReport, config.RouteMeasurement, error) {
	measurement.ObserveDiagnostic(ctx, measurement.DiagnosticProgress{ProbesCompleted: 2, ProbesTotal: 22, ExitState: "complete", SourcesState: "running"})
	close(f.entered)
	select {
	case <-ctx.Done():
		return measurement.NetworkDiagnosticReport{}, config.RouteMeasurement{}, ctx.Err()
	case <-f.release:
	}
	score := 55
	return measurement.NetworkDiagnosticReport{Score: &score, ScoreModel: "fixture"}, config.RouteMeasurement{}, nil
}

func TestNetworkDiagnosticProgressWhileRunningAndAfterCancellation(t *testing.T) {
	f := &fakeNetworkDiagnoser{entered: make(chan struct{}), release: make(chan struct{})}
	c, _ := diagnosticController(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.DiagnoseNetwork(ctx, &pb.NetworkDiagnosticRequest{Mode: "full", OperationId: "round-one"})
		done <- err
	}()
	<-f.entered
	request := &pb.NetworkDiagnosticRequest{ReadProgress: true, OperationId: "round-one"}
	value, err := c.DiagnoseNetwork(context.Background(), request)
	if err != nil || value.GetProgress().GetProbesCompleted() != 2 || value.GetProgress().Finished {
		t.Fatal(value, err)
	}
	value.Progress.ProbesCompleted = 99
	copy, err := c.DiagnoseNetwork(context.Background(), request)
	if err != nil || copy.Progress.ProbesCompleted != 2 {
		t.Fatal("query mutated owner", copy, err)
	}
	if _, err := c.DiagnoseNetwork(context.Background(), &pb.NetworkDiagnosticRequest{ReadProgress: true, OperationId: "other-round"}); status.Code(err) != codes.NotFound {
		t.Fatal("cross-round progress", err)
	}
	cancel()
	if err := <-done; status.Code(err) != codes.Canceled {
		t.Fatal(err)
	}
	last, err := c.DiagnoseNetwork(context.Background(), request)
	if err != nil || !last.Progress.Finished {
		t.Fatal("cancelled progress stayed active", last, err)
	}
	oldUpdate := c.pathDiagnostics.beginProgress("old-round")
	c.pathDiagnostics.beginProgress("new-round")
	oldUpdate(measurement.DiagnosticProgress{ProbesCompleted: 20, ProbesTotal: 22})
	latest, err := c.pathDiagnostics.readProgress("new-round")
	if err != nil || latest.Progress.ProbesCompleted != 0 {
		t.Fatal("late update crossed round", latest, err)
	}
}
func diagnosticController(t *testing.T, f *fakeNetworkDiagnoser) (*Controller, *atomic.Int32) {
	t.Helper()
	c := newTestController(t, f)
	route := seedLocalRoute(t, c)
	if _, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	c.ConfigurePathDiagnostics(nil)
	epoch := &atomic.Int32{}
	epoch.Store(1)
	c.physical = &physicalOwner{sample: func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Epoch: time.Unix(int64(epoch.Load()), 0).String()}, nil
	}}
	return c, epoch
}
func TestNetworkDiagnosticCancellationReleasesAdmission(t *testing.T) {
	f := &fakeNetworkDiagnoser{entered: make(chan struct{}), release: make(chan struct{})}
	c, _ := diagnosticController(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.DiagnoseNetwork(ctx, &pb.NetworkDiagnosticRequest{Mode: "quick"}); done <- err }()
	<-f.entered
	if _, err := c.DiagnoseNetwork(context.Background(), &pb.NetworkDiagnosticRequest{Mode: "quick"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if status.Code(err) != codes.Canceled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not join probe")
	}
	c.tests.mu.Lock()
	defer c.tests.mu.Unlock()
	if c.tests.bandwidth != "" {
		t.Fatal("cancel leaked bandwidth admission")
	}
}
func TestNetworkDiagnosticChangedNetworkInvalidatesEvidence(t *testing.T) {
	f := &fakeNetworkDiagnoser{entered: make(chan struct{}), release: make(chan struct{})}
	c, epoch := diagnosticController(t, f)
	done := make(chan *pb.NetworkDiagnosticReport, 1)
	go func() {
		r, err := c.DiagnoseNetwork(context.Background(), &pb.NetworkDiagnosticRequest{Mode: "quick"})
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	<-f.entered
	epoch.Store(2)
	close(f.release)
	r := <-done
	if r == nil || !r.ChangedDuringCapture {
		t.Fatal("changed network cannot be current success", r)
	}
}

func TestNetworkContextLabelsSavedIntentAndCandidateRules(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.privacy = &privacyOwner{value: config.NetworkPrivacy{Revision: 3, DNSMode: "strict", WebRTC: true}}
	l := config.NewRoutingLibrary()
	l.Packages[0].Rules = []config.RoutingEntry{
		{ID: "one", Method: "domain", Target: "claude.ai", Destination: "direct", Enabled: true},
		{ID: "two", Method: "domain_suffix", Target: "anthropic.com", Destination: "block", Enabled: true},
	}
	c.routing = &routingLibraryOwner{library: l}
	facts, token, err := c.captureNetworkContext(context.Background())
	if err != nil || token != "3/1" {
		t.Fatal(token, err)
	}
	found := map[string]*pb.NetworkDiagnosticFact{}
	for _, f := range facts {
		found[f.Label] = f
	}
	if found["DNS 模式"].Source != "已保存设置" || found["公网 STUN"].Value != "已开启公网 STUN 出口限制" {
		t.Fatal("policy mislabeled", facts)
	}
	if found["claude.ai"].Source != "已保存候选规则" || !strings.Contains(found["claude.ai"].Value, "直连") || !strings.Contains(found["api.anthropic.com"].Value, "阻断") {
		t.Fatal("lost explicit rule evidence", facts)
	}
}
func TestNetworkDiagnosticPolicyChangeInvalidatesEvidence(t *testing.T) {
	f := &fakeNetworkDiagnoser{entered: make(chan struct{}), release: make(chan struct{})}
	c, _ := diagnosticController(t, f)
	c.privacy = &privacyOwner{value: config.DefaultNetworkPrivacy()}
	done := make(chan *pb.NetworkDiagnosticReport, 1)
	go func() {
		r, err := c.DiagnoseNetwork(context.Background(), &pb.NetworkDiagnosticRequest{Mode: "quick"})
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	<-f.entered
	if err := c.query(context.Background(), func(context.Context) { c.privacy.value.Revision++ }); err != nil {
		t.Fatal(err)
	}
	close(f.release)
	r := <-done
	if r == nil || !r.ChangedDuringCapture {
		t.Fatal("policy capture mixed revisions", r)
	}
}
