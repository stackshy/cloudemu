// Package redisenterprise provides an in-memory mock of Azure Redis Enterprise
// (Microsoft.Cache/redisEnterprise) — the ARM control plane only. It manages the
// redisEnterprise cluster lifecycle (create/update/get/delete/list) and the
// nested redisEnterprise/{cluster}/databases child resource (create/update/get/
// delete/list plus the listKeys/regenerateKey actions). The Redis data plane (a
// running Redis Enterprise instance, GET/SET traffic), private endpoints and
// geo-replication linking are out of scope.
//
// This is distinct from the standard Azure Cache for Redis
// (Microsoft.Cache/redis) served by the azurecache service — redisEnterprise is a
// separate resource-provider surface under the same Microsoft.Cache namespace.
//
// Both resources carry computed, service-minted fields that MUST stay stable for
// the lifetime of the resource so infrastructure-as-code tools (Terraform's
// azurerm_redis_enterprise_cluster / azurerm_redis_enterprise_database) see no
// drift on re-plan:
//   - cluster hostName: "<name>.<region>.redisenterprise.cache.azure.net",
//     deterministic from the name and location.
//   - cluster provisioningState ("Succeeded") and resourceState ("Running").
//   - cluster redisVersion — the running engine version real Azure reports.
//   - database provisioningState ("Succeeded") and resourceState ("Running").
//   - database primaryKey / secondaryKey, minted once at create and stable across
//     every get/patch/listKeys/regenerateKey.
//   - database module version — the version real Azure stamps on each module.
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values — across gets, patches,
// listKeys and a snapshot/restore.
package redisenterprise

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
	providerNamespace = "Microsoft.Cache"
	// clusterType is the ARM cluster resource type segment.
	clusterType = "redisEnterprise"
	// databaseType is the ARM child resource type segment.
	databaseType = "databases"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// stateRunning is the terminal resourceState real Azure reports for a
	// provisioned cluster or database.
	stateRunning = "Running"
	// hostNameSuffix is the fixed tail of every redisEnterprise cluster hostname;
	// real Azure emits "<name>.<region>.redisenterprise.cache.azure.net".
	hostNameSuffix = "redisenterprise.cache.azure.net"
	// defaultTLSVersion is the minimumTlsVersion real Azure defaults to.
	defaultTLSVersion = "1.2"
	// defaultRedisVersion is the engine version a fresh cluster reports.
	defaultRedisVersion = "7.4"
	// defaultProtocol is the database clientProtocol real Azure defaults to.
	defaultProtocol = "Encrypted"
	// defaultClusteringPolicy is the database clusteringPolicy default.
	defaultClusteringPolicy = "OSSCluster"
	// defaultEvictionPolicy is the database evictionPolicy default.
	defaultEvictionPolicy = "VolatileLRU"
	// defaultPort is the database port real Azure defaults to.
	defaultPort = 10000
	// defaultModuleVersion is the version real Azure stamps on a loaded module.
	defaultModuleVersion = "1.0.0"
)

// Sku is the pricing tier of a redisEnterprise cluster. Name encodes the tier and
// size (e.g. Enterprise_E10, EnterpriseFlash_F300); Capacity is the unit count
// (the "-N" suffix of Terraform's sku_name).
type Sku struct {
	Name     string `json:"name"`
	Capacity *int   `json:"capacity,omitempty"`
}

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to a redisEnterprise cluster. Type is
// one of SystemAssigned, UserAssigned or "SystemAssigned,UserAssigned".
// PrincipalID and TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// Cluster is a stored Microsoft.Cache/redisEnterprise resource. Subscription,
// ResourceGroup and Name preserve the caller's casing; the computed fields are
// minted at create and never regenerated on a read.
type Cluster struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Sku           *Sku              `json:"sku,omitempty"`
	Zones         []string          `json:"zones,omitempty"`
	Identity      *Identity         `json:"identity,omitempty"`

	MinimumTLSVersion string `json:"minimumTlsVersion,omitempty"`

	// Computed, stable fields.
	HostName          string `json:"hostName"`
	ProvisioningState string `json:"provisioningState"`
	ResourceState     string `json:"resourceState"`
	RedisVersion      string `json:"redisVersion"`
}

// Module is one Redis module loaded into a database (e.g. RediSearch, RedisBloom).
// Name and Args round-trip verbatim; Version is minted by the service.
type Module struct {
	Name    string `json:"name"`
	Args    string `json:"args"`
	Version string `json:"version"`
}

