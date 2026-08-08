package resourcediscovery

import (
	"context"

	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	serverlessdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Drivers bundles the per-service drivers the engine reads from. Any field
// may be nil — the matching walker is skipped in that case. This keeps the
// engine usable in partial test wirings and during the staged rollout of
// per-service walkers in later phases.
type Drivers struct {
	Compute         computedriver.Compute
	Networking      netdriver.Networking
	Storage         storagedriver.Bucket
	Database        dbdriver.Database
	Serverless      serverlessdriver.Serverless
	Databricks      dbxdriver.Databricks
	Kubernetes      KubernetesClusters
	RelationalDB    RelationalDatabases
	ScaleSets       ScaleSets
	AppServicePlans AppServicePlans
	Secrets         secretsdriver.Secrets
	ContainerReg    crdriver.ContainerRegistry
	MessageQueue    mqdriver.MessageQueue
}

// AppServicePlans is the discovery capability for App Service plans (Azure
// serverfarms) — the resource that carries the SKU/tier an App Service or
// Function App is billed on. Provider-projected, like the other adapters.
type AppServicePlans interface {
	DiscoverAppServicePlans(ctx context.Context) ([]DiscoveredAppServicePlan, error)
}

// DiscoveredAppServicePlan is a provider-neutral projection of an App Service
// plan. Attrs.SKU/SKUTier carry the plan's pricing tier (F1/B1/P1v3/…), the
// primary cost signal for App Service / Functions.
type DiscoveredAppServicePlan struct {
	Name   string
	ARN    string
	Region string
	Tags   map[string]string
	Attrs  Attributes
}

// ScaleSets is the discovery capability for VM scale sets (Azure VMSS). Like the
// other adapter-projected capabilities, each cloud's mock lives in its provider
// package and wires a thin adapter that projects onto DiscoveredScaleSet.
type ScaleSets interface {
	DiscoverScaleSets(ctx context.Context) ([]DiscoveredScaleSet, error)
}

// DiscoveredScaleSet is a provider-neutral projection of a VM scale set. Attrs
// carries the SKU (name/tier/capacity) and the nested virtualMachineProfile
// properties (priority/licenseType/osType) a discoverer prices on.
type DiscoveredScaleSet struct {
	Name   string
	ARN    string
	Region string
	Tags   map[string]string
	Attrs  Attributes
}

// RelationalDatabases is the discovery capability for managed relational
// database servers/instances — RDS/Aurora, Azure SQL, Azure MySQL/PostgreSQL
// Flexible Server, Cloud SQL. Like KubernetesClusters, each cloud's relational
// mock lives in its provider package, so a thin adapter in the provider
// projects its databases onto DiscoveredDatabase rather than inverting the
// package layering.
type RelationalDatabases interface {
	DiscoverDatabases(ctx context.Context) ([]DiscoveredDatabase, error)
}

// DiscoveredDatabase is a provider-neutral projection of a managed relational
// database resource for the inventory walk. Type is the portable resource type
// (e.g. "DBInstance", "SqlServer", "MySqlFlexibleServer", "SqlInstance") that
// Resource Explorer / Resource Graph / Cloud Asset translate to the cloud's
// native type string. ARN is used verbatim as the identifier; Region falls back
// to the engine default when empty.
type DiscoveredDatabase struct {
	Name   string
	ARN    string
	Region string
	Type   string
	Tags   map[string]string

	// Attrs carries the same generic slots as Resource (SKU/Properties/…) so a
	// provider adapter can project a DB's compute SKU, storage, and HA mode
	// without a bespoke per-cloud struct.
	Attrs Attributes
}

// Attributes is the generic, resource-agnostic attribute set every Discovered*
// projection can carry, mirroring the slots on Resource. The walker copies it
// onto the emitted Resource verbatim, so no walker branches on resource type.
type Attributes struct {
	SKU         string
	SKUTier     string
	SKUCapacity int
	Kind        string
	ManagedBy   string
	Zones       []string
	Properties  map[string]any
}

// KubernetesClusters is the discovery capability for managed Kubernetes —
// EKS, GKE, and AKS. Each cloud's cluster mock lives in its provider package
// (there is no shared services/*/driver for it, unlike the portable services),
// so rather than import providers here — which would invert the package
// layering — each provider wires in a thin adapter that projects its clusters
// onto DiscoveredCluster.
type KubernetesClusters interface {
	DiscoverClusters(ctx context.Context) ([]DiscoveredCluster, error)
}

// DiscoveredCluster is a provider-neutral projection of a managed Kubernetes
// cluster for the inventory walk. NodeGroups holds the cluster's node-group /
// node-pool / agent-pool names, each surfaced as its own resource.
//
// Region and ResourceGroup feed the per-provider ARN/ID so the identifier
// matches the resource's real location rather than the engine default (GCP
// self-links embed the region; Azure IDs embed the resource group). Both may
// be empty — the walker then falls back to the engine's defaults.
//
// ARN, when set, is used verbatim as the cluster's identifier (e.g. the EKS
// mock's own ARN) instead of a rebuilt best-effort one; empty means build it.
type DiscoveredCluster struct {
	Name          string
	Region        string
	ResourceGroup string
	ARN           string
	Tags          map[string]string
	NodeGroups    []DiscoveredNodeGroup

	// Attrs carries the generic slots (SKU/Properties/…) for the cluster
	// resource, mirroring Resource.
	Attrs Attributes
}

// NodeGroupsFromNames builds name-only node groups (no per-pool attributes),
// for providers that don't yet project per-pool cost signals (EKS/GKE).
func NodeGroupsFromNames(names []string) []DiscoveredNodeGroup {
	out := make([]DiscoveredNodeGroup, 0, len(names))
	for _, n := range names {
		out = append(out, DiscoveredNodeGroup{Name: n})
	}

	return out
}

// DiscoveredNodeGroup is a cluster's node-group / node-pool / agent-pool
// projection. Name is surfaced as the NodeGroup resource's id; Attrs carries
// per-pool cost signals (SKU/vmSize, scaleSetPriority for Spot, count, …) that
// a provider adapter fills and the walker copies onto the emitted Resource.
type DiscoveredNodeGroup struct {
	Name  string
	Attrs Attributes
}

// Engine walks all configured service drivers and returns a normalized
// cross-service resource inventory.
type Engine struct {
	provider  string
	accountID string
	region    string
	drivers   Drivers
}

// New constructs an Engine. provider is one of "aws", "azure", "gcp".
// accountID is the AWS account ID, Azure subscription ID, or GCP project ID;
// it is embedded in the ARN/URN of each returned Resource. region is the
// default region used when a driver does not carry per-resource regions.
// drivers is passed by pointer because the struct is wider than the
// gocritic hugeParam threshold; passing nil for any field skips that walker.
func New(provider, accountID, region string, drivers *Drivers) *Engine {
	var d Drivers
	if drivers != nil {
		d = *drivers
	}

	return &Engine{
		provider:  provider,
		accountID: accountID,
		region:    region,
		drivers:   d,
	}
}

// AccountID returns the AWS account ID, Azure subscription ID, or GCP
// project ID the engine was constructed with. Exposed so handlers built on
// top of the engine (Resource Explorer, Resource Graph, Cloud Asset
// Inventory) don't have to ask their callers to supply the same value a
// second time when wiring up the server.
func (e *Engine) AccountID() string {
	return e.accountID
}

// Region returns the default region the engine was constructed with.
func (e *Engine) Region() string {
	return e.region
}

// ListAll walks every configured driver and returns the merged inventory.
// Nil drivers are skipped silently. The first walker error short-circuits
// the rest.
func (e *Engine) ListAll(ctx context.Context) ([]Resource, error) {
	return e.List(ctx, Query{})
}

// List walks every configured driver and returns resources matching q.
// Filtering happens after collection — walkers always return their full set
// so tag/region resolution is consistent regardless of query shape.
//
//nolint:gocritic // q is the public Query filter, taken by value by API contract
func (e *Engine) List(ctx context.Context, q Query) ([]Resource, error) {
	var out []Resource

	for _, walk := range e.walkers() {
		batch, err := walk(ctx)
		if err != nil {
			return nil, err
		}

		for i := range batch {
			if q.matches(&batch[i]) {
				out = append(out, batch[i])
			}
		}
	}

	return out, nil
}

// walkers returns the active walker functions, skipping any whose driver is nil.
func (e *Engine) walkers() []func(context.Context) ([]Resource, error) {
	var ws []func(context.Context) ([]Resource, error)

	if e.drivers.Compute != nil {
		ws = append(ws, e.walkCompute)
	}

	if e.drivers.Networking != nil {
		ws = append(ws, e.walkNetworking)
	}

	if e.drivers.Storage != nil {
		ws = append(ws, e.walkStorage)
	}

	if e.drivers.Database != nil {
		ws = append(ws, e.walkDatabase)
	}

	if e.drivers.Serverless != nil {
		ws = append(ws, e.walkServerless)
	}

	if e.drivers.Databricks != nil {
		ws = append(ws, e.walkDatabricks)
	}

	if e.drivers.Kubernetes != nil {
		ws = append(ws, e.walkKubernetes)
	}

	if e.drivers.RelationalDB != nil {
		ws = append(ws, e.walkRelationalDB)
	}

	if e.drivers.ScaleSets != nil {
		ws = append(ws, e.walkVMSS)
	}

	if e.drivers.Secrets != nil {
		ws = append(ws, e.walkSecrets)
	}

	if e.drivers.ContainerReg != nil {
		ws = append(ws, e.walkContainerRegistry)
	}

	if e.drivers.MessageQueue != nil {
		ws = append(ws, e.walkMessageQueue)
	}

	if e.drivers.AppServicePlans != nil {
		ws = append(ws, e.walkAppServicePlans)
	}

	return ws
}
