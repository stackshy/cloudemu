// Package azure provides Azure mock provider factories.
package azure

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/providers/azure/acr"
	"github.com/stackshy/cloudemu/v2/providers/azure/ai"
	"github.com/stackshy/cloudemu/v2/providers/azure/aks"
	"github.com/stackshy/cloudemu/v2/providers/azure/blobstorage"
	"github.com/stackshy/cloudemu/v2/providers/azure/cache"
	"github.com/stackshy/cloudemu/v2/providers/azure/containerinstances"
	"github.com/stackshy/cloudemu/v2/providers/azure/cosmosdb"
	"github.com/stackshy/cloudemu/v2/providers/azure/cosmospostgresql"
	"github.com/stackshy/cloudemu/v2/providers/azure/databricks"
	"github.com/stackshy/cloudemu/v2/providers/azure/dns"
	"github.com/stackshy/cloudemu/v2/providers/azure/eventgrid"
	"github.com/stackshy/cloudemu/v2/providers/azure/functions"
	"github.com/stackshy/cloudemu/v2/providers/azure/iam"
	"github.com/stackshy/cloudemu/v2/providers/azure/keyvault"
	"github.com/stackshy/cloudemu/v2/providers/azure/loadbalancer"
	"github.com/stackshy/cloudemu/v2/providers/azure/loganalytics"
	"github.com/stackshy/cloudemu/v2/providers/azure/managedcassandra"
	"github.com/stackshy/cloudemu/v2/providers/azure/monitor"
	"github.com/stackshy/cloudemu/v2/providers/azure/mysqlflex"
	"github.com/stackshy/cloudemu/v2/providers/azure/notificationhubs"
	"github.com/stackshy/cloudemu/v2/providers/azure/postgresflex"
	"github.com/stackshy/cloudemu/v2/providers/azure/search"
	"github.com/stackshy/cloudemu/v2/providers/azure/servicebus"
	"github.com/stackshy/cloudemu/v2/providers/azure/sql"
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

		props := map[string]any{}
		if c.PowerState != "" {
			props["powerState"] = map[string]any{"code": c.PowerState}
		}

		if c.KubernetesVersion != "" {
			props["kubernetesVersion"] = c.KubernetesVersion
		}

		pools, err := a.m.ListAgentPools(ctx, c.ResourceGroup, c.Name)
		if err != nil {
			return nil, err
		}

		out = append(out, resourcediscovery.DiscoveredCluster{
			Name:          c.Name,
			Region:        c.Location,
			ResourceGroup: c.ResourceGroup,
			Tags:          c.Tags,
			NodeGroups:    aksNodeGroups(pools),
			Attrs: resourcediscovery.Attributes{
				SKUTier:    c.Tier,
				Properties: props,
			},
		})
	}

	return out, nil
}

// aksNodeGroups projects each agent pool's cost signals (vmSize as sku,
// scaleSetPriority for Spot detection, count, mode, osType) onto the
// per-node-group Attributes the walker emits.
func aksNodeGroups(pools []aks.AgentPool) []resourcediscovery.DiscoveredNodeGroup {
	out := make([]resourcediscovery.DiscoveredNodeGroup, 0, len(pools))

	for i := range pools {
		p := &pools[i]

		props := map[string]any{"count": int(p.Count)}
		if p.ScaleSetPriority != "" {
			props["scaleSetPriority"] = p.ScaleSetPriority
		}

		if p.Mode != "" {
			props["mode"] = p.Mode
		}

		if p.OSType != "" {
			props["osType"] = p.OSType
		}

		out = append(out, resourcediscovery.DiscoveredNodeGroup{
			Name:  p.Name,
			Attrs: resourcediscovery.Attributes{SKU: p.VMSize, Properties: props},
		})
	}

	return out
}

