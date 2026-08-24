package virtualmachines

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneralizeInstance(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img", InstanceType: "Standard_D2s_v3"}, 1)
	require.NoError(t, err)

	id := insts[0].ID

	// A running VM cannot be generalized — Azure requires it to be stopped or
	// deallocated first.
	require.Error(t, m.GeneralizeInstance(ctx, id), "generalize should reject a running VM")

	// Deallocate, then generalize succeeds.
	require.NoError(t, m.Deallocate(ctx, id))
	require.NoError(t, m.GeneralizeInstance(ctx, id))

	got, err := m.DescribeInstances(ctx, []string{id}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Generalized, "instance should be generalized")

	// Idempotent.
	require.NoError(t, m.GeneralizeInstance(ctx, id))

	// Missing instance.
	assert.Error(t, m.GeneralizeInstance(ctx, "vm-missing"))
}

func TestGrantAndRevokeDiskAccess(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 64})
	require.NoError(t, err)

	sas, err := m.GrantDiskAccess(ctx, vol.ID, "Read", 300)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(sas, "https://"), "SAS should be an https URI: %s", sas)
	assert.Contains(t, sas, "sp=r")
	assert.Contains(t, sas, "se=")
	assert.Contains(t, sas, "st=")

	// Write access grants read+write.
	wsas, err := m.GrantDiskAccess(ctx, vol.ID, "Write", 60)
	require.NoError(t, err)
	assert.Contains(t, wsas, "sp=rw")

	require.NoError(t, m.RevokeDiskAccess(ctx, vol.ID))

	// Missing disk.
	_, err = m.GrantDiskAccess(ctx, "disk-missing", "Read", 300)
	assert.Error(t, err)
	assert.Error(t, m.RevokeDiskAccess(ctx, "disk-missing"))
}

func TestUpdateKeyPair(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateKeyPair(ctx, driver.KeyPairConfig{Name: "k1", Tags: map[string]string{"env": "test"}})
	require.NoError(t, err)

	newKey := "ssh-rsa AAAAB updated"
	updated, err := m.UpdateKeyPair(ctx, "k1", &newKey, map[string]string{"team": "infra"})
	require.NoError(t, err)
	assert.Equal(t, newKey, updated.PublicKey)
	assert.Equal(t, "infra", updated.Tags["team"])
	_, hadEnv := updated.Tags["env"]
	assert.False(t, hadEnv, "replaced tags should not retain env")

	// Nil publicKey leaves the key material unchanged.
	again, err := m.UpdateKeyPair(ctx, "k1", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, newKey, again.PublicKey)

	// Missing key.
	_, err = m.UpdateKeyPair(ctx, "missing", &newKey, nil)
	assert.Error(t, err)
}
