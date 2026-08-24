package virtualmachines

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPowerOffKeepsAllocated(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{InstanceType: "Standard_B1s"}, 1)
	require.NoError(t, err)

	id := insts[0].ID
	require.NoError(t, m.PowerOff(ctx, id))

	got, err := m.DescribeInstances(ctx, []string{id}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)

	assert.Equal(t, compute.StateStopped, got[0].State)
	assert.Equal(t, "stopped", got[0].PowerState)
}

func TestDeallocateReleasesCompute(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{InstanceType: "Standard_B1s"}, 1)
	require.NoError(t, err)

	id := insts[0].ID
	require.NoError(t, m.Deallocate(ctx, id))

	got, err := m.DescribeInstances(ctx, []string{id}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)

	assert.Equal(t, compute.StateStopped, got[0].State)
	assert.Equal(t, "deallocated", got[0].PowerState)
}

func TestUpdateInstanceInPlace(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{
		InstanceType: "Standard_B1s",
		Tags:         map[string]string{"env": "dev"},
	}, 1)
	require.NoError(t, err)

	id := insts[0].ID

	err = m.UpdateInstance(ctx, id, driver.InstanceConfig{
		InstanceType: "Standard_D2s_v3",
		Tags:         map[string]string{"env": "prod"},
	})
	require.NoError(t, err)

	got, err := m.DescribeInstances(ctx, []string{id}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1, "update must not create a duplicate")

	assert.Equal(t, id, got[0].ID, "ID preserved across in-place update")
	assert.Equal(t, "Standard_D2s_v3", got[0].InstanceType)
	assert.Equal(t, "prod", got[0].Tags["env"])
}

func TestUpdateInstanceNotFound(t *testing.T) {
	m := newTestMock()
	err := m.UpdateInstance(context.Background(), "missing", driver.InstanceConfig{})
	assert.Error(t, err)
}

func TestGenerateKeyPairReturnsRealKeys(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateKeyPair(ctx, driver.KeyPairConfig{Name: "key-1"})
	require.NoError(t, err)

	got, err := m.GenerateKeyPair(ctx, "key-1")
	require.NoError(t, err)

	assert.Contains(t, got.PrivateKey, "PRIVATE KEY")
	assert.Contains(t, got.PublicKey, "ssh-rsa ")

	// The generated public key is persisted so a later describe reflects it.
	keys, err := m.DescribeKeyPairs(ctx, []string{"key-1"})
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, got.PublicKey, keys[0].PublicKey)
}

func TestGenerateKeyPairMissing(t *testing.T) {
	m := newTestMock()
	_, err := m.GenerateKeyPair(context.Background(), "nope")
	assert.Error(t, err)
}
