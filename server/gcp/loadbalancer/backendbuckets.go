package loadbalancer

// Cloud CDN backend buckets (compute.backendBuckets): a global load-balancer
// backend that serves a Cloud Storage bucket, referenced from a url-map's
// defaultService / pathMatchers[].defaultService / pathRules[].service the same
// way a backend service is. Records live in the GCP provider's opaque resource
// store through the GCPBackendBucketStore optional capability, so every field
// the client sent round-trips and the record snapshots with the other LB
// resources.
//
// Surface: insert, get, list, patch (JSON merge patch), update (full replace),
// delete, setEdgeSecurityPolicy, addSignedUrlKey, deleteSignedUrlKey. Every
// mutation answers a DONE compute#operation
// recorded in the shared OperationRegistry, polled at
// /compute/v1/projects/{p}/global/operations/{op}.

import (
	"context"
	"net/http"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	resourceBackendBuckets = lbdriver.GCPBackendBucketCollection

	// actionSetEdgeSecurityPolicy is the POST action that attaches (or, with an
	// empty securityPolicy, detaches) an edge security policy.
	actionSetEdgeSecurityPolicy = "setEdgeSecurityPolicy"

	fieldID                 = "id"
	fieldKind               = "kind"
	fieldSelfLink           = "selfLink"
	fieldCreationTimestamp  = "creationTimestamp"
	fieldUsedBy             = "usedBy"
	fieldParams             = "params"
	fieldBucketName         = "bucketName"
	fieldEdgeSecurityPolicy = "edgeSecurityPolicy"

	opInsert = "insert"
	opPatch  = "patch"
	opUpdate = "update"
	opDelete = "delete"
)

// backendBucketOutputOnly are members a client may echo back from a get but the
// server owns: they are dropped from insert/patch/update bodies. params is
// input-only and never persisted; edgeSecurityPolicy is output-only and set
// solely through setEdgeSecurityPolicy.
//
//nolint:gochecknoglobals // immutable lookup table, not mutable state
var backendBucketOutputOnly = []string{
	fieldID, fieldKind, fieldSelfLink, fieldCreationTimestamp, fieldUsedBy, fieldParams, fieldEdgeSecurityPolicy,
}

// BucketLister is the slice of the storage driver the handler needs to check
// that a backend bucket's bucketName names an existing Cloud Storage bucket.
type BucketLister interface {
	ListBuckets(ctx context.Context) ([]storagedriver.BucketInfo, error)
}

// SetBucketLister wires the Cloud Storage backend so backendBuckets insert,
// patch and update reject a bucketName that names no existing bucket. Without
// it bucketName is only required to be present.
func (h *Handler) SetBucketLister(b BucketLister) { h.buckets = b }

