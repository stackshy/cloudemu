package notificationhubs

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// rgPurger is the optional resource-group purge of the notification driver;
// the Azure notificationhubs.Mock implements it.
type rgPurger interface {
	PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error
}

// PurgeResourceGroup deletes every namespace and hub in subscription/
// resourceGroup, backing the resource-group cascade delete.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	p, ok := h.notif.(rgPurger)
	if !ok {
		return cerrors.Newf(cerrors.Unimplemented, "notification backend %T cannot purge a resource group", h.notif)
	}

	return p.PurgeResourceGroup(ctx, subscription, resourceGroup)
}
