// Package mongocluster provides an in-memory mock of Azure Cosmos DB for MongoDB
// (vCore) — the Microsoft.DocumentDB/mongoClusters ARM control plane only. It
// manages the mongo-cluster lifecycle (create/update/get/delete/list) plus the
// listConnectionStrings POST action. The MongoDB data plane (a running mongod
// endpoint, wire-protocol traffic), firewall rules, private endpoints and
// geo-replica (replica) linking are out of scope.
//
// This is distinct from the Cosmos DB core service (Microsoft.DocumentDB/
// databaseAccounts) served by the cosmosdb service — mongoClusters is a separate
// resource type under the same Microsoft.DocumentDB namespace, the Cosmos DB for
// MongoDB (vCore) offering.
//
// The resource carries computed, service-minted fields that MUST stay stable for
// the lifetime of the resource so infrastructure-as-code tools (Terraform's
// azurerm_mongo_cluster) see no drift on re-plan:
//   - provisioningState ("Succeeded") and clusterStatus ("Ready").
//   - the connection string, minted once at create as a deterministic
//     "mongodb+srv://<user>:<password>@<name>.mongocluster.cosmos.azure.com/..."
//     template and stable across every get / patch / listConnectionStrings.
//
// The administrator password is write-only: it is accepted on create/update but
// never echoed on a GET, mirroring real Azure. The connection string embeds a
// literal "<password>" placeholder, not the secret, so no secret ever leaves the
// backend.
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values — across gets, patches,
// listConnectionStrings and a snapshot/restore.
package mongocluster

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

const (
	// providerNamespace is the ARM provider namespace.
	providerNamespace = "Microsoft.DocumentDB"
	// clusterType is the ARM cluster resource type segment.
	clusterType = "mongoClusters"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// statusReady is the terminal clusterStatus real Azure reports for a
	// provisioned mongo cluster.
	statusReady = "Ready"
	// hostNameSuffix is the fixed tail of every mongo-cluster endpoint; real Azure
	// emits "<name>.mongocluster.cosmos.azure.com".
	hostNameSuffix = "mongocluster.cosmos.azure.com"
	// defaultServerVersion is the MongoDB server version a fresh cluster reports
	// when the request omits one.
	defaultServerVersion = "7.0"
	// defaultCreateMode is the createMode real Azure defaults to.
	defaultCreateMode = "Default"
	// defaultPublicNetworkAccess is the publicNetworkAccess default.
	defaultPublicNetworkAccess = "Enabled"
	// haDisabled is the default highAvailability targetMode (no HA).
	haDisabled = "Disabled"
	// connStringOptions is the fixed query-string tail real Azure appends to the
	// default connection string.
	connStringOptions = "?tls=true&authMechanism=SCRAM-SHA-256&retrywrites=false&maxIdleTimeMS=120000"
)

// Compute is the compute tier of a mongo cluster (properties.compute).
type Compute struct {
	Tier string `json:"tier,omitempty"`
}

// Storage is the storage allocation of a mongo cluster (properties.storage).
type Storage struct {
	SizeGb *int64 `json:"sizeGb,omitempty"`
}

// Sharding is the shard layout of a mongo cluster (properties.sharding).
type Sharding struct {
	ShardCount *int32 `json:"shardCount,omitempty"`
}

// HighAvailability is the high-availability mode of a mongo cluster
// (properties.highAvailability). TargetMode is one of Disabled, SameZone or
// ZoneRedundantPreferred.
type HighAvailability struct {
	TargetMode string `json:"targetMode,omitempty"`
}

