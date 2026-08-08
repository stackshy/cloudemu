package identity_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	gcpiam "github.com/stackshy/cloudemu/v2/providers/gcp/iam"
	ociprovider "github.com/stackshy/cloudemu/v2/providers/oci"
	ociidentity "github.com/stackshy/cloudemu/v2/providers/oci/identity"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
	"github.com/stackshy/cloudemu/v2/server/oci/identity"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const (
	tenancy   = config.DefaultTenancyOCID
	usersPath = "/20160918/users"
	adminName = "Admins"
)

func newHandler(drv iamdriver.IAM) *identity.Handler {
	opts := config.NewOptions()

	return identity.New(drv, workrequest.New(opts))
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewServer(newHandler(ociidentity.New(config.NewOptions())))
	t.Cleanup(ts.Close)

	return ts
}

// call issues a request and returns the response status, headers and body.
func call(t *testing.T, ts *httptest.Server, method, path, body string) (int, http.Header, []byte) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, ts.URL+path, reader)
	require.NoError(t, err)

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, resp.Header, raw
}

// decode unmarshals a response body into T.
func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()

	var out T
	require.NoError(t, json.Unmarshal(raw, &out))

	return out
}

// createUser creates a user through the wire and returns its OCID.
func createUser(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()

	status, _, raw := call(t, ts, http.MethodPost, usersPath,
		`{"compartmentId":"`+tenancy+`","name":"`+name+`","description":"d"}`)
	require.Equal(t, http.StatusOK, status, string(raw))

	return decode[map[string]any](t, raw)["id"].(string)
}

func TestMatches(t *testing.T) {
	h := newHandler(ociidentity.New(config.NewOptions()))

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "users collection", path: usersPath, want: true},
		{name: "single user", path: usersPath + "/ocid1.user.oc1..aaa", want: true},
		{name: "groups", path: "/20160918/groups", want: true},
		{name: "memberships", path: "/20160918/userGroupMemberships", want: true},
		{name: "policies", path: "/20160918/policies", want: true},
		{name: "compartments", path: "/20160918/compartments", want: true},
		{name: "trailing slash", path: "/20160918/policies/", want: true},

		// The 20160918 prefix is shared with OCI Core and the work request
		// poller, so everything else under it must fall through.
		{name: "core instances", path: "/20160918/instances"},
		{name: "core vcns", path: "/20160918/vcns"},
		{name: "work requests", path: "/20160918/workRequests"},
		{name: "dynamic groups are not served", path: "/20160918/dynamicGroups"},
		{name: "another API version", path: "/20181201/users"},
		{name: "object storage", path: "/n/tenancy/b/bucket/o/key"},
		{name: "root", path: "/"},
		{name: "too many segments", path: usersPath + "/ocid1.user.oc1..aaa/groups"},
		{name: "empty collection segment", path: "/20160918/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			assert.Equal(t, tc.want, h.Matches(req))
		})
	}
}

func TestUserEndpoints(t *testing.T) {
	ts := newServer(t)

	status, header, raw := call(t, ts, http.MethodPost, usersPath,
		`{"compartmentId":"`+tenancy+`","name":"alice","description":"first"}`)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.NotEmpty(t, header.Get(ocirest.HeaderRequestID))

	created := decode[map[string]any](t, raw)
	assert.Equal(t, "alice", created["name"])
	assert.Equal(t, tenancy, created["compartmentId"])
	assert.Equal(t, "ACTIVE", created["lifecycleState"])
	assert.NotNil(t, created["definedTags"])

	id, _ := created["id"].(string)
	assert.True(t, strings.HasPrefix(id, "ocid1.user.oc1.."), "got %q", id)

	status, _, raw = call(t, ts, http.MethodGet, usersPath+"?compartmentId="+tenancy, "")
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decode[[]map[string]any](t, raw), 1)

	status, _, raw = call(t, ts, http.MethodGet, usersPath+"/"+id, "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "alice", decode[map[string]any](t, raw)["name"])

	status, _, raw = call(t, ts, http.MethodPut, usersPath+"/"+id, `{"description":"updated"}`)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "updated", decode[map[string]any](t, raw)["description"])

	status, _, _ = call(t, ts, http.MethodDelete, usersPath+"/"+id, "")
	assert.Equal(t, http.StatusNoContent, status)

	status, _, _ = call(t, ts, http.MethodGet, usersPath+"/"+id, "")
	assert.Equal(t, http.StatusNotFound, status)
}

