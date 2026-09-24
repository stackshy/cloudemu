package oci_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/oci"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
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

func TestIdentityIsWired(t *testing.T) {
	p := oci.New()

	require.NotNil(t, p.Identity, "the Identity slot is filled by the identity mock")
}

func TestVCNIsWired(t *testing.T) {
	p := oci.New()

	require.NotNil(t, p.VCN, "the VCN slot is filled by the vcn mock")
}

func TestComputeIsWiredToVCN(t *testing.T) {
	p := oci.New()

	require.NotNil(t, p.Compute, "the Compute slot is filled by the compute mock")

	vcn, err := p.VCN.CreateVPC(t.Context(), netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := p.VCN.CreateSubnet(t.Context(), netdriver.SubnetConfig{
		VPCID: vcn.ID, CIDRBlock: "10.0.1.0/24",
	})
	require.NoError(t, err)

	// A launch reaches the VCN mock for its VNIC, so the instance comes back
	// with the subnet's VCN and an address from it.
	launched, err := p.Compute.RunInstances(t.Context(), computedriver.InstanceConfig{
		InstanceType: "VM.Standard.E4.Flex", SubnetID: subnet.ID,
	}, 1)
	require.NoError(t, err)
	require.Len(t, launched, 1)
	assert.Equal(t, vcn.ID, launched[0].VPCID)
	assert.Equal(t, "10.0.1.2", launched[0].PrivateIP)
}

// TestServiceSlotsAreNilOrLive keeps what the retired per-service-name test
// asserted, without naming a service: a slot reads as nil until a branch fills
// it. A typed nil in an interface field passes a != nil check and then panics,
// which is the failure the old test was really guarding against.
func TestServiceSlotsAreNilOrLive(t *testing.T) {
	p := reflect.ValueOf(*oci.New())

	for i := range p.NumField() {
		slot := p.Field(i)
		if slot.Kind() != reflect.Interface || slot.IsNil() {
			continue
		}

		assert.False(t, slot.Elem().Kind() == reflect.Ptr && slot.Elem().IsNil(),
			"%s holds a typed nil: it reads as wired and panics on use", p.Type().Field(i).Name)
	}
}

func TestNewOCIFactory(t *testing.T) {
	p := cloudemu.NewOCI(config.WithRegion("uk-london-1"))

	require.NotNil(t, p)
	assert.Equal(t, "uk-london-1", p.Region)
}