// Cluster is a stored Microsoft.DocumentDB/mongoClusters resource. Subscription,
// ResourceGroup and Name preserve the caller's casing; the computed fields are
// minted at create and never regenerated on a read.
type Cluster struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`

	AdministratorUserName string            `json:"administratorUserName,omitempty"`
	ServerVersion         string            `json:"serverVersion,omitempty"`
	CreateMode            string            `json:"createMode,omitempty"`
	PublicNetworkAccess   string            `json:"publicNetworkAccess,omitempty"`
	PreviewFeatures       []string          `json:"previewFeatures,omitempty"`
	Compute               *Compute          `json:"compute,omitempty"`
	Storage               *Storage          `json:"storage,omitempty"`
	Sharding              *Sharding         `json:"sharding,omitempty"`
	HighAvailability      *HighAvailability `json:"highAvailability,omitempty"`

	// Computed, stable fields.
	ProvisioningState string `json:"provisioningState"`
	ClusterStatus     string `json:"clusterStatus"`
	ConnectionString  string `json:"connectionString"`
}

// ARMID returns the fully-qualified ARM resource id of the cluster.
func (c *Cluster) ARMID() string {
	return idgen.AzureID(c.Subscription, c.ResourceGroup, providerNamespace, clusterType, c.Name)
}

// ConnectionStringEntry is one named connection string returned by the
// listConnectionStrings action.
type ConnectionStringEntry struct {
	Name             string
	ConnectionString string
	Description      string
}

// ClusterInput carries the mutable fields of a cluster create/update request. The
// pointer/slice fields distinguish "not supplied" (nil, preserve existing) from an
// explicit value, so a PATCH overlays only what it names. AdministratorPassword is
// write-only: it is stored transiently to shape the connection string but never
// echoed.
type ClusterInput struct {
	Tags                  map[string]string
	AdministratorUserName *string
	AdministratorPassword *string
	ServerVersion         *string
	CreateMode            *string
	PublicNetworkAccess   *string
	PreviewFeatures       []string
	Compute               *Compute
	Storage               *Storage
	Sharding              *Sharding
	HighAvailability      *HighAvailability
}

// Mock is the in-memory backend for mongo clusters.
type Mock struct {
	mu       sync.RWMutex
	clusters *memstore.Store[*Cluster]
}

// New creates an empty mongo-cluster mock.
func New(_ *config.Options) *Mock {
	return &Mock{clusters: memstore.New[*Cluster]()}
}

// clusterKey is the case-insensitive store key for a cluster.
func clusterKey(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, clusterType, name))
}

// CreateOrUpdateCluster creates a new cluster or updates an existing one. The
// computed fields (provisioningState, clusterStatus, connectionString) are minted
// once at create and preserved across updates. Location is immutable in real Azure
// and is preserved on update. It returns the stored cluster and whether it was
// newly created.
func (m *Mock) CreateOrUpdateCluster(
	_ context.Context, sub, rg, name, location string, in *ClusterInput,
) (Cluster, bool, error) {
	if err := validateCluster(sub, rg, name, location); err != nil {
		return Cluster{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := clusterKey(sub, rg, name)

	existing, existed := m.clusters.Get(k)
	created := !existed

	var c Cluster
	if existed {
		c = *existing
	} else {
		c = newCluster(sub, rg, name, location)
	}

	applyClusterInput(&c, in)

	m.clusters.Set(k, &c)

	return cloneCluster(&c), created, nil
}

// newCluster seeds a fresh cluster with its immutable identity and its computed,
// stable fields.
func newCluster(sub, rg, name, location string) Cluster {
	return Cluster{
		Subscription:        sub,
		ResourceGroup:       rg,
		Name:                name,
		Location:            location,
		ServerVersion:       defaultServerVersion,
		CreateMode:          defaultCreateMode,
		PublicNetworkAccess: defaultPublicNetworkAccess,
		HighAvailability:    &HighAvailability{TargetMode: haDisabled},
		ProvisioningState:   stateSucceeded,
		ClusterStatus:       statusReady,
	}
}

// GetCluster returns the cluster, or a NotFound error.
func (m *Mock) GetCluster(_ context.Context, sub, rg, name string) (Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(clusterKey(sub, rg, name))
	if !ok {
		return Cluster{}, cerrors.Newf(cerrors.NotFound, "mongo cluster %q not found", name)
	}

	return cloneCluster(c), nil
}

// DeleteCluster removes the cluster, reporting whether it existed.
func (m *Mock) DeleteCluster(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.clusters.Delete(clusterKey(sub, rg, name)), nil
}

// ListClustersByResourceGroup returns every cluster in the group, sorted by name.
func (m *Mock) ListClustersByResourceGroup(_ context.Context, sub, rg string) ([]Cluster, error) {
	return m.filterClusters(func(c *Cluster) bool {
		return strings.EqualFold(c.Subscription, sub) && strings.EqualFold(c.ResourceGroup, rg)
	}), nil
}

// ListClustersBySubscription returns every cluster in the subscription, sorted by
// name.
func (m *Mock) ListClustersBySubscription(_ context.Context, sub string) ([]Cluster, error) {
	return m.filterClusters(func(c *Cluster) bool {
		return strings.EqualFold(c.Subscription, sub)
	}), nil
}

// ListConnectionStrings returns the cluster's connection strings — the stable,
// minted default string. It errors with NotFound if the cluster is absent. The
// returned strings embed a literal "<password>" placeholder, never the secret.
func (m *Mock) ListConnectionStrings(_ context.Context, sub, rg, name string) ([]ConnectionStringEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(clusterKey(sub, rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "mongo cluster %q not found", name)
	}

	return []ConnectionStringEntry{{
		Name:             "default",
		ConnectionString: c.ConnectionString,
		Description:      "default connection string",
	}}, nil
}

// DiscoverClusters returns every stored cluster, for the inventory walk.
func (m *Mock) DiscoverClusters(_ context.Context) ([]Cluster, error) {
	return m.filterClusters(func(*Cluster) bool { return true }), nil
}

// PurgeResourceGroup deletes every cluster under sub/rg, so a resource-group
// delete cascades into its mongo clusters.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, c := range m.clusters.All() {
		if strings.EqualFold(c.Subscription, sub) && strings.EqualFold(c.ResourceGroup, rg) {
			m.clusters.Delete(k)
		}
	}

	return nil
}

// filterClusters returns the clusters matching pred, sorted by name.
func (m *Mock) filterClusters(pred func(*Cluster) bool) []Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Cluster

	for _, c := range m.clusters.All() {
		if pred(c) {
			out = append(out, cloneCluster(c))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyClusterInput overlays the mutable request fields onto c, leaving the
// immutable location untouched. A nil pointer/slice means "not supplied": the
// stored value is preserved, so a PATCH merges only what it names. The connection
// string is (re)minted only when it is still empty — i.e. once, at create — so it
// stays stable across every later update.
func applyClusterInput(c *Cluster, in *ClusterInput) {
	if in.Tags != nil {
		c.Tags = maps.Clone(in.Tags)
	}

	if in.PreviewFeatures != nil {
		c.PreviewFeatures = slices.Clone(in.PreviewFeatures)
	}

	applyClusterScalars(c, in)
	applyClusterBlocks(c, in)

	if c.ConnectionString == "" {
		c.ConnectionString = connectionString(c.Name)
	}
}

// applyClusterScalars overlays the string-valued properties. Each is skipped when
// nil or empty, so a PATCH that omits it preserves the stored value.
func applyClusterScalars(c *Cluster, in *ClusterInput) {
	if in.AdministratorUserName != nil && *in.AdministratorUserName != "" {
		c.AdministratorUserName = *in.AdministratorUserName
	}

	if in.ServerVersion != nil && *in.ServerVersion != "" {
		c.ServerVersion = *in.ServerVersion
	}

	if in.CreateMode != nil && *in.CreateMode != "" {
		c.CreateMode = *in.CreateMode
	}

	if in.PublicNetworkAccess != nil && *in.PublicNetworkAccess != "" {
		c.PublicNetworkAccess = *in.PublicNetworkAccess
	}
}

// applyClusterBlocks overlays the nested compute/storage/sharding/highAvailability
// blocks, deep-copying each so callers never alias the backing store.
func applyClusterBlocks(c *Cluster, in *ClusterInput) {
	if in.Compute != nil {
		c.Compute = &Compute{Tier: in.Compute.Tier}
	}

	if in.Storage != nil {
		c.Storage = cloneStorage(in.Storage)
	}

	if in.Sharding != nil {
		c.Sharding = cloneSharding(in.Sharding)
	}

	if in.HighAvailability != nil && in.HighAvailability.TargetMode != "" {
		c.HighAvailability = &HighAvailability{TargetMode: in.HighAvailability.TargetMode}
	}
}

// connectionString derives the stable default connection string, matching the
// "mongodb+srv://<user>:<password>@<name>.mongocluster.cosmos.azure.com/..." form
// real Azure emits on the resource and from listConnectionStrings. Azure keeps the
// literal "<user>" and "<password>" placeholders — it never substitutes the real
// administrator login or the secret — so the string is derived from the (immutable)
// cluster name alone and is inherently stable across gets, patches and the action.
func connectionString(name string) string {
	return "mongodb+srv://<user>:<password>@" +
		strings.ToLower(name) + "." + hostNameSuffix + "/" + connStringOptions
}

// validateCluster rejects a cluster create/update with missing required fields.
func validateCluster(sub, rg, name, location string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "cluster name is required")
	case location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// cloneStorage deep-copies a storage block so callers never alias the backing store.
func cloneStorage(in *Storage) *Storage {
	if in == nil {
		return nil
	}

	out := &Storage{}

	if in.SizeGb != nil {
		v := *in.SizeGb
		out.SizeGb = &v
	}

	return out
}

// cloneSharding deep-copies a sharding block so callers never alias the backing store.
func cloneSharding(in *Sharding) *Sharding {
	if in == nil {
		return nil
	}

	out := &Sharding{}

	if in.ShardCount != nil {
		v := *in.ShardCount
		out.ShardCount = &v
	}

	return out
}

// cloneCluster deep-copies a stored cluster so callers never alias the backing store.
func cloneCluster(c *Cluster) Cluster {
	out := *c
	out.Tags = maps.Clone(c.Tags)
	out.PreviewFeatures = slices.Clone(c.PreviewFeatures)
	out.Storage = cloneStorage(c.Storage)
	out.Sharding = cloneSharding(c.Sharding)

	if c.Compute != nil {
		comp := *c.Compute
		out.Compute = &comp
	}

	if c.HighAvailability != nil {
		ha := *c.HighAvailability
		out.HighAvailability = &ha
	}

	return out
}
