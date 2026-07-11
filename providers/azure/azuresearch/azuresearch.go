// Package azuresearch provides an in-memory mock of Azure AI Search
// (Microsoft.Search/searchServices) — the ARM control plane (service lifecycle,
// admin/query keys, private links) and the search data plane (indexes,
// documents, indexers, data sources, skillsets, synonym maps, aliases).
package azuresearch

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/azuresearch/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

// Compile-time check that Mock implements the full surface.
var _ driver.AzureSearch = (*Mock)(nil)

const (
	searchProvider = "Microsoft.Search"
	defaultSKU     = "standard"
)

// Mock is the in-memory Azure AI Search service.
type Mock struct {
	services     *memstore.Store[*driver.Service]
	adminKeys    *memstore.Store[*driver.AdminKeys]
	queryKeys    *memstore.Store[*driver.QueryKey]
	sharedLinks  *memstore.Store[*driver.SharedPrivateLink]
	privateConns *memstore.Store[*driver.PrivateEndpointConnection]

	// Data-plane stores (keyed by service[/index]/name).
	indexes     *memstore.Store[*driver.Index]
	documents   *memstore.Store[map[string]any]
	indexers    *memstore.Store[*driver.Indexer]
	indexerRuns *memstore.Store[*driver.IndexerStatus]
	dataSources *memstore.Store[*driver.DataSource]
	skillsets   *memstore.Store[*driver.Skillset]
	synonymMaps *memstore.Store[*driver.SynonymMap]
	aliases     *memstore.Store[*driver.Alias]

	seq        atomic.Int64
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new Azure AI Search mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		services:     memstore.New[*driver.Service](),
		adminKeys:    memstore.New[*driver.AdminKeys](),
		queryKeys:    memstore.New[*driver.QueryKey](),
		sharedLinks:  memstore.New[*driver.SharedPrivateLink](),
		privateConns: memstore.New[*driver.PrivateEndpointConnection](),
		indexes:      memstore.New[*driver.Index](),
		documents:    memstore.New[map[string]any](),
		indexers:     memstore.New[*driver.Indexer](),
		indexerRuns:  memstore.New[*driver.IndexerStatus](),
		dataSources:  memstore.New[*driver.DataSource](),
		skillsets:    memstore.New[*driver.Skillset](),
		synonymMaps:  memstore.New[*driver.SynonymMap](),
		aliases:      memstore.New[*driver.Alias](),
		opts:         opts,
	}
}

// SetMonitoring wires a monitoring backend for auto-metric emission.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) { m.monitoring = mon }

// --- shared helpers ---

func (m *Mock) now() string { return m.opts.Clock.Now().UTC().Format(time.RFC3339) }

func key(parts ...string) string { return strings.Join(parts, "/") }

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func (m *Mock) etag() string {
	return fmt.Sprintf(`"%016x"`, m.seq.Add(1))
}

func (m *Mock) emitMetric(metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: searchProvider, MetricName: metricName, Value: value,
		Unit: "Count", Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

func skuOrDefault(s string) string {
	if s == "" {
		return defaultSKU
	}

	return s
}

func hashHex(parts ...string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.Join(parts, ":")))
	h2 := fnv.New64a()
	_, _ = h2.Write([]byte(strings.Join(parts, ":") + "#k"))

	return fmt.Sprintf("%016x%016x", h.Sum64(), h2.Sum64())
}

// --- Services ---

func cloneService(s *driver.Service) *driver.Service {
	out := *s
	out.Tags = copyMap(s.Tags)

	return &out
}

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateService(_ context.Context, cfg driver.ServiceConfig) (*driver.Service, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "service name is required")
	case cfg.ResourceGroup == "":
		return nil, errors.New(errors.InvalidArgument, "resource group is required")
	case cfg.Location == "":
		return nil, errors.New(errors.InvalidArgument, "location is required")
	}

	k := key(cfg.ResourceGroup, cfg.Name)

	replicas := cfg.ReplicaCount
	if replicas < 1 {
		replicas = 1
	}

	partitions := cfg.PartitionCount
	if partitions < 1 {
		partitions = 1
	}

	if existing, ok := m.services.Get(k); ok {
		updated := *existing
		updated.SKUName = skuOrDefault(cfg.SKUName)
		updated.ReplicaCount = replicas
		updated.PartitionCount = partitions
		updated.Tags = copyMap(cfg.Tags)
		m.services.Set(k, &updated)

		return cloneService(&updated), nil
	}

	hosting := cfg.HostingMode
	if hosting == "" {
		hosting = "default"
	}

	svc := &driver.Service{
		ID:                idgen.AzureID(m.opts.AccountID, cfg.ResourceGroup, searchProvider, "searchServices", cfg.Name),
		Name:              cfg.Name,
		ResourceGroup:     cfg.ResourceGroup,
		Location:          cfg.Location,
		SKUName:           skuOrDefault(cfg.SKUName),
		ReplicaCount:      replicas,
		PartitionCount:    partitions,
		HostingMode:       hosting,
		Endpoint:          "https://" + cfg.Name + ".search.windows.net",
		Status:            driver.StatusRunning,
		ProvisioningState: driver.StateSucceeded,
		Tags:              copyMap(cfg.Tags),
		CreatedAt:         m.now(),
	}
	m.services.Set(k, svc)
	m.emitMetric("service/count", 1, map[string]string{"sku": svc.SKUName})

	return cloneService(svc), nil
}

func (m *Mock) GetService(_ context.Context, resourceGroup, name string) (*driver.Service, error) {
	s, ok := m.services.Get(key(resourceGroup, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "search service %q not found", name)
	}

	return cloneService(s), nil
}

func (m *Mock) DeleteService(_ context.Context, resourceGroup, name string) error {
	if !m.services.Delete(key(resourceGroup, name)) {
		return errors.Newf(errors.NotFound, "search service %q not found", name)
	}

	return nil
}

func (m *Mock) UpdateService(
	_ context.Context, resourceGroup, name string, replicas, partitions int, tags map[string]string,
) (*driver.Service, error) {
	k := key(resourceGroup, name)

	s, ok := m.services.Get(k)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "search service %q not found", name)
	}

	updated := *s
	if replicas > 0 {
		updated.ReplicaCount = replicas
	}

	if partitions > 0 {
		updated.PartitionCount = partitions
	}

	if tags != nil {
		updated.Tags = copyMap(tags)
	}

	m.services.Set(k, &updated)

	return cloneService(&updated), nil
}

func (m *Mock) ListServicesByResourceGroup(_ context.Context, resourceGroup string) ([]driver.Service, error) {
	out := make([]driver.Service, 0)

	for _, s := range m.services.All() {
		if s.ResourceGroup == resourceGroup {
			out = append(out, *cloneService(s))
		}
	}

	return out, nil
}

func (m *Mock) ListServices(_ context.Context) ([]driver.Service, error) {
	all := m.services.All()
	out := make([]driver.Service, 0, len(all))

	for _, s := range all {
		out = append(out, *cloneService(s))
	}

	return out, nil
}

func (m *Mock) requireService(resourceGroup, name string) error {
	if !m.services.Has(key(resourceGroup, name)) {
		return errors.Newf(errors.NotFound, "search service %q not found", name)
	}

	return nil
}
