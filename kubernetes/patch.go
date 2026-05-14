package kubernetes

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// JSON-merge-patch content type (RFC 7396). client-go uses this for most
// Patch() calls when no explicit type is specified, and it's the simplest
// shape to implement: the body is a partial JSON document merged into the
// existing object.
const contentTypeJSONMergePatch = "application/merge-patch+json"

// applyJSONPatch reads a JSON-merge-patch (RFC 7396) body, merges it into
// current, and returns a freshly-decoded T containing the result. The
// stored object is never mutated; callers swap the returned value into
// their store explicitly.
//
// Strategic-merge-patch and JSONPatch (RFC 6902) are intentionally not
// supported in Phase 1; they can be added later if a test scenario needs
// them. JSON-merge-patch covers the common client-go.Patch case.
//
// On any wire-level failure (bad content-type, body read error, merge
// error, or final unmarshal mismatch), the function writes a
// metav1.Status-shaped 400 response to w and returns (nil, false). Callers
// must early-return without touching w.
func applyJSONPatch[T any](w http.ResponseWriter, r *http.Request, current *T) (*T, bool) {
	ct := r.Header.Get("Content-Type")
	if ct != "" && ct != contentTypeJSONMergePatch && ct != contentTypeJSON {
		writeBadRequest(w, "k8s api: only application/merge-patch+json is supported in Wave 2 Phase 1, got "+ct)

		return nil, false
	}

	patch, err := io.ReadAll(r.Body)
	if err != nil {
		writeBadRequest(w, "k8s api: read patch body: "+err.Error())

		return nil, false
	}

	curBytes, err := json.Marshal(current)
	if err != nil {
		writeBadRequest(w, "k8s api: marshal current object: "+err.Error())

		return nil, false
	}

	merged, err := mergePatch(curBytes, patch)
	if err != nil {
		writeBadRequest(w, "k8s api: apply merge patch: "+err.Error())

		return nil, false
	}

	var patched T
	if err := json.Unmarshal(merged, &patched); err != nil {
		writeBadRequest(w, "k8s api: decode patched object: "+err.Error())

		return nil, false
	}

	return &patched, true
}

// mergePatch implements RFC 7396 JSON Merge Patch. We avoid pulling in
// another dependency; the recursive map-merge is small enough to inline.
func mergePatch(target, patch []byte) ([]byte, error) {
	var (
		targetVal any
		patchVal  any
	)

	if err := json.Unmarshal(target, &targetVal); err != nil {
		return nil, err
	}

	if err := json.Unmarshal(patch, &patchVal); err != nil {
		return nil, err
	}

	merged := mergeRFC7396(targetVal, patchVal)

	return json.Marshal(merged)
}

// mergeRFC7396 merges patch into target per RFC 7396 semantics:
//
//   - if patch is not an object, replace target with patch.
//   - if patch is an object, for each key:
//   - if the patch value is null, delete the key from target.
//   - else recursively merge into target[key].
func mergeRFC7396(target, patch any) any {
	patchObj, isObj := patch.(map[string]any)
	if !isObj {
		return patch
	}

	targetObj, isObj := target.(map[string]any)
	if !isObj {
		targetObj = make(map[string]any, len(patchObj))
	}

	for k, v := range patchObj {
		if v == nil {
			delete(targetObj, k)

			continue
		}

		targetObj[k] = mergeRFC7396(targetObj[k], v)
	}

	return targetObj
}

// bumpResourceVersion increments cur as an integer. Real apiserver uses
// etcd's modification index; for our in-memory backend a monotonic counter
// per object is enough to give client-go a non-zero, ever-increasing
// resourceVersion to track.
func bumpResourceVersion(cur string) string {
	n, err := strconv.Atoi(cur)
	if err != nil {
		return "1"
	}

	return strconv.Itoa(n + 1)
}
