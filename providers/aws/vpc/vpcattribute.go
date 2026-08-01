package vpc

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// ModifyVPCAttribute sets the DNS attributes of a VPC.
//
// A nil pointer leaves that attribute unchanged: the real API accepts exactly
// one attribute per call, so a caller enabling DNS hostnames must not have its
// DNS-support setting reset as a side effect.
func (m *Mock) ModifyVPCAttribute(
	_ context.Context, id string, update driver.VPCAttributeUpdate,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.vpcs.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "vpc %q not found", id)
	}

	if update.EnableDNSSupport != nil {
		v.EnableDNSSupport = *update.EnableDNSSupport
	}

	if update.EnableDNSHostnames != nil {
		v.EnableDNSHostnames = *update.EnableDNSHostnames
	}

	return nil
}
