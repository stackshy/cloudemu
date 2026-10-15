package objectstorage_test

import (
	"crypto/md5" //nolint:gosec // asserting OCI's object digests
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func md5Of(parts ...[]byte) string {
	var all []byte
	for _, p := range parts {
		all = append(all, p...)
	}

	sum := md5.Sum(all) //nolint:gosec // see import

	return base64.StdEncoding.EncodeToString(sum[:])
}

func (f fixture) createPAR(t *testing.T, bucket string, body map[string]any) (string, *http.Response) {
	t.Helper()

	body["timeExpires"] = inAnHour()

	rec := f.do(t, http.MethodPost, f.bucketPath(bucket)+"/p", body)

	var par struct {
		AccessURI string `json:"accessUri"`
	}

	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &par))
	}

	return par.AccessURI, rec.Result()
}

func objectNames(t *testing.T, body []byte) []string {
	t.Helper()

	var out struct {
		Objects []struct {
			Name string `json:"name"`
		} `json:"objects"`
	}

	require.NoError(t, json.Unmarshal(body, &out), string(body))

	names := make([]string, 0, len(out.Objects))
	for _, o := range out.Objects {
		names = append(names, o.Name)
	}

	return names
}

func TestPARListObjects(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	for _, key := range []string{"logs/a.txt", "logs/b.txt", "other.txt"} {
		require.NoError(t, f.mock.PutObject(t.Context(), "photos", key, []byte("v"), "text/plain", nil))
	}

	uri, res := f.createPAR(t, "photos", map[string]any{
		"name": "list", "objectName": "logs/", "accessType": "AnyObjectRead", "bucketListingAction": "ListObjects",
	})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.True(t, strings.HasSuffix(uri, "/o/"), uri)

	rec := f.do(t, http.MethodGet, uri, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"logs/a.txt", "logs/b.txt"}, objectNames(t, rec.Body.Bytes()),
		"listing is scoped to the PAR's objectName prefix")

	rec = f.do(t, http.MethodGet, uri+"?prefix=logs/b&fields=name,size", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"logs/b.txt"}, objectNames(t, rec.Body.Bytes()))

	rec = f.do(t, http.MethodGet, uri+"?prefix=other", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, objectNames(t, rec.Body.Bytes()), "a prefix outside the PAR's lists nothing")

	rec = f.do(t, http.MethodGet, uri+"?prefix=lo", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"logs/a.txt", "logs/b.txt"}, objectNames(t, rec.Body.Bytes()))

	rec = f.do(t, http.MethodGet, uri+"?limit=1", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"logs/a.txt"}, objectNames(t, rec.Body.Bytes()))

	next := rec.Header().Get("opc-next-page")
	require.NotEmpty(t, next)

	rec = f.do(t, http.MethodGet, uri+"?limit=1&start="+next, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"logs/b.txt"}, objectNames(t, rec.Body.Bytes()))

	rec = f.do(t, http.MethodGet, uri+"logs/a.txt", nil)
	require.Equal(t, http.StatusOK, rec.Code, "the same PAR still reads objects")
	assert.Equal(t, "v", rec.Body.String())

	rec = f.do(t, http.MethodPut, uri, []byte("x"))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = f.do(t, http.MethodGet, strings.Replace(uri, "/b/photos/", "/b/other/", 1), nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestPARListObjectsRefusedWithoutListing(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	for _, body := range []map[string]any{
		{"name": "deny", "accessType": "AnyObjectRead"},
		{"name": "deny", "accessType": "AnyObjectRead", "bucketListingAction": "Deny"},
		{"name": "write", "accessType": "AnyObjectWrite", "bucketListingAction": "ListObjects"},
	} {
		uri, res := f.createPAR(t, "photos", body)
		require.Equal(t, http.StatusOK, res.StatusCode)

		rec := f.do(t, http.MethodGet, uri, nil)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%v", body)
	}

	_, res := f.createPAR(t, "photos", map[string]any{
		"name": "bogus", "accessType": "AnyObjectRead", "bucketListingAction": "Bogus",
	})
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestPARRedemptionChecksNamespace(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")
	require.NoError(t, f.mock.PutObject(t.Context(), "photos", "a.txt", []byte("v"), "text/plain", nil))

	uri, res := f.createPAR(t, "photos", map[string]any{
		"name": "read", "objectName": "a.txt", "accessType": "ObjectRead",
	})
	require.Equal(t, http.StatusOK, res.StatusCode)

	rec := f.do(t, http.MethodGet, strings.Replace(uri, "/n/"+f.ns+"/", "/n/wrongns/", 1), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "NotAuthorizedOrNotFound", errorCode(t, rec))
}

func TestPARAccessURIEscapesObjectName(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")
	require.NoError(t, f.mock.PutObject(t.Context(), "photos", "my file.txt", []byte("spaced"), "text/plain", nil))

	uri, res := f.createPAR(t, "photos", map[string]any{
		"name": "read", "objectName": "my file.txt", "accessType": "ObjectRead",
	})
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.True(t, strings.HasSuffix(uri, "/o/my%20file.txt"), uri)

	rec := f.do(t, http.MethodGet, uri, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "spaced", rec.Body.String())
}

func TestObjectMD5Headers(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	path := f.bucketPath("photos") + "/o/k"

	rec := f.do(t, http.MethodPut, path, []byte("hello"))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, md5Of([]byte("hello")), rec.Header().Get("opc-content-md5"))

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec = f.do(t, method, path, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, md5Of([]byte("hello")), rec.Header().Get("Content-MD5"), method)
		assert.Empty(t, rec.Header().Get("opc-multipart-md5"), method)
		assert.Empty(t, rec.Header().Get("opc-content-md5"), method)
	}

	rec = f.do(t, http.MethodPost, f.bucketPath("photos")+"/u", map[string]any{"object": "big"})
	require.Equal(t, http.StatusOK, rec.Code)

	var up struct {
		UploadID string `json:"uploadId"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &up))

	base := f.bucketPath("photos") + "/u/big?uploadId=" + up.UploadID

	rec = f.do(t, http.MethodPut, base+"&uploadPartNum=1", []byte("AAA"))
	require.Equal(t, http.StatusOK, rec.Code)
	etag1 := rec.Header().Get("ETag")

	rec = f.do(t, http.MethodPut, base+"&uploadPartNum=2", []byte("BB"))
	require.Equal(t, http.StatusOK, rec.Code)
	etag2 := rec.Header().Get("ETag")

	a, b := md5.Sum([]byte("AAA")), md5.Sum([]byte("BB")) //nolint:gosec // see import
	want := md5Of(a[:], b[:]) + "-2"

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"partsToCommit": []map[string]any{{"partNum": 1, "etag": etag1}, {"partNum": 2, "etag": etag2}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, want, rec.Header().Get("opc-multipart-md5"))

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec = f.do(t, method, f.bucketPath("photos")+"/o/big", nil)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, want, rec.Header().Get("opc-multipart-md5"), method)
		assert.Empty(t, rec.Header().Get("Content-MD5"), method)
	}
}

func TestContentMD5IsVerified(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	path := f.bucketPath("photos") + "/o/k"

	rec := f.doWith(t, http.MethodPut, path, []byte("hello"), map[string]string{"Content-MD5": "AAAAAAAAAAAAAAAAAAAAAA=="})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "InvalidParameter", errorCode(t, rec))

	rec = f.do(t, http.MethodHead, path, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code, "a body failing its MD5 is not stored")

	rec = f.doWith(t, http.MethodPut, path, []byte("hello"), map[string]string{"Content-MD5": md5Of([]byte("hello"))})
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("photos")+"/u", map[string]any{"object": "big"})
	require.Equal(t, http.StatusOK, rec.Code)

	var up struct {
		UploadID string `json:"uploadId"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &up))

	part := f.bucketPath("photos") + "/u/big?uploadId=" + up.UploadID + "&uploadPartNum=1"

	rec = f.doWith(t, http.MethodPut, part, []byte("AAA"), map[string]string{"Content-MD5": md5Of([]byte("BBB"))})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.doWith(t, http.MethodPut, part, []byte("AAA"), map[string]string{"Content-MD5": md5Of([]byte("AAA"))})
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, md5Of([]byte("AAA")), rec.Header().Get("opc-content-md5"))
}

