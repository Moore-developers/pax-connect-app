package controller

import (
	"context"
	"crypto/sha256"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"time"
)

type routeRequest struct {
	hash      [32]byte
	operation string
	at        time.Time
}

// documentRequest retains bounded request digests, never documents or credentials, for one daemon lifetime.
func (c *Controller) documentRequest(ctx context.Context, method string, req *paxv1.RouteEditRequest, admit func() (*paxv1.OperationAck, error)) (*paxv1.OperationAck, error) {
	if req.DocumentJson != "" && (req.Protocol != "" || req.Server != "" || req.Port != 0 || req.Transport != "" || req.Security != "" || req.ServerName != "" || req.Uuid != "" || req.Password != "" || req.ReplaceCredentials || req.WsHost != "" || req.WsPath != "" || len(req.Alpn) > 0 || req.Fingerprint != "" || req.RealityPublicKey != "" || req.RealityShortId != "" || req.Flow != "" || req.AlterId != 0 || req.VmessSecurity != "" || req.ObfsPassword != "" || req.TlsSkipSuggested) {
		return nil, status.Error(codes.InvalidArgument, "route_invalid")
	}
	return c.mutationRequest(ctx, method, req, req.ClientId, req.RequestId, admit)
}

// mutationRequest binds a bounded digest and outcome to the client's request ID.
func (c *Controller) mutationRequest(_ context.Context, method string, req proto.Message, clientID, requestID string, admit func() (*paxv1.OperationAck, error)) (*paxv1.OperationAck, error) {
	if requestID == "" {
		return admit()
	}
	if len(requestID) > 128 || len(clientID) > 128 {
		return nil, status.Error(codes.InvalidArgument, "request_invalid")
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "request_invalid")
	}
	hash := sha256.Sum256(append([]byte(method), encoded...))
	key := clientID + "/" + requestID
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	if c.routeRequests == nil {
		c.routeRequests = map[string]routeRequest{}
	}
	now := time.Now()
	for k, v := range c.routeRequests {
		if now.Sub(v.at) > 10*time.Minute {
			c.forgetRouteRequest(k)
		}
	}
	if old, ok := c.routeRequests[key]; ok {
		if old.hash != hash {
			return nil, status.Error(codes.InvalidArgument, "request_conflict")
		}
		c.mu.Lock()
		if result := c.routeRequestResults[old.operation]; result != nil {
			c.snapshotSeq++
			c.current.SnapshotVersion = c.snapshotSeq
			c.fanoutLocked(&paxv1.WatchEvent{Seq: c.snapshotSeq, SnapshotVersion: c.snapshotSeq, Payload: &paxv1.WatchEvent_Operation{Operation: result}})
		}
		c.mu.Unlock()
		return &paxv1.OperationAck{OperationId: old.operation}, nil
	}
	ack, err := admit()
	if err != nil {
		return nil, err
	}
	if len(c.routeRequests) >= 256 {
		var oldestKey string
		var oldest time.Time
		for k, v := range c.routeRequests {
			if oldestKey == "" || v.at.Before(oldest) {
				oldestKey = k
				oldest = v.at
			}
		}
		c.forgetRouteRequest(oldestKey)
	}
	c.mu.Lock()
	if c.routeRequestResults == nil {
		c.routeRequestResults = map[string]*paxv1.OperationResult{}
	}
	c.routeRequestResults[ack.OperationId] = c.completedOperations[ack.OperationId]
	c.mu.Unlock()
	c.routeRequests[key] = routeRequest{hash: hash, operation: ack.OperationId, at: now}
	return ack, nil
}

// requestMu is held by the caller; final outcomes expire with their request digest.
func (c *Controller) forgetRouteRequest(key string) {
	operation := c.routeRequests[key].operation
	delete(c.routeRequests, key)
	c.mu.Lock()
	delete(c.routeRequestResults, operation)
	c.mu.Unlock()
}
