// Package opensearch provides an in-memory mock implementation of Amazon
// OpenSearch Service: domains that are provisioned immediately Active with an
// endpoint, domain configuration, tags, packages and their domain
// associations, VPC endpoints, cross-cluster inbound/outbound connections,
// per-domain and direct-query data sources, applications, reserved instances,
// upgrades, and read-only version/instance-type catalogs.
//
// Read-only catalog and diagnostic operations (ListVersions,
// DescribeInstanceTypeLimits, DescribeDomainHealth, DescribeDomainNodes,
// DescribeReservedInstanceOfferings, and the auto-tune/insight/maintenance
// listings) return plausible synthesized results, since real AWS derives them
// from data a local emulator has no source for. A few write-path capability
// helpers (AuthorizeVpcEndpointAccess, RegisterCapability, AttachDataSource)
// validate their inputs and echo a synthesized result without persisting any
// state, as the emulator models no VPC-access or capability inventory.
package opensearch

import (
	"encoding/json"
	"regexp"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// Compile-time check that Mock implements driver.OpenSearch.
var _ driver.OpenSearch = (*Mock)(nil)

const (
	minDomainNameLen = 3
	maxDomainNameLen = 28
	maxTags          = 50
	defaultEngine    = "OpenSearch_2.11"
	// defaultMaxResults caps a page when the caller requests none.
	defaultMaxResults = 100
)

// domainNameRe is the OpenSearch domain-name rule: start with a lowercase
// letter, then lowercase letters, digits, and hyphens.
var domainNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]+$`)

// domainData is the full server-side state of a domain plus its own lock.
type domainData struct {
	status   driver.DomainStatus
	config   driver.DomainConfig
	tags     map[string]string
	dataSrcs map[string]driver.DataSource
	mu       sync.RWMutex
}

// Mock is an in-memory implementation of Amazon OpenSearch Service.
type Mock struct {
	domains    *memstore.Store[*domainData]
	packages   *memstore.Store[*driver.Package]
	vpcEnds    *memstore.Store[*driver.VpcEndpoint]
	inbound    *memstore.Store[*driver.InboundConnection]
	outbound   *memstore.Store[*driver.OutboundConnection]
	apps       *memstore.Store[*driver.Application]
	dqDataSrcs *memstore.Store[*driver.DirectQueryDataSource]
	reserved   *memstore.Store[*driver.ReservedInstance]
	// pkgAssoc maps "packageID|domainName" to the association record.
	pkgAssoc *memstore.Store[*driver.DomainPackageAssociation]
	// pkgNames and appNames claim the (unique) package/application name to its
	// ID, so a duplicate name is rejected the way real OpenSearch does.
	pkgNames *memstore.Store[string]
	appNames *memstore.Store[string]

	defaultAppMu  sync.RWMutex
	defaultAppSet map[string]json.RawMessage

	opts *config.Options
}

// New creates a new OpenSearch mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		domains:       memstore.New[*domainData](),
		packages:      memstore.New[*driver.Package](),
		vpcEnds:       memstore.New[*driver.VpcEndpoint](),
		inbound:       memstore.New[*driver.InboundConnection](),
		outbound:      memstore.New[*driver.OutboundConnection](),
		apps:          memstore.New[*driver.Application](),
		dqDataSrcs:    memstore.New[*driver.DirectQueryDataSource](),
		reserved:      memstore.New[*driver.ReservedInstance](),
		pkgAssoc:      memstore.New[*driver.DomainPackageAssociation](),
		pkgNames:      memstore.New[string](),
		appNames:      memstore.New[string](),
		defaultAppSet: map[string]json.RawMessage{},
		opts:          opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

func (m *Mock) domainARN(name string) string {
	return idgen.AWSARN("es", m.opts.Region, m.opts.AccountID, "domain/"+name)
}

func (m *Mock) endpointFor(name string) string {
	id := idgen.GenerateID("")

	return "search-" + name + "-" + id + "." + m.opts.Region + ".es.amazonaws.com"
}

// getDomain resolves a domain by name, returning a ResourceNotFoundException
// when absent. The name format is validated first so a malformed name is a
// ValidationException, matching real OpenSearch.
func (m *Mock) getDomain(name string) (*domainData, error) {
	if err := validateDomainName(name); err != nil {
		return nil, err
	}

	dd, ok := m.domains.Get(name)
	if !ok {
		return nil, notFound("Domain not found: %s", name)
	}

	return dd, nil
}

// validateDomainName enforces OpenSearch's domain-name rules.
func validateDomainName(name string) error {
	if len(name) < minDomainNameLen || len(name) > maxDomainNameLen {
		return validation("Domain name %q must be between %d and %d characters", name, minDomainNameLen, maxDomainNameLen)
	}

	if !domainNameRe.MatchString(name) {
		return validation("Domain name %q must start with a lowercase letter and contain only lowercase letters, digits, and hyphens", name)
	}

	return nil
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}

func copyRaw(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}

	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}

	return out
}

func copyRawSlice(in []map[string]json.RawMessage) []map[string]json.RawMessage {
	if in == nil {
		return nil
	}

	out := make([]map[string]json.RawMessage, len(in))
	for i := range in {
		out[i] = copyRaw(in[i])
	}

	return out
}

// paginate returns the offset window and the next token for a slice of length
// n, honoring an opaque numeric offset token. A corrupt or out-of-range token
// is rejected with InvalidPaginationTokenException rather than silently reset to
// the first page, so a client that passes a bad token learns of the error.
func paginate(n int, page driver.Page) (start, end int, next string, err error) {
	start, err = decodeToken(page.NextToken)
	if err != nil {
		return 0, 0, "", err
	}

	if start > n {
		return 0, 0, "", invalidToken("Invalid pagination token: %q", page.NextToken)
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, "", nil
	}

	return start, end, encodeToken(end), nil
}

// listStore returns a deterministic, deep-copied, paginated page of a store's
// values. cp must return an alias-free copy of each stored element so callers
// cannot mutate server state through the result.
func listStore[V any](
	s *memstore.Store[*V], cp func(*V) V, page driver.Page,
) (items []V, nextToken string, err error) {
	vals := s.SortedValues()

	out := make([]V, 0, len(vals))
	for _, v := range vals {
		out = append(out, cp(v))
	}

	start, end, next, err := paginate(len(out), page)
	if err != nil {
		return nil, "", err
	}

	return out[start:end], next, nil
}
