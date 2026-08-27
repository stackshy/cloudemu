package vnet

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestNSG(t *testing.T, m *Mock, vpcID string) string {
	t.Helper()

	ctx := context.Background()

	info, err := m.CreateSecurityGroup(ctx, driver.SecurityGroupConfig{Name: "nsg-1", VPCID: vpcID})
	require.NoError(t, err)

	require.NoError(t, m.PutAzureNSGMetadata(ctx, info.ID, driver.AzureNSGMetadata{Location: "eastus"}))

	return info.ID
}

func TestUpsertAzureNSGRule(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpcID := createTestVPC(t, m)
	nsgID := createTestNSG(t, m, vpcID)

	meta, err := m.UpsertAzureNSGRule(ctx, nsgID, driver.AzureNSGRule{
		Name: "Allow-SSH", Priority: 100, Direction: "Inbound", Access: "Allow", Protocol: "Tcp",
		SourceAddressPrefix: "*", DestinationAddressPrefix: "*", SourcePortRange: "*", DestinationPortRange: "22",
	})
	require.NoError(t, err)
	require.Len(t, meta.SecurityRules, 1)
	assert.Equal(t, "Allow-SSH", meta.SecurityRules[0].Name)

	// A second rule must not disturb the first.
	meta, err = m.UpsertAzureNSGRule(ctx, nsgID, driver.AzureNSGRule{
		Name: "Allow-HTTP", Priority: 110, Direction: "Inbound", Access: "Allow", Protocol: "Tcp",
		DestinationPortRange: "80",
	})
	require.NoError(t, err)
	require.Len(t, meta.SecurityRules, 2)

	// Re-PUT of the same name replaces in place rather than appending.
	meta, err = m.UpsertAzureNSGRule(ctx, nsgID, driver.AzureNSGRule{
		Name: "Allow-SSH", Priority: 100, Direction: "Inbound", Access: "Deny", Protocol: "Tcp",
		DestinationPortRange: "22",
	})
	require.NoError(t, err)
	require.Len(t, meta.SecurityRules, 2)

	for _, r := range meta.SecurityRules {
		if r.Name == "Allow-SSH" {
			assert.Equal(t, "Deny", r.Access)
		}
	}
}

func TestUpsertAzureNSGRuleNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.UpsertAzureNSGRule(context.Background(), "nsg-missing", driver.AzureNSGRule{Name: "r1"})
	require.Error(t, err)
}

func TestDeleteAzureNSGRule(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpcID := createTestVPC(t, m)
	nsgID := createTestNSG(t, m, vpcID)

	_, err := m.UpsertAzureNSGRule(ctx, nsgID, driver.AzureNSGRule{Name: "r1", Priority: 100, Direction: "Inbound"})
	require.NoError(t, err)
	_, err = m.UpsertAzureNSGRule(ctx, nsgID, driver.AzureNSGRule{Name: "r2", Priority: 110, Direction: "Inbound"})
	require.NoError(t, err)

	require.NoError(t, m.DeleteAzureNSGRule(ctx, nsgID, "r1"))

	md, found := m.GetAzureNSGMetadata(ctx, nsgID)
	require.True(t, found)
	require.Len(t, md.SecurityRules, 1)
	assert.Equal(t, "r2", md.SecurityRules[0].Name)
}

func TestDeleteAzureNSGRuleNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpcID := createTestVPC(t, m)
	nsgID := createTestNSG(t, m, vpcID)

	err := m.DeleteAzureNSGRule(ctx, nsgID, "missing")
	require.Error(t, err)

	err = m.DeleteAzureNSGRule(ctx, "nsg-missing", "missing")
	require.Error(t, err)
}

// TestUpsertAzureNSGRuleConcurrent guards the sub-resource CRUD path against
// lost updates: N goroutines each add a distinct rule to the SAME NSG. With a
// non-atomic Get()+Set() read-modify-write all but a handful silently vanish;
// the store.Update path keeps every one. Run with -race.
func TestUpsertAzureNSGRuleConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vpcID := createTestVPC(t, m)
	nsgID := createTestNSG(t, m, vpcID)

	const n = 50

	var wg sync.WaitGroup

	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()

			_, upErr := m.UpsertAzureNSGRule(ctx, nsgID, driver.AzureNSGRule{
				Name: fmt.Sprintf("rule-%d", i), Priority: 100 + i, Direction: "Inbound", Access: "Allow",
			})
			assert.NoError(t, upErr)
		}(i)
	}

	wg.Wait()

	md, found := m.GetAzureNSGMetadata(ctx, nsgID)
	require.True(t, found)
	assert.Len(t, md.SecurityRules, n, "every concurrently-added security rule must survive")
}