// Database is a stored Microsoft.Cache/redisEnterprise/databases child resource.
// It is keyed under its parent cluster; deleting the cluster cascades to it.
type Database struct {
	Subscription  string `json:"subscription"`
	ResourceGroup string `json:"resourceGroup"`
	ClusterName   string `json:"clusterName"`
	Name          string `json:"name"`

	ClientProtocol   string   `json:"clientProtocol"`
	ClusteringPolicy string   `json:"clusteringPolicy"`
	EvictionPolicy   string   `json:"evictionPolicy"`
	Port             *int     `json:"port,omitempty"`
	Modules          []Module `json:"modules,omitempty"`
	GroupNickname    string   `json:"groupNickname,omitempty"`

	// Computed, stable fields.
	ProvisioningState string `json:"provisioningState"`
	ResourceState     string `json:"resourceState"`
	PrimaryKey        string `json:"primaryKey"`
	SecondaryKey      string `json:"secondaryKey"`
}

// ARMID returns the fully-qualified ARM resource id of the cluster.
func (c *Cluster) ARMID() string {
	return idgen.AzureID(c.Subscription, c.ResourceGroup, providerNamespace, clusterType, c.Name)
}

// ARMID returns the fully-qualified ARM resource id of the database, nested under
// its parent cluster.
func (d *Database) ARMID() string {
	return idgen.AzureID(d.Subscription, d.ResourceGroup, providerNamespace, clusterType, d.ClusterName) +
		"/" + databaseType + "/" + d.Name
}

// ClusterInput carries the mutable fields of a cluster create/update request. The
// pointer/slice fields distinguish "not supplied" (nil, preserve existing) from an
// explicit value, so a PATCH overlays only what it names.
type ClusterInput struct {
	Tags              map[string]string
	Sku               *Sku
	Zones             []string
	Identity          *Identity
	MinimumTLSVersion *string
}

// DatabaseInput carries the mutable fields of a database create/update request.
type DatabaseInput struct {
	ClientProtocol   *string
	ClusteringPolicy *string
	EvictionPolicy   *string
	Port             *int
	Modules          []Module
	GroupNickname    *string
}

// Mock is the in-memory backend for redisEnterprise clusters and their databases.
type Mock struct {
	mu        sync.RWMutex
	clusters  *memstore.Store[*Cluster]
	databases *memstore.Store[*Database]

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty redisEnterprise mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		clusters:  memstore.New[*Cluster](),
		databases: memstore.New[*Database](),
		tenantID:  idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// clusterKey is the case-insensitive store key for a cluster.
func clusterKey(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, clusterType, name))
}

// databaseKey is the case-insensitive store key for a database under a cluster.
func databaseKey(sub, rg, cluster, name string) string {
	return clusterKey(sub, rg, cluster) + "/" + databaseType + "/" + strings.ToLower(name)
}

// CreateOrUpdateCluster creates a new cluster or updates an existing one. The
// computed fields (hostName, provisioningState, resourceState, redisVersion,
// identity ids) are minted once at create and preserved across updates. Location
// is immutable in real Azure and is preserved on update. It returns the stored
// cluster and whether it was newly created.
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

	if created && (in.Sku == nil || in.Sku.Name == "") {
		return Cluster{}, false, cerrors.New(cerrors.InvalidArgument, "sku.name is required")
	}

	var c Cluster
	if existed {
		c = *existing
	} else {
		c = newCluster(sub, rg, name, location)
	}

	applyClusterInput(&c, in)

	// Identity follows the same merge-on-nil model as the other mutable fields:
	// a PATCH/PUT that omits the identity block preserves the stored value
	// (ARM merge-patch semantics), so an unrelated tags-only update never wipes
	// a system-assigned identity. An explicit block re-resolves it.
	if in.Identity != nil {
		c.Identity = m.resolveIdentity(in.Identity, sub, rg, name)
	}

	m.clusters.Set(k, &c)

	return cloneCluster(&c), created, nil
}

// newCluster seeds a fresh cluster with its immutable identity and its computed,
// stable fields.
func newCluster(sub, rg, name, location string) Cluster {
	return Cluster{
		Subscription:      sub,
		ResourceGroup:     rg,
		Name:              name,
		Location:          location,
		MinimumTLSVersion: defaultTLSVersion,
		HostName:          hostName(name, location),
		ProvisioningState: stateSucceeded,
		ResourceState:     stateRunning,
		RedisVersion:      defaultRedisVersion,
	}
}

// GetCluster returns the cluster, or a NotFound error.
func (m *Mock) GetCluster(_ context.Context, sub, rg, name string) (Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.clusters.Get(clusterKey(sub, rg, name))
	if !ok {
		return Cluster{}, cerrors.Newf(cerrors.NotFound, "redisEnterprise cluster %q not found", name)
	}

	return cloneCluster(c), nil
}

