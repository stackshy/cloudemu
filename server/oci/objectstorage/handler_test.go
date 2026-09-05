package objectstorage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// A namespace beginning with b must still route GET /n/{ns} to the metadata
// endpoint: the /b bucket collection is a path segment, not a substring.
func TestNamespaceMetadataWithBPrefixedNamespace(t *testing.T) {
	opts := config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
		config.WithTenancyOCID("ocid1.tenancy.oc1..probe0100"),
	)
	mock := osprovider.New(opts)
	f := fixture{handler: ociobjectstorage.New(mock, workrequest.New(opts)), mock: mock, ns: mock.Namespace()}

	require.True(t, strings.HasPrefix(f.ns, "b"), "fixture namespace must start with b, got %q", f.ns)

	rec := f.do(t, http.MethodGet, "/n/"+f.ns, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var meta osprovider.NamespaceMetadata

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &meta))
	assert.Equal(t, f.ns, meta.Namespace)

	// The bucket collection under the same namespace still lists.
	rec = f.do(t, http.MethodGet, "/n/"+f.ns+"/b?compartmentId="+testCompartment, nil)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
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
	assert.Equal(t, "5", rec.Header().Get("Content-Length"), "a HEAD carries no body, so it must report the size")
	assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"))

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

// A driver with no version history is served the same Unimplemented for every
// versioned route rather than a bare 404.
type unversionedStore struct {
	*osprovider.Mock
}

func (unversionedStore) GetObjectVersion(_ context.Context, _, _, _ string) (*driver.Object, error) {
	panic("must not be reached: the handler must not discover this capability")
}

func TestVersioningUnsupportedIsNamed(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("us-ashburn-1"), config.WithCompartmentID(testCompartment))
	mock := osprovider.New(opts)
	// A store that is a driver.Bucket and the OCI Extras, but not a
	// driver.VersionedBucket.
	store := struct {
		driver.Bucket
		ociobjectstorage.Extras
	}{Bucket: mock, Extras: mock}

	f := fixture{
		handler: ociobjectstorage.New(store, workrequest.New(opts)),
		mock:    mock,
		ns:      mock.Namespace(),
	}
	f.createBucket(t, "photos")
	require.NoError(t, mock.PutObject(t.Context(), "photos", "k", []byte("v"), "text/plain", nil))

	paths := []struct {
		name   string
		method string
		path   string
	}{
		{"objectversions", http.MethodGet, f.bucketPath("photos") + "/objectversions"},
		{"get by version", http.MethodGet, f.bucketPath("photos") + "/o/k?versionId=abc"},
		{"head by version", http.MethodHead, f.bucketPath("photos") + "/o/k?versionId=abc"},
		{"delete by version", http.MethodDelete, f.bucketPath("photos") + "/o/k?versionId=abc"},
	}

	for _, tc := range paths {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, tc.method, tc.path, nil)
			assert.Equal(t, http.StatusNotImplemented, rec.Code, rec.Body.String())
		})
	}
}

