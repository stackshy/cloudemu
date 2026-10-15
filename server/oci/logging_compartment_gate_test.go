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

// TestLogGroupCompartmentGate proves the server wires Identity's compartment
// check into the Logging handler: a log group created or moved into a
// nonexistent compartment is rejected with 404 NotAuthorizedOrNotFound, as
// real OCI does, while the seeded root tenancy compartment succeeds.
func TestLogGroupCompartmentGate(t *testing.T) {
	p := ociprovider.New()

	srv := ociserver.New(ociserver.DriversFrom(p))

	ts := httptest.NewServer(srv)
	defer ts.Close()

	post := func(path string, body map[string]any) *http.Response {
		raw, err := json.Marshal(body)
		require.NoError(t, err)

		resp, err := ts.Client().Post(ts.URL+path, "application/json", bytes.NewReader(raw))
		require.NoError(t, err)

		return resp
	}

	codeOf := func(resp *http.Response) string {
		var errBody struct {
			Code string `json:"code"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))

		return errBody.Code
	}

	t.Run("create in the root tenancy compartment succeeds", func(t *testing.T) {
		resp := post("/20200531/logGroups", map[string]any{
			"compartmentId": config.DefaultTenancyOCID, "displayName": "gate-ok",
		})
		defer resp.Body.Close()

		assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	})

	t.Run("create in a nonexistent compartment is 404 NotAuthorizedOrNotFound", func(t *testing.T) {
		resp := post("/20200531/logGroups", map[string]any{
			"compartmentId": "ocid1.compartment.oc1..doesnotexist", "displayName": "gate-missing",
		})
		defer resp.Body.Close()

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, "NotAuthorizedOrNotFound", codeOf(resp))
	})

	t.Run("move into a nonexistent compartment is 404 NotAuthorizedOrNotFound", func(t *testing.T) {
		created := post("/20200531/logGroups", map[string]any{
			"compartmentId": config.DefaultTenancyOCID, "displayName": "gate-move",
		})
		created.Body.Close()
		require.Equal(t, http.StatusAccepted, created.StatusCode)

		wr, err := ts.Client().Get(ts.URL + "/20200531/workRequests/" + created.Header.Get("opc-work-request-id"))
		require.NoError(t, err)

		defer wr.Body.Close()

		var work struct {
			Resources []struct {
				Identifier string `json:"identifier"`
			} `json:"resources"`
		}
		require.NoError(t, json.NewDecoder(wr.Body).Decode(&work))
		require.Len(t, work.Resources, 1)

		resp := post("/20200531/logGroups/"+work.Resources[0].Identifier+"/actions/changeCompartment",
			map[string]any{"compartmentId": "ocid1.compartment.oc1..doesnotexist"})
		defer resp.Body.Close()

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, "NotAuthorizedOrNotFound", codeOf(resp))
	})
}
