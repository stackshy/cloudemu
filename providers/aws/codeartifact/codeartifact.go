// Package codeartifact provides an in-memory mock implementation of the AWS
// CodeArtifact API: domains, repositories (with upstream references and
// external connections), resource tagging and permissions policies, package
// records, package versions with real asset bytes, package origin controls and
// package groups.
//
// The mock serves the 48 management-API operations. It does NOT serve the
// package-manager registry protocols (npm, pip, Maven, ...), so the only
// packages that can be created through the API are generic ones, which is also
// what real AWS allows (PublishPackageVersion is generic-only). A domain and a
// repository are created synchronously in the Active state with stable computed
// fields minted once at create and stored, so repeated reads never drift.
//
// Concurrency: every state change that checks another entity (create with a
// cap or a unique name, delete with dependents, publish, copy, policy and group
// edits) runs under one Mock-level lock, and every read deep-copies what it
// returns. Events are collected under the lock and published after it is
// released, because delivery may call back into the mock.
package codeartifact

import (
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/awsevents"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// Compile-time checks that Mock implements the driver and its optional
// capabilities.
var (
	_ driver.CodeArtifact                  = (*Mock)(nil)
	_ driver.Access                        = (*Mock)(nil)
	_ driver.RepositoryAdministratorLister = (*Mock)(nil)
	_ driver.Policies                      = (*Mock)(nil)
	_ driver.Packages                      = (*Mock)(nil)
	_ driver.PackageVersions               = (*Mock)(nil)
	_ driver.PackageGroups                 = (*Mock)(nil)
)

// ARN resource kinds and the service marker.
const (
	kindDomain       = "domain"
	kindRepository   = "repository"
	kindPackageGroup = "package-group"
	serviceName      = "codeartifact"
)

// Mock is an in-memory implementation of the AWS CodeArtifact API.
//
// The stores are keyed as follows: domains by name; repositories by
// "<domain>/<repository>"; domain policies by domain; repository policies by
// "<domain>/<repository>"; packages by "<domain>/<repository>/<format>/
// <namespace>/<name>"; package groups by "<domain>|<pattern>". Records held by
// pointer (packages, groups) are read and written only under mu.
type Mock struct {
	mu             sync.RWMutex
	domains        *memstore.Store[driver.Domain]
	repos          *memstore.Store[driver.Repository]
	domainPolicies *memstore.Store[driver.ResourcePolicy]
	repoPolicies   *memstore.Store[driver.ResourcePolicy]
	packages       *memstore.Store[*packageRecord]
	groups         *memstore.Store[*groupRecord]
	opts           *config.Options
	events         awsevents.Emitter
}

// New creates a new CodeArtifact mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		domains:        memstore.New[driver.Domain](),
		repos:          memstore.New[driver.Repository](),
		domainPolicies: memstore.New[driver.ResourcePolicy](),
		repoPolicies:   memstore.New[driver.ResourcePolicy](),
		packages:       memstore.New[*packageRecord](),
		groups:         memstore.New[*groupRecord](),
		opts:           opts,
	}
}

// SetEventPublisher wires the EventBridge default bus that package version
// state changes are published to.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// domainARN mints the stable ARN reported for a domain.
func (m *Mock) domainARN(owner, name string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, owner, kindDomain+"/"+name)
}

// repositoryARN mints the stable ARN reported for a repository.
func (m *Mock) repositoryARN(owner, domain, repo string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, owner, kindRepository+"/"+domain+"/"+repo)
}

// groupARN mints the stable ARN reported for a package group. The pattern
// starts with a slash, so the resource reads package-group/<domain>/<format>/...
func (m *Mock) groupARN(owner, domain, pattern string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, owner, kindPackageGroup+"/"+domain+pattern)
}

// repoKey is the store key for a repository.
func repoKey(domain, repo string) string {
	return domain + "/" + repo
}

// domainLocked resolves a domain reference: the name and owner are validated,
// and a domain that does not exist under that owner is a ResourceNotFound. The
// caller holds mu.
func (m *Mock) domainLocked(ref driver.DomainRef) (driver.Domain, error) {
	if err := validateDomainRef(ref); err != nil {
		return driver.Domain{}, err
	}

	d, ok := m.domains.Get(ref.Domain)
	if !ok || (ref.Owner != "" && ref.Owner != d.Owner) {
		return driver.Domain{}, notFound(driver.ResourceTypeDomain, ref.Domain, "Domain %s not found", ref.Domain)
	}

	return d, nil
}

// repositoryLocked resolves a repository reference through its domain. The
// caller holds mu.
func (m *Mock) repositoryLocked(ref driver.RepositoryRef) (driver.Domain, driver.Repository, error) {
	if err := validateRepositoryRef(ref); err != nil {
		return driver.Domain{}, driver.Repository{}, err
	}

	d, err := m.domainLocked(driver.DomainRef{Domain: ref.Domain, Owner: ref.Owner})
	if err != nil {
		return driver.Domain{}, driver.Repository{}, err
	}

	r, ok := m.repos.Get(repoKey(ref.Domain, ref.Repository))
	if !ok {
		return driver.Domain{}, driver.Repository{}, notFound(driver.ResourceTypeRepository, ref.Repository,
			"Repository %s not found in domain %s", ref.Repository, ref.Domain)
	}

	return d, r, nil
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
