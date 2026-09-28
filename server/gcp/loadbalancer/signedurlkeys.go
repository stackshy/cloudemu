package loadbalancer

import (
	"encoding/base64"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Cloud CDN signed URL keys: backendBuckets and backendServices
// addSignedUrlKey (POST, body {keyName, keyValue}) and deleteSignedUrlKey
// (POST, ?keyName=). The key name is then listed under
// cdnPolicy.signedUrlKeyNames on a Get; the key value is write-only in GCP, so
// it is validated and dropped — never stored, never echoed.
const (
	actionAddSignedURLKey    = "addSignedUrlKey"
	actionDeleteSignedURLKey = "deleteSignedUrlKey"

	fieldSignedURLKeyNames = "signedUrlKeyNames"

	// maxSignedURLKeys is how many signed URL keys one backend may hold.
	maxSignedURLKeys = 3
	// signedURLKeyBytes is the decoded size of a key value (a 128-bit key).
	signedURLKeyBytes = 16

	// bsSignedURLKeysTag holds a backend service's signed URL key names apart
	// from its cdnPolicy tag, so a cdnPolicy patch cannot drop them.
	bsSignedURLKeysTag = "cloudemu:gcpBsSignedUrlKeyNames"
)

// signedURLKeyRequest is the SignedUrlKey body of addSignedUrlKey.
type signedURLKeyRequest struct {
	KeyName  string `json:"keyName"`
	KeyValue string `json:"keyValue"`
}

// validateSignedURLKey checks an addSignedUrlKey body: keyName follows the
// compute name grammar and keyValue is an RFC 4648 §5 base64url 128-bit key.
func validateSignedURLKey(req *signedURLKeyRequest) error {
	if !rfc1035Name.MatchString(req.KeyName) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value for field 'signedUrlKeyResource.keyName': '%s'. Must be a match of regex '(?:[a-z](?:[-a-z0-9]{0,61}[a-z0-9])?)'",
			req.KeyName)
	}

	key, err := base64.URLEncoding.DecodeString(req.KeyValue)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(req.KeyValue)
	}

	if err != nil || len(key) != signedURLKeyBytes {
		return cerrors.New(cerrors.InvalidArgument,
			"Invalid value for field 'signedUrlKeyResource.keyValue'. The key value must be a 128-bit key encoded as RFC 4648 Section 5 base64url.")
	}

	return nil
}

// addKeyName appends name to a backend's key names, refusing a duplicate or a
// fourth key.
func addKeyName(names []string, name string) ([]string, error) {
	for _, n := range names {
		if n == name {
			return nil, cerrors.Newf(cerrors.AlreadyExists, "The signed URL key '%s' already exists", name)
		}
	}

	if len(names) >= maxSignedURLKeys {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value for field 'signedUrlKeyResource.keyName': '%s'. A backend can have at most %d signed URL keys.",
			name, maxSignedURLKeys)
	}

	return append(append([]string(nil), names...), name), nil
}

// removeKeyName drops name from a backend's key names, or returns NotFound.
func removeKeyName(names []string, name string) ([]string, error) {
	out := make([]string, 0, len(names))

	for _, n := range names {
		if n != name {
			out = append(out, n)
		}
	}

	if len(out) == len(names) {
		return nil, cerrors.Newf(cerrors.NotFound, "The signed URL key '%s' was not found", name)
	}

	return out, nil
}

// signedURLKeyChange decodes a signed-URL-key action and returns the function
// that applies it to a backend's current key names. It writes the 400 itself
// and returns nil when the request is malformed.
func signedURLKeyChange(w http.ResponseWriter, r *http.Request, action string) func([]string) ([]string, error) {
	if action == actionDeleteSignedURLKey {
		name := r.URL.Query().Get("keyName")
		if name == "" {
			gcprest.WriteError(w, http.StatusBadRequest, "required", "Required parameter 'keyName' is missing.")
			return nil
		}

		return func(names []string) ([]string, error) { return removeKeyName(names, name) }
	}

	var req signedURLKeyRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return nil
	}

	if err := validateSignedURLKey(&req); err != nil {
		gcprest.WriteCErr(w, err)
		return nil
	}

	return func(names []string) ([]string, error) { return addKeyName(names, req.KeyName) }
}

// isSignedURLKeyAction reports whether action is add/deleteSignedUrlKey.
func isSignedURLKeyAction(action string) bool {
	return action == actionAddSignedURLKey || action == actionDeleteSignedURLKey
}

