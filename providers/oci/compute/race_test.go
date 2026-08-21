package compute_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	driver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestConcurrentOperationsDoNotDeadlock exercises the paths that cross stores
// under one lock — a launch writing the instance, its boot volume and its VNIC
// attachment, an attach reading one store before writing another, and a pool
// sync driving launches and terminates. It fails as a hang if an exported
// method ever calls another one that takes the same lock.
func TestConcurrentOperationsDoNotDeadlock(t *testing.T) {
	f := newFixture(t)

	const workers = 8

	var wg sync.WaitGroup

	launch := func() {
		defer wg.Done()

		out, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
			ImageID: f.image, InstanceType: shape, SubnetID: f.subnet,
		}, 1)
		if !assert.NoError(t, err) {
			return
		}

		vol, err := f.compute.CreateVolume(f.ctx, driver.VolumeConfig{Size: 50})
		if !assert.NoError(t, err) {
			return
		}

		assert.NoError(t, f.compute.AttachVolume(f.ctx, vol.ID, out[0].ID, ""))
		assert.NoError(t, f.compute.DetachVolume(f.ctx, vol.ID))
		assert.NoError(t, f.compute.SetTags(out[0].ID, map[string]string{"role": "worker"}))
		assert.NoError(t, f.compute.StopInstances(f.ctx, []string{out[0].ID}))
		assert.NoError(t, f.compute.TerminateInstance(f.ctx, out[0].ID, false))
		assert.NoError(t, f.compute.DeleteVolume(f.ctx, vol.ID))
	}

	read := func() {
		defer wg.Done()

		_, err := f.compute.DescribeInstances(f.ctx, nil, nil)
		assert.NoError(t, err)

		_, err = f.compute.DescribeVolumes(f.ctx, nil)
		assert.NoError(t, err)

		_, err = f.compute.ListVolumeAttachments(f.ctx, f.compartment, "", "")
		assert.NoError(t, err)

		_, err = f.compute.ListBootVolumes(f.ctx, f.compartment)
		assert.NoError(t, err)
	}

	wg.Add(2 * workers)

	for range workers {
		go launch()
		go read()
	}

	wg.Wait()
}

// TestConcurrentPoolScalingDoesNotDeadlock drives the instance pool paths,
// which launch and terminate instances while holding no lock of their own.
func TestConcurrentPoolScalingDoesNotDeadlock(t *testing.T) {
	f := newFixture(t)

	cfg, err := f.compute.CreateInstanceConfiguration(f.ctx, "pool-tpl", ocicompute.LaunchSpec{
		Shape: shape, ImageID: f.image, SubnetID: f.subnet,
	}, nil)
	require.NoError(t, err)

	pool, err := f.compute.CreateInstancePool(f.ctx, "pool", cfg.ID, 1, nil, nil)
	require.NoError(t, err)

	var wg sync.WaitGroup

	wg.Add(6) //nolint:mnd // three resizers and three readers

	for _, size := range []int{2, 3, 1} {
		go func() {
			defer wg.Done()

			_, err := f.compute.UpdateInstancePool(f.ctx, pool.ID, ocicompute.Update{}, size)
			assert.NoError(t, err)
		}()

		go func() {
			defer wg.Done()

			_, err := f.compute.ListInstancePoolInstances(f.ctx, pool.ID)
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	require.NoError(t, f.compute.TerminateInstancePool(f.ctx, pool.ID))
}
