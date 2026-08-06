package kubernetes

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// keySeparator joins namespace and name into the sort key every list path
// orders by (the same "namespace/name" form as the store's map keys).
const keySeparator = "/"

// k8sPageToken is the opaque continue-token payload for a chunked list request.
// It anchors resume to the last-emitted object's key instead of a positional
// offset, so an insert or delete before that key can no longer shift an offset
// into a skipped or duplicated item across the lock-free gap between page
// fetches. It is base64(JSON) and stays opaque to clients.
type k8sPageToken struct {
	LastKey string `json:"k"`
}

func encodePageToken(lastKey string) string {
	data, _ := json.Marshal(k8sPageToken{LastKey: lastKey})

	// URL-safe base64 so the token survives a query string without percent-
	// encoding (StdEncoding's '+' would be decoded back to a space).
	return base64.URLEncoding.EncodeToString(data)
}

func decodePageToken(raw string) (k8sPageToken, error) {
	data, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return k8sPageToken{}, err
	}

	var t k8sPageToken
	if err := json.Unmarshal(data, &t); err != nil {
		return k8sPageToken{}, err
	}

	return t, nil
}

// objectKey returns the "namespace/name" sort key for a list element. In a
// range &items[i] is addressable and every k8s API list element (corev1.Pod,
// appsv1.Deployment, unstructured.Unstructured, …) satisfies metav1.Object via
// its pointer. ok is false when the element does not, in which case the caller
// falls back to an unpaginated full list rather than panicking.
func objectKey[T any](item *T) (key string, ok bool) {
	obj, isObj := any(item).(metav1.Object)
	if !isObj {
		return "", false
	}

	return obj.GetNamespace() + keySeparator + obj.GetName(), true
}

// resumeIndex returns the index of the first item whose key sorts strictly
// after lastKey. Items are already sorted by key, so a binary search finds the
// boundary; a since-deleted lastKey simply resumes at the next greater key
// rather than erroring.
func resumeIndex[T any](items []T, lastKey string) int {
	return sort.Search(len(items), func(i int) bool {
		key, ok := objectKey(&items[i])

		return !ok || key > lastKey
	})
}

// listPage slices items for a `?limit=&continue=` list request (client-go's
// chunked pager / kubectl chunked listing). When limit is absent or non-positive
// the full slice is returned with an empty token, preserving the unpaginated
// default. The returned string is the value for list metadata.continue — "" on
// the final (or only) page. Items MUST already be sorted by key; every list path
// here sorts by namespace/name before calling this.
//
// Resume is anchored to the token's last-emitted key: the next page skips to the
// first item whose key is strictly greater, so a mutation before that key cannot
// skip or duplicate a later item. A malformed continue token (bad base64/JSON or
// wrong shape) writes a 410 Gone Status and returns ok=false — the client-go
// contract — instead of silently returning the full list. A well-formed token
// whose key was since deleted is NOT an error: resume proceeds from the next
// greater key.
func listPage[T any](items []T, w http.ResponseWriter, r *http.Request) (page []T, cont string, ok bool) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		return items, "", true
	}

	// Elements that aren't metav1.Object can't be key-paginated — return the
	// full list. All elements share type T, so probing the first suffices.
	if len(items) > 0 {
		if _, keyed := objectKey(&items[0]); !keyed {
			return items, "", true
		}
	}

	start := 0

	if raw := r.URL.Query().Get("continue"); raw != "" {
		tok, err := decodePageToken(raw)
		if err != nil {
			writeStatus(w, http.StatusGone, metav1.StatusReasonExpired,
				"k8s api: continue token is expired or malformed: "+err.Error())

			return nil, "", false
		}

		start = resumeIndex(items, tok.LastKey)
	}

	end := start + limit
	if end >= len(items) {
		return items[start:], "", true
	}

	nextKey, _ := objectKey(&items[end-1])

	return items[start:end], encodePageToken(nextKey), true
}
