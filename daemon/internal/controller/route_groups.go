package controller

import (
	"context"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"strconv"
)

// SaveRouteGroups atomically replaces the ordered custom groups, leaving all routes unchanged.
func (c *Controller) SaveRouteGroups(ctx context.Context, req *paxv1.SaveRouteGroupsRequest) (*paxv1.OperationAck, error) {
	return c.mutationRequest(ctx, "SaveRouteGroups", req, req.ClientId, req.RequestId, func() (*paxv1.OperationAck, error) {
		return c.admitNamedRoute(ctx, "route.groups", "管理线路分组", func(ctx context.Context) error {
			next := c.routes.catalog.Clone()
			if req.ExpectedCatalogRevision != next.Revision {
				return &productError{code: "catalog_stale", params: []string{strconv.FormatUint(next.Revision, 10)}}
			}
			groups := make([]config.RouteGroup, 0, len(req.Groups))
			for _, g := range req.Groups {
				groups = append(groups, config.RouteGroup{ID: g.GetId(), Name: g.GetName(), MemberIDs: append([]string(nil), g.GetMemberIds()...)})
			}
			if err := next.ValidateGroups(groups); err != nil {
				return err
			}
			next.Groups = groups
			next.Revision++
			return c.saveCatalog(ctx, next)
		})
	})
}
