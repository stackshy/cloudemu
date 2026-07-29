// Package azure provides Azure mock provider factories.
package azure

import (
	"context"

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
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// aksDiscovery adapts the AKS mock to the resourcediscovery KubernetesClusters
// capability, so AKS managed clusters and agent pools surface in Resource
// Graph.
type aksDiscovery struct{ m *aks.Mock }

func (a aksDiscovery) DiscoverClusters(ctx context.Context) ([]resourcediscovery.DiscoveredCluster, error) {
	clusters, err := a.m.ListClusters(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredCluster, 0, len(clusters))

	for i := range clusters {
		c := clusters[i]
		out = append(out, resourcediscovery.DiscoveredCluster{
			Name:          c.Name,
			Region:        c.Location,
			ResourceGroup: c.ResourceGroup,
			Tags:          c.Tags,
			NodeGroups:    c.AgentPoolNames,
		})
	}

	return out, nil
}

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

	// SubscriptionID is the Azure subscription id this provider serves. Azure
	// uses the account id as the subscription id (see the resourcediscovery.New
	// call below, which passes o.AccountID as the subscription).
	SubscriptionID string
	// Region is the Azure location this provider serves.
	Region string
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
		SubscriptionID:   o.AccountID,
		Region:           o.Region,
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
			Compute:      p.VirtualMachines,
			Networking:   p.VNet,
			Storage:      p.BlobStorage,
			Database:     p.CosmosDB,
			Serverless:   p.Functions,
			Databricks:   p.Databricks,
			Kubernetes:   aksDiscovery{p.AKS},
			RelationalDB: sqlDiscovery{sql: p.SQL, mysql: p.MySQLFlex, pg: p.PostgresFlex},
		},
	)

	return p
}

// sqlDiscovery adapts the Azure relational mocks (SQL logical servers plus
// MySQL/PostgreSQL Flexible Servers) to the resourcediscovery
// RelationalDatabases capability, so they surface in Resource Graph.
type sqlDiscovery struct {
	sql   *azuresql.Mock
	mysql *mysqlflex.Mock
	pg    *postgresflex.Mock
}

func (d sqlDiscovery) DiscoverDatabases(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredDatabase, error) {
	clusters, err := d.sql.DescribeClusters(ctx, nil)
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredDatabase, 0, len(clusters))

	for i := range clusters {
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: clusters[i].ID, Type: resourcediscovery.TypeSQLServer,
			ARN: clusters[i].ARN, Tags: clusters[i].Tags,
		})
	}

	myInsts, err := d.mysql.DescribeInstances(ctx, nil)
	if err != nil {
		return nil, err
	}

	out = appendFlexServers(out, myInsts, resourcediscovery.TypeMySQLFlex)

	pgInsts, err := d.pg.DescribeInstances(ctx, nil)
	if err != nil {
		return nil, err
	}

	out = appendFlexServers(out, pgInsts, resourcediscovery.TypePostgresFlex)

	return out, nil
}

func appendFlexServers(
	out []resourcediscovery.DiscoveredDatabase, insts []rdsdriver.Instance, typ string,
) []resourcediscovery.DiscoveredDatabase {
	for i := range insts {
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: insts[i].ID, Type: typ, Region: insts[i].AvailabilityZone,
			ARN: insts[i].ARN, Tags: insts[i].Tags,
		})
	}

	return out
}
