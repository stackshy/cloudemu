package virtualmachines

import "context"

// NICAttacher is the slice of the networking mock the VM lifecycle needs to
// keep a network interface's properties.virtualMachine back-reference in sync
// with the VM(s) referencing it in networkProfile.networkInterfaces. Real
// Azure sets this back-reference when a VM attaches a NIC and clears it when
// the VM is deleted; without a resolver wired, a NIC's GetNetworkInterface
// never reports its owning VM. nil until wired by the provider.
type NICAttacher interface {
	// AttachNetworkInterface associates the NIC (resourceGroup, name) with
	// vmID, the ARM resource id of the owning VM. It must fail when the NIC is
	// already attached to a different VM (a NIC attaches to only one VM at a
	// time) and succeed idempotently when it is already attached to vmID.
	AttachNetworkInterface(ctx context.Context, resourceGroup, name, vmID string) error
	// DetachNetworkInterface clears the NIC's back-reference, but only when it
	// currently points at vmID; detaching a NIC that points elsewhere (or
	// isn't attached) is a no-op.
	DetachNetworkInterface(ctx context.Context, resourceGroup, name, vmID string) error
}

// SetNICAttacher wires the networking mock in. Without it, a VM's
// networkProfile.networkInterfaces references are accepted but the
// referenced NICs' properties.virtualMachine back-reference is never set.
func (m *Mock) SetNICAttacher(a NICAttacher) {
	m.nicAttacher = a
}
