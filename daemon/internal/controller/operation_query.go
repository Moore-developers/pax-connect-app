package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
)

const operationRetention = 10 * time.Minute
const operationCapacity = 256

// GetOperation does not replay a command or infer its outcome from current state.
func (c *Controller) GetOperation(ctx context.Context, req *pb.GetOperationRequest) (*pb.OperationState, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if req.DaemonInstance == "" {
		return nil, status.Error(codes.InvalidArgument, "request_invalid")
	}
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := &pb.OperationState{DaemonInstance: c.instance, State: "unknown", UnknownReason: "not_found"}
	switch id := req.Identity.(type) {
	case *pb.GetOperationRequest_OperationId:
		if !validOperationID(id.OperationId) {
			return nil, status.Error(codes.InvalidArgument, "request_invalid")
		}
		out.OperationId = id.OperationId
	case *pb.GetOperationRequest_Request:
		if id.Request == nil || !validOperationID(id.Request.ClientId) || !validOperationID(id.Request.RequestId) {
			return nil, status.Error(codes.InvalidArgument, "request_invalid")
		}
		out.RequestId = id.Request.RequestId
		if found, ok := c.routeRequests["mcp:"+id.Request.ClientId+"/"+id.Request.RequestId]; ok {
			out.OperationId = found.operation
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "request_invalid")
	}
	if req.DaemonInstance != c.instance {
		out.UnknownReason = "daemon_restarted"
		return out, nil
	}
	if op := c.operations[out.OperationId]; op != nil {
		if op.FinishedAt != nil && time.Since(op.FinishedAt.AsTime()) >= operationRetention {
			out.UnknownReason = "expired"
			return out, nil
		}
		out := proto.Clone(op).(*pb.OperationState)
		// Owner locks are never acquired while holding the snapshot lock.
		c.mu.Unlock()
		c.observeOperation(out)
		c.mu.Lock()
		return out, nil
	}
	// Legacy App operations have real retained outcomes but no admission metadata.
	// Do not fabricate owner/start time; consumers can inspect only tracked requests.
	return out, nil
}

func validOperationID(v string) bool {
	return v != "" && len(v) <= 128 && !strings.ContainsAny(v, "/\\\x00\r\n")
}

func validRequestTime(v string, now time.Time) bool {
	stamp, uuid, ok := strings.Cut(v, ":")
	if !ok || len(uuid) != 36 {
		return false
	}
	for i, ch := range uuid {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", ch) {
			return false
		}
	}
	ms, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return false
	}
	at := time.UnixMilli(ms)
	return !at.Before(now.Add(-operationRetention)) && !at.After(now.Add(30*time.Second))
}

// contextRequest reserves result capacity before admission and deduplicates only
// within this daemon instance. The digest contains no persisted input/credential.
func (c *Controller) contextRequest(ctx context.Context, kind string, req proto.Message, identity *pb.OperationContext, admit func() (*pb.OperationAck, error)) (*pb.OperationAck, error) {
	return c.trackRequest(ctx, kind, req, identity, false, admit)
}

