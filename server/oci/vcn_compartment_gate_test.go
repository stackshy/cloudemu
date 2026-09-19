package oci_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	ociprovider "github.com/stackshy/cloudemu/v2/providers/oci"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
)

// TestCreateVCNCompartmentGate proves the VCN create handler rejects a create
// into a nonexistent compartment with 404 NotAuthorizedOrNotFound, as real OCI
// does, while a create into the seeded root tenancy compartment succeeds.
func TestCreateVCNCompartmentGate(t *testing.T) {
	p := ociprovider.New()

	srv := ociserver.New(ociserver.DriversFrom(p))

	ts := httptest.NewServer(srv)
	defer ts.Close()

	createVCN := func(compartmentID string) *http.Response {
		body, err := json.Marshal(map[string]any{
			"compartmentId": compartmentID,
			"cidrBlock":     "10.0.0.0/16",
			"displayName":   "gate-test",
		})
		require.NoError(t, err)

		resp, err := ts.Client().Post(ts.URL+"/20160918/vcns", "application/json", bytes.NewReader(body))
		require.NoError(t, err)

		return resp
	}

	t.Run("root tenancy compartment succeeds", func(t *testing.T) {
		resp := createVCN(config.DefaultTenancyOCID)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("nonexistent compartment is 404 NotAuthorizedOrNotFound", func(t *testing.T) {
		resp := createVCN("ocid1.compartment.oc1..doesnotexist")
		defer resp.Body.Close()

		require.Equal(t, http.StatusNotFound, resp.StatusCode)

		var errBody struct {
			Code string `json:"code"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
		assert.Equal(t, "NotAuthorizedOrNotFound", errBody.Code)
	})
}
