// Package elasticsan provides an in-memory mock of Azure Elastic SAN
// (Microsoft.ElasticSan/elasticSans) — the ARM control plane only. It manages
// the top-level elasticSans resource lifecycle (create/update/get/delete/list);
// the nested volumeGroups and volumes, private endpoints and the iSCSI data
// plane are out of scope.
//
// The resource carries a set of computed, service-minted fields that MUST stay
// stable for the lifetime of the resource so infrastructure-as-code tools
// (Terraform's azurerm_elastic_san) see no drift on re-plan. Every one is
// derived deterministically from base_size_in_tib, extended_size_in_tib and the
// sku, matching the values real Azure reports:
//   - totalIops       = baseSizeTiB * 5000  (5,000 IOPS per base TiB)
//   - totalMBps       = baseSizeTiB * 200   (200 MB/s per base TiB)
//   - totalSizeTiB    = baseSizeTiB + extendedCapacitySizeTiB
//   - totalVolumeSizeGiB = 0 — the sum of provisioned volume sizes; volumes are
//     a deferred nested resource, so a bare SAN reports 0 (matching real Azure).
//   - volumeGroupCount   = 0 — same reason.
//   - provisioningState  = "Succeeded" once provisioning completes.
//
// Extended (additional) capacity adds storage but NOT IOPS or throughput, so it
// never contributes to totalIops/totalMBps — only to totalSizeTiB. The totals
// are recomputed whenever base_size_in_tib or extended_size_in_tib changes on an
// update, which is exactly what Terraform expects.
package elasticsan

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
	providerNamespace = "Microsoft.ElasticSan"
	// resourceType is the ARM resource type segment.
	resourceType = "elasticSans"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// enabled / disabled are the ARM enum values for publicNetworkAccess.
	enabled  = "Enabled"
	disabled = "Disabled"
	// defaultSkuTier is the sku tier an Elastic SAN defaults to when the request
	// omits it (Premium is the only tier real Azure offers).
	defaultSkuTier = "Premium"
	// iopsPerBaseTiB and mbpsPerBaseTiB are the per-base-TiB performance rates
	// real Azure provisions: each base TiB grants 5,000 IOPS and 200 MB/s.
	// Extended capacity grants neither, so both scale off base size alone.
	iopsPerBaseTiB = 5000
	mbpsPerBaseTiB = 200
)

// Sku is the pricing tier of an Elastic SAN. Name is required (Premium_LRS or
// Premium_ZRS); Tier defaults to Premium and is echoed back to match real Azure.
type Sku struct {
	Name string `json:"name"`
	Tier string `json:"tier,omitempty"`
}

// ElasticSan is a stored Microsoft.ElasticSan/elasticSans resource. Subscription,
// ResourceGroup and Name preserve the caller's casing; the computed total_*
// fields are minted from the sizes at create/update and never regenerated on a
// read.
type ElasticSan struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Sku           *Sku              `json:"sku,omitempty"`
	Zones         []string          `json:"zones,omitempty"`

	// Writable properties.
	BaseSizeTiB         int64  `json:"baseSizeTiB"`
	ExtendedSizeTiB     int64  `json:"extendedCapacitySizeTiB"`
	PublicNetworkAccess string `json:"publicNetworkAccess"`

	// Computed, stable fields — derived from the sizes and sku.
	ProvisioningState  string `json:"provisioningState"`
	TotalIops          int64  `json:"totalIops"`
	TotalMBps          int64  `json:"totalMBps"`
	TotalSizeTiB       int64  `json:"totalSizeTiB"`
	TotalVolumeSizeGiB int64  `json:"totalVolumeSizeGiB"`
	VolumeGroupCount   int64  `json:"volumeGroupCount"`
}

// ARMID returns the fully-qualified ARM resource id.
func (s *ElasticSan) ARMID() string {
	return idgen.AzureID(s.Subscription, s.ResourceGroup, providerNamespace, resourceType, s.Name)
}

// Input carries the mutable fields of a create/update request. The pointer
// fields distinguish "not supplied" (nil, preserve existing) from an explicit
// value, so a PATCH overlays only what it names.
type Input struct {
	Tags                map[string]string
	Sku                 *Sku
	Zones               []string
	BaseSizeTiB         *int64
	ExtendedSizeTiB     *int64
	PublicNetworkAccess *string
}

// Mock is the in-memory backend for Elastic SAN resources.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*ElasticSan]
}

// New creates an empty Elastic SAN mock.
func New(_ *config.Options) *Mock {
	return &Mock{store: memstore.New[*ElasticSan]()}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new Elastic SAN or updates an existing one. The
// computed total_* fields are recomputed from the (possibly updated) base and
// extended sizes so they stay consistent with what real Azure reports. Location
// is immutable and preserved on update. It returns the stored resource and
// whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name, location string, in *Input) (ElasticSan, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	var s ElasticSan
	if existed {
		s = *existing
	} else {
		s = newElasticSan(sub, rg, name, location)
	}

	if err := validate(sub, rg, name, in, created); err != nil {
		return ElasticSan{}, false, err
	}

	applyInput(&s, in)
	mintComputed(&s)

	m.store.Set(k, &s)

	return clone(&s), created, nil
}

