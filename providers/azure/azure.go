// Package azure provides Azure mock provider factories.
package azure

import (
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/providers/azure/acr"
	"github.com/stackshy/cloudemu/providers/azure/azurecache"
	"github.com/stackshy/cloudemu/providers/azure/azuredns"
	"github.com/stackshy/cloudemu/providers/azure/azureiam"
	"github.com/stackshy/cloudemu/providers/azure/azurelb"
	"github.com/stackshy/cloudemu/providers/azure/azuremonitor"
	"github.com/stackshy/cloudemu/providers/azure/azuresql"
	"github.com/stackshy/cloudemu/providers/azure/blobstorage"
	"github.com/stackshy/cloudemu/providers/azure/cosmosdb"
	"github.com/stackshy/cloudemu/providers/azure/eventgrid"
	"github.com/stackshy/cloudemu/providers/azure/functions"
	"github.com/stackshy/cloudemu/providers/azure/keyvault"
	"github.com/stackshy/cloudemu/providers/azure/loganalytics"
	"github.com/stackshy/cloudemu/providers/azure/notificationhubs"
	"github.com/stackshy/cloudemu/providers/azure/servicebus"
	"github.com/stackshy/cloudemu/providers/azure/virtualmachines"
	"github.com/stackshy/cloudemu/providers/azure/vnet"
)

// Provider holds all Azure mock services.
type Provider struct {
	BlobStorage      *blobstorage.Mock
	VirtualMachines  *virtualmachines.Mock
	CosmosDB         *cosmosdb.Mock
	Functions        *functions.Mock
	VNet             *vnet.Mock
	Monitor          *azuremonitor.Mock
	IAM              *azureiam.Mock
	DNS              *azuredns.Mock
	LB               *azurelb.Mock
	ServiceBus       *servicebus.Mock
	Cache            *azurecache.Mock
	KeyVault         *keyvault.Mock
	LogAnalytics     *loganalytics.Mock
	NotificationHubs *notificationhubs.Mock
	ACR              *acr.Mock
	EventGrid        *eventgrid.Mock
	SQL              *azuresql.Mock
}

// New creates a new Azure provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	p := &Provider{
		BlobStorage:      blobstorage.New(o),
		VirtualMachines:  virtualmachines.New(o),
		CosmosDB:         cosmosdb.New(o),
		Functions:        functions.New(o),
		VNet:             vnet.New(o),
		Monitor:          azuremonitor.New(o),
		IAM:              azureiam.New(o),
		DNS:              azuredns.New(o),
		LB:               azurelb.New(o),
		ServiceBus:       servicebus.New(o),
		Cache:            azurecache.New(o),
		KeyVault:         keyvault.New(o),
		LogAnalytics:     loganalytics.New(o),
		NotificationHubs: notificationhubs.New(o),
		ACR:              acr.New(o),
		EventGrid:        eventgrid.New(o),
		SQL:              azuresql.New(o),
	}
	p.VirtualMachines.SetMonitoring(p.Monitor)
	p.BlobStorage.SetMonitoring(p.Monitor)
	p.CosmosDB.SetMonitoring(p.Monitor)
	p.Functions.SetMonitoring(p.Monitor)
	p.ServiceBus.SetMonitoring(p.Monitor)
	p.Cache.SetMonitoring(p.Monitor)
	p.LogAnalytics.SetMonitoring(p.Monitor)
	p.NotificationHubs.SetMonitoring(p.Monitor)
	p.ACR.SetMonitoring(p.Monitor)
	p.EventGrid.SetMonitoring(p.Monitor)
	p.SQL.SetMonitoring(p.Monitor)

	return p
}