// DeleteCluster removes the cluster and cascades to every database under it,
// reporting whether the cluster existed.
func (m *Mock) DeleteCluster(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existed := m.clusters.Delete(clusterKey(sub, rg, name))

	prefix := clusterKey(sub, rg, name) + "/" + databaseType + "/"
	for dk := range m.databases.All() {
		if strings.HasPrefix(dk, prefix) {
			m.databases.Delete(dk)
		}
	}

	return existed, nil
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

// DiscoverClusters returns every stored cluster, for the inventory walk.
func (m *Mock) DiscoverClusters(_ context.Context) ([]Cluster, error) {
	return m.filterClusters(func(*Cluster) bool { return true }), nil
}

// PurgeResourceGroup deletes every cluster and database under sub/rg, so a
// resource-group delete cascades into its redisEnterprise resources.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, c := range m.clusters.All() {
		if strings.EqualFold(c.Subscription, sub) && strings.EqualFold(c.ResourceGroup, rg) {
			m.clusters.Delete(k)
		}
	}

	for k, d := range m.databases.All() {
		if strings.EqualFold(d.Subscription, sub) && strings.EqualFold(d.ResourceGroup, rg) {
			m.databases.Delete(k)
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
// immutable location and computed fields untouched. A nil pointer/slice means
// "not supplied": the stored value is preserved, so a PATCH merges only what it
// names.
func applyClusterInput(c *Cluster, in *ClusterInput) {
	if in.Tags != nil {
		c.Tags = maps.Clone(in.Tags)
	}

	if in.Sku != nil {
		c.Sku = cloneSku(in.Sku)
	}

	if in.Zones != nil {
		c.Zones = slices.Clone(in.Zones)
	}

	if in.MinimumTLSVersion != nil && *in.MinimumTLSVersion != "" {
		c.MinimumTLSVersion = *in.MinimumTLSVersion
	}

	if c.MinimumTLSVersion == "" {
		c.MinimumTLSVersion = defaultTLSVersion
	}
}

// resolveIdentity normalizes an incoming managed identity, synthesizing the
// deterministic ids Azure mints on assignment. A nil or "None" identity resolves
// to nil.
func (m *Mock) resolveIdentity(in *Identity, sub, rg, name string) *Identity {
	if in == nil || in.Type == "" || strings.EqualFold(in.Type, "None") {
		return nil
	}

	out := &Identity{Type: in.Type}

	if strings.Contains(strings.ToLower(in.Type), "systemassigned") {
		id := clusterKey(sub, rg, name)
		out.PrincipalID = idgen.SyntheticGUID("principal/" + id)
		out.TenantID = m.tenantID
	}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = UserAssignedValue{
				PrincipalID: idgen.SyntheticGUID("ua-principal/" + strings.ToLower(id)),
				ClientID:    idgen.SyntheticGUID("ua-client/" + strings.ToLower(id)),
			}
		}
	}

	return out
}

// hostName derives the stable cluster hostname from its name and location, matching
// the "<name>.<region>.redisenterprise.cache.azure.net" form real Azure emits. The
// region is normalized to lower-case with whitespace stripped (e.g. "West US" ->
// "westus").
func hostName(name, location string) string {
	region := strings.ToLower(strings.ReplaceAll(location, " ", ""))

	return strings.ToLower(name) + "." + region + "." + hostNameSuffix
}

// validateCluster rejects a cluster create/update with missing required fields.
// The sku is required on create; that check lives in CreateOrUpdateCluster where
// the created/existed distinction is known.
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