// newElasticSan seeds a fresh resource with its immutable identity, location and
// the ARM default publicNetworkAccess value.
func newElasticSan(sub, rg, name, location string) ElasticSan {
	return ElasticSan{
		Subscription:        sub,
		ResourceGroup:       rg,
		Name:                name,
		Location:            location,
		PublicNetworkAccess: enabled,
		ProvisioningState:   stateSucceeded,
	}
}

// Get returns the resource, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (ElasticSan, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return ElasticSan{}, cerrors.Newf(cerrors.NotFound, "elastic san %q not found", name)
	}

	return clone(s), nil
}

// Delete removes the resource, reporting whether it existed.
func (m *Mock) Delete(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, rg, name)), nil
}

// ListByResourceGroup returns every resource in the group, sorted by name.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]ElasticSan, error) {
	return m.filter(func(s *ElasticSan) bool {
		return strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]ElasticSan, error) {
	return m.filter(func(s *ElasticSan) bool {
		return strings.EqualFold(s.Subscription, sub)
	}), nil
}

// DiscoverElasticSans returns every stored resource, for the inventory walk.
func (m *Mock) DiscoverElasticSans(_ context.Context) ([]ElasticSan, error) {
	return m.filter(func(*ElasticSan) bool { return true }), nil
}

// PurgeResourceGroup deletes every Elastic SAN under sub/rg, so a resource-group
// delete cascades into its Elastic SANs.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, s := range m.store.All() {
		if strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg) {
			m.store.Delete(k)
		}
	}

	return nil
}

// filter returns the resources matching pred, sorted by name for a stable order.
func (m *Mock) filter(pred func(*ElasticSan) bool) []ElasticSan {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []ElasticSan

	for _, s := range m.store.All() {
		if pred(s) {
			out = append(out, clone(s))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyInput overlays the mutable request fields onto s, leaving the immutable
// location untouched. A nil pointer/slice field means "not supplied": the stored
// value is preserved, so a PATCH merges only what it names.
func applyInput(s *ElasticSan, in *Input) {
	if in.Tags != nil {
		s.Tags = maps.Clone(in.Tags)
	}

	if in.Sku != nil {
		s.Sku = resolveSku(in.Sku)
	}

	if in.Zones != nil {
		s.Zones = slices.Clone(in.Zones)
	}

	if in.BaseSizeTiB != nil {
		s.BaseSizeTiB = *in.BaseSizeTiB
	}

	if in.ExtendedSizeTiB != nil {
		s.ExtendedSizeTiB = *in.ExtendedSizeTiB
	}

	if in.PublicNetworkAccess != nil && *in.PublicNetworkAccess != "" {
		s.PublicNetworkAccess = normalizeEnum(*in.PublicNetworkAccess)
	}
}

// resolveSku normalizes an incoming sku, defaulting the tier to Premium so a read
// echoes the same fields real Azure computes. A nil or nameless sku resolves to
// nil.
func resolveSku(in *Sku) *Sku {
	if in == nil || in.Name == "" {
		return nil
	}

	tier := in.Tier
	if tier == "" {
		tier = defaultSkuTier
	}

	return &Sku{Name: in.Name, Tier: tier}
}

// mintComputed recomputes the stable, service-minted fields from the sizes. The
// totals are derived deterministically so they stay consistent across reads and
// recompute correctly when base/extended size changes on an update.
func mintComputed(s *ElasticSan) {
	s.ProvisioningState = stateSucceeded

	if s.PublicNetworkAccess == "" {
		s.PublicNetworkAccess = enabled
	}

	// Base capacity determines performance; extended capacity adds none.
	s.TotalIops = s.BaseSizeTiB * iopsPerBaseTiB
	s.TotalMBps = s.BaseSizeTiB * mbpsPerBaseTiB
	s.TotalSizeTiB = s.BaseSizeTiB + s.ExtendedSizeTiB

	// The sum of provisioned volume sizes and the volume-group count are both 0:
	// volume groups and volumes are deferred nested resources, so a bare SAN
	// reports 0, exactly as real Azure does before any volume is created.
	s.TotalVolumeSizeGiB = 0
	s.VolumeGroupCount = 0
}

// normalizeEnum canonicalizes a toggle value to the ARM "Enabled"/"Disabled"
// casing, leaving any other value untouched.
func normalizeEnum(v string) string {
	switch {
	case strings.EqualFold(v, enabled):
		return enabled
	case strings.EqualFold(v, disabled):
		return disabled
	default:
		return v
	}
}

// validate rejects a create/update with missing required fields. base_size_in_tib
// and the sku name are required on create; a PATCH may omit them (preserving the
// stored value), so they are only enforced when the resource is newly created.
func validate(sub, rg, name string, in *Input, created bool) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "elastic san name is required")
	}

	if created {
		if in.Sku == nil || in.Sku.Name == "" {
			return cerrors.New(cerrors.InvalidArgument, "sku.name is required")
		}

		if in.BaseSizeTiB == nil || *in.BaseSizeTiB <= 0 {
			return cerrors.New(cerrors.InvalidArgument, "baseSizeTiB must be a positive integer")
		}
	}

	return nil
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(s *ElasticSan) ElasticSan {
	out := *s
	out.Tags = maps.Clone(s.Tags)
	out.Zones = slices.Clone(s.Zones)

	if s.Sku != nil {
		sku := *s.Sku
		out.Sku = &sku
	}

	return out
}
