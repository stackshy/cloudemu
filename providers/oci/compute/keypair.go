package compute

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// errNoKeyPairs is what every key pair operation reports. OCI models no key
// pair resource: an SSH public key is passed to LaunchInstance as the
// ssh_authorized_keys instance metadata entry and is never stored as a
// resource of its own, so there is nothing here to create, list or delete.
func errNoKeyPairs() error {
	return cerrors.New(cerrors.Unimplemented,
		"OCI has no key pair resource; an SSH public key is the ssh_authorized_keys instance metadata entry")
}

// CreateKeyPair is unsupported: see errNoKeyPairs.
func (*Mock) CreateKeyPair(_ context.Context, _ driver.KeyPairConfig) (*driver.KeyPairInfo, error) {
	return nil, errNoKeyPairs()
}

// DeleteKeyPair is unsupported: see errNoKeyPairs.
func (*Mock) DeleteKeyPair(_ context.Context, _ string) error {
	return errNoKeyPairs()
}

// DescribeKeyPairs is unsupported: see errNoKeyPairs. It reports the gap
// rather than an empty list, which would read as "no keys exist".
func (*Mock) DescribeKeyPairs(_ context.Context, _ []string) ([]driver.KeyPairInfo, error) {
	return nil, errNoKeyPairs()
}