func TestCommitRejectsDuplicatePartNumbers(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	rec := f.do(t, http.MethodPost, f.bucketPath("photos")+"/u", map[string]any{"object": "big"})
	require.Equal(t, http.StatusOK, rec.Code)

	var up struct {
		UploadID string `json:"uploadId"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &up))

	base := f.bucketPath("photos") + "/u/big?uploadId=" + up.UploadID

	rec = f.do(t, http.MethodPut, base+"&uploadPartNum=1", []byte("AAA"))
	etag1 := rec.Header().Get("ETag")
	rec = f.do(t, http.MethodPut, base+"&uploadPartNum=2", []byte("BB"))
	etag2 := rec.Header().Get("ETag")

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"partsToCommit": []map[string]any{
			{"partNum": 1, "etag": etag1}, {"partNum": 1, "etag": etag1}, {"partNum": 2, "etag": etag2},
		},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	rec = f.do(t, http.MethodHead, f.bucketPath("photos")+"/o/big", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestLastModifiedIsHTTPDate(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	path := f.bucketPath("photos") + "/o/k"

	rec := f.do(t, http.MethodPut, path, []byte("hello"))
	require.Equal(t, http.StatusOK, rec.Code)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec = f.do(t, method, path, nil)
		require.Equal(t, http.StatusOK, rec.Code)

		lm := rec.Header().Get("Last-Modified")
		_, err := http.ParseTime(lm)
		require.NoError(t, err, "%s Last-Modified %q", method, lm)
		assert.True(t, strings.HasSuffix(lm, " GMT"), lm)
	}
}

func TestUnemulatedOperationsAnswer501(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	b := f.bucketPath("photos")

	tests := []struct {
		method, path, mention string
	}{
		{http.MethodGet, b + "/replicationPolicies", "replication"},
		{http.MethodPost, b + "/replicationPolicies", "replication"},
		{http.MethodGet, b + "/replicationPolicies/ocid1.replicationpolicy.oc1..x", "replication"},
		{http.MethodGet, b + "/replicationSources", "replication"},
		{http.MethodPost, b + "/actions/batchDeleteObjects", "batchDeleteObjects"},
		{http.MethodPost, b + "/actions/makeBucketWritable", "makeBucketWritable"},
		{http.MethodPost, b + "/actions/reencrypt/dir/a.txt", "reencrypt"},
		{http.MethodPut, "/n/" + f.ns, "UpdateNamespaceMetadata"},
		{http.MethodGet, "/n/" + f.ns + "/pe", "private endpoints"},
		{http.MethodPost, "/n/" + f.ns + "/pe", "private endpoints"},
		{http.MethodGet, "/n/" + f.ns + "/pe/ocid1.objectstorageprivateendpoint.oc1..x", "private endpoints"},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := f.do(t, tc.method, tc.path, nil)
			assert.Equal(t, http.StatusNotImplemented, rec.Code, rec.Body.String())
			assert.Equal(t, "NotImplemented", errorCode(t, rec))
			assert.Contains(t, rec.Body.String(), tc.mention)
		})
	}
}
