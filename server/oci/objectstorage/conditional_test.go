package objectstorage_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doWith issues a request carrying extra headers.
func (f fixture) doWith(t *testing.T, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Code string `json:"code"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())

	return body.Code
}

func TestObjectConditionalRequests(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	key := f.bucketPath("photos") + "/o/k"

	// if-none-match: * creates only when absent.
	rec := f.doWith(t, http.MethodPut, key, []byte("v1"), map[string]string{"if-none-match": "*"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	first := rec.Header().Get("ETag")

	rec = f.doWith(t, http.MethodPut, key, []byte("clobber"), map[string]string{"if-none-match": "*"})
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)
	assert.Equal(t, "IfNoneMatchFailed", errorCode(t, rec))

	rec = f.doWith(t, http.MethodPut, key, []byte("x"), map[string]string{"if-none-match": first})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a write's if-none-match supports only *")

	// if-match must name the current ETag.
	rec = f.doWith(t, http.MethodPut, key, []byte("clobber"), map[string]string{"if-match": "bogus"})
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)
	assert.Equal(t, "IfMatchFailed", errorCode(t, rec))

	rec = f.do(t, http.MethodGet, key, nil)
	assert.Equal(t, "v1", rec.Body.String(), "a failed precondition must not write")

	// Identical bytes still mint a new ETag: it is opaque, not a content hash.
	rec = f.doWith(t, http.MethodPut, key, []byte("v1"), map[string]string{"if-match": first})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	second := rec.Header().Get("ETag")
	assert.NotEqual(t, first, second)

	rec = f.doWith(t, http.MethodPut, key, []byte("v2"), map[string]string{"if-match": first})
	assert.Equal(t, http.StatusPreconditionFailed, rec.Code, "the old ETag no longer matches")

	// Reads: a stale if-match is 412, a matching if-none-match is 304.
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec = f.doWith(t, method, key, nil, map[string]string{"if-match": first})
		assert.Equal(t, http.StatusPreconditionFailed, rec.Code, method)

		rec = f.doWith(t, method, key, nil, map[string]string{"if-match": second})
		assert.Equal(t, http.StatusOK, rec.Code, method)

		rec = f.doWith(t, method, key, nil, map[string]string{"if-none-match": second})
		assert.Equal(t, http.StatusNotModified, rec.Code, method)
		assert.Empty(t, rec.Body.String())

		rec = f.doWith(t, method, key, nil, map[string]string{"if-none-match": first})
		assert.Equal(t, http.StatusOK, rec.Code, method)
	}

	// Delete honors if-match.
	rec = f.doWith(t, http.MethodDelete, key, nil, map[string]string{"if-match": first})
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)
	assert.Equal(t, "IfMatchFailed", errorCode(t, rec))

	rec = f.doWith(t, http.MethodDelete, key, nil, map[string]string{"if-match": second})
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = f.doWith(t, http.MethodPut, key, []byte("x"), map[string]string{"if-match": second})
	assert.Equal(t, http.StatusPreconditionFailed, rec.Code, "if-match on an absent object fails")
}

func TestBucketConditionalRequests(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	rec := f.do(t, http.MethodGet, f.bucketPath("photos"), nil)
	etag := rec.Header().Get("ETag")
	require.NotEmpty(t, etag)

	rec = f.doWith(t, http.MethodPost, f.bucketPath("photos"), []byte(`{"freeformTags":{"a":"b"}}`),
		map[string]string{"if-match": "stale"})
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)
	assert.Equal(t, "IfMatchFailed", errorCode(t, rec))

	rec = f.doWith(t, http.MethodPost, f.bucketPath("photos"), []byte(`{"freeformTags":{"a":"b"}}`),
		map[string]string{"if-match": etag})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	updated := rec.Header().Get("ETag")

	rec = f.doWith(t, http.MethodDelete, f.bucketPath("photos"), nil, map[string]string{"if-match": etag})
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)

	rec = f.doWith(t, http.MethodDelete, f.bucketPath("photos"), nil, map[string]string{"if-match": updated})
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestGetObjectRange(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	key := f.bucketPath("photos") + "/o/k"
	rec := f.doWith(t, http.MethodPut, key, []byte("0123456789"), map[string]string{"Content-Type": "text/plain"})
	require.Equal(t, http.StatusOK, rec.Code)

	tests := []struct {
		spec, body, contentRange string
	}{
		{"bytes=0-2", "012", "bytes 0-2/10"},
		{"bytes=7-", "789", "bytes 7-9/10"},
		{"bytes=-4", "6789", "bytes 6-9/10"},
		{"bytes=8-100", "89", "bytes 8-9/10"},
	}

	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			rec := f.doWith(t, http.MethodGet, key, nil, map[string]string{"Range": tc.spec})
			require.Equal(t, http.StatusPartialContent, rec.Code, rec.Body.String())
			assert.Equal(t, tc.body, rec.Body.String())
			assert.Equal(t, tc.contentRange, rec.Header().Get("Content-Range"))
			assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"))
		})
	}

	rec = f.doWith(t, http.MethodGet, key, nil, map[string]string{"Range": "bytes=20-30"})
	require.Equal(t, http.StatusRequestedRangeNotSatisfiable, rec.Code)
	assert.Equal(t, "bytes */10", rec.Header().Get("Content-Range"))

	rec = f.doWith(t, http.MethodGet, key, nil, map[string]string{"Range": "bytes=0-1,4-5"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a multi-range request is refused, not answered in full")

	rec = f.do(t, http.MethodGet, key, nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
}

func TestBucketRenameAndErrorCodes(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "tfb")
	f.createBucket(t, "taken")
	require.NoError(t, f.mock.PutObject(t.Context(), "tfb", "k", []byte("v"), "text/plain", nil))

	rec := f.do(t, http.MethodPost, "/n/"+f.ns+"/b", map[string]any{"name": "tfb", "compartmentId": testCompartment})
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "BucketAlreadyExists", errorCode(t, rec))

	rec = f.do(t, http.MethodDelete, f.bucketPath("tfb"), nil)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "BucketNotEmpty", errorCode(t, rec))

	rec = f.do(t, http.MethodPost, f.bucketPath("tfb"), map[string]any{"name": "taken"})
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "BucketAlreadyExists", errorCode(t, rec))

	rec = f.do(t, http.MethodPost, f.bucketPath("tfb"), map[string]any{"name": "bad name!"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("tfb"), map[string]any{"namespace": "elsewhere"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a cross-namespace move is refused, not ignored")

	rec = f.do(t, http.MethodPost, f.bucketPath("tfb"), map[string]any{"name": "tfb2"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"name":"tfb2"`)

	rec = f.do(t, http.MethodGet, f.bucketPath("tfb"), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("tfb2")+"/o/k", nil)
	require.Equal(t, http.StatusOK, rec.Code, "objects follow the bucket")
	assert.Equal(t, "v", rec.Body.String())
}

