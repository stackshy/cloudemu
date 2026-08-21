package compute_test

import (
	computeprovider "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	ocicompute "github.com/stackshy/cloudemu/v2/server/oci/compute"
)

// The provider mock must satisfy the handler's OCI-only capability interface.
// The check lives here because the import direction only allows it here.
var _ ocicompute.Extras = (*computeprovider.Mock)(nil)
