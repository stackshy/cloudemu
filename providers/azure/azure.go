// Package azure provides Azure mock provider factories.
package azure

import (
	"github.com/NitinKumar004/cloudemu/config"
	"github.com/NitinKumar004/cloudemu/providers/azure/azuredns"
	"github.com/NitinKumar004/cloudemu/providers/azure/azureiam"
	"github.com/NitinKumar004/cloudemu/providers/azure/azurelb"
	"github.com/NitinKumar004/cloudemu/providers/azure/azuremonitor"
	"github.com/NitinKumar004/cloudemu/providers/azure/blobstorage"
	"github.com/NitinKumar004/cloudemu/providers/azure/cosmosdb"
	"github.com/NitinKumar004/cloudemu/providers/azure/functions"
	"github.com/NitinKumar004/cloudemu/providers/azure/servicebus"
	"github.com/NitinKumar004/cloudemu/providers/azure/virtualmachines"
	"github.com/NitinKumar004/cloudemu/providers/azure/vnet"
)

// Provider holds all Azure mock services.
type Provider struct {
	BlobStorage     *blobstorage.Mock
	VirtualMachines *virtualmachines.Mock
	CosmosDB        *cosmosdb.Mock
	Functions       *functions.Mock
	VNet            *vnet.Mock
	Monitor         *azuremonitor.Mock
	IAM             *azureiam.Mock
	DNS             *azuredns.Mock
	LB              *azurelb.Mock
	ServiceBus      *servicebus.Mock
}

// New creates a new Azure provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	return &Provider{
		BlobStorage:     blobstorage.New(o),
		VirtualMachines: virtualmachines.New(o),
		CosmosDB:        cosmosdb.New(o),
		Functions:       functions.New(o),
		VNet:            vnet.New(o),
		Monitor:         azuremonitor.New(o),
		IAM:             azureiam.New(o),
		DNS:             azuredns.New(o),
		LB:              azurelb.New(o),
		ServiceBus:      servicebus.New(o),
	}
}
