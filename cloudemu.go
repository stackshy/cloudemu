package cloudemu

import (
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/providers/aws"
	"github.com/stackshy/cloudemu/providers/azure"
	"github.com/stackshy/cloudemu/providers/gcp"
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
