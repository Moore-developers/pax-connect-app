package controller

import (
	"context"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/importers"
	"pax-connect/daemon/internal/singbox"
)

// parseRouteBatch is the import boundary for preview and refresh alike. A
// node that fails kernel validation is an incomplete row, not a merge input.
func parseRouteBatch(ctx context.Context, body []byte) (importers.ParsedBatch, error) {
	batch, err := importers.ParseSubscription(body)
	for i := range batch.Items {
		if ctx.Err() != nil {
			break
		}
		item := &batch.Items[i]
		if item.Route == nil {
			continue
		}
		if validation := singbox.ValidateImportedRouteDocument(ctx, config.DocumentForRoute(*item.Route)); validation != nil {
			item.Error = catalogErrorCode(validation)
			item.Route = nil
		}
	}
	if ctx.Err() != nil {
		return importers.ParsedBatch{}, ctx.Err()
	}
	return batch, err
}
