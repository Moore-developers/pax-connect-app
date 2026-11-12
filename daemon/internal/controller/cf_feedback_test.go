package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/measurement"
	"pax-connect/daemon/internal/network"
)

func TestCFLatestFailureMatchesOnlyAttemptedDraft(t *testing.T) {
	tester := &fakeTester{duration: 150 * time.Millisecond}
	c := newTestController(t, tester)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := c.CloseProxy(ctx); err != nil {
			t.Error(err)
		}
	}()
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "8.8.8.8", "", "" })
	c.ConfigureCFCandidates()
	c.ConfigureCFWorkspace(&memoryCFFile{}, func(network.Physical) cf.Probe { return cf.Probe{} })
	sink, stop := operationSink(c)
	defer stop()
	raw := `{"type":"trojan","tag":"CF","server":"104.16.0.1","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"identity.test"},"transport":{"type":"ws","path":"/before","headers":{"Host":"identity.test"}}}`
	ack, err := c.AddRoute(context.Background(), &pb.RouteEditRequest{DocumentJson: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, sink, ack.OperationId).Ok {
		t.Fatal("add failed")
	}
	node := waitComplete(t, c).Routes[0]
	request := &pb.CFValidationRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: raw, Ip: "104.16.0.2"}
	first, err := c.ValidateCFCandidate(context.Background(), request)
	if err != nil || first.Error != nil {
		t.Fatal("initial validation", first, err)
	}
	tester.mu.Lock()
	tester.err = &measurement.Failure{Code: "node_connection_timeout", Stage: "node_tcp"}
	tester.mu.Unlock()
	changed := strings.Replace(raw, "/before", "/after", 1)
	request.DocumentJson = changed
	failed, err := c.ValidateCFCandidate(context.Background(), request)
	if err != nil || failed.GetError().GetCode() != "node_connection_timeout" {
		t.Fatal("failure missing", failed, err)
	}
	for _, tc := range []struct {
		document string
		matches  bool
	}{{raw, false}, {changed, true}} {
		workspace, err := c.QueryCFWorkspace(context.Background(), &pb.CFWorkspaceRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: tc.document})
		if err != nil || len(workspace.Records) != 1 {
			t.Fatal("workspace", err)
		}
		record := workspace.Records[0]
		if record.ValidationMatches != tc.matches || record.ProofCurrent || record.Validation.ErrorCode != "node_connection_timeout" {
			t.Fatal("latest attempt attributed incorrectly", record)
		}
	}
}