// Provider holds all Azure mock services.
type Provider struct {
	BlobStorage      *blobstorage.Mock
	VirtualMachines  *virtualmachines.Mock
	CosmosDB         *cosmosdb.Mock
	ManagedCassandra *managedcassandra.Mock
	CosmosPostgreSQL *cosmospostgresql.Mock
	Functions        *functions.Mock
	VNet             *vnet.Mock
	Monitor          *monitor.Mock
	IAM              *iam.Mock
	DNS              *dns.Mock
	LB               *loadbalancer.Mock
	ServiceBus       *servicebus.Mock
	// QueueStorage backs the Azure Queue Storage data-plane handler. It reuses
	// the messagequeue provider, but is a distinct instance from ServiceBus so
	// the two services keep separate queue namespaces.
	QueueStorage *servicebus.Mock
	// TableStorage backs the Azure Table Storage data-plane handler.
	TableStorage       *tablestorage.Mock
	Cache              *cache.Mock
	KeyVault           *keyvault.Mock
	LogAnalytics       *loganalytics.Mock
	NotificationHubs   *notificationhubs.Mock
	ACR                *acr.Mock
	ContainerInstances *containerinstances.Mock
	EventGrid          *eventgrid.Mock
	SQL                *sql.Mock
	PostgresFlex       *postgresflex.Mock
	MySQLFlex          *mysqlflex.Mock
	AKS                *aks.Mock
	Databricks         *databricks.Mock
	AI                 *ai.Mock
	Search             *search.Mock

	ResourceDiscovery *resourcediscovery.Engine

	// SubscriptionID is the Azure subscription id this provider serves. Azure
	// uses the account id as the subscription id (see the resourcediscovery.New
	// call below, which passes o.AccountID as the subscription).
	SubscriptionID string
	// Region is the Azure location this provider serves.
	Region string

	// EnforceAuth mirrors config.Options.EnforceAuth: when true the Azure wire
	// server validates the claims of each request's Bearer token (audience,
	// expiry, principal) and rejects bad/missing tokens with 401. The token
	// signature is not verified (cloudemu has no Azure AD signing key). Off by
	// default.
	EnforceAuth bool

	// engineClosers holds any wired real engines that implement io.Closer, so
	// Close can cascade teardown to them. Empty for the in-memory default.
	engineClosers []io.Closer
}

// New creates a new Azure provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	p := &Provider{
		BlobStorage:        blobstorage.New(o),
		VirtualMachines:    virtualmachines.New(o),
		CosmosDB:           cosmosdb.New(o),
		ManagedCassandra:   managedcassandra.New(o),
		CosmosPostgreSQL:   cosmospostgresql.New(o),
		Functions:          functions.New(o),
		VNet:               vnet.New(o),
		Monitor:            monitor.New(o),
		IAM:                iam.New(o),
		DNS:                dns.New(o),
		LB:                 loadbalancer.New(o),
		ServiceBus:         servicebus.New(o),
		QueueStorage:       servicebus.New(o),
		TableStorage:       tablestorage.New(o),
		Cache:              cache.New(o),
		KeyVault:           keyvault.New(o),
		LogAnalytics:       loganalytics.New(o),
		NotificationHubs:   notificationhubs.New(o),
		ACR:                acr.New(o),
		ContainerInstances: containerinstances.New(o),
		EventGrid:          eventgrid.New(o),
		SQL:                sql.New(o),
		PostgresFlex:       postgresflex.New(o),
		MySQLFlex:          mysqlflex.New(o),
		AKS:                aks.New(o),
		Databricks:         databricks.New(o),
		AI:                 ai.New(o),
		Search:             search.New(o),
		SubscriptionID:     o.AccountID,
		Region:             o.Region,
		EnforceAuth:        o.EnforceAuth,
	}
	p.VirtualMachines.SetMonitoring(p.Monitor)
	p.VirtualMachines.SetNICAttacher(p.VNet)
	p.BlobStorage.SetMonitoring(p.Monitor)
	p.CosmosDB.SetMonitoring(p.Monitor)
	p.Functions.SetMonitoring(p.Monitor)
	// Azure Functions invocations write execution logs (and captured stdout/
	// stderr on the real-engine path) to Log Analytics.
	p.Functions.SetLogSink(p.LogAnalytics)
	p.ServiceBus.SetMonitoring(p.Monitor)
	p.Cache.SetMonitoring(p.Monitor)
	p.LogAnalytics.SetMonitoring(p.Monitor)
	p.NotificationHubs.SetMonitoring(p.Monitor)
	p.ACR.SetMonitoring(p.Monitor)
	p.EventGrid.SetMonitoring(p.Monitor)
	p.EventGrid.SetServiceBusDeliverer(p.ServiceBus)
	p.EventGrid.SetFunctionInvoker(p.Functions)
	p.BlobStorage.SetEventGridPublisher(p.EventGrid)
	p.SQL.SetMonitoring(p.Monitor)
	p.PostgresFlex.SetMonitoring(p.Monitor)
	p.MySQLFlex.SetMonitoring(p.Monitor)
	p.AKS.SetMonitoring(p.Monitor)
	p.AI.SetMonitoring(p.Monitor)
	p.Search.SetMonitoring(p.Monitor)

	p.ResourceDiscovery = resourcediscovery.New(
		resourcediscovery.ProviderAzure, o.AccountID, o.Region,
		&resourcediscovery.Drivers{
			Compute:         p.VirtualMachines,
			Networking:      p.VNet,
			Storage:         p.BlobStorage,
			Database:        p.CosmosDB,
			Serverless:      p.Functions,
			Databricks:      p.Databricks,
			Kubernetes:      aksDiscovery{p.AKS},
			RelationalDB:    sqlDiscovery{sql: p.SQL, mysql: p.MySQLFlex, pg: p.PostgresFlex},
			ScaleSets:       vmssDiscovery{p.VirtualMachines},
			AppServicePlans: appServicePlanDiscovery{p.Functions},
			Secrets:         p.KeyVault,
			KeyVaults:       p.KeyVault,
			ContainerReg:    p.ACR,
			MessageQueue:    p.ServiceBus,
			Notification:    p.NotificationHubs,
			DNS:             p.DNS,
			Logging:         p.LogAnalytics,
			Cache:           p.Cache,
			LoadBalancer:    p.LB,
			Monitoring:      p.Monitor,
			IAM:             p.IAM,
			Extra: []resourcediscovery.GenericResources{
				azureMLDiscovery{p.AI},
			},
		},
	)

	p.engineClosers = o.EngineClosers()

	return p
}

