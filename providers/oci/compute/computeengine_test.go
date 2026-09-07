package compute_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	ocivcn "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	driver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// fakeComputeEngine is a recording config.ComputeEngine, so the OCI provider's
// provision/deprovision/console-output wiring is provable without Docker.
type fakeComputeEngine struct {
	mu            sync.Mutex
	provisioned   []config.ComputeProvisionRequest
	deprovisioned []string
	ip            string
	console       []byte
	// failProvisionOn is the 1-based Provision call number that returns an
	// error (0 disables it). A failed Provision records nothing.
	failProvisionOn int
	provisionCalls  int
}

func (f *fakeComputeEngine) Provision(
	_ context.Context, req config.ComputeProvisionRequest,
) (config.ComputeProvisionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.provisionCalls++

	if f.failProvisionOn > 0 && f.provisionCalls == f.failProvisionOn {
		return config.ComputeProvisionResult{}, errors.New("provision failed")
	}

	f.provisioned = append(f.provisioned, req)

	return config.ComputeProvisionResult{IP: f.ip}, nil
}

func (f *fakeComputeEngine) ConsoleOutput(_ context.Context, _ string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.console, nil
}

func (f *fakeComputeEngine) Deprovision(_ context.Context, instanceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deprovisioned = append(f.deprovisioned, instanceID)

	return nil
}

// newEngineFixture is newFixture with a compute engine wired into the options.
func newEngineFixture(t *testing.T, engine config.ComputeEngine) *fixture {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-ashburn-1"),
		config.WithComputeEngine(engine),
	)

	vcnMock := ocivcn.New(opts)
	computeMock := ocicompute.New(opts)
	computeMock.SetNetworking(vcnMock)

	ctx := t.Context()

	vcn, err := vcnMock.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	require.NoError(t, err)

	subnet, err := vcnMock.CreateSubnet(ctx, netdriver.SubnetConfig{VPCID: vcn.ID, CIDRBlock: "10.0.1.0/24"})
	require.NoError(t, err)

	images, err := computeMock.ListImages(ctx, opts.CompartmentID, "", "")
	require.NoError(t, err)
	require.NotEmpty(t, images)

	return &fixture{
		compute:     computeMock,
		vcn:         vcnMock,
		subnet:      subnet.ID,
		vcnID:       vcn.ID,
		image:       images[0].ID,
		compartment: opts.CompartmentID,
		ctx:         ctx,
	}
}

func TestRunInstancesProvisionsThroughTheComputeEngine(t *testing.T) {
	eng := &fakeComputeEngine{ip: "172.30.1.9", console: []byte("boot log")}
	f := newEngineFixture(t, eng)

	out, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
		ImageID:      f.image,
		InstanceType: shape,
		SubnetID:     f.subnet,
		UserData:     "#!/bin/sh\necho hi",
	}, 2)
	require.NoError(t, err)
	require.Len(t, out, 2)

	// Each instance was provisioned with its image and decoded boot script.
	require.Len(t, eng.provisioned, 2)
	assert.Equal(t, out[0].ID, eng.provisioned[0].InstanceID)
	assert.Equal(t, f.image, eng.provisioned[0].ImageID)
	assert.Equal(t, "#!/bin/sh\necho hi", string(eng.provisioned[0].BootScript))

	// The engine's address replaces the VNIC's synthetic one, and is what a
	// later describe reports.
	for _, inst := range out {
		assert.Equal(t, "172.30.1.9", inst.PrivateIP)
	}

	got, err := f.compute.DescribeInstances(f.ctx, []string{out[0].ID}, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "172.30.1.9", got[0].PrivateIP)

	console, err := f.compute.GetConsoleOutput(f.ctx, out[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "boot log", string(console))
}

func TestTerminateInstanceDeprovisionsThroughTheComputeEngine(t *testing.T) {
	eng := &fakeComputeEngine{ip: "172.30.1.9"}
	f := newEngineFixture(t, eng)

	out, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
		ImageID: f.image, InstanceType: shape, SubnetID: f.subnet,
	}, 1)
	require.NoError(t, err)

	require.NoError(t, f.compute.TerminateInstance(f.ctx, out[0].ID, false))
	assert.Equal(t, []string{out[0].ID}, eng.deprovisioned)

	// TerminateInstances, the portable entry point, tears the backing down too.
	out, err = f.compute.RunInstances(f.ctx, driver.InstanceConfig{
		ImageID: f.image, InstanceType: shape, SubnetID: f.subnet,
	}, 1)
	require.NoError(t, err)

	require.NoError(t, f.compute.TerminateInstances(f.ctx, []string{out[0].ID}))
	assert.Len(t, eng.deprovisioned, 2)
	assert.Equal(t, out[0].ID, eng.deprovisioned[1])
}

func TestRunInstancesRollsBackAPartialBatch(t *testing.T) {
	eng := &fakeComputeEngine{ip: "172.30.1.9", failProvisionOn: 3}
	f := newEngineFixture(t, eng)

	out, err := f.compute.RunInstances(f.ctx, driver.InstanceConfig{
		ImageID: f.image, InstanceType: shape, SubnetID: f.subnet,
	}, 5)
	require.Error(t, err)
	assert.Nil(t, out)

	// The two instances built before the failure were deprovisioned, so no
	// live backing is orphaned.
	assert.Len(t, eng.provisioned, 2)
	assert.Len(t, eng.deprovisioned, 2)

	// Nor is any mock state: no instance, no boot volume, no VNIC survives.
	instances, err := f.compute.DescribeInstances(f.ctx, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, instances)

	vols, err := f.compute.ListBootVolumes(f.ctx, f.compartment)
	require.NoError(t, err)
	assert.Empty(t, vols)

	vnics, err := f.vcn.DescribeVNICs(f.ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, vnics)
}

func TestComputeEngineHooksAreNoOpsWithoutAnEngine(t *testing.T) {
	f := newFixture(t)
	inst := f.launch(t)

	// The synthetic VNIC address is left alone and no console output exists.
	assert.NotEmpty(t, inst.PrivateIP)

	console, err := f.compute.GetConsoleOutput(f.ctx, inst.ID)
	require.NoError(t, err)
	assert.Empty(t, console)

	require.NoError(t, f.compute.TerminateInstance(f.ctx, inst.ID, false))
}

func TestGetConsoleOutputUnknownInstance(t *testing.T) {
	f := newEngineFixture(t, &fakeComputeEngine{})

	_, err := f.compute.GetConsoleOutput(f.ctx, "ocid1.instance.oc1.iad.missing")
	require.Error(t, err)
}
