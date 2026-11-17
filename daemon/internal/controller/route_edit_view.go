package controller

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/singbox"
)

// GetRouteEditView never moves existing credentials across the daemon boundary.
func (c *Controller) GetRouteEditView(ctx context.Context, req *pb.GetRouteEditorRequest) (*pb.RouteEditView, error) {
	var result *pb.RouteEditView
	var failure error
	err := c.query(ctx, func(ctx context.Context) {
		record, ok := c.routes.catalog.Find(req.RouteId)
		if !ok {
			failure = status.Error(codes.NotFound, "route_not_found")
			return
		}
		resolved, e := c.resolvedRecord(ctx, record)
		if e != nil {
			failure = status.Error(codes.FailedPrecondition, tlsErrorCode(e))
			return
		}
		result = &pb.RouteEditView{RouteId: req.RouteId, Revision: record.Revision, ConfigRevision: connectionVersion(resolved, resolved.Config.TLSResolved), Name: record.Config.Name, Hidden: record.Hidden, SourceRemoved: record.SourceRemoved}
		if record.Chain != nil {
			result.Chain = &pb.ChainRoute{FrontId: record.Chain.FrontID, FinalId: record.Chain.FinalID}
			return
		}
		raw := config.DocumentForRoute(record.Config)
		if e := singbox.ValidateRouteDocument(ctx, raw); e != nil {
			failure = status.Error(codes.FailedPrecondition, "redaction_unsupported")
			return
		}
		document, credentials, e := config.RedactRouteDocument(raw)
		if e != nil {
			failure = status.Error(codes.FailedPrecondition, "redaction_unsupported")
			return
		}
		result.DocumentJson = string(document)
		for _, item := range credentials {
			result.Credentials = append(result.Credentials, &pb.RouteCredential{Path: item.Path, Present: item.Present})
		}
	})
	if err != nil {
		return nil, err
	}
	return result, failure
}

// SaveRouteEdit owns CAS, secret preservation, validation and commit in one queue turn.
func (c *Controller) SaveRouteEdit(ctx context.Context, req *pb.SaveRouteEditRequest) (*pb.OperationAck, error) {
	admit := func() (*pb.OperationAck, error) {
		outcome := &pb.RouteSaveResult{}
		return c.admitRouteOperation(ctx, &routeOperation{label: "编辑线路", result: outcome, apply: func(ctx context.Context) error {
			if strings.TrimSpace(req.Name) == "" {
				return errors.New("route_invalid")
			}
			var previous []byte
			if req.RouteId != "" {
				old, ok := c.routes.catalog.Find(req.RouteId)
				if !ok {
					return errors.New("route_not_found")
				}
				if old.Chain != nil {
					return errors.New("chain_edit_required")
				}
				if old.Revision != req.ExpectedRevision {
					return &productError{code: "route_stale", params: []string{revisionText(old.Revision)}}
				}
				previous = config.DocumentForRoute(old.Config)
			} else if req.ExpectedRevision != 0 {
				return errors.New("route_invalid")
			}
			raw, err := mergeRouteEditSecrets(req.DocumentJson, previous, req.SecretUpdates)
			if err != nil {
				return err
			}
			p, err := config.RouteFromDocument(raw, req.RouteId, strings.TrimSpace(req.Name))
			if err != nil {
				return err
			}
			if err = singbox.ValidateRouteDocument(ctx, p.Document); err != nil {
				return err
			}
			if err := c.checkDocumentTLS(ctx, p, previous); err != nil {
				return err
			}
			next := c.routes.catalog.Clone()
			if req.RouteId == "" {
				outcome.RouteId, outcome.Created, err = next.Add(p)
			} else {
				err = next.Update(req.RouteId, req.ExpectedRevision, p)
				outcome.RouteId = req.RouteId
			}
			if err != nil {
				return err
			}
			if err = c.saveCatalog(ctx, next); err != nil {
				return err
			}
			outcome.CatalogRevision = next.Revision
			if saved, ok := next.Find(outcome.RouteId); ok {
				outcome.RouteRevision = saved.Revision
			}
			return nil
		}})
	}
	if req.Context != nil {
		return c.contextRequest(ctx, "route.save", req, req.Context, admit)
	}
	return admit()
}

func revisionText(v uint64) string { return strconv.FormatUint(v, 10) }

// A changed insecure hint can follow an explicit policy choice for the same
// identity, but cannot carry skip consent onto a different server.
func (c *Controller) checkDocumentTLS(ctx context.Context, p config.RouteConfig, previous []byte) error {
	if len(previous) == 0 {
		if p.TLSSkipSuggested {
			return errors.New("tls_confirmation_required")
		}
		return nil
	}
	old, err := config.RouteFromDocument(previous, p.ID, p.Name)
	if err != nil {
		return err
	}
	sameIdentity := config.TLSIdentity(old) == config.TLSIdentity(p)
	if p.TLSSkipSuggested == old.TLSSkipSuggested && (!p.TLSSkipSuggested || sameIdentity) {
		return nil
	}
	if c.tls == nil {
		return errors.New("tls_confirmation_required")
	}
	view, err := c.tls.Resolve(ctx, old)
	if err != nil {
		return err
	}
	if !view.UserOverride || p.TLSSkipSuggested && (!sameIdentity || view.Mode != config.TLSSkip) || !p.TLSSkipSuggested && view.Mode != config.TLSStrict {
		return errors.New("tls_confirmation_required")
	}
	return nil
}

func mergeRouteEditSecrets(document string, previous []byte, items []*pb.RouteSecretUpdate) ([]byte, error) {
	updates := make([]config.DocumentSecretUpdate, 0, len(items))
	for _, item := range items {
		if item == nil {
			return nil, errors.New("secret_update_invalid")
		}
		update := config.DocumentSecretUpdate{Path: item.Path}
		switch change := item.Change.(type) {
		case *pb.RouteSecretUpdate_Value:
			update.Value = &change.Value
		case *pb.RouteSecretUpdate_Clear:
			update.Clear = change.Clear
		default:
			return nil, errors.New("secret_update_invalid")
		}
		updates = append(updates, update)
	}
	return config.MergeRouteSecrets([]byte(document), previous, updates)
}