// Close tears down any real engines wired into the provider via
// config.With<X>Engine, stopping the Docker containers or subprocesses they
// own. It is a no-op when no engine is wired — the in-memory default — and is
// safe to call more than once, since engine Close is idempotent.
func (p *Provider) Close() error {
	var errs []error

	for _, c := range p.engineClosers {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// SnapshotServices returns the provider's services that support identity-
// preserving snapshotting, keyed by a stable lowercased field-name service key
// (e.g. "blobstorage", "cosmosdb", "virtualmachines"). persist iterates this
// map, so the persisted surface automatically tracks whichever services
// implement snapshot.Snapshottable — no hand-kept registry to drift.
func (p *Provider) SnapshotServices() map[string]snapshot.Snapshottable {
	return snapshot.Discover(p)
}

// sqlDiscovery adapts the Azure relational mocks (SQL logical servers plus
// MySQL/PostgreSQL Flexible Servers) to the resourcediscovery
// RelationalDatabases capability, so they surface in Resource Graph.
type sqlDiscovery struct {
	sql   *sql.Mock
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
			Attrs: resourcediscovery.Attributes{
				Properties: nonEmptyProps(map[string]any{"version": clusters[i].EngineVersion}),
			},
		})

		dbs, dbErr := d.sql.ListDatabases(ctx, clusters[i].ID)
		if dbErr != nil {
			return nil, dbErr
		}

		for j := range dbs {
			db := &dbs[j]
			out = append(out, resourcediscovery.DiscoveredDatabase{
				Name: db.Name, Type: resourcediscovery.TypeSQLDatabase, ARN: db.ARN,
				Attrs: resourcediscovery.Attributes{
					SKU:     db.SKUName,
					SKUTier: db.SKUTier,
					Properties: nonEmptyProps(map[string]any{
						"zoneRedundant": db.ZoneRedundant,
						"currentSku":    map[string]any{"name": db.SKUName, "tier": db.SKUTier},
					}),
				},
			})
		}
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

	mis, err := d.sql.ListManagedInstances(ctx)
	if err != nil {
		return nil, err
	}

	for i := range mis {
		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: mis[i].Name, Type: resourcediscovery.TypeManagedInstance,
			Region: mis[i].Location, ARN: mis[i].ARN, Tags: mis[i].Tags,
			Attrs: resourcediscovery.Attributes{
				SKU: mis[i].SKUName,
				Properties: nonEmptyProps(map[string]any{
					"vCores":             mis[i].VCores,
					"storageSizeInGB":    mis[i].StorageGB,
					"tier":               mis[i].SKUTier,
					"licenseType":        mis[i].LicenseType,
					"storageAccountType": mis[i].StorageAccountType,
				}),
			},
		})
	}

	return out, nil
}

// vmssDiscovery projects the VM Scale Sets stored on the virtualmachines mock
// onto DiscoveredScaleSet for Resource Graph.
type vmssDiscovery struct{ m *virtualmachines.Mock }

func (v vmssDiscovery) DiscoverScaleSets(ctx context.Context) ([]resourcediscovery.DiscoveredScaleSet, error) {
	sets, err := v.m.ListScaleSets(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredScaleSet, 0, len(sets))

	for i := range sets {
		s := &sets[i]

		profile := map[string]any{}
		if s.Priority != "" {
			profile["priority"] = s.Priority
		}

		if s.LicenseType != "" {
			profile["licenseType"] = s.LicenseType
		}

		if s.OSType != "" {
			profile["storageProfile"] = map[string]any{"osDisk": map[string]any{"osType": s.OSType}}
		}

		props := map[string]any{}
		if len(profile) > 0 {
			props["virtualMachineProfile"] = profile
		}

		out = append(out, resourcediscovery.DiscoveredScaleSet{
			Name: s.Name, ARN: s.ID, Region: s.Location, Tags: s.Tags,
			Attrs: resourcediscovery.Attributes{
				SKU:         s.SKUName,
				SKUTier:     s.SKUTier,
				SKUCapacity: s.Capacity,
				Properties:  props,
			},
		})
	}

	return out, nil
}

