package kubernetes

import (
	"encoding/json"
	"io"
	"net/http"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
)

// Patch content types kubectl and client-go send. JSON-merge-patch (RFC 7396)
// is client-go's default; strategic-merge-patch is kubectl's default for
// `set`, `edit`, `label`, etc.; JSONPatch (RFC 6902) is `kubectl patch
// --type=json`. apply-patch (server-side apply) is treated as a merge.
const (
	contentTypeJSONMergePatch = "application/merge-patch+json"
	contentTypeStrategicMerge = "application/strategic-merge-patch+json"
	contentTypeJSONPatch      = "application/json-patch+json"
	contentTypeApplyPatch     = "application/apply-patch+yaml"
)

// applyJSONPatch reads a patch body of the request's content type, applies it
// to current, and returns a freshly-decoded T containing the result. The
// stored object is never mutated; callers swap the returned value into their
// store explicitly.
//
// All four patch content types kubectl and client-go use are supported:
// JSON-merge-patch (RFC 7396), strategic-merge-patch (real strategic merge
// against T's struct tags, so `kubectl set image` merges the container list by
// name rather than replacing it), JSONPatch (RFC 6902), and server-side apply
// (treated as a merge — the mock has no field managers).
//
// On any wire-level failure (bad content-type, body read error, patch error,
// or final unmarshal mismatch), the function writes a metav1.Status-shaped 400
// response to w and returns (nil, false). Callers must early-return without
// touching w.
func applyJSONPatch[T any](w http.ResponseWriter, r *http.Request, current *T) (*T, bool) {
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

	merged, ok := applyPatchBytes(w, r.Header.Get("Content-Type"), curBytes, patch, current)
	if !ok {
		return nil, false
	}

	var patched T
	if err := json.Unmarshal(merged, &patched); err != nil {
		writeBadRequest(w, "k8s api: decode patched object: "+err.Error())

		return nil, false
	}

	return &patched, true
}

// applyPatchBytes applies patch to curBytes per the given content type,
// returning the patched JSON. dataStruct is a zero value of the target type,
// used for strategic-merge's struct-tag-aware list/map merging.
func applyPatchBytes[T any](w http.ResponseWriter, ct string, curBytes, patch []byte, dataStruct *T) ([]byte, bool) {
	switch ct {
	case "", contentTypeJSON, contentTypeJSONMergePatch, contentTypeApplyPatch:
		merged, err := mergePatch(curBytes, patch)
		if err != nil {
			writeBadRequest(w, "k8s api: apply merge patch: "+err.Error())

			return nil, false
		}

		return merged, true

	case contentTypeStrategicMerge:
		merged, err := strategicpatch.StrategicMergePatch(curBytes, patch, dataStruct)
		if err != nil {
			writeBadRequest(w, "k8s api: apply strategic-merge patch: "+err.Error())

			return nil, false
		}

		return merged, true

	case contentTypeJSONPatch:
		p, err := jsonpatch.DecodePatch(patch)
		if err != nil {
			writeBadRequest(w, "k8s api: decode json patch: "+err.Error())

			return nil, false
		}

		merged, err := p.Apply(curBytes)
		if err != nil {
			writeBadRequest(w, "k8s api: apply json patch: "+err.Error())

			return nil, false
		}

		return merged, true

	default:
		writeBadRequest(w, "k8s api: unsupported patch content-type: "+ct)

		return nil, false
	}
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