func TestBucketItemWire(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	rec := f.do(t, http.MethodHead, f.bucketPath("photos"), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("ETag"))

	rec = f.do(t, http.MethodHead, f.bucketPath("missing"), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("photos"), []byte("{"))
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a malformed body is refused")

	rec = f.do(t, http.MethodPost, f.bucketPath("photos"), map[string]any{"versioning": "On"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "an unmodelled versioning value is named")

	rec = f.do(t, http.MethodPost, f.bucketPath("missing"), map[string]any{"publicAccessType": "NoPublicAccess"})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("photos"), map[string]any{
		"compartmentId": "ocid1.compartment.oc1..moved",
		"autoTiering":   "InfrequentAccess",
		"metadata":      map[string]string{"team": "infra"},
		"freeformTags":  map[string]string{"env": "dev"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"autoTiering":"InfrequentAccess"`)

	rec = f.do(t, http.MethodDelete, f.bucketPath("missing"), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPost, "/n/"+f.ns+"/b", []byte("not json"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodDelete, "/n/"+f.ns+"/b", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = f.do(t, http.MethodDelete, "/n/"+f.ns, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "the namespace itself takes only GET")

	rec = f.do(t, http.MethodPost, "/n", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/unknown", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodGet, "/nope", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// An unspecified limit must yield OCI's page size of 1000, not the 100 the
// other OCI services share through ocirest.DefaultLimit.
func TestListObjectsDefaultPageSizeIsOCIs1000(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	ctx := t.Context()
	for i := range 150 {
		require.NoError(t, f.mock.PutObject(ctx, "photos",
			fmt.Sprintf("k-%03d", i), []byte("v"), "text/plain", nil))
	}

	var list struct {
		Objects       []map[string]any `json:"objects"`
		NextStartWith string           `json:"nextStartWith"`
	}

	rec := f.do(t, http.MethodGet, f.bucketPath("photos")+"/o", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	assert.Len(t, list.Objects, 150, "all 150 fit in OCI's default page")
	assert.Empty(t, list.NextStartWith)
	assert.Empty(t, rec.Header().Get("opc-next-page"))

	// An explicit limit is still honoured, and still paginates.
	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/o?limit=100", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	list.Objects = nil

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	assert.Len(t, list.Objects, 100)
	assert.NotEmpty(t, list.NextStartWith)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/o?start="+list.NextStartWith, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	list.Objects = nil

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	assert.Len(t, list.Objects, 50)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/o?start=%7Bbroken", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("missing")+"/o", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("photos")+"/o", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = f.do(t, http.MethodPatch, f.bucketPath("photos")+"/o/k-000", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestObjectVersionWire(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	ctx := t.Context()
	require.NoError(t, f.mock.SetVersioningStatus(ctx, "photos", osprovider.VersioningEnabled))
	require.NoError(t, f.mock.PutObject(ctx, "photos", "k", []byte("v1"), "text/plain", nil))

	first, err := f.mock.HeadObject(ctx, "photos", "k")
	require.NoError(t, err)
	require.NoError(t, f.mock.PutObject(ctx, "photos", "k", []byte("v2"), "text/plain", nil))

	rec := f.do(t, http.MethodGet, f.bucketPath("photos")+"/o/k?versionId="+first.VersionID, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "v1", rec.Body.String())
	assert.Equal(t, first.VersionID, rec.Header().Get("version-id"))

	rec = f.do(t, http.MethodHead, f.bucketPath("photos")+"/o/k?versionId="+first.VersionID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "2", rec.Header().Get("Content-Length"))

	rec = f.do(t, http.MethodHead, f.bucketPath("photos")+"/o/k?versionId=nope", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/o/k?versionId=nope", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// A top-level delete on a versioned bucket reports the delete marker.
	rec = f.do(t, http.MethodDelete, f.bucketPath("photos")+"/o/k", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "true", rec.Header().Get("is-delete-marker"))
	assert.NotEmpty(t, rec.Header().Get("version-id"))

	rec = f.do(t, http.MethodDelete, f.bucketPath("photos")+"/o/k?versionId="+first.VersionID, nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, first.VersionID, rec.Header().Get("version-id"))

	rec = f.do(t, http.MethodDelete, f.bucketPath("photos")+"/o/k?versionId=nope", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("photos")+"/objectversions?prefix=k", nil)
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("missing")+"/objectversions", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPost, f.bucketPath("photos")+"/objectversions", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestMultipartWireErrors(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	base := f.bucketPath("photos") + "/u"

	rec := f.do(t, http.MethodPost, base, []byte("{"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "object is required")

	rec = f.do(t, http.MethodPost, base, map[string]any{"object": "big", "storageTier": "Glacier"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"object": "big", "contentType": "text/plain", "metadata": map[string]string{"owner": "ada"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var up struct {
		UploadID  string `json:"uploadId"`
		Namespace string `json:"namespace"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &up))
	assert.Equal(t, f.ns, up.Namespace)

	rec = f.do(t, http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), up.UploadID)

	rec = f.do(t, http.MethodGet, f.bucketPath("missing")+"/u", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodDelete, base, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = f.do(t, http.MethodPut, base+"/big", []byte("aaa"))
	assert.Equal(t, http.StatusBadRequest, rec.Code, "uploadId is required")

	item := base + "/big?uploadId=" + up.UploadID

	rec = f.do(t, http.MethodPut, item, []byte("aaa"))
	assert.Equal(t, http.StatusBadRequest, rec.Code, "uploadPartNum is required")

	rec = f.do(t, http.MethodPut, item+"&uploadPartNum=abc", []byte("aaa"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPut, item+"&uploadPartNum=1", []byte("aaa"))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("ETag"))

	rec = f.do(t, http.MethodGet, item, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"partNumber":1`)

	rec = f.do(t, http.MethodGet, base+"/big?uploadId=nope", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPost, item, []byte("{"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, item, map[string]any{
		"partsToCommit":  []map[string]any{{"partNum": 1}},
		"partsToExclude": []int{2},
	})
	assert.Equal(t, http.StatusNotImplemented, rec.Code, "partsToExclude is rejected, not dropped")

	rec = f.do(t, http.MethodPost, item, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "partsToCommit is required")

	rec = f.do(t, http.MethodPost, item, map[string]any{"partsToCommit": []map[string]any{{"partNum": 9}}})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a part that was never uploaded")

	rec = f.do(t, http.MethodPost, item, map[string]any{"partsToCommit": []map[string]any{{"partNum": 1}}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("ETag"))

	rec = f.do(t, http.MethodDelete, base+"/big?uploadId=nope", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPatch, item, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestPARWireErrors(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")
	require.NoError(t, f.mock.PutObject(t.Context(), "photos", "a.txt", []byte("v"), "text/plain", nil))

	base := f.bucketPath("photos") + "/p"

	rec := f.do(t, http.MethodPost, base, []byte("{"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"name": "bad-time", "objectName": "a.txt", "accessType": "ObjectRead", "timeExpires": "tomorrow",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "RFC3339")

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"name": "bad-access", "objectName": "a.txt", "accessType": "ObjectAppend",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"name": "read", "objectName": "a.txt", "accessType": "ObjectRead",
		"timeExpires": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var par struct {
		ID        string `json:"id"`
		AccessURI string `json:"accessUri"`
		FullPath  string `json:"fullPath"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &par))
	require.NotEmpty(t, par.ID)
	assert.Equal(t, par.AccessURI, par.FullPath)

	rec = f.do(t, http.MethodGet, base+"/"+par.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"accessType":"ObjectRead"`)
	assert.NotContains(t, rec.Body.String(), "accessUri", "a later Get never returns the access URI")

	rec = f.do(t, http.MethodGet, base+"?objectNamePrefix=a", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), par.ID)

	rec = f.do(t, http.MethodGet, f.bucketPath("missing")+"/p", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodGet, base+"/ocid1.preauthenticatedrequest.oc1..missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPatch, base, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = f.do(t, http.MethodPatch, base+"/"+par.ID, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	// Redeem it.
	token := par.AccessURI[len("/p/"):]
	token = token[:strings.Index(token, "/")]

	rec = f.do(t, http.MethodGet, "/p/"+token+"/n/"+f.ns+"/b/photos/o/a.txt", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "v", rec.Body.String())

	rec = f.do(t, http.MethodPut, "/p/"+token+"/n/"+f.ns+"/b/photos/o/a.txt", []byte("nope"))
	assert.Equal(t, http.StatusForbidden, rec.Code, "an ObjectRead PAR does not authorize a write")

	rec = f.do(t, http.MethodGet, "/p/"+token+"/n/"+f.ns+"/b/photos/o/other.txt", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code, "the PAR is bound to its object")

	rec = f.do(t, http.MethodGet, "/p/"+token+"/n/"+f.ns+"/b/other/o/a.txt", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code, "the PAR is bound to its bucket")

	rec = f.do(t, http.MethodGet, "/p/"+token+"/n/"+f.ns+"/b/photos", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a PAR addresses an object under /o/")

	rec = f.do(t, http.MethodGet, "/p/no-such-token/n/"+f.ns+"/b/photos/o/a.txt", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodDelete, base+"/"+par.ID, nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = f.do(t, http.MethodDelete, base+"/"+par.ID, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPARWriteRedemption(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	rec := f.do(t, http.MethodPost, f.bucketPath("photos")+"/p", map[string]any{
		"name": "write", "objectName": "upload.txt", "accessType": "ObjectWrite",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var par struct {
		AccessURI string `json:"accessUri"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &par))

	token := par.AccessURI[len("/p/"):]
	token = token[:strings.Index(token, "/")]

	rec = f.do(t, http.MethodPut, "/p/"+token+"/n/"+f.ns+"/b/photos/o/upload.txt", []byte("written"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	obj, err := f.mock.GetObject(t.Context(), "photos", "upload.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("written"), obj.Data)

	rec = f.do(t, http.MethodGet, "/p/"+token+"/n/"+f.ns+"/b/photos/o/upload.txt", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code, "an ObjectWrite PAR does not authorize a read")
}

func TestRetentionWireErrors(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	base := f.bucketPath("photos") + "/retentionRules"

	rec := f.do(t, http.MethodPost, base, []byte("{"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"displayName": "bad-lock", "timeRuleLocked": "next week",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "RFC3339")

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"displayName": "bad-unit",
		"duration":    map[string]any{"timeAmount": 1, "timeUnit": "MONTHS"},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base, map[string]any{
		"displayName":    "hold",
		"duration":       map[string]any{"timeAmount": 10, "timeUnit": "DAYS"},
		"timeRuleLocked": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339),
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("ETag"))

	var rule struct {
		ID string `json:"id"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))
	require.NotEmpty(t, rule.ID)

	rec = f.do(t, http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), rule.ID)

	rec = f.do(t, http.MethodGet, base+"/"+rule.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"timeUnit":"DAYS"`)

	rec = f.do(t, http.MethodPost, base+"/"+rule.ID, map[string]any{
		"displayName": "hold-longer",
		"duration":    map[string]any{"timeAmount": 20, "timeUnit": "DAYS"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "hold-longer")

	rec = f.do(t, http.MethodPost, base+"/"+rule.ID, []byte("{"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base+"/ocid1.retentionrule.oc1..missing", map[string]any{"displayName": "x"})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodGet, base+"/ocid1.retentionrule.oc1..missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodGet, f.bucketPath("missing")+"/retentionRules", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodDelete, base+"/ocid1.retentionrule.oc1..missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodDelete, base+"/"+rule.ID, nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = f.do(t, http.MethodPatch, base, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = f.do(t, http.MethodPatch, base+"/"+rule.ID, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestLifecycleWireErrors(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "photos")

	path := f.bucketPath("photos") + "/l"

	rec := f.do(t, http.MethodPut, path, []byte("{"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodGet, path, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code, "no policy yet")

	rec = f.do(t, http.MethodDelete, path, nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	tests := []struct {
		name string
		item map[string]any
	}{
		{"unsupported action", map[string]any{
			"name": "r", "action": "TELEPORT", "timeAmount": 1, "timeUnit": "DAYS", "isEnabled": true,
		}},
		{"unsupported time unit", map[string]any{
			"name": "r", "action": "DELETE", "timeAmount": 1, "timeUnit": "MONTHS", "isEnabled": true,
		}},
		{"more than one inclusion prefix", map[string]any{
			"name": "r", "action": "DELETE", "timeAmount": 1, "timeUnit": "DAYS", "isEnabled": true,
			"objectNameFilter": map[string]any{"inclusionPrefixes": []string{"a/", "b/"}},
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, http.MethodPut, path, map[string]any{"items": []map[string]any{tc.item}})
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}

	// Every action the handler does model, including the YEARS unit.
	rec = f.do(t, http.MethodPut, path, map[string]any{"items": []map[string]any{
		{
			"name": "expire", "action": "DELETE", "timeAmount": 30, "timeUnit": "DAYS", "isEnabled": true,
			"objectNameFilter": map[string]any{"inclusionPrefixes": []string{"logs/"}},
		},
		{"name": "archive", "action": "ARCHIVE", "timeAmount": 1, "timeUnit": "YEARS", "isEnabled": true},
		{"name": "infreq", "action": "INFREQUENT_ACCESS", "timeAmount": 10, "timeUnit": "DAYS", "isEnabled": true},
		{"name": "abort", "action": "ABORT", "timeAmount": 7, "isEnabled": false},
	}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = f.do(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Items []struct {
			Name             string `json:"name"`
			Action           string `json:"action"`
			TimeAmount       int64  `json:"timeAmount"`
			TimeUnit         string `json:"timeUnit"`
			ObjectNameFilter *struct {
				InclusionPrefixes []string `json:"inclusionPrefixes"`
			} `json:"objectNameFilter"`
		} `json:"items"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 4)
	assert.Equal(t, "DELETE", body.Items[0].Action)
	assert.Equal(t, []string{"logs/"}, body.Items[0].ObjectNameFilter.InclusionPrefixes)
	assert.Equal(t, int64(365), body.Items[1].TimeAmount, "YEARS is normalised to days")
	assert.Equal(t, "DAYS", body.Items[1].TimeUnit)
	assert.Equal(t, "ABORT", body.Items[3].Action)

	rec = f.do(t, http.MethodDelete, path, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = f.do(t, http.MethodPut, f.bucketPath("missing")+"/l", map[string]any{"items": []map[string]any{}})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodPatch, path, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestObjectActionWireErrors(t *testing.T) {
	f := newFixture(t)
	f.createBucket(t, "src")

	base := f.bucketPath("src") + "/actions"

	rec := f.do(t, http.MethodPost, base+"/renameObject", []byte("{"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base+"/copyObject", []byte("{"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base+"/copyObject", map[string]any{
		"sourceObjectName": "a", "destinationBucket": "dst", "destinationObjectName": "b",
		"destinationNamespace": "someotherns",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "cross-namespace")

	rec = f.do(t, http.MethodPost, base+"/updateObjectStorageTier", []byte("{"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base+"/updateObjectStorageTier", map[string]any{"objectName": "a"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = f.do(t, http.MethodPost, base+"/updateObjectStorageTier", map[string]any{
		"objectName": "missing", "storageTier": "Archive",
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = f.do(t, http.MethodGet, base+"/renameObject", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// copyObject needs the shared work-request store; without one it says so
// rather than pretending the copy was accepted.
func TestCopyObjectWithoutWorkRequests(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("us-ashburn-1"), config.WithCompartmentID(testCompartment))
	mock := osprovider.New(opts)
	f := fixture{handler: ociobjectstorage.New(mock, nil), mock: mock, ns: mock.Namespace()}
	f.createBucket(t, "src")

	rec := f.do(t, http.MethodPost, f.bucketPath("src")+"/actions/copyObject", map[string]any{
		"sourceObjectName": "a", "destinationBucket": "src", "destinationObjectName": "b",
	})
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}
