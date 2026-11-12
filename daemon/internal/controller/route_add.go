package controller

import (
	"context"
	"errors"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/singbox"
)

func routeFromEdit(req *paxv1.RouteEditRequest) config.RouteConfig {
	return config.RouteConfig{NodeProfile: config.NodeProfile{ID: req.RouteId, Name: req.Name, ProtocolName: req.Protocol, Server: req.Server, Port: uint16(req.Port)}, SchemaVersion: 1, UUID: req.Uuid, Password: req.Password, Transport: req.Transport, Security: req.Security, ServerName: req.ServerName, WSHost: req.WsHost, WSPath: req.WsPath, ALPN: req.Alpn, Fingerprint: req.Fingerprint, RealityPublicKey: req.RealityPublicKey, RealityShortID: req.RealityShortId, Flow: req.Flow, AlterID: int(req.AlterId), VMessSecurity: req.VmessSecurity, ObfsPassword: req.ObfsPassword, TLSSkipSuggested: req.TlsSkipSuggested}
}
func (c *Controller) AddRoute(ctx context.Context, req *paxv1.RouteEditRequest) (*paxv1.OperationAck, error) {
	return c.documentRequest(ctx, "add", req, func() (*paxv1.OperationAck, error) { return c.addRoute(ctx, req) })
}
func (c *Controller) addRoute(ctx context.Context, req *paxv1.RouteEditRequest) (*paxv1.OperationAck, error) {
	outcome := &paxv1.RouteSaveResult{}
	evidence := operationEvidence("线路操作", "", "", "")
	evidence.Type, evidence.Name = "route.add", "新增线路"
	return c.admitRouteOperation(ctx, &routeOperation{label: "新增线路", evidence: evidence, result: outcome, apply: func(ctx context.Context) error {
		if req.Port > 65535 {
			return errors.New("route_invalid")
		}
		p := routeFromEdit(req)
		if req.DocumentJson != "" {
			var err error
			p, err = config.RouteFromDocument([]byte(req.DocumentJson), "", req.Name)
			if err != nil {
				return err
			}
			if err = singbox.ValidateRouteDocument(ctx, p.Document); err != nil {
				return err
			}
		}
		p.ID = ""
		next := c.routes.catalog.Clone()
		id, created, err := next.Add(p)
		if err != nil {
			return err
		}
		if err := c.saveCatalog(ctx, next); err != nil {
			return err
		}
		outcome.RouteId, outcome.Created = id, created
		return nil
	}})
}
