package oci_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/oci"
)

func TestNewAppliesIdentityDefaults(t *testing.T) {
	p := oci.New()

	assert.Equal(t, config.DefaultTenancyOCID, p.TenancyOCID)
	assert.Equal(t, config.DefaultRealm, p.Realm)
	// The AWS default region is not a real OCI region, so it is substituted.
	assert.Equal(t, config.DefaultOCIRegion, p.Region)
	assert.Equal(t, p.TenancyOCID, p.CompartmentID)
}

func TestNewHonoursIdentityOptions(t *testing.T) {
	p := oci.New(
		config.WithTenancyOCID("ocid1.tenancy.oc1..custom"),
		config.WithCompartmentID("ocid1.compartment.oc1..dev"),
		config.WithRealm("oc2"),
		config.WithRegion("eu-frankfurt-1"),
	)

	assert.Equal(t, "ocid1.tenancy.oc1..custom", p.TenancyOCID)
	assert.Equal(t, "ocid1.compartment.oc1..dev", p.CompartmentID)
	assert.Equal(t, "oc2", p.Realm)
	assert.Equal(t, "eu-frankfurt-1", p.Region)
}

func TestCompartmentDefaultsToCustomTenancy(t *testing.T) {
	// A caller who sets only the tenancy must not get the built-in root as
	// their default compartment.
	p := oci.New(config.WithTenancyOCID("ocid1.tenancy.oc1..custom"))

	assert.Equal(t, "ocid1.tenancy.oc1..custom", p.CompartmentID)
}

func TestResourceDiscoveryIsWired(t *testing.T) {
	p := oci.New()

	require.NotNil(t, p.ResourceDiscovery, "discovery engine must exist even with no services")
}

func TestUnimplementedServicesAreNil(t *testing.T) {
	// Every service slot is declared up front, so a service that has not
	// landed yet reads as nil rather than failing to compile.
	p := oci.New()

	assert.Nil(t, p.ObjectStorage)
	assert.Nil(t, p.Compute)
	assert.Nil(t, p.VCN)
}

func TestIdentityIsWired(t *testing.T) {
	p := oci.New()

	require.NotNil(t, p.Identity, "the Identity slot is filled by the identity mock")
}

func TestNewOCIFactory(t *testing.T) {
	p := cloudemu.NewOCI(config.WithRegion("uk-london-1"))

	require.NotNil(t, p)
	assert.Equal(t, "uk-london-1", p.Region)
}
