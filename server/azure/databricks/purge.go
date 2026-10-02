package databricks

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// rgPurger is the optional resource-group purge of the Databricks driver; the
// Azure databricks.Mock implements it.
type rgPurger interface {
	PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error
}

// PurgeResourceGroup deletes every workspace, with its private endpoint
// connections and VNet peerings, and every access connector in subscription/
// resourceGroup, backing the resource-group cascade delete.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	p, ok := h.dbx.(rgPurger)
	if !ok {
		return cerrors.Newf(cerrors.Unimplemented, "databricks backend %T cannot purge a resource group", h.dbx)
	}

	return p.PurgeResourceGroup(ctx, subscription, resourceGroup)
}
