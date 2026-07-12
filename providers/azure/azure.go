// Package azure provides Azure mock provider factories.
package azure

import (
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/acr"
	"github.com/stackshy/cloudemu/v2/providers/azure/aks"
	"github.com/stackshy/cloudemu/v2/providers/azure/azureai"
	"github.com/stackshy/cloudemu/v2/providers/azure/azurecache"
	"github.com/stackshy/cloudemu/v2/providers/azure/azuredns"
	"github.com/stackshy/cloudemu/v2/providers/azure/azureiam"
	"github.com/stackshy/cloudemu/v2/providers/azure/azurelb"
	"github.com/stackshy/cloudemu/v2/providers/azure/azuremonitor"
	"github.com/stackshy/cloudemu/v2/providers/azure/azuresearch"
	"github.com/stackshy/cloudemu/v2/providers/azure/azuresql"
	"github.com/stackshy/cloudemu/v2/providers/azure/blobstorage"
	"github.com/stackshy/cloudemu/v2/providers/azure/cosmosdb"
	"github.com/stackshy/cloudemu/v2/providers/azure/databricks"
	"github.com/stackshy/cloudemu/v2/providers/azure/eventgrid"
	"github.com/stackshy/cloudemu/v2/providers/azure/functions"
	"github.com/stackshy/cloudemu/v2/providers/azure/keyvault"
	"github.com/stackshy/cloudemu/v2/providers/azure/loganalytics"
	"github.com/stackshy/cloudemu/v2/providers/azure/mysqlflex"
	"github.com/stackshy/cloudemu/v2/providers/azure/notificationhubs"
	"github.com/stackshy/cloudemu/v2/providers/azure/postgresflex"
	"github.com/stackshy/cloudemu/v2/providers/azure/servicebus"
	"github.com/stackshy/cloudemu/v2/providers/azure/tablestorage"
	"github.com/stackshy/cloudemu/v2/providers/azure/virtualmachines"
	"github.com/stackshy/cloudemu/v2/providers/azure/vnet"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
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
	// QueueStorage backs the Azure Queue Storage data-plane handler. It reuses
	// the messagequeue provider, but is a distinct instance from ServiceBus so
	// the two services keep separate queue namespaces.
	QueueStorage *servicebus.Mock
	// TableStorage backs the Azure Table Storage data-plane handler.
	TableStorage     *tablestorage.Mock
	Cache            *azurecache.Mock
	KeyVault         *keyvault.Mock
	LogAnalytics     *loganalytics.Mock
	NotificationHubs *notificationhubs.Mock
	ACR              *acr.Mock
	EventGrid        *eventgrid.Mock
	SQL              *azuresql.Mock
	PostgresFlex     *postgresflex.Mock
	MySQLFlex        *mysqlflex.Mock
	AKS              *aks.Mock
	Databricks       *databricks.Mock
	AzureAI          *azureai.Mock
	AzureSearch      *azuresearch.Mock

	ResourceDiscovery *resourcediscovery.Engine
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
		QueueStorage:     servicebus.New(o),
		TableStorage:     tablestorage.New(o),
		Cache:            azurecache.New(o),
		KeyVault:         keyvault.New(o),
		LogAnalytics:     loganalytics.New(o),
		NotificationHubs: notificationhubs.New(o),
		ACR:              acr.New(o),
		EventGrid:        eventgrid.New(o),
		SQL:              azuresql.New(o),
		PostgresFlex:     postgresflex.New(o),
		MySQLFlex:        mysqlflex.New(o),
		AKS:              aks.New(o),
		Databricks:       databricks.New(o),
		AzureAI:          azureai.New(o),
		AzureSearch:      azuresearch.New(o),
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
	p.PostgresFlex.SetMonitoring(p.Monitor)
	p.MySQLFlex.SetMonitoring(p.Monitor)
	p.AKS.SetMonitoring(p.Monitor)
	p.AzureAI.SetMonitoring(p.Monitor)
	p.AzureSearch.SetMonitoring(p.Monitor)

	p.ResourceDiscovery = resourcediscovery.New(
		resourcediscovery.ProviderAzure, o.AccountID, o.Region,
		&resourcediscovery.Drivers{
			Compute:    p.VirtualMachines,
			Networking: p.VNet,
			Storage:    p.BlobStorage,
			Database:   p.CosmosDB,
			Serverless: p.Functions,
			Databricks: p.Databricks,
		},
	)

	return p
}