// backendBucketSignedURLKey serves backendBuckets.addSignedUrlKey and
// deleteSignedUrlKey: the key names live under the stored body's
// cdnPolicy.signedUrlKeyNames, changed under the store lock.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) backendBucketSignedURLKey(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	store lbdriver.GCPBackendBucketStore,
) {
	change := signedURLKeyChange(w, r, rp.Action)
	if change == nil {
		return
	}

	err := store.UpdateGCPBackendBucket(r.Context(), rp.ResourceName, func(res *lbdriver.GCPResource) error {
		body := deepCopyMap(res.Body)

		policy, _ := body["cdnPolicy"].(map[string]any)
		if policy == nil {
			policy = map[string]any{}
		}

		names, err := change(bodyKeyNames(policy))
		if err != nil {
			return err
		}

		setBodyKeyNames(policy, names)
		body["cdnPolicy"] = policy
		res.Body = body

		return nil
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeBackendBucketOp(w, r, rp, rp.ResourceName, rp.Action)
}

// bodyKeyNames reads cdnPolicy.signedUrlKeyNames from a decoded policy.
func bodyKeyNames(policy map[string]any) []string {
	raw, _ := policy[fieldSignedURLKeyNames].([]any)
	out := make([]string, 0, len(raw))

	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

// setBodyKeyNames writes names as cdnPolicy.signedUrlKeyNames, removing the
// member when no key is left.
func setBodyKeyNames(policy map[string]any, names []string) {
	if len(names) == 0 {
		delete(policy, fieldSignedURLKeyNames)
		return
	}

	list := make([]any, 0, len(names))
	for _, n := range names {
		list = append(list, n)
	}

	policy[fieldSignedURLKeyNames] = list
}

// carryKeyNames keeps the stored signed URL key names across a patch or
// update body: they are output-only, set solely through add/deleteSignedUrlKey.
func carryKeyNames(stored, next map[string]any) {
	storedPolicy, _ := stored["cdnPolicy"].(map[string]any)
	names := bodyKeyNames(storedPolicy)

	nextPolicy, _ := next["cdnPolicy"].(map[string]any)
	if nextPolicy == nil {
		if len(names) == 0 {
			return
		}

		nextPolicy = map[string]any{}
		next["cdnPolicy"] = nextPolicy
	}

	setBodyKeyNames(nextPolicy, names)
}

// stripClientKeyNames drops a client-sent cdnPolicy.signedUrlKeyNames.
func stripClientKeyNames(body map[string]any) {
	if policy, ok := body["cdnPolicy"].(map[string]any); ok {
		delete(policy, fieldSignedURLKeyNames)
	}
}

// backendServiceSignedURLKey serves backendServices.addSignedUrlKey and
// deleteSignedUrlKey, keeping the key names in their own tag under the
// driver's patch lock.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) backendServiceSignedURLKey(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	patcher, ok := h.lb.(lbdriver.GCPBackendServicePatcher)
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver cannot patch backend services")
		return
	}

	change := signedURLKeyChange(w, r, rp.Action)
	if change == nil {
		return
	}

	var changeErr error

	err := patcher.PatchGCPBackendService(r.Context(), scopedDriverName(rp, rp.ResourceName), func(tg *lbdriver.TargetGroupInfo) {
		var names []string

		decodeJSONTag(tg.Tags, bsSignedURLKeysTag, &names)

		next, cerr := change(names)
		if cerr != nil {
			changeErr = cerr
			return
		}

		if tg.Tags == nil {
			tg.Tags = map[string]string{}
		}

		if len(next) == 0 {
			delete(tg.Tags, bsSignedURLKeysTag)
		} else {
			encodeJSONTag(tg.Tags, bsSignedURLKeysTag, next)
		}
	})
	if err == nil {
		err = changeErr
	}

	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName,
		resourceBackendServices, rp.ResourceName, rp.Action))
}

// backendServiceKeyNames overlays a backend service's signed URL key names onto
// its response cdnPolicy.
func backendServiceKeyNames(tags map[string]string, resp *backendServiceResponse) {
	var names []string

	decodeJSONTag(tags, bsSignedURLKeysTag, &names)

	if len(names) == 0 {
		if resp.CdnPolicy != nil {
			resp.CdnPolicy.SignedURLKeyNames = nil
		}

		return
	}

	if resp.CdnPolicy == nil {
		resp.CdnPolicy = &cdnPolicy{}
	}

	resp.CdnPolicy.SignedURLKeyNames = names
}

// --- urlMaps.invalidateCache ---

const actionInvalidateCache = "invalidateCache"

// cacheInvalidationRule is the urlMaps.invalidateCache body.
type cacheInvalidationRule struct {
	Path      string   `json:"path"`
	Host      string   `json:"host,omitempty"`
	CacheTags []string `json:"cacheTags,omitempty"`
}

// invalidateURLMapCache serves urlMaps.invalidateCache: the url map must exist
// and the rule must name a path (starting with "/") or cache tags. The emulator
// caches nothing, so a valid request just records a DONE operation.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) invalidateURLMapCache(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	store, ok := h.gcpStore()
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver has no GCP resource store")
		return
	}

	var rule cacheInvalidationRule
	if !gcprest.DecodeJSON(w, r, &rule) {
		return
	}

	if len(rule.CacheTags) == 0 && !strings.HasPrefix(rule.Path, "/") {
		gcprest.WriteCErr(w, cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value for field 'resource.path': '%s'. The path must start with '/'.", rule.Path))

		return
	}

	if _, err := store.GetGCPResource(r.Context(), resourceURLMaps, scopeKeyOf(rp), rp.ResourceName); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName,
		resourceURLMaps, rp.ResourceName, actionInvalidateCache))
}
