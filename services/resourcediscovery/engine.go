package resourcediscovery

import (
	"context"

	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	serverlessdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Drivers bundles the per-service drivers the engine reads from. Any field
// may be nil — the matching walker is skipped in that case. This keeps the
// engine usable in partial test wirings and during the staged rollout of
// per-service walkers in later phases.
type Drivers struct {
	Compute      computedriver.Compute
	Networking   netdriver.Networking
	Storage      storagedriver.Bucket
	Database     dbdriver.Database
	Serverless   serverlessdriver.Serverless
	Databricks   dbxdriver.Databricks
	Kubernetes   KubernetesClusters
	RelationalDB RelationalDatabases
}

// RelationalDatabases is the discovery capability for managed relational
// databases (RDS/Aurora and family). Like KubernetesClusters, the concrete
// mocks live in provider packages, so each provider wires a thin adapter that
// projects its databases onto DiscoveredDatabase rather than having services/
// import providers.
type RelationalDatabases interface {
	DiscoverDatabases(ctx context.Context) ([]DiscoveredDatabase, error)
}

// DiscoveredDatabase is a provider-neutral projection of a managed relational
// database resource (instance, cluster, or snapshot). ARN is used verbatim as
// the resource identifier; Region falls back to the engine default when empty.
type DiscoveredDatabase struct {
	Name   string
	ARN    string
	Region string
	Type   string // one of Type{DBInstance,DBCluster,DBSnapshot}
	Tags   map[string]string
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
	NodeGroups    []string
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

	return ws
}
