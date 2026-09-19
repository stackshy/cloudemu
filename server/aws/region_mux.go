package aws

import (
	"context"
	"net/http"
	"sync"

	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// RegionEntry is one region's fully-wired backend: the wire server that serves
// it and the provider that backs it. The mux keeps the provider so persistence,
// seeding, cost, and reset can enumerate live regions.
type RegionEntry struct {
	Server   http.Handler
	Provider *awsprovider.Provider
}

// regionHolder guards a single region's lazy construction. once ensures the
// (expensive) provider+server build runs exactly once even under a burst of
// concurrent first-touch requests to the same new region, without holding the
// mux map lock across the build — so a first request to a brand-new region never
// blocks requests to already-built (or unrelated new) regions.
type regionHolder struct {
	once  sync.Once
	entry RegionEntry
}

// RegionMux dispatches each AWS request to the wire server for the region the
// caller addressed (via the SigV4 credential scope), building and caching a
// fresh regional backend on first touch. An unsigned request — or one whose
// scope names the default region — routes to the pre-built default entry, so the
// single-region path is byte-identical to a non-muxed server.
//
// Global services (IAM, STS, Route 53, CloudFront, Global Accelerator) need no
// special routing: every region's provider shares the SAME global instances, so
// a global request handled by any region's server reads and writes the one
// shared state.
type RegionMux struct {
	defaultRegion string
	build         func(region string) RegionEntry

	mu      sync.Mutex
	holders map[string]*regionHolder
	built   map[string]RegionEntry // regions whose build completed, for enumeration
}

// NewRegionMux builds a mux seeded with the pre-built default-region entry.
// build constructs a fresh backend for any other region on first touch. The
// default region is never built through build — it is the entry passed here.
func NewRegionMux(defaultRegion string, defaultEntry RegionEntry, build func(region string) RegionEntry) *RegionMux {
	m := &RegionMux{
		defaultRegion: defaultRegion,
		build:         build,
		holders:       make(map[string]*regionHolder),
		built:         make(map[string]RegionEntry),
	}

	dh := &regionHolder{entry: defaultEntry}
	dh.once.Do(func() {}) // consume the once so build never runs for the default region
	m.holders[defaultRegion] = dh
	m.built[defaultRegion] = defaultEntry

	return m
}

// regionOf returns the region the request addressed, defaulting to the mux's
// default region when the request is unsigned or its scope is malformed.
func (m *RegionMux) regionOf(r *http.Request) string {
	region := awsquery.CredentialScopeRegion(r.Header.Get("Authorization"))
	if region == "" {
		return m.defaultRegion
	}

	return region
}

// holder returns the region's holder, creating an empty one under the map lock.
// The lock is held only for this get-or-create, never across the build.
func (m *RegionMux) holder(region string) *regionHolder {
	m.mu.Lock()
	defer m.mu.Unlock()

	h, ok := m.holders[region]
	if !ok {
		h = &regionHolder{}
		m.holders[region] = h
	}

	return h
}

// entryFor returns the region's entry, building it once on first touch.
func (m *RegionMux) entryFor(region string) RegionEntry {
	h := m.holder(region)
	h.once.Do(func() {
		e := m.build(region)

		m.mu.Lock()
		h.entry = e
		m.built[region] = e
		m.mu.Unlock()
	})

	return h.entry
}

// ServeHTTP routes the request to its region's backend.
func (m *RegionMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.entryFor(m.regionOf(r)).Server.ServeHTTP(w, r)
}

// GetOrCreate returns the provider for region, building it if it does not exist
// yet. An empty region maps to the default region. Persist restore uses this to
// materialize a region that was captured in a snapshot but has no live provider
// yet.
func (m *RegionMux) GetOrCreate(region string) *awsprovider.Provider {
	if region == "" {
		region = m.defaultRegion
	}

	return m.entryFor(region).Provider
}

// LiveProviders returns every built region's provider, keyed by region.
func (m *RegionMux) LiveProviders() map[string]*awsprovider.Provider {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make(map[string]*awsprovider.Provider, len(m.built))
	for region, e := range m.built {
		out[region] = e.Provider
	}

	return out
}

// LiveEngines returns the resource-discovery engine of every built region, for
// the cross-region cost/Resource-Explorer aggregator.
func (m *RegionMux) LiveEngines() []*resourcediscovery.Engine {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]*resourcediscovery.Engine, 0, len(m.built))

	for _, e := range m.built {
		if e.Provider != nil && e.Provider.ResourceDiscovery != nil {
			out = append(out, e.Provider.ResourceDiscovery)
		}
	}

	return out
}

// Close tears down every built region's provider (freeing any wired real
// engines), so the mux itself is the closer the assembly layer tracks across a
// reset swap. Close is idempotent and a no-op for engineless providers.
func (m *RegionMux) Close() error {
	m.mu.Lock()

	providers := make([]*awsprovider.Provider, 0, len(m.built))
	for _, e := range m.built {
		providers = append(providers, e.Provider)
	}

	m.mu.Unlock()

	var firstErr error

	for _, p := range providers {
		if p == nil {
			continue
		}

		if err := p.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// AggregatingInventory fans ListAll/List out over every live region's engine, so
// Cost Explorer sums all regions and Resource Explorer's aggregator index spans
// them. It satisfies both services/cost.Inventory and
// resourceexplorer2.ResourceLister structurally.
type AggregatingInventory struct {
	engines func() []*resourcediscovery.Engine
}

// NewAggregatingInventory returns a cross-region inventory that queries every
// engine the supplied function reports at call time (so newly-created regions
// are included automatically). Assign it to Drivers.CostExplorer and
// Drivers.ResourceExplorerLister.
func NewAggregatingInventory(engines func() []*resourcediscovery.Engine) *AggregatingInventory {
	return &AggregatingInventory{engines: engines}
}

// ListAll concatenates every region's inventory (each engine lists only its own
// region's resources, so the union has no duplicates).
func (a *AggregatingInventory) ListAll(ctx context.Context) ([]resourcediscovery.Resource, error) {
	var all []resourcediscovery.Resource

	for _, e := range a.engines() {
		rs, err := e.ListAll(ctx)
		if err != nil {
			return nil, err
		}

		all = append(all, rs...)
	}

	return all, nil
}

// List concatenates every region's filtered inventory for the query.
//
//nolint:gocritic // q is by value to satisfy resourcediscovery.Engine.List and the ResourceLister interface.
func (a *AggregatingInventory) List(ctx context.Context, q resourcediscovery.Query) ([]resourcediscovery.Resource, error) {
	var all []resourcediscovery.Resource

	for _, e := range a.engines() {
		rs, err := e.List(ctx, q)
		if err != nil {
			return nil, err
		}

		all = append(all, rs...)
	}

	return all, nil
}
