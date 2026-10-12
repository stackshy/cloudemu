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

	"github.com/stackshy/cloudemu/v2/config"
	ociprovider "github.com/stackshy/cloudemu/v2/providers/oci"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
)

// The Object Storage handler gets the same identity-backed compartment check
// as VCN: a bucket created in, or moved to, a compartment that does not exist
// is 404 NotAuthorizedOrNotFound.
func TestObjectStorageCompartmentGate(t *testing.T) {
	p := ociprovider.New()
	ts := httptest.NewServer(ociserver.New(ociserver.DriversFrom(p)))
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/n")
	require.NoError(t, err)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	var ns string

	require.NoError(t, json.Unmarshal(raw, &ns))

	post := func(path string, body map[string]any) int {
		b, err := json.Marshal(body)
		require.NoError(t, err)

		resp, err := ts.Client().Post(ts.URL+path, "application/json", bytes.NewReader(b))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		return resp.StatusCode
	}

	root := "/n/" + ns + "/b"

	assert.Equal(t, http.StatusNotFound,
		post(root, map[string]any{"name": "ghost", "compartmentId": "ocid1.compartment.oc1..doesnotexist"}))
	assert.Equal(t, http.StatusOK,
		post(root, map[string]any{"name": "real", "compartmentId": config.DefaultTenancyOCID}))
	assert.Equal(t, http.StatusNotFound,
		post(root+"/real", map[string]any{"compartmentId": "ocid1.compartment.oc1..doesnotexist"}))
}