// Legacy App commands retain their request semantics and get an explicit App owner.
func (c *Controller) appRequest(ctx context.Context, kind string, req proto.Message, requestID string, admit func() (*pb.OperationAck, error)) (*pb.OperationAck, error) {
	if requestID == "" {
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return nil, status.Error(codes.Internal, "internal_error")
		}
		requestID = hex.EncodeToString(nonce)
	}
	return c.trackRequest(ctx, kind, req, &pb.OperationContext{ClientId: "app", RequestId: requestID, ExpectedDaemonInstance: c.instance}, true, admit)
}
func (c *Controller) trackRequest(ctx context.Context, kind string, req proto.Message, identity *pb.OperationContext, legacy bool, admit func() (*pb.OperationAck, error)) (*pb.OperationAck, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if !validOperationID(identity.ClientId) || !validOperationID(identity.RequestId) {
		return nil, status.Error(codes.InvalidArgument, "request_invalid")
	}
	if identity.ExpectedDaemonInstance != c.instance {
		return nil, status.Error(codes.FailedPrecondition, "daemon_restarted")
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "request_invalid")
	}
	digest := operationDigest(kind, encoded)
	key := "mcp:" + identity.ClientId + "/" + identity.RequestId
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	now := time.Now()
	c.mu.Lock()
	c.pruneTrackedOperationsLocked(now)
	if old, ok := c.routeRequests[key]; ok {
		c.mu.Unlock()
		if old.hash != digest {
			return nil, status.Error(codes.InvalidArgument, "request_conflict")
		}
		return &pb.OperationAck{OperationId: old.operation}, nil
	}
	if !legacy && !validRequestTime(identity.RequestId, now) {
		c.mu.Unlock()
		return nil, status.Error(codes.InvalidArgument, "request_expired")
	}
	if kind != "operation.cancel" && len(c.operations)+c.operationReservations >= operationCapacity {
		c.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "operation_busy")
	}
	budget := 4096
	if strings.HasPrefix(kind, "profile.") || strings.HasPrefix(kind, "diagnostic.") || strings.HasPrefix(kind, "environment.") {
		budget = 1 << 20
	}
	if strings.HasPrefix(kind, "test-route.") {
		budget = 512 << 10
	}
	switch kind {
	case "SaveRouteGroups":
		budget = proto.Size(req) + 4096
	case "confirm-import", "delete-subscription":
		budget = 1000*132 + 4096
	}
	used := 0
	for _, reserved := range c.operationResultBudgets {
		used += reserved
	}
	if kind != "operation.cancel" && (budget > 1<<20 || used+budget > 32<<20) {
		c.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "operation_busy")
	}
	c.operationReservations++
	c.mu.Unlock()
	ack, err := admit()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.operationReservations--
	if err != nil {
		return nil, err
	}
	op := &pb.OperationState{DaemonInstance: c.instance, OperationId: ack.OperationId, RequestId: identity.RequestId, OwnerClientId: identity.ClientId, Kind: kind, State: "accepted", StartedAt: timestamppb.New(now)}
	switch v := req.(type) {
	case interface{ GetRouteId() string }:
		op.AffectedTarget = v.GetRouteId()
	case interface{ GetSessionId() string }:
		op.AffectedTarget = v.GetSessionId()
	}
	if existing := c.operations[ack.OperationId]; existing == nil {
		c.operations[ack.OperationId] = op
		if c.operationResultBudgets == nil {
			c.operationResultBudgets = map[string]int{}
		}
		c.operationResultBudgets[ack.OperationId] = budget
	}
	if c.routeRequests == nil {
		c.routeRequests = map[string]routeRequest{}
	}
	c.routeRequests[key] = routeRequest{hash: digest, operation: ack.OperationId, at: now}
	if result := c.completedOperations[ack.OperationId]; result != nil {
		c.finishTrackedOperationLocked(ack.OperationId, result)
	}
	return ack, nil
}

// c.mu is held; completion is recorded before publishing the Watch result.
func (c *Controller) finishTrackedOperationLocked(id string, result *pb.OperationResult) {
	op := c.operations[id]
	if op == nil {
		return
	}
	op.State = "succeeded"
	if !result.Ok {
		op.State = "failed"
		op.Error = result.Error
	}
	if result.GetError().GetCode() == "operation_cancelled" {
		op.State = "cancelled"
	}
	op.FinishedAt = timestamppb.Now()
	op.Cancellable = false
	if proto.Size(result) > c.operationResultBudgets[id] {
		op.State = "failed"
		op.Error = &pb.ProductError{Code: "result_too_large"}
		return
	}
	switch {
	case result.Settings != nil:
		op.Result = &pb.OperationState_Settings{Settings: result.Settings}
	case result.CfWorkspace != nil:
		op.Result = &pb.OperationState_CfWorkspace{CfWorkspace: result.CfWorkspace}
	case result.CfValidation != nil:
		if !result.Ok {
			result.CfValidation.Success = false
			result.CfValidation.ValidationId = ""
			result.CfValidation.DraftDigest = ""
			result.CfValidation.ExpiresAt = nil
		}
		op.Result = &pb.OperationState_CfValidation{CfValidation: result.CfValidation}
	case result.RouteSave != nil:
		op.Result = &pb.OperationState_RouteSave{RouteSave: result.RouteSave}
	case result.Proxy != nil:
		op.Result = &pb.OperationState_Proxy{Proxy: result.Proxy}
	case result.Measurements != nil:
		op.Result = &pb.OperationState_Measurements{Measurements: result.Measurements}
	case result.CurrentPath != nil:
		op.Result = &pb.OperationState_CurrentPath{CurrentPath: result.CurrentPath}
	case result.Connection != nil:
		op.Result = &pb.OperationState_Connection{Connection: result.Connection}
	case result.Network != nil:
		op.Result = &pb.OperationState_Network{Network: result.Network}
	case result.AiProfile != nil:
		op.Result = &pb.OperationState_AiProfile{AiProfile: result.AiProfile}
	case result.Environment != nil:
		op.Result = &pb.OperationState_Environment{Environment: result.Environment}
	}
	op.Progress = nil
}

// Both requestMu and mu are held. Live identities remain pinned until terminal.
func (c *Controller) pruneTrackedOperationsLocked(now time.Time) {
	for id, op := range c.operations {
		if op.FinishedAt != nil && now.Sub(op.FinishedAt.AsTime()) >= operationRetention {
			delete(c.operations, id)
			delete(c.operationResultBudgets, id)
			delete(c.completedOperations, id)
			delete(c.routeRequestResults, id)
			for key, request := range c.routeRequests {
				if request.operation == id {
					delete(c.routeRequests, key)
				}
			}
		}
	}
}

func operationDigest(kind string, data []byte) [32]byte {
	return sha256.Sum256(append([]byte(kind), data...))
}
