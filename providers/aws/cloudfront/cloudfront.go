// Package cloudfront provides an in-memory mock of the AWS CloudFront
// distribution control plane. It backs the REST/XML wire handler and speaks the
// services/cloudfront driver interface: distributions, their ETag-based
// optimistic concurrency, synchronous Deployed status, tags, and synchronous
// invalidations. No edge/CDN data plane is emulated.
package cloudfront

import (
	"context"
	"crypto/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

// Compile-time check that Mock implements the CloudFront driver.
var _ driver.CloudFront = (*Mock)(nil)

const (
	// distributionIDLen is the number of random characters after the "E" prefix
	// in a 14-character CloudFront distribution id.
	distributionIDLen = 13
	// domainNameLen is the number of random characters after the "d" prefix in a
	// CloudFront <domain>.cloudfront.net hostname.
	domainNameLen = 13
	// etagLen is the length of an opaque ETag token.
	etagLen = 14
)

// idAlphabet is the uppercase alphanumeric set CloudFront distribution ids and
// ETags draw from.
const idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// domainAlphabet is the lowercase alphanumeric set CloudFront domain-name
// prefixes draw from.
const domainAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// arnResourcePrefix is the resource portion of a distribution ARN, before the id.
const arnResourcePrefix = "distribution/"

// Mock is an in-memory CloudFront distribution control plane.
type Mock struct {
	opts *config.Options

	// mu guards the check-then-act sequences (CallerReference dedup on create,
	// ETag validation on update/delete) that span more than one store call.
	mu    sync.Mutex
	dists *memstore.Store[driver.Distribution]

	// invMu guards the per-distribution invalidation maps.
	invMu sync.Mutex
	// invalidations maps a distribution id to its invalidations by id.
	invalidations map[string]map[string]driver.Invalidation

	// seq is the monotonic creation counter that orders ListDistributions.
	seqMu sync.Mutex
	seq   int64
}

// New creates a CloudFront mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		opts:          opts,
		dists:         memstore.New[driver.Distribution](),
		invalidations: map[string]map[string]driver.Invalidation{},
	}
}

func (m *Mock) now() time.Time {
	if m.opts != nil && m.opts.Clock != nil {
		return m.opts.Clock.Now()
	}

	return time.Now()
}

func (m *Mock) accountID() string {
	if m.opts != nil && m.opts.AccountID != "" {
		return m.opts.AccountID
	}

	return "123456789012"
}

func (m *Mock) nextSeq() int64 {
	m.seqMu.Lock()
	defer m.seqMu.Unlock()

	m.seq++

	return m.seq
}

// randToken returns a length-char string drawn from alphabet, falling back to
// the monotonic id generator if crypto/rand is unavailable.
func randToken(alphabet string, length int) string {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return strings.ToUpper(idgen.GenerateID(""))
	}

	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}

	return string(buf)
}

func newDistributionID() string { return "E" + randToken(idAlphabet, distributionIDLen) }
func newDomainName() string {
	return "d" + randToken(domainAlphabet, domainNameLen) + ".cloudfront.net"
}
func newETag() string { return randToken(idAlphabet, etagLen) }

// arnFor builds a region-less CloudFront distribution ARN.
func (m *Mock) arnFor(id string) string {
	return idgen.AWSARN("cloudfront", "", m.accountID(), arnResourcePrefix+id)
}

// idFromARN extracts a distribution id from its ARN, or "" if it is not a
// distribution ARN.
func idFromARN(arn string) string {
	i := strings.LastIndex(arn, arnResourcePrefix)
	if i < 0 {
		return ""
	}

	return arn[i+len(arnResourcePrefix):]
}

