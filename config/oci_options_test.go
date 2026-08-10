package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewOptionsOCIDefaults(t *testing.T) {
	o := NewOptions()

	assert.Equal(t, DefaultTenancyOCID, o.TenancyOCID)
	assert.Equal(t, DefaultRealm, o.Realm)
	assert.Equal(t, o.TenancyOCID, o.CompartmentID)
}

func TestCompartmentDefaultsToSuppliedTenancy(t *testing.T) {
	o := NewOptions(WithTenancyOCID("ocid1.tenancy.oc1..custom"))

	assert.Equal(t, "ocid1.tenancy.oc1..custom", o.CompartmentID)
}

func TestExplicitCompartmentWins(t *testing.T) {
	o := NewOptions(
		WithTenancyOCID("ocid1.tenancy.oc1..custom"),
		WithCompartmentID("ocid1.compartment.oc1..dev"),
	)

	assert.Equal(t, "ocid1.compartment.oc1..dev", o.CompartmentID)
}

func TestWithRealm(t *testing.T) {
	assert.Equal(t, "oc2", NewOptions(WithRealm("oc2")).Realm)
}

func TestOCIRegion(t *testing.T) {
	tests := []struct {
		name   string
		region string
		expect string
	}{
		{name: "untouched AWS default is substituted", region: DefaultRegion, expect: DefaultOCIRegion},
		{name: "empty is substituted", region: "", expect: DefaultOCIRegion},
		{name: "explicit OCI region is kept", region: "eu-frankfurt-1", expect: "eu-frankfurt-1"},
		{name: "any other region is kept", region: "us-west-2", expect: "us-west-2"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &Options{Region: tc.region}
			assert.Equal(t, tc.expect, o.OCIRegion())
		})
	}
}

func TestOCIOptionsDoNotDisturbOtherProviders(t *testing.T) {
	o := NewOptions()

	assert.Equal(t, DefaultRegion, o.Region)
	assert.Equal(t, "123456789012", o.AccountID)
	assert.Equal(t, "mock-project", o.ProjectID)
}