// appServicePlanDiscovery projects the App Service plans stored on the functions
// mock onto DiscoveredAppServicePlan for Resource Graph.
type appServicePlanDiscovery struct{ m *functions.Mock }

func (a appServicePlanDiscovery) DiscoverAppServicePlans(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredAppServicePlan, error) {
	plans, err := a.m.ListAppServicePlans(ctx, "", "")
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredAppServicePlan, 0, len(plans))

	for i := range plans {
		p := &plans[i]

		out = append(out, resourcediscovery.DiscoveredAppServicePlan{
			Name: p.Name, ARN: p.ID, Region: p.Location, Tags: p.Tags,
			Attrs: resourcediscovery.Attributes{
				SKU:         p.SKUName,
				SKUTier:     p.SKUTier,
				SKUCapacity: p.Capacity,
				Kind:        p.Kind,
			},
		})
	}

	return out, nil
}

// flexTier derives the Azure Flexible Server SKU tier (Burstable /
// GeneralPurpose / MemoryOptimized) from the SKU name, which encodes it as a
// prefix in both the current ("Standard_B1ms") and legacy ("B_Gen5_1") naming.
// The prefix list is known-incomplete on purpose: a name outside the listed
// families (e.g. Standard_F*) returns "" as an intentional best-effort fallback
// — a pricing consumer then falls back to sku.name — so an empty tier here is
// deliberate, not a bug, and this is not meant to enumerate every Azure family.
func flexTier(skuName string) string {
	switch {
	case strings.HasPrefix(skuName, "Standard_B"), strings.HasPrefix(skuName, "B_"):
		return "Burstable"
	case strings.HasPrefix(skuName, "Standard_E"), strings.HasPrefix(skuName, "MO_"):
		return "MemoryOptimized"
	case strings.HasPrefix(skuName, "Standard_D"), strings.HasPrefix(skuName, "GP_"):
		return "GeneralPurpose"
	default:
		return ""
	}
}

func appendFlexServers(
	out []resourcediscovery.DiscoveredDatabase, insts []rdsdriver.Instance, typ string,
) []resourcediscovery.DiscoveredDatabase {
	for i := range insts {
		// Prefer the explicit HA mode the ARM API recorded (which can be
		// SameZone, not just ZoneRedundant); fall back to MultiAZ for instances
		// created through the portable API, which has no HA-mode input.
		ha := insts[i].HighAvailabilityMode
		if ha == "" {
			ha = rdsdriver.HAModeDisabled
			if insts[i].MultiAZ {
				ha = rdsdriver.HAModeZoneRedundant
			}
		}

		haProps := map[string]any{"mode": ha}
		if insts[i].StandbyAvailabilityZone != "" {
			haProps["standbyAvailabilityZone"] = insts[i].StandbyAvailabilityZone
		}

		out = append(out, resourcediscovery.DiscoveredDatabase{
			Name: insts[i].ID, Type: typ, Region: insts[i].AvailabilityZone,
			ARN: insts[i].ARN, Tags: insts[i].Tags,
			Attrs: resourcediscovery.Attributes{
				SKU:     insts[i].InstanceClass,
				SKUTier: flexTier(insts[i].InstanceClass),
				Properties: nonEmptyProps(map[string]any{
					"storage":          map[string]any{"storageSizeGB": insts[i].AllocatedStorage},
					"highAvailability": haProps,
					"version":          insts[i].EngineVersion,
				}),
			},
		})
	}

	return out
}

// nonEmptyProps prunes entries that carry no cost information so the Properties
// bag only holds attributes the resource actually has: it drops empty strings,
// zero ints, nil values, and nested map[string]any entries that are (or become,
// after their own recursive pruning) empty. Bools are intentionally preserved
// as-is, including false — real Azure ARG genuinely surfaces properties such as
// properties.zoneRedundant as a bool including false, so emitting false is
// faithful. Returns nil for an empty result.
func nonEmptyProps(m map[string]any) map[string]any {
	for k, v := range m {
		switch val := v.(type) {
		case string:
			if val == "" {
				delete(m, k)
			}
		case int:
			if val == 0 {
				delete(m, k)
			}
		case map[string]any:
			// An inner map that prunes down to nothing carries no cost info —
			// drop it so an empty currentSku/storage object isn't emitted.
			if nonEmptyProps(val) == nil {
				delete(m, k)
			}
		case nil:
			delete(m, k)
		}
	}

	if len(m) == 0 {
		return nil
	}

	return m
}