func TestErrorResponses(t *testing.T) {
	ts := newServer(t)
	id := createUser(t, ts, "alice")

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
		code   string
	}{
		{
			name: "create without a compartment", method: http.MethodPost, path: usersPath,
			body: `{"name":"bob"}`, status: http.StatusBadRequest, code: "InvalidParameter",
		},
		{
			name: "create with a malformed body", method: http.MethodPost, path: usersPath,
			body: `{`, status: http.StatusBadRequest, code: "InvalidParameter",
		},
		{
			name: "duplicate user", method: http.MethodPost, path: usersPath,
			body:   `{"compartmentId":"` + tenancy + `","name":"alice"}`,
			status: http.StatusConflict, code: "Conflict",
		},
		{
			name: "list without a compartment", method: http.MethodGet, path: usersPath,
			status: http.StatusBadRequest, code: "InvalidParameter",
		},
		{
			name: "unknown user", method: http.MethodGet, path: usersPath + "/ocid1.user.oc1..missing",
			status: http.StatusNotFound, code: "NotAuthorizedOrNotFound",
		},
		{
			name: "unsupported verb on a collection", method: http.MethodPut, path: usersPath,
			status: http.StatusMethodNotAllowed, code: "MethodNotAllowed",
		},
		{
			name: "unsupported verb on a resource", method: http.MethodPatch, path: usersPath + "/" + id,
			status: http.StatusMethodNotAllowed, code: "MethodNotAllowed",
		},
		{
			name: "put is not a membership verb", method: http.MethodPut,
			path:   "/20160918/userGroupMemberships/ocid1.usergroupmembership.oc1..aaa",
			status: http.StatusMethodNotAllowed, code: "MethodNotAllowed",
		},
		{
			name: "membership without ids", method: http.MethodPost, path: "/20160918/userGroupMemberships",
			body: `{}`, status: http.StatusBadRequest, code: "InvalidParameter",
		},
		{
			name: "unparseable policy statement", method: http.MethodPost, path: "/20160918/policies",
			body:   `{"compartmentId":"` + tenancy + `","name":"p","statements":["do whatever"]}`,
			status: http.StatusBadRequest, code: "InvalidParameter",
		},
		{
			name: "unknown compartment", method: http.MethodGet,
			path:   "/20160918/compartments/ocid1.compartment.oc1..missing",
			status: http.StatusNotFound, code: "NotAuthorizedOrNotFound",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, _, raw := call(t, ts, tc.method, tc.path, tc.body)
			require.Equal(t, tc.status, status, string(raw))

			body := decode[ocirest.ErrorBody](t, raw)
			assert.Equal(t, tc.code, body.Code)
			assert.NotEmpty(t, body.Message)
		})
	}
}

func TestListsAreScopedToOneCompartment(t *testing.T) {
	ts := newServer(t)

	status, _, raw := call(t, ts, http.MethodPost, "/20160918/compartments",
		`{"compartmentId":"`+tenancy+`","name":"dev"}`)
	require.Equal(t, http.StatusOK, status, string(raw))

	dev, _ := decode[map[string]any](t, raw)["id"].(string)

	createUser(t, ts, "root-user")

	status, _, raw = call(t, ts, http.MethodPost, usersPath,
		`{"compartmentId":"`+dev+`","name":"dev-user"}`)
	require.Equal(t, http.StatusOK, status, string(raw))

	status, _, raw = call(t, ts, http.MethodGet, usersPath+"?compartmentId="+dev, "")
	require.Equal(t, http.StatusOK, status)

	listed := decode[[]map[string]any](t, raw)
	require.Len(t, listed, 1)
	assert.Equal(t, "dev-user", listed[0]["name"])
}

