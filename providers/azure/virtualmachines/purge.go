package virtualmachines

import (
	"context"
	"errors"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/compute"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

var (
	_ driver.AzureVMDeleter           = (*Mock)(nil)
	_ driver.AzureResourceGroupPurger = (*Mock)(nil)
)

// rgTag and subTag mirror the tag keys the ARM wire handlers for disks,
// snapshots, images and SSH public keys record the owning resource group and
// subscription under (subTag also on VMs). They are duplicated here, not
// imported, because the driver layer must not depend on the wire server layer.
const (
	rgTag  = "cloudemu:azureRG"
	subTag = "cloudemu:azureSub"
)

// DeleteInstances removes Azure VMs outright. Each VM gets the same teardown as
// TerminateInstances (disk deleteOption cascade, NIC detach, engine
// deprovision) and is then dropped from the store, because Azure has no
// terminated state: a deleted VM must not stay listed. A VM that is already
// terminated (a row restored from an older snapshot) is simply dropped.
func (m *Mock) DeleteInstances(ctx context.Context, instanceIDs []string) error {
	live := make([]string, 0, len(instanceIDs))

	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}

		if inst.State != compute.StateTerminated {
			live = append(live, id)
		}
	}

	var err error
	if len(live) > 0 {
		err = m.TerminateInstances(ctx, live)
	}

	// Drop only the VMs that reached terminated: a failed transition leaves
	// the VM in place so the caller can retry.
	for _, id := range instanceIDs {
		if inst, ok := m.instances.Get(id); ok && inst.State == compute.StateTerminated {
			m.instances.Delete(id)
			m.sm.Remove(id)
		}
	}

	return err
}

// PurgeComputeResourceGroup tears down every compute resource recorded under
// resourceGroup of subscription, in dependency order: VMs (which detach their
// NICs and settle their disks), scale sets, managed disks, snapshots, images
// and SSH public keys. Membership is a case-insensitive match on the resource
// group and the recorded subscription, so a same-named group in another
// subscription keeps its resources. A record with no subscription (restored
// from an older snapshot) matches on the group alone. A disk in another group
// that was attached to a VM here keeps its own group and survives, detached.
func (m *Mock) PurgeComputeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	var errs []error

	var vmIDs []string

	for id, inst := range m.instances.All() {
		if inGroup(inst.Tags[subTag], inst.ResourceGroup, subscription, resourceGroup) {
			vmIDs = append(vmIDs, id)
		}
	}

	if len(vmIDs) > 0 {
		errs = append(errs, m.DeleteInstances(ctx, vmIDs))
	}

	for key, s := range m.scaleSets.All() {
		if inGroup(s.Subscription, s.ResourceGroup, subscription, resourceGroup) {
			m.scaleSets.Delete(key)
		}
	}

	for id, v := range m.volumes.All() {
		if inGroup(v.Tags[subTag], v.Tags[rgTag], subscription, resourceGroup) {
			errs = append(errs, m.DeleteVolume(ctx, id))
		}
	}

	purgeTagged(m.snapshots, subscription, resourceGroup, func(s *driver.SnapshotInfo) map[string]string { return s.Tags })
	purgeTagged(m.images, subscription, resourceGroup, func(i *driver.ImageInfo) map[string]string { return i.Tags })
	purgeTagged(m.keyPairs, subscription, resourceGroup, func(k *driver.KeyPairInfo) map[string]string { return k.Tags })

	for key, set := range m.availabilitySets.All() {
		if inGroup(set.Subscription, set.ResourceGroup, subscription, resourceGroup) {
			m.availabilitySets.Delete(key)
		}
	}

	return errors.Join(errs...)
}

// purgeTagged deletes every record in store whose resource-group and
// subscription tags place it in resourceGroup of subscription.
func purgeTagged[T any](
	store *memstore.Store[*T], subscription, resourceGroup string, tags func(*T) map[string]string,
) {
	for key, v := range store.All() {
		t := tags(v)
		if inGroup(t[subTag], t[rgTag], subscription, resourceGroup) {
			store.Delete(key)
		}
	}
}

// inGroup reports whether a record created in recordedSub/recordedRG belongs to
// resourceGroup of subscription, using the same rule as the scoped services.
func inGroup(recordedSub, recordedRG, subscription, resourceGroup string) bool {
	return scope.Scope{Subscription: recordedSub, ResourceGroup: recordedRG}.InResourceGroup(subscription, resourceGroup)
}
