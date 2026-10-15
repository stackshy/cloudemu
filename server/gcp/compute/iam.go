package compute

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcpiam"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// SetIAMStore makes the handler keep instance, disk, image, snapshot and
// instance template policies in s, the store shared with the other GCP
// handlers.
func (h *Handler) SetIAMStore(s gcpiam.Store) { h.iam = s }

// serveIAM answers getIamPolicy, setIamPolicy and testIamPermissions on a
// compute resource that has IAM in real GCE. It returns false for any other
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
	case resourceInstances:
		return func() error { _, err := findInZone(ctx, h.compute, rp.ResourceName, rp.ScopeName); return err }
	case resourceDisks:
		return func() error { _, err := findDiskByName(ctx, h.compute, rp.ResourceName, rp.ScopeName); return err }
	case resourceImages:
		return func() error { _, err := findImageByName(ctx, h.compute, rp.ResourceName); return err }
	case resourceSnapshots:
		return func() error { _, err := findSnapshotByName(ctx, h.compute, rp.ResourceName); return err }
	case resourceTemplates:
		return func() error {
			backend, ok := h.compute.(templateBackend)
			if ok {
				if _, found := findTemplate(backend, rp.Project, rp.ResourceName); found {
					return nil
				}
			}

			return cerrors.Newf(cerrors.NotFound, "The resource '%s' was not found", gcpiam.ComputeName(rp))
		}
	}

	return nil
}

// dropPolicy forgets the policy of a deleted resource, so a resource created
// later under the same name starts with an empty policy.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) dropPolicy(rp gcprest.ResourcePath) {
	h.iam.Delete(gcpiam.ComputeName(rp))
}