func TestGroupsAndMemberships(t *testing.T) {
	ts := newServer(t)
	userID := createUser(t, ts, "alice")

	status, _, raw := call(t, ts, http.MethodPost, "/20160918/groups",
		`{"compartmentId":"`+tenancy+`","name":"`+adminName+`"}`)
	require.Equal(t, http.StatusOK, status, string(raw))

	groupID, _ := decode[map[string]any](t, raw)["id"].(string)

	status, _, raw = call(t, ts, http.MethodPost, "/20160918/userGroupMemberships",
		`{"userId":"`+userID+`","groupId":"`+groupID+`"}`)
	require.Equal(t, http.StatusOK, status, string(raw))

	membership := decode[map[string]any](t, raw)
	assert.Equal(t, userID, membership["userId"])

	memberID, _ := membership["id"].(string)
	assert.True(t, strings.HasPrefix(memberID, "ocid1.usergroupmembership.oc1.."), "got %q", memberID)

	status, _, raw = call(t, ts, http.MethodGet,
		"/20160918/userGroupMemberships?compartmentId="+tenancy+"&userId="+userID, "")
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decode[[]map[string]any](t, raw), 1)

	status, _, raw = call(t, ts, http.MethodGet,
		"/20160918/userGroupMemberships?compartmentId="+tenancy+"&groupId=ocid1.group.oc1..other", "")
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, decode[[]map[string]any](t, raw))

	status, _, raw = call(t, ts, http.MethodGet, "/20160918/userGroupMemberships/"+memberID, "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, groupID, decode[map[string]any](t, raw)["groupId"])

	status, _, _ = call(t, ts, http.MethodDelete, "/20160918/userGroupMemberships/"+memberID, "")
	assert.Equal(t, http.StatusNoContent, status)
}

func TestPolicyEndpoints(t *testing.T) {
	ts := newServer(t)

	status, _, raw := call(t, ts, http.MethodPost, "/20160918/policies",
		`{"compartmentId":"`+tenancy+`","name":"admins","description":"d",`+
			`"statements":["Allow group Admins to manage all-resources in tenancy"]}`)
	require.Equal(t, http.StatusOK, status, string(raw))

	created := decode[map[string]any](t, raw)
	assert.Len(t, created["statements"], 1)
	assert.NotEmpty(t, created["versionDate"])

	id, _ := created["id"].(string)

	status, _, raw = call(t, ts, http.MethodPut, "/20160918/policies/"+id,
		`{"statements":["Allow group Admins to read buckets in tenancy"]}`)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Equal(t, []any{"Allow group Admins to read buckets in tenancy"},
		decode[map[string]any](t, raw)["statements"])

	status, _, raw = call(t, ts, http.MethodGet, "/20160918/policies?compartmentId="+tenancy, "")
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decode[[]map[string]any](t, raw), 1)

	status, _, _ = call(t, ts, http.MethodDelete, "/20160918/policies/"+id, "")
	assert.Equal(t, http.StatusNoContent, status)
}

