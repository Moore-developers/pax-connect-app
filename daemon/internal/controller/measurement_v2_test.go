package controller

import (
	"context"
	"fmt"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryPreferences struct {
	mu     sync.Mutex
	value  config.MeasurementPreferences
	exists bool
}

func (s *memoryPreferences) Load() (config.MeasurementPreferences, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, s.exists, nil
}
func (s *memoryPreferences) Save(_ context.Context, v config.MeasurementPreferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = v
	s.exists = true
	return nil
}
func TestMeasurementPreferencesDefaultEightAndCAS(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	value, err := c.GetMeasurementPreferences(context.Background(), &emptypb.Empty{})
	if err != nil || value.DownloadParallel != 8 || value.LatencyParallel != 32 || value.ExitParallel != 8 {
		t.Fatal(value, err)
	}
	value.Mode = "manual"
	value.DownloadMode = "manual"
	value.DownloadParallel = 4
	saved, err := c.SaveMeasurementPreferences(context.Background(), value)
	if err != nil || saved.Revision != 2 || saved.DownloadParallel != 4 {
		t.Fatal(saved, err)
	}
	if _, err := c.SaveMeasurementPreferences(context.Background(), value); status.Convert(err).Message() != "measurement_preferences_stale" {
		t.Fatal(err)
	}
	saved.DownloadParallel = 9
	if _, err := c.SaveMeasurementPreferences(context.Background(), saved); status.Convert(err).Message() != "measurement_preferences_invalid" {
		t.Fatal(err)
	}
	shutdown, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

type eightDownloads struct {
	fakeTester
	started chan struct{}
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (f *eightDownloads) DownloadRoute(ctx context.Context, _ config.RouteConfig) (config.DownloadMeasurement, error) {
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for old := f.peak.Load(); active > old; old = f.peak.Load() {
		if f.peak.CompareAndSwap(old, active) {
			break
		}
	}
	f.started <- struct{}{}
	select {
	case <-ctx.Done():
		return config.DownloadMeasurement{}, ctx.Err()
	case <-f.release:
	}
	return config.DownloadMeasurement{Bytes: 1024 * 1024, Duration: 6 * time.Second, BytesPerSecond: 1024 * 1024 / 6, End: "sample_window"}, nil
}
func TestEightDownloadWorkersAndPartialWindowSamples(t *testing.T) {
	f := &eightDownloads{started: make(chan struct{}, 8), release: make(chan struct{})}
	c := newTestController(t, f)
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	results, stop := operationSink(c)
	defer stop()
	for i := 0; i < 8; i++ {
		ack, err := c.AddRoute(context.Background(), &paxv1.RouteEditRequest{DocumentJson: fmt.Sprintf(`{"type":"trojan","tag":"Node%d","server":"node%d.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"identity.test"}}`, i, i)})
		if err != nil {
			t.Fatal(err)
		}
		if !awaitResult(t, results, ack.OperationId).Ok {
			t.Fatal("route rejected")
		}
	}
	req := &paxv1.TestRouteRequest{Kind: "download"}
	for _, node := range waitComplete(t, c).Routes {
		req.Targets = append(req.Targets, &paxv1.RouteTestTarget{RouteId: node.Id, ExpectedRouteRevision: node.Revision})
	}
	ack, err := c.TestRoute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.After(time.Second)
	for i := 0; i < 8; i++ {
		select {
		case <-f.started:
		case <-timeout:
			t.Fatal("eight workers not active")
		}
	}
	if f.peak.Load() != 8 {
		t.Fatal(f.peak.Load())
	}
	close(f.release)
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	for _, node := range waitComplete(t, c).Routes {
		if !node.DownloadSummary.GetSuccess() || node.DownloadSummary.Bytes != 1024*1024 || node.DownloadSummary.Parallel != 8 {
			t.Fatal("window sample missing")
		}
	}
	shutdown, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

func TestCancelEightDownloadsDoesNotDispatchNinth(t *testing.T) {
	f := &eightDownloads{started: make(chan struct{}, 8), release: make(chan struct{})}
	c := newTestController(t, f)
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	results, stop := operationSink(c)
	defer stop()
	for i := 0; i < 9; i++ {
		ack, err := c.AddRoute(context.Background(), &paxv1.RouteEditRequest{DocumentJson: fmt.Sprintf(`{"type":"trojan","tag":"Node%d","server":"node%d.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"identity.test"}}`, i, i)})
		if err != nil {
			t.Fatal(err)
		}
		if !awaitResult(t, results, ack.OperationId).Ok {
			t.Fatal("route rejected")
		}
	}
	req := &paxv1.TestRouteRequest{Kind: "download"}
	for _, node := range waitComplete(t, c).Routes {
		req.Targets = append(req.Targets, &paxv1.RouteTestTarget{RouteId: node.Id, ExpectedRouteRevision: node.Revision})
	}
	ack, err := c.TestRoute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.After(time.Second)
	for i := 0; i < 8; i++ {
		select {
		case <-f.started:
		case <-timeout:
			t.Fatal("eight workers not active")
		}
	}
	if f.peak.Load() != 8 {
		t.Fatal(f.peak.Load())
	}
	if _, err := c.CancelOperation(context.Background(), &paxv1.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
		t.Fatal(err)
	}
	result := awaitResult(t, results, ack.OperationId)
	if result.Error.GetCode() != "operation_cancelled" {
		t.Fatal(result)
	}
	select {
	case <-f.started:
		t.Fatal("ninth request was dispatched after cancellation")
	default:
	}
	if len(waitComplete(t, c).DownloadingRouteIds) != 0 {
		t.Fatal("cancel retained running flags")
	}
	shutdown, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

func TestCFCatalogUsesRealValidationWithoutSelection(t *testing.T) {
	c := newTestController(t, &fakeTester{duration: 150 * time.Millisecond})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "network-A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	results, stop := operationSink(c)
	defer stop()
	raw := `{"type":"trojan","tag":"CF","server":"origin.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"identity.test"},"transport":{"type":"ws","path":"/tunnel","headers":{"Host":"identity.test"}}}`
	ack, err := c.AddRoute(context.Background(), &paxv1.RouteEditRequest{DocumentJson: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("add failed")
	}
	node := waitComplete(t, c).Routes[0]
	catalog, err := c.QueryCFCandidates(context.Background(), &emptypb.Empty{})
	if err != nil || len(catalog.Items) == 0 || len(catalog.Items) > 100 {
		t.Fatal(catalog, err)
	}
	for _, candidate := range catalog.Items {
		if candidate.LatencyMs != 0 || candidate.At != nil {
			t.Fatal("seed faked measurement")
		}
	}
	value, err := c.ValidateCFCandidate(context.Background(), &paxv1.CFValidationRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: raw, Ip: catalog.Items[0].Ip})
	if err != nil || value.Error != nil || value.LatencyMs != 150 {
		t.Fatal(value, err)
	}
	snapshot := waitComplete(t, c)
	if snapshot.Status != paxv1.ProtectionStatus_PROTECTION_STATUS_OFF || snapshot.SelectedRouteId != "" || snapshot.Routes[0].Server != "origin.test" {
		t.Fatal("CF validation changed route")
	}
	catalog, err = c.QueryCFCandidates(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range catalog.Items {
		if candidate.LatencyMs != 0 {
			t.Fatal("another draft inherited cached validation")
		}
	}
	shutdown, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalEgressDropsLateOldNetworkResult(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	var epoch atomic.Int32
	epoch.Store(1)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: fmt.Sprint(epoch.Load())}, nil
	}, func(ctx context.Context, p network.Physical) (string, string, string) {
		if p.Epoch == "1" {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return "8.8.8.8", "", ""
	})
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("initial physical probe did not run")
	}
	epoch.Store(2)
	close(release)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := c.GetPhysicalEgress(context.Background(), &emptypb.Empty{})
		if state.Network == "1" && state.Phase == "ready" {
			t.Fatal("old network result became current")
		}
		if state.Network == "2" && state.Phase == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := c.GetPhysicalEgress(context.Background(), &emptypb.Empty{})
	if state.Network != "2" || state.Ipv4 != "8.8.8.8" {
		t.Fatal(state)
	}
	shutdown, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

type stableAssessmentDownloads struct{ fakeTester }

func (f *stableAssessmentDownloads) DownloadRoute(context.Context, config.RouteConfig) (config.DownloadMeasurement, error) {
	return config.DownloadMeasurement{Bytes: 1024 * 1024, Duration: time.Second, BytesPerSecond: 1024 * 1024, End: "sample_window"}, nil
}
func TestAssessmentCompletesAndStoresRecommendation(t *testing.T) {
	c := newTestController(t, &stableAssessmentDownloads{})
	c.ConfigureMeasurementPreferences(&memoryPreferences{}, 32)
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	targets := seedBatch(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.EvaluateMeasurementPreferences(context.Background(), &paxv1.TestRouteRequest{Targets: targets})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, results, ack.OperationId); !result.Ok {
		t.Fatal(result)
	}
	prefs, err := c.GetMeasurementPreferences(context.Background(), &emptypb.Empty{})
	if err != nil || prefs.RecommendedDownload == 0 || prefs.EvaluatedAt == nil || prefs.RecommendationNetwork != "A" {
		t.Fatal(prefs, err)
	}
	if prefs.DownloadParallel != 8 {
		t.Fatal("assessment silently changed manual/default budget")
	}
	shutdown, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := c.CloseProxy(shutdown); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalEgressReadsDoNotTriggerNewProbes(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	var calls atomic.Int32
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Epoch: "stable"}, nil
	}, func(context.Context, network.Physical) (string, string, string) {
		calls.Add(1)
		return "192.0.2.1", "", ""
	})
	deadline := time.Now().Add(4 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatal("initial probe missing")
	}
	for i := 0; i < 1000; i++ {
		_, _ = c.GetPhysicalEgress(context.Background(), &emptypb.Empty{})
	}
	if calls.Load() != 1 {
		t.Fatal("cache reads triggered network")
	}
	cached := &paxv1.PhysicalEgress{Network: "stable", Phase: "ready", Ipv4: "192.0.2.1"}
	if physicalRefresh("stable", cached).Ipv4 != cached.Ipv4 {
		t.Fatal("same-network cache flickered")
	}
	if physicalRefresh("other", cached).Ipv4 != "" {
		t.Fatal("stale IP used on new network")
	}
}
