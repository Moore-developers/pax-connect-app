package controller

import (
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"reflect"
)

func requestClient(legacy string, identity *pb.OperationContext) string {
	if identity != nil {
		return identity.ClientId
	}
	return legacy
}

// catalogOutcome freezes the actual commit facts inside the serial catalog owner.
// MCP selects the fields specified for each tool, never inferring success from a later read.
func catalogOutcome(out *pb.RouteSaveResult, before, after config.Catalog, action string) {
	out.CatalogRevision = after.Revision
	for _, r := range after.Routes {
		old, exists := before.Find(r.Config.ID)
		if !exists {
			out.ImportedRouteIds = append(out.ImportedRouteIds, r.Config.ID)
			out.Created = true
		}
		if !exists || old.Revision != r.Revision {
			out.RouteId = r.Config.ID
			out.RouteRevision = r.Revision
			if exists && !old.Hidden && r.Hidden {
				out.Disposition = "hidden"
			}
		}
	}
	for _, r := range before.Routes {
		if _, exists := after.Find(r.Config.ID); !exists {
			out.DeletedRouteIds = append(out.DeletedRouteIds, r.Config.ID)
			out.RouteId = r.Config.ID
			out.Disposition = "deleted"
		}
	}
	for _, source := range after.Subscriptions {
		old, exists := before.FindSubscription(source.ID)
		if !exists || old.Revision != source.Revision || !reflect.DeepEqual(old.LastAttempt, source.LastAttempt) {
			out.SubscriptionId = source.ID
			out.SourceRevision = source.Revision
			out.Added = uint32(source.LastRefresh.Added)
			out.Updated = uint32(source.LastRefresh.Updated)
			out.Hidden = uint32(source.LastRefresh.Hidden)
			out.Removed = uint32(source.LastRefresh.Removed)
		}
	}
	for _, source := range before.Subscriptions {
		if _, exists := after.FindSubscription(source.ID); !exists {
			out.SubscriptionId = source.ID
		}
	}
	if action != "route.groups" {
		return
	}
	for _, group := range after.Groups {
		out.Groups = append(out.Groups, &pb.RouteGroup{Id: group.ID, Name: group.Name, MemberIds: append([]string(nil), group.MemberIDs...)})
	}
}