func TestBucketNamesAreValidated(t *testing.T) {
	f := newFixture(t)

	for _, name := range []string{"bad name!", "slash/name", strings.Repeat("a", 257)} {
		rec := f.do(t, http.MethodPost, "/n/"+f.ns+"/b", map[string]any{"name": name, "compartmentId": testCompartment})
		assert.Equal(t, http.StatusBadRequest, rec.Code, name)
	}

	for _, name := range []string{"ok-name_1.2", strings.Repeat("a", 256)} {
		rec := f.do(t, http.MethodPost, "/n/"+f.ns+"/b", map[string]any{"name": name, "compartmentId": testCompartment})
		assert.Equal(t, http.StatusOK, rec.Code, name)
	}
}

func TestCompartmentCheckerGatesCreateAndMove(t *testing.T) {
	f := newFixture(t)
	f.handler.SetCompartmentChecker(func(id string) bool { return id == testCompartment })

	rec := f.do(t, http.MethodPost, "/n/"+f.ns+"/b", map[string]any{
		"name": "nope", "compartmentId": "ocid1.compartment.oc1..doesnotexist",
	})
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "NotAuthorizedOrNotFound", errorCode(t, rec))

	f.createBucket(t, "photos")

	rec = f.do(t, http.MethodPost, f.bucketPath("photos"), map[string]any{
		"compartmentId": "ocid1.compartment.oc1..doesnotexist",
	})
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos"), nil)
	assert.Contains(t, rec.Body.String(), testCompartment, "a refused move leaves the bucket where it was")
}

