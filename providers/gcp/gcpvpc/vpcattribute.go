package gcpvpc

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ModifyVPCAttribute sets the DNS attributes of a VPC.
//
// A nil pointer leaves that attribute unchanged: the real API accepts exactly
// one attribute per call, so a caller enabling DNS hostnames must not have its
// DNS-support setting reset as a side effect.
func (m *Mock) ModifyVPCAttribute(
	_ context.Context, id string, enableDNSSupport, enableDNSHostnames *bool,
) error {
	v, ok := m.vpcs.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "vpc %q not found", id)
	}

	if enableDNSSupport != nil {
		v.EnableDNSSupport = *enableDNSSupport
	}

	if enableDNSHostnames != nil {
		v.EnableDNSHostnames = *enableDNSHostnames
	}

	return nil
}
