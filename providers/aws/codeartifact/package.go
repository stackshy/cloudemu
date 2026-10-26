package codeartifact

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// assetRecord is one stored file of a package version. Content is shared between
// copies of a version and is never modified after it is stored, so it can be
// shared safely; reads hand out a copy. After a snapshot taken without assets
// Stored is false: the metadata survives, the bytes do not.
type assetRecord struct {
	Name    string            `json:"name"`
	Size    int64             `json:"size"`
	Hashes  map[string]string `json:"hashes"`
	Content []byte            `json:"content,omitempty"`
	Stored  bool              `json:"stored"`
}

// versionRecord is a stored package version.
type versionRecord struct {
	Version       string                  `json:"version"`
	Revision      string                  `json:"revision"`
	Status        string                  `json:"status"`
	PublishedTime time.Time               `json:"publishedTime"`
	Origin        driver.VersionOrigin    `json:"origin"`
	Assets        map[string]*assetRecord `json:"assets,omitempty"`
}

// packageRecord is a stored package with its versions. Seq is the last event
// sequence number issued for the package. ExplicitOrigin marks a package whose
// origin controls were set by PutPackageOriginConfiguration, which keeps the
// record alive when its last version is deleted. Records are read and written
// only under the Mock lock.
type packageRecord struct {
	Domain         string                    `json:"domain"`
	Repository     string                    `json:"repository"`
	Format         string                    `json:"format"`
	Namespace      string                    `json:"namespace"`
	Name           string                    `json:"name"`
	Restrictions   driver.OriginRestrictions `json:"restrictions"`
	ExplicitOrigin bool                      `json:"explicitOrigin,omitempty"`
	Seq            int64                     `json:"seq"`
	Versions       map[string]*versionRecord `json:"versions,omitempty"`
}

// packageKey is the store key of a package.
func packageKey(domain, repo, format, namespace, name string) string {
	return strings.Join([]string{domain, repo, format, namespace, name}, "/")
}

func refKey(ref *driver.PackageRef) string {
	return packageKey(ref.Domain, ref.Repository, ref.Format, ref.Namespace, ref.Package)
}

func (p *packageRecord) toDriver() *driver.Package {
	return &driver.Package{Format: p.Format, Namespace: p.Namespace, Name: p.Name, Restrictions: p.Restrictions}
}

// packageLocked resolves a package reference: the coordinates are validated and
// the repository and package must exist. The caller holds mu.
func (m *Mock) packageLocked(ref *driver.PackageRef) (driver.Repository, *packageRecord, error) {
	if err := validatePackageRef(ref); err != nil {
		return driver.Repository{}, nil, err
	}

	_, r, err := m.repositoryLocked(driver.RepositoryRef{Domain: ref.Domain, Owner: ref.Owner, Repository: ref.Repository})
	if err != nil {
		return driver.Repository{}, nil, err
	}

	p, ok := m.packages.Get(refKey(ref))
	if !ok {
		return driver.Repository{}, nil, notFound(driver.ResourceTypePackage, ref.Package,
			"Package %s not found in repository %s", ref.Package, ref.Repository)
	}

	return r, p, nil
}

