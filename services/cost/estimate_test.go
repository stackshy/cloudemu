package cost

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/pricing"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// fakeInventory is a canned resource source for exercising the estimator
// without standing up a full discovery engine.
type fakeInventory struct {
	res []resourcediscovery.Resource
	err error
}

func (f fakeInventory) ListAll(context.Context) ([]resourcediscovery.Resource, error) {
	return f.res, f.err
}

// twoPricedOneFree holds two always-on resources that price positively (a VM
// and an idle public IP) plus one usage-based resource that prices at zero (an
// object-storage bucket), so the always-on filter has something to drop.
func twoPricedOneFree() []resourcediscovery.Resource {
	return []resourcediscovery.Resource{
		{Provider: "azure", Service: "compute", Type: "Instance", ID: "vm-1", SKU: "Standard_D2s_v3", Region: "eastus"},
		{Provider: "azure", Service: "networking", Type: "ElasticIP", ID: "ip-1", Region: "eastus"},
		{Provider: "azure", Service: "storage", Type: "Bucket", ID: "blob-1", Region: "eastus"},
	}
}

func TestEstimate_PricesAndDropsFreeResources(t *testing.T) {
	res := twoPricedOneFree()

	wantVM := pricing.Monthly("azure", "compute", "Instance", "Standard_D2s_v3", "eastus", nil)
	wantIP := pricing.Monthly("azure", "networking", "ElasticIP", "", "eastus", nil)
	require.Positive(t, wantVM)
	require.Positive(t, wantIP)

	lines, total, err := Estimate(context.Background(), fakeInventory{res: res})
	require.NoError(t, err)

	// The zero-cost bucket is dropped; only the two always-on resources remain.
	require.Len(t, lines, 2)

	byID := map[string]Line{}
	for _, l := range lines {
		byID[l.ID] = l
	}

	assert.InDelta(t, wantVM, byID["vm-1"].MonthlyUSD, 1e-9)
	assert.Equal(t, "compute", byID["vm-1"].Service)
	assert.InDelta(t, wantIP, byID["ip-1"].MonthlyUSD, 1e-9)
	assert.Equal(t, "networking", byID["ip-1"].Service)

	_, hasBucket := byID["blob-1"]
	assert.False(t, hasBucket, "zero-cost resources must not appear as lines")

	assert.InDelta(t, wantVM+wantIP, total, 1e-9, "total is the sum of the priced lines")
}

func TestServiceMonthly_BucketsByService(t *testing.T) {
	wantVM := pricing.Monthly("azure", "compute", "Instance", "Standard_D2s_v3", "eastus", nil)
	wantIP := pricing.Monthly("azure", "networking", "ElasticIP", "", "eastus", nil)

	byService, err := ServiceMonthly(context.Background(), fakeInventory{res: twoPricedOneFree()})
	require.NoError(t, err)

	require.Len(t, byService, 2, "only the two priced services are present")
	assert.InDelta(t, wantVM, byService["compute"], 1e-9)
	assert.InDelta(t, wantIP, byService["networking"], 1e-9)
	_, hasStorage := byService["storage"]
	assert.False(t, hasStorage)
}

func TestEstimate_NilInventory(t *testing.T) {
	lines, total, err := Estimate(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, lines)
	assert.Zero(t, total)
}

func TestServiceMonthly_SumsSameService(t *testing.T) {
	res := []resourcediscovery.Resource{
		{Provider: "azure", Service: "compute", Type: "Instance", ID: "vm-1", SKU: "Standard_D2s_v3", Region: "eastus"},
		{Provider: "azure", Service: "compute", Type: "Instance", ID: "vm-2", SKU: "Standard_D2s_v3", Region: "eastus"},
	}

	one := pricing.Monthly("azure", "compute", "Instance", "Standard_D2s_v3", "eastus", nil)

	byService, err := ServiceMonthly(context.Background(), fakeInventory{res: res})
	require.NoError(t, err)

	require.Len(t, byService, 1)
	assert.InDelta(t, 2*one, byService["compute"], 1e-9, "two same-service resources sum into one bucket")
}
