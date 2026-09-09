// Package codeartifact provides an in-memory mock implementation of the AWS
// CodeArtifact control plane: domains, repositories (with upstream references
// and external connections), and resource tagging.
//
// The mock is control-plane only — it does NOT run a package data plane (no
// package publish, version resolution or asset storage). A domain and a
// repository are created synchronously in the Active state with stable computed
// fields (the domain arn/owner/createdTime, the repository
// arn/administratorAccount/createdTime) minted once at create and stored, so
// repeated reads never drift. The domain repositoryCount is derived live from
// the child repositories and assetSizeBytes is always 0.
package codeartifact

import (
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// Compile-time check that Mock implements driver.CodeArtifact.
var _ driver.CodeArtifact = (*Mock)(nil)

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 100

// ARN resource kinds and the service marker.
const (
	kindDomain     = "domain"
	kindRepository = "repository"
	serviceName    = "codeartifact"
)

// Mock is an in-memory implementation of the AWS CodeArtifact control plane.
// Domains are keyed by name; repositories are keyed by "<domain>/<repository>".
type Mock struct {
	domains *memstore.Store[driver.Domain]
	repos   *memstore.Store[driver.Repository]
	opts    *config.Options
}

// New creates a new CodeArtifact mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		domains: memstore.New[driver.Domain](),
		repos:   memstore.New[driver.Repository](),
		opts:    opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// resolveOwner returns the effective domain owner: the caller-supplied owner
// when present, otherwise the emulator's account id.
func (m *Mock) resolveOwner(domainOwner string) string {
	if domainOwner == "" {
		return m.opts.AccountID
	}

	return domainOwner
}

// domainARN mints the stable ARN reported for a domain.
func (m *Mock) domainARN(owner, name string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, owner, kindDomain+"/"+name)
}

// repositoryARN mints the stable ARN reported for a repository.
func (m *Mock) repositoryARN(owner, domain, repo string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, owner, kindRepository+"/"+domain+"/"+repo)
}

// repoKey is the store key for a repository.
func repoKey(domain, repo string) string {
	return domain + "/" + repo
}

// resourceRefFromARN extracts the (kind, key) pair from a CodeArtifact resource
// ARN. A domain ARN of the form arn:aws:codeartifact:{region}:{acct}:domain/{name}
// yields (domain, name); a repository ARN of the form
// arn:aws:codeartifact:{region}:{acct}:repository/{domain}/{repo} yields
// (repository, "{domain}/{repo}").
func resourceRefFromARN(arn string) (kind, key string) {
	const arnParts = 6

	if !strings.Contains(arn, ":"+serviceName+":") {
		return "", ""
	}

	parts := strings.SplitN(arn, ":", arnParts)
	if len(parts) < arnParts {
		return "", ""
	}

	resource := parts[arnParts-1]

	slash := strings.IndexByte(resource, '/')
	if slash < 0 {
		return "", ""
	}

	return resource[:slash], resource[slash+1:]
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

func copyUpstreams(in []driver.UpstreamRef) []driver.UpstreamRef {
	if in == nil {
		return nil
	}

	return append([]driver.UpstreamRef(nil), in...)
}

func copyExternalConns(in []driver.ExternalConnection) []driver.ExternalConnection {
	if in == nil {
		return nil
	}

	return append([]driver.ExternalConnection(nil), in...)
}

// copyDomain returns an alias-free copy of a domain so callers cannot mutate
// stored state through the result.
func copyDomain(d *driver.Domain) driver.Domain {
	out := *d
	out.Tags = copyTags(d.Tags)

	return out
}

// copyRepository returns an alias-free copy of a repository.
func copyRepository(r *driver.Repository) driver.Repository {
	out := *r
	out.Tags = copyTags(r.Tags)
	out.Upstreams = copyUpstreams(r.Upstreams)
	out.ExternalConnections = copyExternalConns(r.ExternalConnections)

	return out
}

// repositoryCount returns the number of repositories that belong to a domain.
func (m *Mock) repositoryCount(domain string) int64 {
	var n int64

	repos := m.repos.SortedValues()
	for i := range repos {
		if repos[i].DomainName == domain {
			n++
		}
	}

	return n
}

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}
