package controller

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func (c *Controller) GetRouteEditor(ctx context.Context, req *paxv1.GetRouteEditorRequest) (*paxv1.RouteEditor, error) {
	var result *paxv1.RouteEditor
	var failure error
	err := c.query(ctx, func(context.Context) {
		record, ok := c.routes.catalog.Find(req.RouteId)
		if !ok {
			failure = status.Error(codes.NotFound, "route_not_found")
			return
		}
		if record.Chain != nil {
			failure = errors.New("chain_edit_required")
			return
		}
		p := record.Config
		view := config.SourceTLSPolicy(p)
		if p.Security == "tls" && c.tls != nil {
			resolved, err := c.tls.Resolve(ctx, p)
			if err != nil {
				failure = status.Error(codes.FailedPrecondition, tlsErrorCode(err))
				return
			}
			view = resolved
		}
		result = &paxv1.RouteEditor{CredentialsSet: p.Password != "" || p.UUID != "", DocumentJson: string(config.DocumentForRoute(p)), ConfigRevision: connectionVersion(record, view), Fields: &paxv1.RouteEditRequest{RouteId: p.ID, ExpectedRevision: record.Revision, Name: p.Name, Protocol: p.ProtocolName, Server: p.Server, Port: uint32(p.Port), Transport: p.Transport, Security: p.Security, ServerName: p.ServerName, WsHost: p.WSHost, WsPath: p.WSPath, Alpn: p.ALPN, Fingerprint: p.Fingerprint, RealityPublicKey: p.RealityPublicKey, RealityShortId: p.RealityShortID, Flow: p.Flow, AlterId: uint32(p.AlterID), VmessSecurity: p.VMessSecurity, TlsSkipSuggested: p.TLSSkipSuggested}}
	})
	if err != nil {
		return nil, err
	}
	return result, failure
}