// backendBucketStore returns the backend-bucket capability, or false when the
// backing driver does not implement it (non-GCP driver).
func (h *Handler) backendBucketStore() (lbdriver.GCPBackendBucketStore, bool) {
	s, ok := h.lb.(lbdriver.GCPBackendBucketStore)

	return s, ok
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeBackendBuckets(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	store, ok := h.backendBucketStore()
	if !ok || rp.Scope != gcprest.ScopeGlobal {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "backendBuckets are global only")
		return
	}

	switch {
	case rp.ResourceName == "":
		h.routeBackendBucketCollection(w, r, rp, store)
	case rp.Action != "":
		h.backendBucketAction(w, r, rp, store)
	default:
		h.routeBackendBucketItem(w, r, rp, store)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeBackendBucketCollection(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPBackendBucketStore,
) {
	switch r.Method {
	case http.MethodPost:
		h.insertBackendBucket(w, r, rp, store)
	case http.MethodGet:
		listBackendBuckets(w, r, rp, store)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) routeBackendBucketItem(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPBackendBucketStore,
) {
	switch r.Method {
	case http.MethodGet:
		getBackendBucket(w, r, rp, store)
	case http.MethodPatch:
		h.mutateBackendBucket(w, r, rp, store, true)
	case http.MethodPut:
		h.mutateBackendBucket(w, r, rp, store, false)
	case http.MethodDelete:
		h.deleteBackendBucket(w, r, rp, store)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) backendBucketAction(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPBackendBucketStore,
) {
	if r.Method == http.MethodPost && isSignedURLKeyAction(rp.Action) {
		h.backendBucketSignedURLKey(w, r, rp, store)
		return
	}

	if r.Method != http.MethodPost || rp.Action != actionSetEdgeSecurityPolicy {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented",
			"backendBuckets."+rp.Action+" is not implemented")

		return
	}

	var req struct {
		SecurityPolicy string `json:"securityPolicy"`
	}

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	// The emulator has no securityPolicies resource, so the reference is stored
	// as given; an empty reference detaches the policy.
	err := store.UpdateGCPBackendBucket(r.Context(), rp.ResourceName, func(res *lbdriver.GCPResource) error {
		body := deepCopyMap(res.Body)
		if req.SecurityPolicy == "" {
			delete(body, fieldEdgeSecurityPolicy)
		} else {
			body[fieldEdgeSecurityPolicy] = req.SecurityPolicy
		}

		res.Body = body

		return nil
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeBackendBucketOp(w, r, rp, rp.ResourceName, actionSetEdgeSecurityPolicy)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertBackendBucket(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPBackendBucketStore,
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

	stripOutputOnly(body)
	stripClientKeyNames(body)
	applyBackendBucketDefaults(body)

	if err := h.validateBackendBucket(r.Context(), body, true); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	res := lbdriver.GCPResource{
		Name:              name,
		ID:                numericID(resourceBackendBuckets + "/" + name),
		CreationTimestamp: time.Now().UTC().Format(time.RFC3339),
		Body:              body,
	}

	if err := store.InsertGCPBackendBucket(r.Context(), res); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeBackendBucketOp(w, r, rp, name, opInsert)
}

// mutateBackendBucket serves backendBuckets.patch (merge=true, RFC 7386 JSON
// merge patch: only members present in the body change, nested objects such as
// cdnPolicy merge member-by-member, null removes) and backendBuckets.update
// (merge=false, full replace). The merged result is validated under the store
// lock, so a rejected change leaves the stored record untouched.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) mutateBackendBucket(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPBackendBucketStore, merge bool,
) {
	var body map[string]any
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	stripOutputOnly(body)
	stripClientKeyNames(body)

	// The GCS lookup happens outside the store lock; only a changed bucketName
	// needs it.
	if _, present := body[fieldBucketName]; present {
		if err := h.requireStorageBucket(r.Context(), body[fieldBucketName]); err != nil {
			gcprest.WriteCErr(w, err)
			return
		}
	}

	err := store.UpdateGCPBackendBucket(r.Context(), rp.ResourceName, func(res *lbdriver.GCPResource) error {
		next := nextBackendBucketBody(res.Body, body, merge)
		next["name"] = res.Name
		carryKeyNames(res.Body, next)

		if err := h.validateBackendBucket(r.Context(), next, false); err != nil {
			return err
		}

		res.Body = next

		return nil
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	verb := opUpdate
	if merge {
		verb = opPatch
	}

	h.writeBackendBucketOp(w, r, rp, rp.ResourceName, verb)
}

// nextBackendBucketBody computes the body a patch or update produces from the
// stored body, without touching the stored map.
func nextBackendBucketBody(stored, req map[string]any, merge bool) map[string]any {
	if merge {
		next := deepCopyMap(stored)
		mergePatch(next, req)
		dropTTLsForbiddenByMode(next, req)
		applyBackendBucketDefaults(next)

		return next
	}

	next := deepCopyMap(req)
	// edgeSecurityPolicy is output-only: a full replace keeps the attached policy.
	if policy, ok := stored[fieldEdgeSecurityPolicy]; ok {
		next[fieldEdgeSecurityPolicy] = policy
	}

	applyBackendBucketDefaults(next)

	return next
}

//nolint:gocritic // rp is a request-scoped value
func getBackendBucket(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, store lbdriver.GCPBackendBucketStore) {
	res, err := store.GetGCPBackendBucket(r.Context(), rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, gcpResourceJSON(res, rp, hostOf(r)))
}

//nolint:gocritic // rp is a request-scoped value
func listBackendBuckets(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, store lbdriver.GCPBackendBucketStore) {
	items, err := store.ListGCPBackendBuckets(r.Context())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeGCPResourceList(w, r, rp, items)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteBackendBucket(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPBackendBucketStore,
) {
	// Real GCP refuses to delete a backend bucket a url-map still routes to (400
	// resourceInUseByAnotherResource), so the url-map is never left dangling.
	if ref := h.urlMapRefBackendBucket(r.Context(), rp.ResourceName); ref != "" {
		gcprest.WriteError(w, http.StatusBadRequest, reasonResourceInUse,
			"The "+singularOf(resourceBackendBuckets)+" resource '"+rp.ResourceName+"' is already being used by '"+ref+"'")

		return
	}

	if err := store.DeleteGCPBackendBucket(r.Context(), rp.ResourceName); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeBackendBucketOp(w, r, rp, rp.ResourceName, opDelete)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) writeBackendBucketOp(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, name, opType string) {
	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName, resourceBackendBuckets, name, opType)
	gcprest.WriteJSON(w, http.StatusOK, op)
}

// urlMapRefBackendBucket returns the name of a global url-map whose
// defaultService / pathMatchers[].defaultService / pathRules[].service /
// routeRules[].service names the backend bucket, or "" when none does.
func (h *Handler) urlMapRefBackendBucket(ctx context.Context, name string) string {
	store, ok := h.gcpStore()
	if !ok {
		return ""
	}

	maps, err := store.ListGCPResources(ctx, resourceURLMaps, gcprest.ScopeGlobal)
	if err != nil {
		return ""
	}

	for i := range maps {
		var refs []namedRef

		collectRefs(maps[i].Body, urlMapServiceFields, &refs)

		for _, ref := range refs {
			if isBackendBucketRef(ref.value) && lastPathSegment(ref.value) == name {
				return maps[i].Name
			}
		}
	}

	return ""
}

// requireStorageBucket rejects a bucketName that names no existing Cloud
// Storage bucket. It is a no-op when no storage backend is wired.
func (h *Handler) requireStorageBucket(ctx context.Context, v any) error {
	name, _ := v.(string)
	if name == "" || h.buckets == nil {
		return nil
	}

	buckets, err := h.buckets.ListBuckets(ctx)
	if err != nil {
		return err
	}

	for i := range buckets {
		if buckets[i].Name == name {
			return nil
		}
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"Invalid value for field 'resource.bucketName': '%s'. The referenced Cloud Storage bucket cannot be found.", name)
}

// validateBackendBucket checks a complete backend-bucket body: bucketName is
// required (and, on insert, must name an existing Cloud Storage bucket), and
// compressionMode / cdnPolicy must hold values the real API accepts.
func (h *Handler) validateBackendBucket(ctx context.Context, body map[string]any, checkBucket bool) error {
	bucket, _ := body[fieldBucketName].(string)
	if bucket == "" {
		return cerrors.New(cerrors.InvalidArgument, "Invalid value for field 'resource.bucketName': ''. Required.")
	}

	if checkBucket {
		if err := h.requireStorageBucket(ctx, bucket); err != nil {
			return err
		}
	}

	if err := validateCompressionMode(body["compressionMode"]); err != nil {
		return err
	}

	return validateCDNPolicy(body["cdnPolicy"])
}

// stripOutputOnly drops server-owned members from a request body.
func stripOutputOnly(body map[string]any) {
	for _, k := range backendBucketOutputOnly {
		delete(body, k)
	}
}

// mergePatch applies an RFC 7386 JSON merge patch: objects merge recursively,
// a null member removes the target member, anything else (including arrays)
// replaces it.
func mergePatch(dst, patch map[string]any) {
	for k, v := range patch {
		if v == nil {
			delete(dst, k)
			continue
		}

		sub, isMap := v.(map[string]any)
		cur, curIsMap := dst[k].(map[string]any)

		if isMap && curIsMap {
			mergePatch(cur, sub)
			continue
		}

		dst[k] = deepCopyValue(v)
	}
}

// deepCopyMap copies a decoded JSON object so the copy shares no nested map or
// slice with the original.
func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}

	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = deepCopyValue(t[i])
		}

		return out
	default:
		return v
	}
}