// Every Object Storage list pages by limit and page and stamps opc-next-page.
func TestListsPaginate(t *testing.T) {
	f := newFixture(t)

	for _, b := range []string{"b1", "b2", "b3"} {
		f.createBucket(t, b)
	}

	ctx := t.Context()
	require.NoError(t, f.mock.SetVersioningStatus(ctx, "b1", "Enabled"))

	for range 3 {
		require.NoError(t, f.mock.PutObject(ctx, "b1", "k", []byte("v"), "text/plain", nil))
	}

	for i := range 3 {
		rec := f.do(t, http.MethodPost, f.bucketPath("b1")+"/u", map[string]any{"object": "big" + string(rune('a'+i))})
		require.Equal(t, http.StatusOK, rec.Code)

		rec = f.do(t, http.MethodPost, f.bucketPath("b1")+"/p", map[string]any{
			"name": "p", "accessType": "AnyObjectRead", "timeExpires": inAnHour(),
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}

	countOf := func(raw []byte) int {
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) == nil {
			return len(arr)
		}

		var env struct {
			Items []json.RawMessage `json:"items"`
		}

		require.NoError(t, json.Unmarshal(raw, &env))

		return len(env.Items)
	}

	for _, path := range []string{
		"/n/" + f.ns + "/b?compartmentId=" + testCompartment,
		f.bucketPath("b1") + "/objectversions",
		f.bucketPath("b1") + "/p",
		f.bucketPath("b1") + "/u",
	} {
		t.Run(path, func(t *testing.T) {
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}

			seen := 0
			next := ""

			for range 5 {
				url := path + sep + "limit=1"
				if next != "" {
					url += "&page=" + next
				}

				rec := f.do(t, http.MethodGet, url, nil)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				assert.Equal(t, 1, countOf(rec.Body.Bytes()))

				seen++
				next = rec.Header().Get("opc-next-page")

				if next == "" {
					break
				}
			}

			assert.Equal(t, 3, seen, "three items one page at a time, the last with no opc-next-page")
		})
	}
}

// copyObject applies or refuses every CopyObjectDetails field rather than
// dropping one.
func TestCopyObjectHonoursEveryField(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "src")
	f.createBucket(t, "dst")

	ctx := t.Context()
	require.NoError(t, f.mock.SetVersioningStatus(ctx, "src", "Enabled"))

	rec := f.doWith(t, http.MethodPut, f.bucketPath("src")+"/o/a", []byte("v1"), map[string]string{"opc-meta-owner": "ada"})
	require.Equal(t, http.StatusOK, rec.Code)
	v1 := rec.Header().Get("version-id")
	srcETag := rec.Header().Get("ETag")

	rec = f.do(t, http.MethodPut, f.bucketPath("src")+"/o/a", []byte("v2"))
	require.Equal(t, http.StatusOK, rec.Code)

	copyTo := func(extra map[string]any) *httptest.ResponseRecorder {
		body := map[string]any{
			"sourceObjectName": "a", "destinationRegion": "us-ashburn-1", "destinationNamespace": f.ns,
			"destinationBucket": "dst", "destinationObjectName": "b",
		}
		for k, v := range extra {
			body[k] = v
		}

		return f.do(t, http.MethodPost, f.bucketPath("src")+"/actions/copyObject", body)
	}

	rec = copyTo(map[string]any{"destinationRegion": "eu-frankfurt-1"})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "cross-region")

	rec = copyTo(map[string]any{"destinationRegion": ""})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "destinationRegion is required")

	rec = copyTo(map[string]any{"sourceObjectIfMatchETag": srcETag})
	require.Equal(t, http.StatusPreconditionFailed, rec.Code, "the source has moved on from that ETag")

	rec = copyTo(map[string]any{"destinationObjectStorageTier": "Glacier"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// A source version, replacement metadata and a storage tier all apply.
	rec = copyTo(map[string]any{
		"sourceVersionId":              v1,
		"destinationObjectMetadata":    map[string]string{"owner": "bo"},
		"destinationObjectStorageTier": "InfrequentAccess",
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	rec = f.do(t, http.MethodGet, f.bucketPath("dst")+"/o/b", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "v1", rec.Body.String())
	assert.Equal(t, "bo", rec.Header().Get("opc-meta-owner"))
	assert.Equal(t, "InfrequentAccess", rec.Header().Get("storage-tier"))

	// The destination preconditions apply too.
	rec = copyTo(map[string]any{"destinationObjectIfNoneMatchETag": "*"})
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)
	assert.Equal(t, "IfNoneMatchFailed", errorCode(t, rec))

	rec = copyTo(map[string]any{"destinationObjectIfMatchETag": "stale"})
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)
	assert.Equal(t, "IfMatchFailed", errorCode(t, rec))
}
