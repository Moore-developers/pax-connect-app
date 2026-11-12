package controller

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
)

func TestPathDiagnosticsDoesNotBlockStopAndMarksChangedCapture(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New("test", "")
	catalog := config.NewCatalog()
	profile := config.RouteConfig{NodeProfile: config.NodeProfile{ID: "test", Name: "Fixture", ProtocolName: "trojan", Server: "example.test", Port: 443}, SchemaVersion: 1, Password: "fixture", Security: "tls", Transport: "tcp", ServerName: "example.test"}
	if _, _, err := catalog.Add(profile); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Select(catalog.Routes[0].Config.ID); err != nil {
		t.Fatal(err)
	}
	c.ConfigureCatalog(&memoryCatalog{catalog: catalog, exists: true})
	c.ConfigureOperations(ctx, func() (config.DebugProfile, error) {
		return config.DebugProfile{NodeProfile: config.NodeProfile{ID: "test", ProtocolName: "hysteria2"}}, nil
	})

	c.ConfigureNetworkEpoch(func() (string, error) { return "network", nil })
	entered, release := make(chan struct{}), make(chan struct{})
	c.ConfigurePathDiagnostics(func(ctx context.Context, iface string) network.PathReport {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return network.PathReport{Findings: []network.PathFinding{{Kind: "route", Subject: "1.1.1.1", State: "pax", Value: iface}}}
	})
	t.Cleanup(func() {
		shutdown, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		_ = c.CloseProxy(shutdown)
	})
	if _, err := c.SetProxyEnabled(ctx, &pb.SetProxyEnabledRequest{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	c.ConfigureCoreSession(&fakeCoreSession{})
	result := make(chan *pb.TrafficPathDiagnostics, 1)
	failure := make(chan error, 1)
	go func() {
		report, err := c.GetTrafficPathDiagnostics(ctx, &emptypb.Empty{})
		if err != nil {
			failure <- err
		} else {
			result <- report
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("diagnostics did not start")
	}
	if _, err := c.SetProxyEnabled(ctx, &pb.SetProxyEnabledRequest{}); err != nil {
		t.Fatal(err)
	}
	if s := waitComplete(t, c); s.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("inspection blocked Stop")
	}
	// Restore the exact same interface/route/status values, but a different
	// runtime session. A before/after value comparison alone misses this ABA.
	if _, err := c.SetProxyEnabled(ctx, &pb.SetProxyEnabledRequest{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if s := waitComplete(t, c); s.Status == pb.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("restart failed")
	}
	close(release)
	select {
	case report := <-result:
		if !report.ChangedDuringCapture || report.TunConnections != 0 {
			t.Fatal("mixed runtime evidence presented as current", report)
		}
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("diagnostic did not finish")
	}
}

func TestConnectionDiagnosisRetainsFailedAttemptAndDoesNotProbeOnOpen(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	if _, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	c.ConfigureUserMode(func() {}, 0)
	c.ConfigureCoreSession(&fakeCoreSession{applyError: detailedCoreFailure{}})
	c.ConfigurePathDiagnostics(func(context.Context, string) network.PathReport {
		t.Error("opening diagnosis ran active inspection")
		return network.PathReport{}
	})
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.SetProxyEnabled(context.Background(), &pb.SetProxyEnabledRequest{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, sink, ack.OperationId)
	report, err := c.DiagnoseConnection(context.Background(), &pb.ConnectionDiagnosticRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if report.TargetRouteId != route.Id || report.RunningRouteId != "" || report.OperationId != ack.OperationId || !report.AttemptMatchesTarget {
		t.Fatal("attempt target confused with actual running route", report)
	}
	if len(report.Steps) != 6 || len(report.AttemptSteps) == 0 {
		t.Fatal("missing fixed sections", report)
	}
	found := false
	for _, step := range report.AttemptSteps {
		if step.Kind == "core" && step.State == "fail" && step.Code == "core_profile_start_failed:address_in_use" {
			found = true
		}
	}
	if !found {
		t.Fatal("lost controlled failure cause", report)
	}
}

func TestConnectionDiagnosisFailedSwitchKeepsTargetDistinctFromRunningRoute(t *testing.T) {
	core := &switchingCore{}
	c, a, b := switchFixture(t, core)
	c.ConfigurePathDiagnostics(func(context.Context, string) network.PathReport { return network.PathReport{} })
	core.switchMu.Lock()
	core.fail = errors.New("route_selection_failed")
	core.switchMu.Unlock()
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: b.Id, ExpectedRouteRevision: b.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, sink, ack.OperationId)
	report, err := c.DiagnoseConnection(context.Background(), &pb.ConnectionDiagnosticRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if report.TargetRouteId != b.Id || report.RunningRouteId != a.Id || !report.AttemptMatchesTarget {
		t.Fatal("failed switch diagnosed old route as target", report)
	}
	if report.Steps[1].State != "fail" || report.Steps[1].Code != "route_selection_failed" {
		t.Fatal("lost failed-switch stage", report)
	}
}

type diagnosticTester struct {
	fakeTester
	probed atomic.Bool
	bound  atomic.Bool
}

func (t *diagnosticTester) DiagnoseRoute(_ context.Context, p config.RouteConfig) config.ConnectionProbe {
	t.probed.Store(true)
	t.bound.Store(bytes.Contains(config.DocumentForRoute(p), []byte(`"bind_interface":"en0"`)))
	return config.ConnectionProbe{NodeDuration: time.Millisecond, HTTPSDuration: 2 * time.Millisecond}
}
func TestConnectionDiagnosisDiscardsSuccessfulProbeAfterNetworkChange(t *testing.T) {
	tester := &diagnosticTester{}
	c := newTestController(t, tester)
	route := seedLocalRoute(t, c)
	if _, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Epoch: "A", Index: 1}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "", "", "" })
	var epoch atomic.Value
	epoch.Store("A")
	c.ConfigureNetworkEpoch(func() (string, error) { return epoch.Load().(string), nil })
	c.ConfigurePathDiagnostics(func(context.Context, string) network.PathReport {
		epoch.Store("B")
		return network.PathReport{Findings: []network.PathFinding{{Kind: "route", Subject: "1.1.1.1", State: "other", Value: "en0"}}}
	})
	report, err := c.DiagnoseConnection(context.Background(), &pb.ConnectionDiagnosticRequest{Recheck: true})
	if err != nil {
		t.Fatal(err)
	}
	if !tester.bound.Load() {
		t.Fatal("probe bypassed physical binding")
	}
	if !tester.probed.Load() {
		t.Fatal("recheck did not actually probe")
	}
	if !report.ChangedDuringCapture {
		t.Fatal("changed network accepted", report)
	}
	for _, step := range report.Steps {
		if step.State == "pass" {
			t.Fatal("stale successful evidence escaped", report)
		}
	}
}

type readOnlyTLSPolicy struct{ writes atomic.Int32 }

func (r *readOnlyTLSPolicy) Resolve(context.Context, config.RouteConfig) (config.TLSPolicyView, error) {
	r.writes.Add(1)
	return config.TLSPolicyView{}, errors.New("unexpected write")
}
func (*readOnlyTLSPolicy) Update(context.Context, config.RouteConfig, string, uint64, config.TLSMode, bool) error {
	return errors.New("unexpected update")
}
func (*readOnlyTLSPolicy) Lookup(p config.RouteConfig) (config.TLSPolicyView, error) {
	return config.SourceTLSPolicy(p), nil
}
func TestConnectionDiagnosisDoesNotPersistTLSPolicy(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	if _, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: route.Id, ExpectedRouteRevision: route.Revision}); err != nil {
		t.Fatal(err)
	}
	waitComplete(t, c)
	policy := &readOnlyTLSPolicy{}
	c.ConfigureTLS(policy)
	c.ConfigurePathDiagnostics(nil)
	report, err := c.DiagnoseConnection(context.Background(), &pb.ConnectionDiagnosticRequest{})
	if err != nil || report.GetTargetRouteId() != route.Id || policy.writes.Load() != 0 {
		t.Fatal("opening report wrote TLS state", report, err, policy.writes.Load())
	}
}

