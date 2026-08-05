package vnet

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocateAddressDefaultsSKUAndAllocationMethod(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{})
	require.NoError(t, err)
	assert.NotEmpty(t, eip.AllocationID)
	assert.NotEmpty(t, eip.PublicIP)
	assert.Equal(t, "Standard", eip.SKU)
	assert.Equal(t, "Static", eip.AllocationMethod)
}

func TestAllocateAddressExplicitSKUAndAllocationMethod(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	eip, err := m.AllocateAddress(ctx, driver.ElasticIPConfig{
		SKU:              "Basic",
		AllocationMethod: "Dynamic",
	})
	require.NoError(t, err)
	assert.Equal(t, "Basic", eip.SKU)
	assert.Equal(t, "Dynamic", eip.AllocationMethod)

	got, err := m.DescribeAddresses(ctx, []string{eip.AllocationID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Basic", got[0].SKU)
	assert.Equal(t, "Dynamic", got[0].AllocationMethod)
}
