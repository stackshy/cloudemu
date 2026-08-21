package objectstorage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	osprovider "github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	ociobjectstorage "github.com/stackshy/cloudemu/v2/server/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const testCompartment = "ocid1.compartment.oc1..aaaaaaaatest"

// The mock must satisfy the handler's OCI-only capability interface.
var _ ociobjectstorage.Extras = (*osprovider.Mock)(nil)

type fixture struct {
	handler *ociobjectstorage.Handler
	mock    *osprovider.Mock
	ns      string
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	opts := config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
	)
	mock := osprovider.New(opts)

	return fixture{
		handler: ociobjectstorage.New(mock, workrequest.New(opts)),
		mock:    mock,
		ns:      mock.Namespace(),
	}
}

func (f fixture) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader

	switch b := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}

	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

func (f fixture) bucketPath(bucket string) string {
	return "/n/" + f.ns + "/b/" + bucket
}

// createBucket creates a bucket over the wire and fails if the handler refuses.
func (f fixture) createBucket(t *testing.T, name string) {
	t.Helper()

	rec := f.do(t, http.MethodPost, "/n/"+f.ns+"/b", map[string]any{
		"name": name, "compartmentId": testCompartment,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestMatches(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name   string
		path   string
		expect bool
	}{
		{name: "namespace root", path: "/n", expect: true},
		{name: "namespace", path: "/n/axaxnpcrorw5", expect: true},
		{name: "bucket collection", path: "/n/axaxnpcrorw5/b", expect: true},
		{name: "bucket", path: "/n/axaxnpcrorw5/b/photos", expect: true},
		{name: "object", path: "/n/axaxnpcrorw5/b/photos/o/dir/a.jpg", expect: true},
		{name: "multipart", path: "/n/axaxnpcrorw5/b/photos/u/big", expect: true},
		{name: "par redemption", path: "/p/tok/n/axaxnpcrorw5/b/photos/o/a.jpg", expect: true},
		{name: "retention rules", path: "/n/axaxnpcrorw5/b/photos/retentionRules", expect: true},

		{name: "vcn collection", path: "/20160918/vcns", expect: false},
		{name: "vcn subnet", path: "/20160918/subnets/ocid1.subnet.oc1.iad.a", expect: false},
		{name: "work requests", path: "/20160918/workRequests", expect: false},
		{name: "identity users", path: "/20160918/users", expect: false},
		{name: "monitoring", path: "/20180401/metrics", expect: false},
		{name: "namespaces is not the namespace root", path: "/namespaces", expect: false},
		{name: "nodes is not the namespace root", path: "/nodes/n", expect: false},
		{name: "root", path: "/", expect: false},
		{name: "par without a namespace", path: "/p/tok/o/a.jpg", expect: false},
		{name: "namespace with a foreign collection", path: "/n/axaxnpcrorw5/vcns", expect: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			assert.Equal(t, tc.expect, f.handler.Matches(req))
		})
	}
}

func TestExtrasAbsentServes501(t *testing.T) {
	h := ociobjectstorage.New(bareBucket{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/n", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
	assert.Contains(t, rec.Body.String(), "namespaces")
}

func TestNamespaceEndpoints(t *testing.T) {
	f := newFixture(t)

	rec := f.do(t, http.MethodGet, "/n", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var ns string

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ns))
	assert.Equal(t, f.ns, ns)
	assert.NotEmpty(t, rec.Header().Get("opc-request-id"))

	rec = f.do(t, http.MethodGet, "/n/"+f.ns, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"defaultS3CompartmentId"`)

	rec = f.do(t, http.MethodGet, "/n/wrongns/b", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBucketWire(t *testing.T) {
	f := newFixture(t)

	rec := f.do(t, http.MethodPost, "/n/"+f.ns+"/b", map[string]any{
		"name": "photos", "compartmentId": testCompartment, "publicAccessType": "ObjectRead",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("ETag"))

	var created map[string]any

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "ObjectRead", created["publicAccessType"])
	assert.Contains(t, created["id"], "ocid1.bucket.oc1.iad.")

	rec = f.do(t, http.MethodPost, "/n/"+f.ns+"/b", map[string]any{
		"name": "photos", "compartmentId": testCompartment,
	})
	assert.Equal(t, http.StatusConflict, rec.Code)

	rec = f.do(t, http.MethodPost, "/n/"+f.ns+"/b", map[string]any{"name": "nocompartment"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodGet, "/n/"+f.ns+"/b", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "compartmentId is required on ListBuckets")

	rec = f.do(t, http.MethodGet, "/n/"+f.ns+"/b?compartmentId="+testCompartment, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var summaries []map[string]any

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &summaries))
	require.Len(t, summaries, 1)

	rec = f.do(t, http.MethodGet, "/n/"+f.ns+"/b?compartmentId=ocid1.compartment.oc1..other", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `[]`, rec.Body.String())

	rec = f.do(t, http.MethodGet, f.bucketPath("photos"), nil)
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("missing"), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("photos"), map[string]any{"versioning": "Enabled"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"versioning":"Enabled"`)

	rec = f.do(t, http.MethodDelete, f.bucketPath("photos"), nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = f.do(t, http.MethodPatch, f.bucketPath("photos"), nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestObjectWire(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	req := httptest.NewRequest(http.MethodPut, f.bucketPath("photos")+"/o/dir/a.txt", bytes.NewReader([]byte("hello")))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("opc-meta-owner", "ada")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("ETag"))
	assert.NotEmpty(t, rec.Header().Get("opc-content-md5"))

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/o/dir/a.txt", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "hello", rec.Body.String())
	assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"))
	assert.Equal(t, "ada", rec.Header().Get("opc-meta-owner"))

	rec = f.do(t, http.MethodHead, f.bucketPath("photos")+"/o/dir/a.txt", nil)
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/o/nope.txt", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/o?prefix=dir/&delimiter=/", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var list struct {
		Objects []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"objects"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Objects, 1)
	assert.Equal(t, "dir/a.txt", list.Objects[0].Name)
	assert.Equal(t, int64(5), list.Objects[0].Size)

	rec = f.do(t, http.MethodDelete, f.bucketPath("photos")+"/o/dir/a.txt", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = f.do(t, http.MethodDelete, f.bucketPath("photos")+"/o/dir/a.txt", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestObjectActions(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "src")
	f.createBucket(t, "dst")
	require.NoError(t, f.mock.PutObject(context.Background(), "src", "old", []byte("v"), "text/plain", nil))

	rec := f.do(t, http.MethodPost, f.bucketPath("src")+"/actions/renameObject", map[string]any{
		"sourceName": "old", "newName": "new",
	})
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = f.do(t, http.MethodPost, f.bucketPath("src")+"/actions/renameObject", map[string]any{
		"sourceName": "old", "newName": "other",
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("src")+"/actions/copyObject", map[string]any{
		"sourceObjectName": "new", "destinationBucket": "dst", "destinationObjectName": "copied",
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("opc-work-request-id"))

	obj, err := f.mock.GetObject(context.Background(), "dst", "copied")
	require.NoError(t, err)
	assert.Equal(t, []byte("v"), obj.Data)

	rec = f.do(t, http.MethodPost, f.bucketPath("src")+"/actions/copyObject", map[string]any{
		"sourceObjectName": "new", "destinationBucket": "dst",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("src")+"/actions/updateObjectStorageTier", map[string]any{
		"objectName": "new", "storageTier": "Archive",
	})
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("src")+"/actions/reencrypt", nil)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("src")+"/actions/restoreObjects", nil)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("src")+"/actions/teleport", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMultipartWire(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	rec := f.do(t, http.MethodPost, f.bucketPath("photos")+"/u", map[string]any{"object": "big"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var up struct {
		UploadID string `json:"uploadId"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &up))
	require.NotEmpty(t, up.UploadID)

	base := f.bucketPath("photos") + "/u/big?uploadId=" + up.UploadID

	rec = f.do(t, http.MethodPut, base+"&uploadPartNum=1", []byte("aaa"))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = f.do(t, http.MethodPut, base+"&uploadPartNum=2", []byte("bbb"))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = f.do(t, http.MethodPut, base+"&uploadPartNum=notanumber", []byte("x"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"partNumber":1`)

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"partsToCommit":  []map[string]any{{"partNum": 1}, {"partNum": 2}},
		"partsToExclude": []int{3},
	})
	assert.Equal(t, http.StatusNotImplemented, rec.Code, "partsToExclude must be rejected, not dropped")

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"partsToCommit": []map[string]any{{"partNum": 1}, {"partNum": 2}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	obj, err := f.mock.GetObject(context.Background(), "photos", "big")
	require.NoError(t, err)
	assert.Equal(t, []byte("aaabbb"), obj.Data)

	rec = f.do(t, http.MethodDelete, f.bucketPath("photos")+"/u/big?uploadId="+up.UploadID, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPut, f.bucketPath("photos")+"/u/big", []byte("x"))
	assert.Equal(t, http.StatusBadRequest, rec.Code, "uploadId is required")
}

func TestVersioningWire(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	rec := f.do(t, http.MethodPost, f.bucketPath("photos"), map[string]any{"versioning": "Enabled"})
	require.Equal(t, http.StatusOK, rec.Code)

	ctx := context.Background()
	require.NoError(t, f.mock.PutObject(ctx, "photos", "k", []byte("v1"), "text/plain", nil))
	require.NoError(t, f.mock.PutObject(ctx, "photos", "k", []byte("v2"), "text/plain", nil))

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/objectversions", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var versions struct {
		Items []struct {
			VersionID string `json:"versionId"`
			Size      int64  `json:"size"`
		} `json:"items"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &versions))
	require.Len(t, versions.Items, 2)

	oldest := versions.Items[1].VersionID
	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/o/k?versionId="+oldest, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "v1", rec.Body.String())

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/o/k?versionId=bogus", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodDelete, f.bucketPath("photos")+"/o/k", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "true", rec.Header().Get("is-delete-marker"))
}

func TestPARWire(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")
	require.NoError(t, f.mock.PutObject(context.Background(), "photos", "a.txt", []byte("hi"), "text/plain", nil))

	rec := f.do(t, http.MethodPost, f.bucketPath("photos")+"/p", map[string]any{
		"name": "read-a", "objectName": "a.txt", "accessType": "ObjectRead",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var par struct {
		ID        string `json:"id"`
		AccessURI string `json:"accessUri"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &par))
	require.NotEmpty(t, par.AccessURI)
	assert.Contains(t, par.ID, "ocid1.preauthenticatedrequest.")

	rec = f.do(t, http.MethodGet, par.AccessURI, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "hi", rec.Body.String())

	rec = f.do(t, http.MethodPut, par.AccessURI, []byte("nope"))
	assert.Equal(t, http.StatusForbidden, rec.Code, "a read PAR must not authorize a write")

	rec = f.do(t, http.MethodPost, f.bucketPath("photos")+"/p", map[string]any{
		"name": "bad", "accessType": "ObjectRead",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/p", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), par.ID)

	rec = f.do(t, http.MethodDelete, f.bucketPath("photos")+"/p/"+par.ID, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = f.do(t, http.MethodGet, par.AccessURI, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code, "a revoked PAR stops working")
}

func TestRetentionWire(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	rec := f.do(t, http.MethodPost, f.bucketPath("photos")+"/retentionRules", map[string]any{
		"displayName": "thirty",
		"duration":    map[string]any{"timeAmount": 30, "timeUnit": "DAYS"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var rule struct {
		ID string `json:"id"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/retentionRules", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), rule.ID)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/retentionRules/"+rule.ID, nil)
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/retentionRules/missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("photos")+"/retentionRules", map[string]any{
		"duration": map[string]any{"timeAmount": 1, "timeUnit": "FORTNIGHTS"},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodDelete, f.bucketPath("photos")+"/retentionRules/"+rule.ID, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestLifecycleWire(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	rec := f.do(t, http.MethodPut, f.bucketPath("photos")+"/l", map[string]any{
		"items": []map[string]any{{
			"name": "expire-logs", "action": "DELETE", "timeAmount": 30, "timeUnit": "DAYS",
			"isEnabled": true, "objectNameFilter": map[string]any{"inclusionPrefixes": []string{"logs/"}},
		}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/l", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"action":"DELETE"`)
	assert.Contains(t, rec.Body.String(), `"logs/"`)

	rec = f.do(t, http.MethodPut, f.bucketPath("photos")+"/l", map[string]any{
		"items": []map[string]any{{"name": "bogus", "action": "TELEPORT", "timeAmount": 1, "timeUnit": "DAYS"}},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPut, f.bucketPath("photos")+"/l", map[string]any{
		"items": []map[string]any{{
			"name": "multi", "action": "DELETE", "timeAmount": 1, "timeUnit": "DAYS",
			"objectNameFilter": map[string]any{"inclusionPrefixes": []string{"a/", "b/"}},
		}},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a dropped prefix would silently change the policy")

	rec = f.do(t, http.MethodDelete, f.bucketPath("photos")+"/l", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/l", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// bareBucket is a storage driver that implements nothing beyond driver.Bucket,
// standing in for a non-OCI provider wired into the handler.
type bareBucket struct{ driver.Bucket }
