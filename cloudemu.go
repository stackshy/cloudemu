package cloudemu

import (
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws"
	"github.com/stackshy/cloudemu/v2/providers/azure"
	"github.com/stackshy/cloudemu/v2/providers/gcp"
	"github.com/stackshy/cloudemu/v2/providers/oci"
)

// NewAWS creates a new AWS mock provider.
func NewAWS(opts ...config.Option) *aws.Provider {
	return aws.New(opts...)
}

// NewAzure creates a new Azure mock provider.
func NewAzure(opts ...config.Option) *azure.Provider {
	return azure.New(opts...)
}

// NewGCP creates a new GCP mock provider.
func NewGCP(opts ...config.Option) *gcp.Provider {
	return gcp.New(opts...)
}

// NewOCI creates a new OCI mock provider.
func NewOCI(opts ...config.Option) *oci.Provider {
	return oci.New(opts...)
}
