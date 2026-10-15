package loadbalancer

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcpiam"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// SetIAMStore makes the handler keep backend service and service attachment
// policies in s, the store shared with the other GCP handlers.
func (h *Handler) SetIAMStore(s gcpiam.Store) { h.iam = s }

// serveIAM answers getIamPolicy, setIamPolicy and testIamPermissions on a
// backend service or service attachment. It returns false for any other
// request, which then takes the normal route.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveIAM(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) bool {
	if !gcpiam.IsVerb(rp.Action) || rp.ResourceName == "" {
		return false
	}

	exists := h.iamResourceExists(r.Context(), rp)
	if exists == nil {
		return false
	}

	gcpiam.ServeCompute(w, r, rp, h.iam, exists)

	return true
}

// iamResourceExists returns the lookup that confirms the resource rp names
// exists, or nil when its collection has no IAM verbs.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) iamResourceExists(ctx context.Context, rp gcprest.ResourcePath) func() error {
	switch rp.ResourceType {
	case resourceBackendServices:
		return func() error { _, err := h.findTGByName(ctx, rp, rp.ResourceName); return err }
	case resourceServiceAttachments:
		store, ok := h.serviceAttachmentStore()
		if !ok || rp.Scope != gcprest.ScopeRegions {
			return nil
		}

		return func() error { _, err := store.GetGCPServiceAttachment(ctx, rp.ScopeName, rp.ResourceName); return err }
	}

	return nil
}