type observedSwitchingCore struct {
	*switchingCore
	observation config.CoreObservation
}

func (s *observedSwitchingCore) ObserveCore(context.Context) (config.CoreObservation, error) {
	return s.observation, nil
}
func TestConnectionDiagnosisRecheckDoesNotBorrowOldCoreSelection(t *testing.T) {
	core := &switchingCore{}
	c, a, b := switchFixture(t, core)
	var active config.RouteConfig
	if err := c.query(context.Background(), func(ctx context.Context) { active, _ = c.diagnosticProfile(ctx, a.Id) }); err != nil {
		t.Fatal(err)
	}
	c.ConfigureCoreSession(&observedSwitchingCore{switchingCore: core, observation: config.CoreObservation{Status: "STARTED", Interface: "utun7", SelectedTag: config.RuntimeTag(active)}})
	core.switchMu.Lock()
	core.fail = errors.New("route_selection_failed")
	core.switchMu.Unlock()
	c.ConfigurePathDiagnostics(func(context.Context, string) network.PathReport { return network.PathReport{} })
	sink, stop := operationSink(c)
	defer stop()
	ack, err := c.SelectRoute(context.Background(), &pb.SelectRouteRequest{RouteId: b.Id, ExpectedRouteRevision: b.Revision})
	if err != nil {
		t.Fatal(err)
	}
	awaitResult(t, sink, ack.OperationId)
	report, err := c.DiagnoseConnection(context.Background(), &pb.ConnectionDiagnosticRequest{Recheck: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.TargetRouteId != b.Id || report.RunningRouteId != a.Id || report.Steps[1].State != "fail" || report.Steps[1].Code != "target_not_running" {
		t.Fatal("old active core masqueraded as target success", report)
	}
}

func TestConnectionDiagnosisCancellationReleasesInspector(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	entered, exited := make(chan struct{}), make(chan struct{})
	c.ConfigurePathDiagnostics(func(ctx context.Context, _ string) network.PathReport {
		close(entered)
		<-ctx.Done()
		close(exited)
		return network.PathReport{Partial: true}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.DiagnoseConnection(ctx, &pb.ConnectionDiagnosticRequest{Recheck: true})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("not inspecting")
	}
	cancel()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("inspector survived cancellation")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled diagnosis succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("request survived cancellation")
	}
}
