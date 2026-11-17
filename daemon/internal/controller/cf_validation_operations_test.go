package controller

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/grpc/status"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	cf "pax-connect/daemon/internal/cfoptimizer"
	"pax-connect/daemon/internal/network"
)

func cfValidationFixture(t *testing.T, tester RouteTester) (*Controller, *pb.StartCFValidationRequest) {
	t.Helper()
	c := newTestController(t, tester)
	c.ConfigurePhysicalEgress(func(context.Context) (network.Physical, error) {
		return network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil
	}, func(context.Context, network.Physical) (string, string, string) { return "", "", "" })
	c.physical.publishLocalSample(network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil)
	c.ConfigureCFCandidates()
	c.ConfigureCFWorkspace(&memoryCFFile{}, func(network.Physical) cf.Probe { return cf.Probe{} })
	sink, stop := operationSink(c)
	defer stop()
	raw := `{"type":"trojan","tag":"CF","server":"104.16.0.1","server_port":443,"password":"PRIVATE-KEEP","tls":{"enabled":true,"server_name":"identity.test"},"transport":{"type":"ws","path":"/before","headers":{"Host":"identity.test"}}}`
	ack, err := c.AddRoute(context.Background(), &pb.RouteEditRequest{DocumentJson: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !awaitResult(t, sink, ack.OperationId).Ok {
		t.Fatal("add failed")
	}
	node := waitComplete(t, c).Routes[0]
	view, err := c.GetRouteEditView(context.Background(), &pb.GetRouteEditorRequest{RouteId: node.Id})
	if err != nil {
		t.Fatal(err)
	}
	return c, &pb.StartCFValidationRequest{RouteId: node.Id, ExpectedRouteRevision: view.Revision, ExpectedConfigRevision: view.ConfigRevision, Ip: "104.16.0.2", Context: operationIdentity(c)}
}
func cfTracked(t *testing.T, c *Controller, id string) *pb.OperationState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		op, err := c.GetOperation(context.Background(), &pb.GetOperationRequest{DaemonInstance: c.instance, Identity: &pb.GetOperationRequest_OperationId{OperationId: id}})
		if err != nil {
			t.Fatal(err)
		}
		if op.FinishedAt != nil {
			return op
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("CF operation did not finish")
	return nil
}
func cfStart(t *testing.T, c *Controller, req *pb.StartCFValidationRequest) *pb.OperationState {
	t.Helper()
	ack, err := c.StartCFValidation(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return cfTracked(t, c, ack.OperationId)
}
func cfSave(t *testing.T, c *Controller, req *pb.StartCFValidationRequest, token string) *pb.OperationState {
	t.Helper()
	ack, err := c.SaveValidatedCFRoute(context.Background(), &pb.SaveValidatedCFRouteRequest{RouteId: req.RouteId, ExpectedRouteRevision: req.ExpectedRouteRevision, ExpectedConfigRevision: req.ExpectedConfigRevision, ValidationId: token, Context: &pb.OperationContext{ClientId: "save", RequestId: req.Context.RequestId, ExpectedDaemonInstance: c.instance}})
	if err != nil {
		t.Fatal(err)
	}
	return cfTracked(t, c, ack.OperationId)
}
func TestCFValidatedSaveFreezesPrivateDraftAndReplays(t *testing.T) {
	c, req := cfValidationFixture(t, &fakeTester{duration: 5 * time.Millisecond})
	view, _ := c.GetRouteEditView(context.Background(), &pb.GetRouteEditorRequest{RouteId: req.RouteId})
	raw := view.DocumentJson
	req.DocumentJson = &raw
	req.SecretUpdates = []*pb.RouteSecretUpdate{{Path: "/transport/path", Change: &pb.RouteSecretUpdate_Value{Value: "/after"}}}
	op := cfStart(t, c, req)
	proof := op.GetCfValidation()
	if op.State != "succeeded" || !proof.Success || proof.ValidationId == "" || proof.DraftDigest == proof.ValidationId {
		t.Fatal(op)
	}
	before, _ := c.GetRouteEditView(context.Background(), &pb.GetRouteEditorRequest{RouteId: req.RouteId})
	if before.Revision != view.Revision || strings.Contains(before.DocumentJson, "/after") {
		t.Fatal("validation saved draft")
	}
	saved := cfSave(t, c, req, proof.ValidationId)
	if saved.State != "succeeded" || saved.GetRouteSave().ConfigRevision == 0 {
		t.Fatal(saved)
	}
	var record string
	if err := c.query(context.Background(), func(context.Context) { v, _ := c.routes.catalog.Find(req.RouteId); record = string(v.Config.Document) }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(record, "PRIVATE-KEEP") || !strings.Contains(record, "/after") || !strings.Contains(record, "104.16.0.2") {
		t.Fatal("frozen draft/secret not saved")
	}
	replay := cfSave(t, c, req, proof.ValidationId)
	if replay.OperationId != saved.OperationId {
		t.Fatal("save replay wrote again")
	}
	if c.current.SelectedRouteId != "" || c.current.Status != pb.ProtectionStatus_PROTECTION_STATUS_OFF {
		t.Fatal("CF save selected/enabled route")
	}
}
func TestCFValidatedSaveRejectsChangedBoundaries(t *testing.T) {
	for _, change := range []string{"route", "config", "network", "network-return", "expired", "draft", "chain"} {
		t.Run(change, func(t *testing.T) {
			c, req := cfValidationFixture(t, &fakeTester{duration: time.Millisecond})
			if change == "chain" {
				sink, stop := operationSink(c)
				defer stop()
				added, err := c.AddRoute(context.Background(), &pb.RouteEditRequest{Name: "final", Protocol: "trojan", Server: "203.0.113.2", Port: 443, Password: "private", Transport: "tcp", Security: "tls", ServerName: "identity.test"})
				if err != nil {
					t.Fatal(err)
				}
				resultFinal := awaitResult(t, sink, added.OperationId)
				if !resultFinal.Ok {
					t.Fatal(resultFinal)
				}
				final := resultFinal.GetRouteSave().RouteId
				viewFinal, err := c.GetRouteEditView(context.Background(), &pb.GetRouteEditorRequest{RouteId: final})
				if err != nil {
					t.Fatal(err)
				}
				ack, err := c.SaveChainRoute(context.Background(), &pb.SaveChainRouteRequest{Name: "chain", FrontId: req.RouteId, FinalId: final, FrontRevision: req.ExpectedRouteRevision, FinalRevision: viewFinal.Revision})
				if err != nil {
					t.Fatal(err)
				}
				result := awaitResult(t, sink, ack.OperationId)
				if !result.Ok {
					t.Fatal(result)
				}
				req.ChainId = result.GetRouteSave().RouteId
				view, err := c.GetRouteEditView(context.Background(), &pb.GetRouteEditorRequest{RouteId: req.ChainId})
				if err != nil {
					t.Fatal(err)
				}
				req.ChainRevision = view.Revision
			}
			op := cfStart(t, c, req)
			proof := op.GetCfValidation()
			if op.State != "succeeded" {
				t.Fatal(op)
			}
			expected := "route_stale"
			switch change {
			case "route", "config", "chain":
				if err := c.query(context.Background(), func(context.Context) {
					for i := range c.routes.catalog.Routes {
						r := &c.routes.catalog.Routes[i]
						if change == "chain" && r.Config.ID != req.RouteId && r.Chain == nil {
							r.ConfigRevision++
						} else if r.Config.ID == req.RouteId {
							switch change {
							case "route":
								r.Revision++
							case "config":
								r.ConfigRevision++
							}
						}
					}
				}); err != nil {
					t.Fatal(err)
				}
			case "network", "network-return":
				c.physical.publishLocalSample(network.Physical{Name: "en1", Index: 2, Epoch: "B"}, nil)
				if change == "network-return" {
					c.physical.publishLocalSample(network.Physical{Name: "en0", Index: 1, Epoch: "A"}, nil)
				}
				expected = "network_changed"
			case "expired":
				c.cf.mu.Lock()
				c.cf.validations[proof.ValidationId].expires = time.Now().Add(-time.Second)
				c.cf.mu.Unlock()
				expected = "cf_validation_expired"
			case "draft":
				next := proto.Clone(req).(*pb.StartCFValidationRequest)
				next.Context = &pb.OperationContext{ClientId: "newdraft", RequestId: req.Context.RequestId, ExpectedDaemonInstance: c.instance}
				view, _ := c.GetRouteEditView(context.Background(), &pb.GetRouteEditorRequest{RouteId: req.RouteId})
				raw := strings.Replace(view.DocumentJson, "/before", "/changed", 1)
				next.DocumentJson = &raw
				next.SecretUpdates = []*pb.RouteSecretUpdate{{Path: "/transport/path", Change: &pb.RouteSecretUpdate_Value{Value: "/changed"}}}
				if cfStart(t, c, next).State != "succeeded" {
					t.Fatal("new draft failed")
				}
				expected = "cf_validation_expired"
			}
			saved := cfSave(t, c, req, proof.ValidationId)
			if saved.State != "failed" || saved.GetError().GetCode() != expected {
				t.Fatal(saved)
			}
			view, _ := c.GetRouteEditView(context.Background(), &pb.GetRouteEditorRequest{RouteId: req.RouteId})
			if strings.Contains(view.DocumentJson, "104.16.0.2") {
				t.Fatal("rejected save mutated route")
			}
		})
	}
}
func TestCFValidationFailureAndCancelCannotSave(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "cancelled"}[cancel], func(t *testing.T) {
			tester := &fakeTester{err: errors.New("failed")}
			if cancel {
				tester.block = true
			}
			c, req := cfValidationFixture(t, tester)
			ack, err := c.StartCFValidation(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if cancel {
				if _, err = c.CancelOperation(context.Background(), &pb.CancelOperationRequest{OperationId: ack.OperationId}); err != nil {
					t.Fatal(err)
				}
			}
			op := cfTracked(t, c, ack.OperationId)
			if op.State == "succeeded" || op.GetCfValidation().GetValidationId() != "" {
				t.Fatal(op)
			}
			saved := cfSave(t, c, req, "missing")
			if saved.GetError().GetCode() != "cf_validation_expired" {
				t.Fatal(saved)
			}
		})
	}
}

func TestCFValidationCapacityRejectsBeforeProbeAndExpires(t *testing.T) {
	c, req := cfValidationFixture(t, &fakeTester{duration: time.Millisecond})
	c.cf.mu.Lock()
	for i := 0; i < 100; i++ {
		c.cf.validations[fmt.Sprint(i)] = &cfValidatedDraft{request: &pb.StartCFValidationRequest{RouteId: fmt.Sprint(i)}, expires: time.Now().Add(time.Minute)}
	}
	c.cf.mu.Unlock()
	if _, err := c.StartCFValidation(context.Background(), req); status.Convert(err).Message() != "cf_busy" {
		t.Fatal(err)
	}
	c.cf.mu.Lock()
	for _, draft := range c.cf.validations {
		draft.expires = time.Now().Add(-time.Second)
	}
	c.cf.mu.Unlock()
	if cfStart(t, c, req).State != "succeeded" {
		t.Fatal("expired capacity not released")
	}
}
