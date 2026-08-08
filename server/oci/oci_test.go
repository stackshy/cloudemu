package oci_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	ociprovider "github.com/stackshy/cloudemu/v2/providers/oci"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
)

func TestNewServesWorkRequests(t *testing.T) {
	store := workrequest.New(config.NewOptions(config.WithRegion("us-ashburn-1")))
	id := store.Accept("CREATE_INSTANCE", "ocid1.compartment.oc1..abc")

	srv := ociserver.New(ociserver.Drivers{WorkRequests: store})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/20160918/workRequests/" + id)
	require.NoError(t, err)

	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var wr workrequest.WorkRequest
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&wr))
	assert.Equal(t, id, wr.ID)
}

func TestNewCreatesWorkRequestStoreWhenAbsent(t *testing.T) {
	// A caller assembling Drivers by hand must still get a working poller.
	srv := ociserver.New(ociserver.Drivers{Region: "eu-frankfurt-1"})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/20160918/workRequests")
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestUnroutedRequestIsNotImplemented(t *testing.T) {
	// No services have landed yet, so anything else falls through the empty
	// handler chain.
	srv := ociserver.New(ociserver.Drivers{})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/20160918/instances?compartmentId=x")
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
}

func TestDriversFromCarriesIdentity(t *testing.T) {
	p := ociprovider.New(
		config.WithTenancyOCID("ocid1.tenancy.oc1..custom"),
		config.WithCompartmentID("ocid1.compartment.oc1..dev"),
		config.WithRealm("oc2"),
		config.WithRegion("ap-mumbai-1"),
	)

	d := ociserver.DriversFrom(p)

	assert.Equal(t, "ocid1.tenancy.oc1..custom", d.TenancyOCID)
	assert.Equal(t, "ocid1.compartment.oc1..dev", d.CompartmentID)
	assert.Equal(t, "oc2", d.Realm)
	assert.Equal(t, "ap-mumbai-1", d.Region)
	assert.NotNil(t, d.ResourceDiscovery)
	assert.Nil(t, d.K8sAPI, "K8sAPI is injected by the caller, not the provider")
}

func TestDriversFromProviderBuildsServer(t *testing.T) {
	p := ociprovider.New()

	srv := ociserver.New(ociserver.DriversFrom(p))
	require.NotNil(t, srv)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/20160918/workRequests")
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
