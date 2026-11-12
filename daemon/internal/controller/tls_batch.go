package controller

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

type tlsBatchRepository interface {
	ConfirmBatch(context.Context, []config.TLSBatchChange) error
}

// ConfirmRouteTLSBatch only authorizes exact, currently failing certificate identities.
// Transport errors, unknown nodes and old-version results cannot gain skip consent.
func (c *Controller) ConfirmRouteTLSBatch(ctx context.Context, request *paxv1.ConfirmRouteTLSBatchRequest) (*paxv1.OperationAck, error) {
	req := proto.Clone(request).(*paxv1.ConfirmRouteTLSBatchRequest)
	if len(req.Routes) == 0 || len(req.Routes) > 1000 {
		return nil, status.Error(codes.InvalidArgument, "tls_batch_invalid")
	}
	repository, ok := c.tls.(tlsBatchRepository)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "tls_batch_unavailable")
	}
	return c.mutationRequest(ctx, "confirm-tls-batch", req, req.ClientId, req.RequestId, func() (*paxv1.OperationAck, error) {
		return c.admitRouteOperation(ctx, &routeOperation{label: "证书设置", evidence: operationEvidence("证书设置", "", "", ""), apply: func(ctx context.Context) error {
			changes := make([]config.TLSBatchChange, 0, len(req.Routes))
			automatic := make([]automaticRouteTest, 0, len(req.Routes))
			seen := map[string]bool{}
			for _, item := range req.Routes {
				if item == nil || seen[item.RouteId] || item.Mode != paxv1.TLSMode_TLS_MODE_SKIP_VERIFY || !item.ConfirmSkip {
					return errors.New("tls_batch_invalid")
				}
				seen[item.RouteId] = true
				record, ok := c.routes.catalog.Find(item.RouteId)
				if !ok || record.Hidden || record.Config.Security != "tls" {
					return config.ErrTLSStale
				}
				view, err := c.tls.Resolve(ctx, record.Config)
				if err != nil {
					return err
				}
				measured, ok := c.routes.testResults[item.RouteId]
				if !ok || measured.code != "tls_certificate_invalid" || measured.configRevision != connectionVersion(record, view) || view.IdentityToken != item.IdentityToken || view.Revision != item.ExpectedRevision {
					return config.ErrTLSStale
				}
				changes = append(changes, config.TLSBatchChange{Profile: record.Config, Token: item.IdentityToken, Revision: item.ExpectedRevision})
				automatic = append(automatic, automaticRouteTest{id: item.RouteId, revision: record.Revision})
			}
			if err := repository.ConfirmBatch(ctx, changes); err != nil {
				return err
			}
			c.publishCatalog(ctx)
			c.enqueueAutomaticTests(automatic)
			return nil
		}})
	})
}