// CreateOrUpdateDatabase creates a new database or updates an existing one under
// its parent cluster. The parent cluster must exist — otherwise it returns a
// NotFound error (the wire layer maps it to ParentResourceNotFound). The computed
// keys, provisioningState and resourceState are minted once at create and
// preserved across updates. It returns the stored database and whether it was
// newly created.
func (m *Mock) CreateOrUpdateDatabase(
	_ context.Context, sub, rg, cluster, name string, in *DatabaseInput,
) (Database, bool, error) {
	if err := validateDatabase(sub, rg, cluster, name); err != nil {
		return Database{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(clusterKey(sub, rg, cluster)); !ok {
		return Database{}, false, cerrors.Newf(cerrors.NotFound,
			"redisEnterprise cluster %q not found", cluster)
	}

	k := databaseKey(sub, rg, cluster, name)

	existing, existed := m.databases.Get(k)
	created := !existed

	var d Database
	if existed {
		d = *existing
	} else {
		d = newDatabase(sub, rg, cluster, name)
	}

	applyDatabaseInput(&d, in)

	m.databases.Set(k, &d)

	return cloneDatabase(&d), created, nil
}

// newDatabase seeds a fresh database with its immutable identity, its ARM defaults
// and its computed, stable fields.
func newDatabase(sub, rg, cluster, name string) Database {
	id := databaseKey(sub, rg, cluster, name)

	return Database{
		Subscription:      sub,
		ResourceGroup:     rg,
		ClusterName:       cluster,
		Name:              name,
		ClientProtocol:    defaultProtocol,
		ClusteringPolicy:  defaultClusteringPolicy,
		EvictionPolicy:    defaultEvictionPolicy,
		ProvisioningState: stateSucceeded,
		ResourceState:     stateRunning,
		PrimaryKey:        mintKey("primary/" + id),
		SecondaryKey:      mintKey("secondary/" + id),
	}
}

// GetDatabase returns the database, or a NotFound error.
func (m *Mock) GetDatabase(_ context.Context, sub, rg, cluster, name string) (Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	d, ok := m.databases.Get(databaseKey(sub, rg, cluster, name))
	if !ok {
		return Database{}, cerrors.Newf(cerrors.NotFound, "redisEnterprise database %q not found", name)
	}

	return cloneDatabase(d), nil
}

// DeleteDatabase removes the database, reporting whether it existed.
func (m *Mock) DeleteDatabase(_ context.Context, sub, rg, cluster, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.databases.Delete(databaseKey(sub, rg, cluster, name)), nil
}

// ListDatabasesByCluster returns every database under the cluster, sorted by name.
func (m *Mock) ListDatabasesByCluster(_ context.Context, sub, rg, cluster string) ([]Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := clusterKey(sub, rg, cluster) + "/" + databaseType + "/"

	var out []Database

	for k, d := range m.databases.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, cloneDatabase(d))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// validateDatabase rejects a database create/update with missing required fields.
func validateDatabase(sub, rg, cluster, name string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case cluster == "":
		return cerrors.New(cerrors.InvalidArgument, "cluster name is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "database name is required")
	default:
		return nil
	}
}

// applyDatabaseInput overlays the mutable request fields onto d, leaving the
// computed fields untouched. A nil pointer/slice means "not supplied": the stored
// (or default) value is preserved, so a PATCH merges only what it names. Modules
// round-trip verbatim, each stamped with a stable version.
func applyDatabaseInput(d *Database, in *DatabaseInput) {
	if in.ClientProtocol != nil && *in.ClientProtocol != "" {
		d.ClientProtocol = *in.ClientProtocol
	}

	if in.ClusteringPolicy != nil && *in.ClusteringPolicy != "" {
		d.ClusteringPolicy = *in.ClusteringPolicy
	}

	if in.EvictionPolicy != nil && *in.EvictionPolicy != "" {
		d.EvictionPolicy = *in.EvictionPolicy
	}

	if in.GroupNickname != nil {
		d.GroupNickname = *in.GroupNickname
	}

	if in.Modules != nil {
		d.Modules = resolveModules(in.Modules)
	}

	applyDatabasePort(d, in.Port)
}

// applyDatabasePort overlays a supplied port, defaulting to the ARM default when
// none is set yet.
func applyDatabasePort(d *Database, port *int) {
	if port != nil {
		p := *port
		d.Port = &p
	}

	if d.Port == nil {
		p := defaultPort
		d.Port = &p
	}
}

// resolveModules normalizes an incoming module list, stamping each with a stable
// version so a read echoes the version real Azure computes.
func resolveModules(in []Module) []Module {
	out := make([]Module, 0, len(in))

	for i := range in {
		version := in[i].Version
		if version == "" {
			version = defaultModuleVersion
		}

		out = append(out, Module{Name: in[i].Name, Args: in[i].Args, Version: version})
	}

	return out
}

// mintKey derives a stable 44-character base64-shaped access key from a seed.
func mintKey(seed string) string {
	a := strings.ReplaceAll(idgen.SyntheticGUID(seed), "-", "")
	b := strings.ReplaceAll(idgen.SyntheticGUID(seed+"#2"), "-", "")

	return (a + b + a)[:44]
}

// cloneSku deep-copies a sku so callers never alias the backing store.
func cloneSku(in *Sku) *Sku {
	if in == nil {
		return nil
	}

	out := &Sku{Name: in.Name}

	if in.Capacity != nil {
		c := *in.Capacity
		out.Capacity = &c
	}

	return out
}

// cloneCluster deep-copies a stored cluster so callers never alias the backing
// store.
func cloneCluster(c *Cluster) Cluster {
	out := *c
	out.Tags = maps.Clone(c.Tags)
	out.Zones = slices.Clone(c.Zones)
	out.Sku = cloneSku(c.Sku)

	if c.Identity != nil {
		id := *c.Identity
		id.UserAssigned = maps.Clone(c.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}

// cloneDatabase deep-copies a stored database so callers never alias the backing
// store.
func cloneDatabase(d *Database) Database {
	out := *d
	out.Modules = slices.Clone(d.Modules)

	if d.Port != nil {
		p := *d.Port
		out.Port = &p
	}

	return out
}
