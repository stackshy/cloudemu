package loadbalancer

import (
	"context"
	"net/http"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// compute.serviceAttachments: the producer side of Private Service Connect.
// A regional resource publishing a producer forwarding rule (targetService)
// through NAT subnets, with a connectionPreference and consumer accept/reject
// lists that decide which consumer PSC endpoints connect.
//
// Surface: insert, get, list, patch (JSON merge patch), delete. The records and
// the connection decisions live in the provider (GCPServiceAttachmentStore);
// this handler only shapes the wire. Every mutation answers a DONE
// compute#operation in the shared OperationRegistry.
const resourceServiceAttachments = lbdriver.GCPServiceAttachmentCollection

// serviceAttachmentOutputOnly are members a client may echo back from a get
// but the server owns.
//
//nolint:gochecknoglobals // immutable lookup table, not mutable state
var serviceAttachmentOutputOnly = []string{
	fieldID, fieldKind, fieldSelfLink, fieldCreationTimestamp, "region", "fingerprint",
	"connectedEndpoints", "pscServiceAttachmentId",
}

// serviceAttachmentStore returns the capability, or false when the driver
// does not implement it.
func (h *Handler) serviceAttachmentStore() (lbdriver.GCPServiceAttachmentStore, bool) {
	s, ok := h.lb.(lbdriver.GCPServiceAttachmentStore)

	return s, ok
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeServiceAttachments(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	store, ok := h.serviceAttachmentStore()
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented",
			"load balancer driver has no service attachment store")

		return
	}

	if rp.Scope != gcprest.ScopeRegions {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "serviceAttachments are regional resources")
		return
	}

	switch {
	case rp.ResourceName == "":
		h.routeServiceAttachmentCollection(w, r, rp, store)
	case rp.Action == "":
		h.routeServiceAttachmentItem(w, r, rp, store)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeServiceAttachmentCollection(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPServiceAttachmentStore,
) {
	switch r.Method {
	case http.MethodPost:
		h.insertServiceAttachment(w, r, rp, store)
	case http.MethodGet:
		items, err := store.ListGCPServiceAttachments(r.Context(), rp.ScopeName)
		if err != nil {
			gcprest.WriteCErr(w, err)
			return
		}

		writeGCPResourceList(w, r, rp, items)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeServiceAttachmentItem(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPServiceAttachmentStore,
) {
	switch r.Method {
	case http.MethodGet:
		res, err := store.GetGCPServiceAttachment(r.Context(), rp.ScopeName, rp.ResourceName)
		if err != nil {
			gcprest.WriteCErr(w, err)
			return
		}

		gcprest.WriteJSON(w, http.StatusOK, gcpResourceJSON(res, rp, hostOf(r)))
	case http.MethodPatch:
		h.patchServiceAttachment(w, r, rp, store)
	case http.MethodDelete:
		if err := store.DeleteGCPServiceAttachment(r.Context(), rp.ScopeName, rp.ResourceName); err != nil {
			gcprest.WriteCErr(w, err)
			return
		}

		h.writeServiceAttachmentOp(w, r, rp, rp.ResourceName, opDelete)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertServiceAttachment(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPServiceAttachmentStore,
) {
	var body map[string]any
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	name, _ := body["name"].(string)
	if err := validateRFC1035Name(name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	for _, k := range serviceAttachmentOutputOnly {
		delete(body, k)
	}

	err := store.InsertGCPServiceAttachment(r.Context(), lbdriver.GCPResource{
		Scope:             rp.ScopeName,
		Name:              name,
		ID:                numericID(resourceServiceAttachments + "/" + rp.ScopeName + "/" + name),
		CreationTimestamp: time.Now().UTC().Format(time.RFC3339),
		Body:              body,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeServiceAttachmentOp(w, r, rp, name, opInsert)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchServiceAttachment(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPServiceAttachmentStore,
) {
	var patch map[string]any
	if !gcprest.DecodeJSON(w, r, &patch) {
		return
	}

	for _, k := range serviceAttachmentOutputOnly {
		delete(patch, k)
	}

	err := store.UpdateGCPServiceAttachment(r.Context(), rp.ScopeName, rp.ResourceName,
		func(res *lbdriver.GCPResource) error {
			mergePatch(res.Body, patch)
			res.Body["name"] = res.Name

			return nil
		})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeServiceAttachmentOp(w, r, rp, rp.ResourceName, opPatch)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) writeServiceAttachmentOp(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, name, opType string) {
	gcprest.WriteJSON(w, http.StatusOK, h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName,
		resourceServiceAttachments, name, opType))
}

// attachmentRef splits a ".../regions/{r}/serviceAttachments/{n}" target into
// its region and name.
func attachmentRef(ref string) (region, name string, ok bool) {
	i := strings.Index(ref, pscServiceAttachmentsSegment)
	if i < 0 {
		return "", "", false
	}

	name = ref[i+len(pscServiceAttachmentsSegment):]

	const regionsMarker = "regions/"

	j := strings.LastIndex(ref[:i], regionsMarker)
	if j < 0 || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}

	return ref[j+len(regionsMarker) : i], name, true
}

// validateAttachmentTarget checks a PSC consumer rule's service-attachment
// target: the rule must be regional, in the attachment's region, and the
// attachment must exist — GCP refuses an endpoint for an attachment it cannot
// find.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) validateAttachmentTarget(ctx context.Context, rp gcprest.ResourcePath, target string) error {
	store, ok := h.serviceAttachmentStore()
	if !ok || !strings.Contains(target, pscServiceAttachmentsSegment) {
		return nil
	}

	region, name, parsed := attachmentRef(target)
	if !parsed || rp.Scope != gcprest.ScopeRegions || region != rp.ScopeName {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value for field 'resource.target': '%s'. A service attachment target must be in the forwarding rule's region.",
			target)
	}

	if _, err := store.GetGCPServiceAttachment(ctx, region, name); err != nil {
		if cerrors.IsNotFound(err) {
			return invalidRefErr("target", target, "serviceAttachment")
		}

		return err
	}

	return nil
}
