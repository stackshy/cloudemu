package virtualmachines

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/vnet"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const rollbackNICRG = "rg-1"

// newVMMockWithVNet builds a VM mock wired to a real vnet mock as its
// NICAttacher (mirroring the provider factory's SetNICAttacher), optionally
// backed by a compute engine, so a failed RunInstances exercises the real
// attach/detach cross-reference path end to end.
func newVMMockWithVNet(engine config.ComputeEngine) (*Mock, *vnet.Mock) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	vmOpts := []config.Option{config.WithClock(clk), config.WithAccountID("test-sub")}
	if engine != nil {
		vmOpts = append(vmOpts, config.WithComputeEngine(engine))
	}

	net := vnet.New(config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub")))
	m := New(config.NewOptions(vmOpts...))
	m.SetNICAttacher(net)

	return m, net
}

// createSharedNIC provisions a single NIC used to force a mid-batch attach
// failure (the second instance in a count=2 batch cannot attach it).
func createSharedNIC(t *testing.T, net *vnet.Mock, name string) {
	t.Helper()

	if _, err := net.CreateOrUpdateNetworkInterface(context.Background(), rollbackNICRG, name, netdriver.AzureNICConfig{
		Location: "eastus",
		IPConfigs: []netdriver.AzureIPConfig{{
			Name:             "ipconfig1",
			PrivateIP:        "10.0.1.50",
			AllocationMethod: "Static",
			Primary:          true,
		}},
	}); err != nil {
		t.Fatalf("create nic %q: %v", name, err)
	}
}

// assertNICReleased asserts the NIC is no longer attached to any VM: its
// back-reference is cleared, it can be deleted (not "in use"), and — before
// deletion — it can be re-attached to a fresh instance.
func assertNICReleased(ctx context.Context, t *testing.T, m *Mock, net *vnet.Mock, name string) {
	t.Helper()

	got, err := net.GetNetworkInterface(ctx, rollbackNICRG, name)
	require.NoError(t, err)
	assert.Empty(t, got.VirtualMachineID, "NIC must not stay attached to a rolled-back VM")

	// A freed NIC re-attaches to a fresh instance...
	reCfg := driver.InstanceConfig{
		ImageID:           "img-123",
		InstanceType:      "Standard_B1s",
		ResourceGroup:     rollbackNICRG,
		NetworkInterfaces: []driver.AzureNICRef{{ResourceGroup: rollbackNICRG, Name: name}},
	}

	insts, err := m.RunInstances(ctx, reCfg, 1)
	require.NoError(t, err, "freed NIC must be re-attachable")
	require.Len(t, insts, 1)

	// ...and once that VM is terminated the NIC is deletable (not "in use").
	require.NoError(t, m.TerminateInstances(ctx, []string{insts[0].ID}))
	require.NoError(t, net.DeleteNetworkInterface(ctx, rollbackNICRG, name),
		"a released NIC must be deletable, never stuck reporting in-use")
}

// TestRunInstancesRollbackReleasesNICsOnAttachFailure reproduces the HIGH bug:
// a multi-count RunInstances whose later instance fails to attach a NIC must not
// leave the earlier instance's NIC pointing at a now-deleted VM. The whole batch
// is rolled back, so the shared NIC must be released, not orphaned.
func TestRunInstancesRollbackReleasesNICsOnAttachFailure(t *testing.T) {
	ctx := context.Background()
	m, net := newVMMockWithVNet(nil)

	const nicName = "nic-shared"
	createSharedNIC(t, net, nicName)

	cfg := driver.InstanceConfig{
		ImageID:           "img-123",
		InstanceType:      "Standard_B1s",
		ResourceGroup:     rollbackNICRG,
		NetworkInterfaces: []driver.AzureNICRef{{ResourceGroup: rollbackNICRG, Name: nicName}},
	}

	// count=2: instance 1 attaches nic-shared, instance 2's attach fails as
	// designed (a NIC attaches to one VM at a time), so the batch rolls back.
	insts, err := m.RunInstances(ctx, cfg, 2)
	require.Error(t, err, "second instance must fail to attach the already-attached NIC")
	assert.Nil(t, insts)

	// The rolled-back instance 1 must have released nic-shared.
	assertNICReleased(ctx, t, m, net, nicName)
}

// failOnNthProvision is a ComputeEngine that provisions successfully until the
// nth call, which it fails — driving the pre-existing engine-provision rollback
// path in RunInstances (distinct from the attachNICs path).
type failOnNthProvision struct {
	failAt int
	calls  int
}

func (e *failOnNthProvision) Provision(
	_ context.Context, _ config.ComputeProvisionRequest,
) (config.ComputeProvisionResult, error) {
	e.calls++
	if e.calls == e.failAt {
		return config.ComputeProvisionResult{}, errors.New("boom: provision failed")
	}

	return config.ComputeProvisionResult{IP: "10.0.0.5"}, nil
}

func (e *failOnNthProvision) ConsoleOutput(context.Context, string) ([]byte, error) { return nil, nil }
func (e *failOnNthProvision) Deprovision(context.Context, string) error             { return nil }

// TestRunInstancesRollbackReleasesNICsOnProvisionFailure covers the other
// rollback trigger: instance 1 provisions and attaches its NIC, then instance
// 2's compute-engine Provision fails before it ever attaches. The rollback of
// instance 1 must still release its NIC.
func TestRunInstancesRollbackReleasesNICsOnProvisionFailure(t *testing.T) {
	ctx := context.Background()
	m, net := newVMMockWithVNet(&failOnNthProvision{failAt: 2})

	const nicName = "nic-engine"
	createSharedNIC(t, net, nicName)

	cfg := driver.InstanceConfig{
		ImageID:           "img-123",
		InstanceType:      "Standard_B1s",
		ResourceGroup:     rollbackNICRG,
		NetworkInterfaces: []driver.AzureNICRef{{ResourceGroup: rollbackNICRG, Name: nicName}},
	}

	insts, err := m.RunInstances(ctx, cfg, 2)
	require.Error(t, err, "second instance's provision must fail")
	assert.Nil(t, insts)

	assertNICReleased(ctx, t, m, net, nicName)
}
