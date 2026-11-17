package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

func operationIdentity(c *Controller) *pb.OperationContext {
	return &pb.OperationContext{ClientId: "acceptance", RequestId: fmt.Sprintf("%d:12345678-1234-1234-1234-123456789abc", time.Now().UnixMilli()), ExpectedDaemonInstance: c.instance}
}

func TestOperationRecoveryAndIdentity(t *testing.T) {
	c := New("test", "test")
	identity := operationIdentity(c)
	req := &pb.SetProxyEnabledRequest{Context: identity}
	calls := 0
	admit := func() (*pb.OperationAck, error) {
		calls++
		c.completeOperation("job", "test", "")
		return &pb.OperationAck{OperationId: "job"}, nil
	}
	for i := 0; i < 2; i++ {
		if _, err := c.contextRequest(context.Background(), "proxy.set", req, identity, admit); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("replayed %d times", calls)
	}
	got, err := c.GetOperation(context.Background(), &pb.GetOperationRequest{DaemonInstance: c.instance, Identity: &pb.GetOperationRequest_Request{Request: &pb.OperationRequestIdentity{ClientId: identity.ClientId, RequestId: identity.RequestId}}})
	if err != nil || got.State != "succeeded" || got.OwnerClientId != "acceptance" || got.FinishedAt == nil {
		t.Fatalf("%v %v", got, err)
	}
	got.State = "failed"
	req.Enabled = true
	if _, err = c.contextRequest(context.Background(), "proxy.set", req, identity, admit); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("content conflict: %v", err)
	}
	other, err := c.GetOperation(context.Background(), &pb.GetOperationRequest{DaemonInstance: "old-instance", Identity: &pb.GetOperationRequest_OperationId{OperationId: "job"}})
	if err != nil || other.UnknownReason != "daemon_restarted" || other.State != "unknown" {
		t.Fatalf("%v %v", other, err)
	}
	if New("test", "test").instance == c.instance {
		t.Fatal("instance reused")
	}
}

func TestOperationCapacityAndExpiry(t *testing.T) {
	c := New("test", "")
	for i := 0; i < operationCapacity; i++ {
		identity := operationIdentity(c)
		identity.ClientId = fmt.Sprintf("client-%d", i)
		req := &pb.SetProxyEnabledRequest{Context: identity}
		if _, err := c.contextRequest(context.Background(), "proxy.set", req, identity, func() (*pb.OperationAck, error) { return &pb.OperationAck{OperationId: identity.ClientId}, nil }); err != nil {
			t.Fatal(err)
		}
	}
	identity := operationIdentity(c)
	req := &pb.SetProxyEnabledRequest{Context: identity}
	admitted := false
	if _, err := c.contextRequest(context.Background(), "proxy.set", req, identity, func() (*pb.OperationAck, error) { admitted = true; return nil, nil }); status.Code(err) != codes.ResourceExhausted || admitted {
		t.Fatalf("capacity allowed side effect: %v", err)
	}
	c.completeOperation("client-0", "done", "")
	identity.RequestId = "1:12345678-1234-1234-1234-123456789abc"
	if _, err := c.contextRequest(context.Background(), "proxy.set", req, identity, func() (*pb.OperationAck, error) { t.Fatal("old request replayed"); return nil, nil }); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.operations["client-0"].FinishedAt.Seconds -= int64(operationRetention/time.Second) + 1
	c.mu.Unlock()
	got, err := c.GetOperation(context.Background(), &pb.GetOperationRequest{DaemonInstance: c.instance, Identity: &pb.GetOperationRequest_OperationId{OperationId: "client-0"}})
	if err != nil || got.UnknownReason != "expired" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestOperationCancelledBeforeAdmission(t *testing.T) {
	c := New("test", "")
	identity := operationIdentity(c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.contextRequest(ctx, "proxy.set", &pb.SetProxyEnabledRequest{Context: identity}, identity, func() (*pb.OperationAck, error) { t.Fatal("cancelled request admitted"); return nil, nil })
	if status.Code(err) != codes.Canceled {
		t.Fatal(err)
	}
}