func TestCompartmentEndpoints(t *testing.T) {
	ts := newServer(t)

	status, header, raw := call(t, ts, http.MethodPost, "/20160918/compartments",
		`{"compartmentId":"`+tenancy+`","name":"dev","description":"engineering"}`)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.NotEmpty(t, header.Get(ocirest.HeaderWorkRequestID), "compartment mutations are asynchronous in OCI")

	dev := decode[map[string]any](t, raw)
	assert.Equal(t, tenancy, dev["compartmentId"], "compartmentId names the parent")
	assert.Equal(t, true, dev["isAccessible"])

	devID, _ := dev["id"].(string)

	status, _, raw = call(t, ts, http.MethodPost, "/20160918/compartments",
		`{"compartmentId":"`+devID+`","name":"team"}`)
	require.Equal(t, http.StatusOK, status, string(raw))

	status, _, raw = call(t, ts, http.MethodGet, "/20160918/compartments?compartmentId="+tenancy, "")
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decode[[]map[string]any](t, raw), 1, "a plain list returns direct children only")

	status, _, raw = call(t, ts, http.MethodGet,
		"/20160918/compartments?compartmentId="+tenancy+"&compartmentIdInSubtree=true", "")
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decode[[]map[string]any](t, raw), 2, "the subtree flag descends the tree")

	status, _, raw = call(t, ts, http.MethodGet, "/20160918/compartments/"+tenancy, "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "root", decode[map[string]any](t, raw)["name"], "the tenancy is the root compartment")

	status, _, raw = call(t, ts, http.MethodPut, "/20160918/compartments/"+devID, `{"name":"development"}`)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Equal(t, "development", decode[map[string]any](t, raw)["name"])

	// dev still holds team, so OCI refuses to delete it.
	status, _, _ = call(t, ts, http.MethodDelete, "/20160918/compartments/"+devID, "")
	assert.Equal(t, http.StatusConflict, status)
}

func TestPagination(t *testing.T) {
	ts := newServer(t)

	for _, name := range []string{"alice", "bob", "carol"} {
		createUser(t, ts, name)
	}

	status, header, raw := call(t, ts, http.MethodGet, usersPath+"?compartmentId="+tenancy+"&limit=2", "")
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decode[[]map[string]any](t, raw), 2)
	require.Equal(t, "2", header.Get(ocirest.HeaderNextPage))

	status, header, raw = call(t, ts, http.MethodGet,
		usersPath+"?compartmentId="+tenancy+"&limit=2&page=2", "")
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decode[[]map[string]any](t, raw), 1)
	assert.Empty(t, header.Get(ocirest.HeaderNextPage), "the last page carries no cursor")
}

func TestRequestIDIsEchoed(t *testing.T) {
	ts := newServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		ts.URL+usersPath+"?compartmentId="+tenancy, nil)
	require.NoError(t, err)
	req.Header.Set(ocirest.HeaderRequestID, "caller-supplied-id")

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, "caller-supplied-id", resp.Header.Get(ocirest.HeaderRequestID))
}

func TestDriverWithoutTheOCICapabilities(t *testing.T) {
	// The GCP IAM mock satisfies driver.IAM but models neither compartments nor
	// statement policies, so every endpoint answers 501 rather than a wrong shape.
	ts := httptest.NewServer(newHandler(gcpiam.New(config.NewOptions())))
	defer ts.Close()

	for _, path := range []string{usersPath, "/20160918/groups", "/20160918/userGroupMemberships",
		"/20160918/policies", "/20160918/compartments"} {
		status, _, raw := call(t, ts, http.MethodGet, path+"?compartmentId="+tenancy, "")
		assert.Equal(t, http.StatusNotImplemented, status, path)
		assert.Equal(t, "NotImplemented", decode[ocirest.ErrorBody](t, raw).Code)
	}
}

func TestRegisteredInTheOCIServer(t *testing.T) {
	ts := httptest.NewServer(ociserver.New(ociserver.DriversFrom(ociprovider.New())))
	defer ts.Close()

	status, header, raw := call(t, ts, http.MethodPost, "/20160918/compartments",
		`{"compartmentId":"`+tenancy+`","name":"dev"}`)
	require.Equal(t, http.StatusOK, status, string(raw))

	// The shared poller, registered ahead of every service, answers the work
	// request this handler recorded.
	workID := header.Get(ocirest.HeaderWorkRequestID)
	require.NotEmpty(t, workID)

	status, _, raw = call(t, ts, http.MethodGet, "/20160918/workRequests/"+workID, "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "CREATE_COMPARTMENT", decode[map[string]any](t, raw)["operationType"])
}

func TestMalformedPathIsRejected(t *testing.T) {
	// Unreachable through server.Server, which calls Matches first, but
	// ServeHTTP stays correct when mounted on a plain mux.
	h := newHandler(ociidentity.New(config.NewOptions()))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/20160918/users/a/b", nil))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
