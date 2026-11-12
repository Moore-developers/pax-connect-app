package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/network"
	"pax-connect/daemon/internal/stats"
	"strings"
	"testing"
	"time"
)

func TestHistoryQueryPagingPersistsAndRejectsStaleCursor(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	route := seedLocalRoute(t, c)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c.ConfigureRouteData(root, uint32(os.Getuid()))
	if err = c.query(context.Background(), func(ctx context.Context) {
		for i := 0; i < 3; i++ {
			c.appendHistory(ctx, config.HistorySample{RouteID: route.Id, Metric: "latency", At: time.Now().Add(time.Duration(i) * time.Millisecond), ConfigRevision: 1, Value: float64(10 + i), Success: true})
		}
	}); err != nil {
		t.Fatal(err)
	}
	req := &pb.RouteHistoryRequest{RouteId: route.Id, Metric: "latency", Limit: 2}
	page, err := c.QueryRouteHistory(context.Background(), req)
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	req.Cursor = page.NextCursor
	second, err := c.QueryRouteHistory(context.Background(), req)
	if err != nil || len(second.Items) != 1 {
		t.Fatal(second, err)
	}
	h, err := config.NewRouteHistoryStore(root+"/route-history.json", uint32(os.Getuid())).Load()
	if err != nil || len(h.Items) != 3 {
		t.Fatal("history not persisted", err)
	}
	_ = c.query(context.Background(), func(ctx context.Context) {
		c.appendHistory(ctx, config.HistorySample{RouteID: route.Id, Metric: "latency", At: time.Now(), ConfigRevision: 2, Value: 42, Success: true})
	})
	if _, err = c.QueryRouteHistory(context.Background(), req); err == nil {
		t.Fatal("stale cursor accepted")
	}
}
func TestSavedChainResolvesFrozenMembersAndRejectsReferencedDelete(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	a := seedLocalRoute(t, c)
	results, stop := operationSink(c)
	defer stop()
	ack, err := c.AddRoute(context.Background(), &pb.RouteEditRequest{Name: "Final", Protocol: "trojan", Server: "203.0.113.9", Port: 443, Password: "fixture", Security: "tls", Transport: "tcp", ServerName: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, results, ack.OperationId); !r.Ok {
		t.Fatal(r)
	}
	snapshot := waitComplete(t, c)
	var b *pb.NodeProfile
	for _, r := range snapshot.Routes {
		if r.Id != a.Id {
			b = r
		}
	}
	// Hysteria2 may be a UDP-capable front for TCP final.
	ack, err = c.SaveChainRoute(context.Background(), &pb.SaveChainRouteRequest{Name: "Pair", FrontId: a.Id, FinalId: b.Id, FrontRevision: a.Revision, FinalRevision: b.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, results, ack.OperationId); !r.Ok {
		t.Fatal(r)
	}
	snapshot = waitComplete(t, c)
	var chain *pb.NodeProfile
	for _, r := range snapshot.Routes {
		if r.Chain != nil {
			chain = r
		}
	}
	if chain == nil || chain.Chain.FrontId != a.Id {
		t.Fatal("missing chain projection")
	}
	if err = c.query(context.Background(), func(ctx context.Context) {
		r, _ := c.routes.catalog.Find(chain.Id)
		resolved, e := c.resolvedRecord(ctx, r)
		if e != nil || resolved.Config.Front == nil || resolved.Config.Front.ID != a.Id {
			t.Error("no frozen front", e)
		}
	}); err != nil {
		t.Fatal(err)
	}
	ack, err = c.DeleteRoute(context.Background(), &pb.DeleteRouteRequest{RouteId: a.Id, ExpectedRevision: a.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, results, ack.OperationId); r.Ok || r.GetError().GetCode() != "route_referenced" {
		t.Fatal(r)
	}
}

type captureChainTester struct{ values chan config.RouteConfig }

func (t *captureChainTester) TestRoute(_ context.Context, p config.RouteConfig) (time.Duration, error) {
	t.values <- p
	return 20 * time.Millisecond, nil
}

func TestChainCFValidatesRequestedHopAndDoesNotMutateCatalog(t *testing.T) {
	tester := &captureChainTester{values: make(chan config.RouteConfig, 4)}
	c := newTestController(t, tester)
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	results, stop := operationSink(c)
	defer stop()
	raw := `{"type":"trojan","tag":"CF","server":"origin.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"identity.test"},"transport":{"type":"ws","path":"/ws","headers":{"Host":"identity.test"}}}`
	for i := 0; i < 2; i++ {
		ack, e := c.AddRoute(context.Background(), &pb.RouteEditRequest{DocumentJson: strings.Replace(raw, "origin.test", fmt.Sprintf("origin%d.test", i), 1)})
		if e != nil {
			t.Fatal(e)
		}
		if !awaitResult(t, results, ack.OperationId).Ok {
			t.Fatal("add")
		}
	}
	nodes := waitComplete(t, c).Routes
	request := &pb.SaveChainRouteRequest{Name: "Pair", FrontId: nodes[0].Id, FinalId: nodes[1].Id, FrontRevision: nodes[0].Revision, FinalRevision: nodes[1].Revision, ClientId: "fixture", RequestId: "chain-once"}
	ack, err := c.SaveChainRoute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, results, ack.OperationId).Ok {
		t.Fatal("chain")
	}
	again, err := c.SaveChainRoute(context.Background(), request)
	if err != nil || again.OperationId != ack.OperationId {
		t.Fatal("chain retry duplicated", err)
	}
	var chain *pb.NodeProfile
	for _, n := range waitComplete(t, c).Routes {
		if n.Chain != nil {
			chain = n
		}
	}
	if chain == nil {
		t.Fatal("missing chain")
	}
	for i, n := range nodes {
		value, e := c.ValidateCFCandidate(context.Background(), &pb.CFValidationRequest{RouteId: n.Id, ExpectedRevision: n.Revision, DocumentJson: raw, Ip: "104.16.0.2", ChainId: chain.Id, ChainRevision: chain.Revision})
		if e != nil || value.Error != nil {
			t.Fatal(value, e)
		}
		p := <-tester.values
		if p.Front == nil {
			t.Fatal("chain bypassed")
		}
		if i == 0 && (p.Front.Server != "104.16.0.2" || p.Server != "origin1.test") {
			t.Fatal("front candidate applied to wrong hop")
		}
		if i == 1 && (p.Server != "104.16.0.2" || p.Front.Server != "origin0.test") {
			t.Fatal("final candidate applied to wrong hop")
		}
		var front map[string]any
		_ = json.Unmarshal(p.Front.Document, &front)
		if front["bind_interface"] != "en0" {
			t.Fatal("front lacks frozen physical interface")
		}
	}
	ack, err = c.UpdateRoute(context.Background(), &pb.RouteEditRequest{RouteId: chain.Id, ExpectedRevision: chain.Revision, DocumentJson: raw})
	if err != nil {
		t.Fatal(err)
	}
	if r := awaitResult(t, results, ack.OperationId); r.Ok || r.GetError().GetCode() != "chain_edit_required" {
		t.Fatal(r)
	}
	for _, n := range waitComplete(t, c).Routes {
		if n.Server == "104.16.0.2" {
			t.Fatal("validation saved draft implicitly")
		}
	}
}

func TestExpiredMeasurementIsNotReturnedWithoutNewTests(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	node := seedLocalRoute(t, c)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	h := config.NewRouteHistory()
	h.Append(config.HistorySample{RouteID: node.Id, Metric: "latency", At: time.Now().Add(-8 * 24 * time.Hour), ConfigRevision: 1, Value: 25, Success: true}, time.Now().Add(-8*24*time.Hour))
	if err = config.NewRouteHistoryStore(root+"/route-history.json", uint32(os.Getuid())).Save(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	c.ConfigureRouteData(root, uint32(os.Getuid()))
	page, err := c.QueryRouteHistory(context.Background(), &pb.RouteHistoryRequest{RouteId: node.Id})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("expired history survived reload", page, err)
	}
}

type failedUsageSession struct {
	fakeCoreSession
	fail bool
}

func (s *failedUsageSession) ReadUsage(context.Context) (config.UsageSnapshot, error) {
	if s.fail {
		return config.UsageSnapshot{}, errors.New("unavailable")
	}
	return config.UsageSnapshot{Session: "s", Sequence: 1, At: time.Now(), ConnectionsKnown: true}, nil
}
func TestFinalUsageReadFailureDoesNotClaimCleanCheckpoint(t *testing.T) {
	c := newTestController(t, &fakeTester{})
	node := seedLocalRoute(t, c)
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	c.ConfigureRouteData(root, uint32(os.Getuid()))
	session := &failedUsageSession{fail: true}
	c.ConfigureCoreSession(session)
	if e = c.query(context.Background(), func(ctx context.Context) {
		c.mu.Lock()
		c.current.Status = pb.ProtectionStatus_PROTECTION_STATUS_DEGRADED
		c.mu.Unlock()
		c.sampleUsage(ctx, true)
	}); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(root + "/route-usage.json")
	if e != nil {
		t.Fatal(e)
	}
	var saved stats.Document
	if e = json.Unmarshal(raw, &saved); e != nil {
		t.Fatal(e)
	}
	if saved.Clean || !saved.Partial {
		t.Fatal("missing final usage presented as clean")
	}
	page, e := c.QueryRouteUsage(context.Background(), &pb.RouteHistoryRequest{RouteId: node.Id})
	if e != nil || page.GetError().GetCode() != "usage_sample_failed" || !page.Partial {
		t.Fatal(page, e)
	}
	if e = c.query(context.Background(), func(ctx context.Context) { session.fail = false; c.sampleUsage(ctx, true) }); e != nil {
		t.Fatal(e)
	}
	page, e = c.QueryRouteUsage(context.Background(), &pb.RouteHistoryRequest{RouteId: node.Id})
	if e != nil || page.Error != nil {
		t.Fatal("recovered sampling retained error", page, e)
	}
}