// CreateDistribution stores a new distribution. A CallerReference already used
// by an existing distribution is rejected with ErrDistributionAlreadyExists.
func (m *Mock) CreateDistribution(_ context.Context, in *driver.CreateDistributionInput) (*driver.Distribution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing := m.dists.All()
	for k := range existing {
		if existing[k].CallerReference == in.CallerReference {
			return nil, driver.ErrDistributionAlreadyExists
		}
	}

	id := newDistributionID()
	now := m.now()

	dist := driver.Distribution{
		ID:               id,
		ARN:              m.arnFor(id),
		Status:           driver.StatusDeployed,
		DomainName:       newDomainName(),
		ETag:             newETag(),
		LastModifiedTime: now,
		CallerReference:  in.CallerReference,
		Enabled:          in.Enabled,
		Comment:          in.Comment,
		ConfigXML:        cloneBytes(in.ConfigXML),
		Tags:             cloneTags(in.Tags),
		Seq:              m.nextSeq(),
	}

	m.dists.Set(id, dist)

	return distPtr(&dist), nil
}

// GetDistribution returns a distribution by id.
func (m *Mock) GetDistribution(_ context.Context, id string) (*driver.Distribution, error) {
	dist, ok := m.dists.Get(id)
	if !ok {
		return nil, driver.ErrNoSuchDistribution
	}

	return distPtr(&dist), nil
}

// UpdateDistribution replaces a distribution's config. It requires a matching
// If-Match ETag, forbids changing the CallerReference, and rotates the ETag.
func (m *Mock) UpdateDistribution(_ context.Context, in *driver.UpdateDistributionInput) (*driver.Distribution, error) {
	if strings.TrimSpace(in.IfMatch) == "" {
		return nil, driver.ErrInvalidIfMatchVersion
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dist, ok := m.dists.Get(in.ID)
	if !ok {
		return nil, driver.ErrNoSuchDistribution
	}

	if in.IfMatch != dist.ETag {
		return nil, driver.ErrPreconditionFailed
	}

	if in.CallerReference != dist.CallerReference {
		return nil, driver.ErrCallerReferenceImmutable
	}

	dist.Enabled = in.Enabled
	dist.Comment = in.Comment
	dist.ConfigXML = cloneBytes(in.ConfigXML)
	dist.ETag = newETag()
	dist.LastModifiedTime = m.now()
	dist.Status = driver.StatusDeployed

	m.dists.Set(in.ID, dist)

	return distPtr(&dist), nil
}

// DeleteDistribution removes a distribution. It requires a matching If-Match
// ETag and that the distribution be disabled first.
func (m *Mock) DeleteDistribution(_ context.Context, id, ifMatch string) error {
	if strings.TrimSpace(ifMatch) == "" {
		return driver.ErrInvalidIfMatchVersion
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dist, ok := m.dists.Get(id)
	if !ok {
		return driver.ErrNoSuchDistribution
	}

	if ifMatch != dist.ETag {
		return driver.ErrPreconditionFailed
	}

	if dist.Enabled {
		return driver.ErrDistributionNotDisabled
	}

	m.dists.Delete(id)

	m.invMu.Lock()
	delete(m.invalidations, id)
	m.invMu.Unlock()

	return nil
}

// ListDistributions returns every distribution ordered by creation.
func (m *Mock) ListDistributions(_ context.Context) ([]driver.Distribution, error) {
	all := m.dists.All()
	out := make([]driver.Distribution, 0, len(all))

	for k := range all {
		d := all[k]
		out = append(out, *distPtr(&d))
	}

	sortBySeq(out)

	return out, nil
}

// distPtr returns a deep copy of dist as a pointer, so callers cannot mutate the
// stored config bytes or tag map.
func distPtr(dist *driver.Distribution) *driver.Distribution {
	cp := *dist
	cp.ConfigXML = cloneBytes(dist.ConfigXML)
	cp.Tags = cloneTags(dist.Tags)

	return &cp
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}

	out := make([]byte, len(b))
	copy(out, b)

	return out
}

func cloneTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// sortBySeq orders distributions by their creation sequence, ascending.
func sortBySeq(d []driver.Distribution) {
	sort.Slice(d, func(i, j int) bool { return d[i].Seq < d[j].Seq })
}
