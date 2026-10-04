package azurearm

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ResourceGroupPurger is the optional capability an Azure provider mock
// exposes for the resource-group delete cascade: it removes every resource
// (and child) recorded under the group.
type ResourceGroupPurger interface {
	PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error
}

// PurgeVia forwards a resource-group purge to drv. A driver without the
// capability is reported as an error rather than silently skipped, so the
// cascade cannot quietly turn into a no-op.
func PurgeVia(ctx context.Context, drv any, subscription, resourceGroup string) error {
	p, ok := drv.(ResourceGroupPurger)
	if !ok {
		return cerrors.Newf(cerrors.Unimplemented, "driver %T cannot purge a resource group", drv)
	}

	return p.PurgeResourceGroup(ctx, subscription, resourceGroup)
}