// ListPackages returns a deterministic page of a repository's packages ordered by
// format, namespace and name.
func (m *Mock) ListPackages(_ context.Context, in *driver.ListPackagesInput) ([]*driver.Package, string, error) {
	if err := validatePackageFilters(in); err != nil {
		return nil, "", err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, _, err := m.repositoryLocked(in.Repository); err != nil {
		return nil, "", err
	}

	all := m.packages.SortedValues()
	filtered := make([]*driver.Package, 0, len(all))

	for _, p := range all {
		if p.Domain == in.Repository.Domain && p.Repository == in.Repository.Repository && packageMatchesFilters(p, in) {
			filtered = append(filtered, p.toDriver())
		}
	}

	sort.Slice(filtered, func(i, j int) bool { return lessPackage(filtered[i], filtered[j]) })

	return pageOf(filtered, in.Page)
}

// lessPackage orders packages by format, namespace and name.
func lessPackage(a, b *driver.Package) bool {
	if a.Format != b.Format {
		return a.Format < b.Format
	}

	if a.Namespace != b.Namespace {
		return a.Namespace < b.Namespace
	}

	return a.Name < b.Name
}

func validatePackageFilters(in *driver.ListPackagesInput) error {
	if in.Format != "" && !validFormat(in.Format) {
		return validation("package format %q is not one of npm, pypi, maven, nuget, generic, ruby, swift, cargo", in.Format)
	}

	for _, f := range []struct{ name, value string }{{"namespace filter", in.Namespace}, {"package prefix", in.Prefix}} {
		if f.value != "" && !validComponent(f.value) {
			return validation("%s %q is invalid", f.name, f.value)
		}
	}

	for _, v := range []string{in.Publish, in.Upstream} {
		if v != "" && v != driver.OriginAllow && v != driver.OriginBlock {
			return validation("origin filter %q must be ALLOW or BLOCK", v)
		}
	}

	return nil
}

func packageMatchesFilters(p *packageRecord, in *driver.ListPackagesInput) bool {
	return (in.Format == "" || p.Format == in.Format) &&
		strings.HasPrefix(p.Namespace, in.Namespace) &&
		strings.HasPrefix(p.Name, in.Prefix) &&
		(in.Publish == "" || p.Restrictions.Publish == in.Publish) &&
		(in.Upstream == "" || p.Restrictions.Upstream == in.Upstream)
}

// DescribePackage returns a package record.
//
//nolint:gocritic // ref is passed by value to implement driver.Packages.
func (m *Mock) DescribePackage(_ context.Context, ref driver.PackageRef) (*driver.Package, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, p, err := m.packageLocked(&ref)
	if err != nil {
		return nil, err
	}

	return p.toDriver(), nil
}

// PutPackageOriginConfiguration sets a package's origin controls. The package
// need not exist: an empty record is created to hold the restrictions.
//
//nolint:gocritic // ref is passed by value to implement driver.Packages.
func (m *Mock) PutPackageOriginConfiguration(_ context.Context, ref driver.PackageRef,
	restrictions driver.OriginRestrictions) (*driver.Package, error) {
	if err := validatePackageRef(&ref); err != nil {
		return nil, err
	}

	for _, v := range []string{restrictions.Publish, restrictions.Upstream} {
		if v != driver.OriginAllow && v != driver.OriginBlock {
			return nil, validation("origin restriction %q must be ALLOW or BLOCK", v)
		}
	}

	if ref.Namespace != "" && !formatHasNamespace(ref.Format) {
		return nil, validation("%s packages have no namespace", ref.Format)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, _, err := m.repositoryLocked(driver.RepositoryRef{Domain: ref.Domain, Owner: ref.Owner, Repository: ref.Repository}); err != nil {
		return nil, err
	}

	key := refKey(&ref)

	p, ok := m.packages.Get(key)
	if !ok {
		p = &packageRecord{
			Domain: ref.Domain, Repository: ref.Repository, Format: ref.Format, Namespace: ref.Namespace, Name: ref.Package,
			Versions: map[string]*versionRecord{},
		}
		m.packages.Set(key, p)
	}

	p.Restrictions = restrictions
	p.ExplicitOrigin = true

	return p.toDriver(), nil
}

// formatHasNamespace reports whether packages of a format can have a namespace.
// Python, NuGet, Ruby and Cargo packages have none.
func formatHasNamespace(format string) bool {
	switch format {
	case driver.FormatPyPI, driver.FormatNuGet, driver.FormatRuby, driver.FormatCargo:
		return false
	default:
		return true
	}
}

// DeletePackage deletes a package with all its versions and assets, publishing
// one Deleted event per version.
//
//nolint:gocritic // ref is passed by value to implement driver.Packages.
func (m *Mock) DeletePackage(ctx context.Context, ref driver.PackageRef) (*driver.Package, error) {
	out, events, err := m.deletePackage(&ref)
	m.publishVersionEvents(ctx, events)

	return out, err
}

func (m *Mock) deletePackage(ref *driver.PackageRef) (*driver.Package, []versionEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	repo, p, err := m.packageLocked(ref)
	if err != nil {
		return nil, nil, err
	}

	out := p.toDriver()

	events := make([]versionEvent, 0, len(p.Versions))

	for _, v := range sortedVersions(p) {
		events = append(events, m.newVersionEventLocked(&repo, p, v, operationDeleted, versionChanges{
			AssetsRemoved: len(v.Assets), StatusChanged: true,
		}))
	}

	m.packages.Delete(refKey(ref))

	return out, events, nil
}
