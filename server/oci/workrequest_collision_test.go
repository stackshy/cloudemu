package oci_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cloudemu "github.com/stackshy/cloudemu/v2"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
)

// The work request poller is registered first, so it must never claim an
// Object Storage path: a key or a bucket named workRequests is user data.
func TestWorkRequestsNamedObjectAndBucketStayReachable(t *testing.T) {
	cloud := cloudemu.NewOCI()
	ts := httptest.NewServer(ociserver.New(ociserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	call := func(method, path string, body []byte) (int, http.Header, []byte) {
		t.Helper()

		req, err := http.NewRequestWithContext(t.Context(), method, ts.URL+path, bytes.NewReader(body))
		require.NoError(t, err)

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		out, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		return resp.StatusCode, resp.Header, out
	}

	_, _, raw := call(http.MethodGet, "/n", nil)

	var ns string

	require.NoError(t, json.Unmarshal(raw, &ns))

	root := "/n/" + ns + "/b"

	for _, bucket := range []string{"tfb", "workRequests"} {
		spec, err := json.Marshal(map[string]string{"name": bucket, "compartmentId": cloud.CompartmentID})
		require.NoError(t, err)

		code, _, body := call(http.MethodPost, root, spec)
		require.Equal(t, http.StatusOK, code, string(body))
	}

	code, _, body := call(http.MethodGet, root+"/workRequests", nil)
	require.Equal(t, http.StatusOK, code, string(body))
	assert.Contains(t, string(body), `"name":"workRequests"`)

	key := root + "/tfb/o/workRequests/x"

	code, _, body = call(http.MethodPut, key, []byte("payload"))
	require.Equal(t, http.StatusOK, code, string(body))

	code, _, body = call(http.MethodGet, key, nil)
	require.Equal(t, http.StatusOK, code, string(body))
	assert.Equal(t, "payload", string(body))

	// The copy's work request still polls, both unversioned (Object Storage's
	// own form) and under a version prefix.
	copySpec, err := json.Marshal(map[string]string{
		"sourceObjectName": "workRequests/x", "destinationRegion": cloud.Region,
		"destinationBucket": "workRequests", "destinationObjectName": "copied",
	})
	require.NoError(t, err)

	code, hdr, body := call(http.MethodPost, root+"/tfb/actions/copyObject", copySpec)
	require.Equal(t, http.StatusAccepted, code, string(body))

	id := hdr.Get("opc-work-request-id")
	require.NotEmpty(t, id)

	for _, poll := range []string{"/workRequests/" + id, "/20160918/workRequests/" + id} {
		code, _, body = call(http.MethodGet, poll, nil)
		require.Equal(t, http.StatusOK, code, string(body))
		assert.Contains(t, string(body), `"status":"SUCCEEDED"`)
	}
}
