package virtualmachines

import (
	"context"
	"maps"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

var _ driver.AzureVMExtensions = (*Mock)(nil)

// extensionKey keys an extension under its VM, so deleting the VM finds every
// extension it owns.
func extensionKey(instanceID, name string) string {
	return instanceID + "/" + strings.ToLower(name)
}

// liveInstance reports whether the VM exists and is not terminated.
func (m *Mock) liveInstance(instanceID string) bool {
	inst, ok := m.instances.Get(instanceID)

	return ok && inst.State != compute.StateTerminated
}

// PutVMExtension creates or replaces an extension on a VM.
func (m *Mock) PutVMExtension(
	_ context.Context, instanceID string, ext driver.AzureVMExtension,
) (*driver.AzureVMExtension, bool, error) {
	if !m.liveInstance(instanceID) {
		return nil, false, cerrors.Newf(cerrors.NotFound, "virtual machine %q not found", instanceID)
	}

	key := extensionKey(instanceID, ext.Name)
	_, existed := m.vmExtensions.Get(key)

	stored := cloneExtension(&ext)
	delete(stored.Properties, "protectedSettings")
	m.vmExtensions.Set(key, stored)

	return cloneExtension(stored), !existed, nil
}

// GetVMExtension returns one extension of a VM.
func (m *Mock) GetVMExtension(_ context.Context, instanceID, name string) (*driver.AzureVMExtension, error) {
	ext, ok := m.vmExtensions.Get(extensionKey(instanceID, name))
	if !ok || !m.liveInstance(instanceID) {
		return nil, cerrors.Newf(cerrors.NotFound, "extension %q not found", name)
	}

	return cloneExtension(ext), nil
}

// ListVMExtensions returns a VM's extensions sorted by name.
func (m *Mock) ListVMExtensions(_ context.Context, instanceID string) ([]driver.AzureVMExtension, error) {
	if !m.liveInstance(instanceID) {
		return nil, cerrors.Newf(cerrors.NotFound, "virtual machine %q not found", instanceID)
	}

	prefix := instanceID + "/"
	keys := slices.Sorted(maps.Keys(m.vmExtensions.Filter(func(k string, _ *driver.AzureVMExtension) bool {
		return strings.HasPrefix(k, prefix)
	})))

	out := make([]driver.AzureVMExtension, 0, len(keys))

	for _, k := range keys {
		if ext, ok := m.vmExtensions.Get(k); ok {
			out = append(out, *cloneExtension(ext))
		}
	}

	return out, nil
}

// DeleteVMExtension removes one extension of a VM.
func (m *Mock) DeleteVMExtension(_ context.Context, instanceID, name string) error {
	if !m.vmExtensions.Delete(extensionKey(instanceID, name)) {
		return cerrors.Newf(cerrors.NotFound, "extension %q not found", name)
	}

	return nil
}

// dropExtensions removes every extension of the given VMs; extensions do not
// outlive their VM.
func (m *Mock) dropExtensions(instanceIDs []string) {
	for _, id := range instanceIDs {
		prefix := id + "/"
		for k := range m.vmExtensions.All() {
			if strings.HasPrefix(k, prefix) {
				m.vmExtensions.Delete(k)
			}
		}
	}
}

func cloneExtension(e *driver.AzureVMExtension) *driver.AzureVMExtension {
	out := *e
	out.Tags = maps.Clone(e.Tags)
	out.Properties = cloneAnyMap(e.Properties)

	return &out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}

	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAny(v)
	}

	return out
}

func cloneAny(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneAnyMap(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = cloneAny(t[i])
		}

		return out
	default:
		return v
	}
}
