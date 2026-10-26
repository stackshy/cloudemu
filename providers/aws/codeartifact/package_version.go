package codeartifact

import (
	"context"
	"crypto/md5"  //nolint:gosec // MD5 is one of the four asset hashes CodeArtifact reports, not a security control.
	"crypto/sha1" //nolint:gosec // SHA-1 is one of the four asset hashes CodeArtifact reports, not a security control.
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

const revisionLen = 32

// computeRevision derives a version's revision from its assets: it is stable
// across reads and changes whenever an asset is added, removed or replaced.
func computeRevision(assets map[string]*assetRecord) string {
	names := make([]string, 0, len(assets))
	for n := range assets {
		names = append(names, n)
	}

	sort.Strings(names)

	h := sha256.New()

	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write([]byte(assets[n].Hashes[driver.HashSHA256]))
		h.Write([]byte{'\n'})
	}

	return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))[:revisionLen]
}

// assetHashes computes the four hashes CodeArtifact reports for an asset.
func assetHashes(content []byte) map[string]string {
	md5sum := md5.Sum(content)   //nolint:gosec // see import
	sha1sum := sha1.Sum(content) //nolint:gosec // see import
	sha256sum := sha256.Sum256(content)
	sha512sum := sha512.Sum512(content)

	return map[string]string{
		driver.HashMD5:    hex.EncodeToString(md5sum[:]),
		driver.HashSHA1:   hex.EncodeToString(sha1sum[:]),
		driver.HashSHA256: hex.EncodeToString(sha256sum[:]),
		driver.HashSHA512: hex.EncodeToString(sha512sum[:]),
	}
}

func (a *assetRecord) toDriver() driver.Asset {
	return driver.Asset{Name: a.Name, Size: a.Size, Hashes: copyTags(a.Hashes)}
}

func (v *versionRecord) identity(p *packageRecord) driver.VersionIdentity {
	return driver.VersionIdentity{Format: p.Format, Namespace: p.Namespace, Package: p.Name, Version: v.Version, Revision: v.Revision}
}

// sortedAssets returns a version's assets ordered by name.
func sortedAssets(v *versionRecord) []*assetRecord {
	out := make([]*assetRecord, 0, len(v.Assets))
	for _, a := range v.Assets {
		out = append(out, a)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// domainAssetBytesLocked sums the size of the distinct assets stored in a domain:
// an asset is stored once per domain however many versions and repositories
// hold it, so identical SHA-256 digests count once. The caller holds mu.
func (m *Mock) domainAssetBytesLocked(domain string) int64 {
	seen := map[string]bool{}

	var total int64

	for _, p := range m.packages.Filter(func(k string, _ *packageRecord) bool { return strings.HasPrefix(k, domain+"/") }) {
		for _, v := range p.Versions {
			for _, a := range v.Assets {
				if sum := a.Hashes[driver.HashSHA256]; !seen[sum] {
					seen[sum] = true
					total += a.Size
				}
			}
		}
	}

	return total
}

// versionLocked resolves a package version. The caller holds mu.
func (m *Mock) versionLocked(ref *driver.PackageRef, version string) (*packageRecord, *versionRecord, error) {
	if err := validateVersion(version); err != nil {
		return nil, nil, err
	}

	_, p, err := m.packageLocked(ref)
	if err != nil {
		return nil, nil, err
	}

	v, ok := p.Versions[version]
	if !ok {
		return nil, nil, notFound(driver.ResourceTypePackageVersion, version,
			"Version %s of package %s not found", version, ref.Package)
	}

	return p, v, nil
}

func validVersionStatus(s string) bool {
	switch s {
	case driver.VersionStatusPublished, driver.VersionStatusUnfinished, driver.VersionStatusUnlisted,
		driver.VersionStatusArchived, driver.VersionStatusDisposed, driver.VersionStatusDeleted:
		return true
	default:
		return false
	}
}

// ListPackageVersions returns a page of a package's versions, newest published
// first, optionally filtered by status and origin type.
func (m *Mock) ListPackageVersions(_ context.Context, in *driver.ListPackageVersionsInput) (*driver.PackageVersionList, error) {
	if err := validateVersionFilters(in); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	_, p, err := m.packageLocked(&in.Ref)
	if err != nil {
		return nil, err
	}

	all := sortedVersions(p)
	sort.SliceStable(all, func(i, j int) bool { return all[i].PublishedTime.After(all[j].PublishedTime) })

	out := &driver.PackageVersionList{
		Format: p.Format, Namespace: p.Namespace, Package: p.Name, DefaultDisplayVersion: defaultDisplayVersion(all),
	}

	filtered := make([]driver.PackageVersionSummary, 0, len(all))

	for _, v := range all {
		if (in.Status == "" || v.Status == in.Status) && (in.OriginType == "" || v.Origin.OriginType == in.OriginType) {
			filtered = append(filtered, driver.PackageVersionSummary{
				Version: v.Version, Revision: v.Revision, Status: v.Status, Origin: v.Origin,
			})
		}
	}

	out.Versions, out.NextToken, err = pageOf(filtered, in.Page)
	if err != nil {
		return nil, err
	}

	return out, nil
}

// defaultDisplayVersion is the newest Published version of a list sorted newest
// first, or "" when none is Published.
func defaultDisplayVersion(sorted []*versionRecord) string {
	for _, v := range sorted {
		if v.Status == driver.VersionStatusPublished {
			return v.Version
		}
	}

	return ""
}

func validateVersionFilters(in *driver.ListPackageVersionsInput) error {
	if in.Status != "" && !validVersionStatus(in.Status) {
		return validation("status %q is not a package version status", in.Status)
	}

	if in.SortBy != "" && in.SortBy != driver.SortByPublishedTime {
		return validation("sortBy %q must be %s", in.SortBy, driver.SortByPublishedTime)
	}

	switch in.OriginType {
	case "", driver.OriginTypeInternal, driver.OriginTypeExternal, driver.OriginTypeUnknown:
		return nil
	default:
		return validation("originType %q must be INTERNAL, EXTERNAL or UNKNOWN", in.OriginType)
	}
}

// DescribePackageVersion returns the description of one package version.
//
//nolint:gocritic // ref is passed by value to implement driver.PackageVersions.
func (m *Mock) DescribePackageVersion(_ context.Context, ref driver.PackageRef,
	version string) (*driver.PackageVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, v, err := m.versionLocked(&ref, version)
	if err != nil {
		return nil, err
	}

	return &driver.PackageVersion{
		Format: p.Format, Namespace: p.Namespace, PackageName: p.Name, DisplayName: p.Name, Version: v.Version,
		Revision: v.Revision, Status: v.Status, PublishedTime: v.PublishedTime, Origin: v.Origin,
	}, nil
}

// ListPackageVersionAssets returns a page of a version's assets ordered by name.
//
//nolint:gocritic // ref is passed by value to implement driver.PackageVersions.
func (m *Mock) ListPackageVersionAssets(_ context.Context, ref driver.PackageRef, version string,
	page driver.Page) (*driver.AssetList, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, v, err := m.versionLocked(&ref, version)
	if err != nil {
		return nil, "", err
	}

	assets := sortedAssets(v)
	out := &driver.AssetList{VersionIdentity: v.identity(p)}

	window, next, err := pageOf(assets, page)
	if err != nil {
		return nil, "", err
	}

	out.Assets = make([]driver.Asset, 0, len(window))
	for _, a := range window {
		out.Assets = append(out.Assets, a.toDriver())
	}

	return out, next, nil
}

// GetPackageVersionAsset returns an asset's bytes. An archived version cannot be
// downloaded (ConflictException); a disposed version or unknown asset is a
// ResourceNotFoundException, as is a revision other than the current one.
//
//nolint:gocritic // ref is passed by value to implement driver.PackageVersions.
func (m *Mock) GetPackageVersionAsset(_ context.Context, ref driver.PackageRef,
	version, asset, revision string) (*driver.AssetContent, error) {
	if err := validateAssetRequest(asset, revision); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	_, v, err := m.versionLocked(&ref, version)
	if err != nil {
		return nil, err
	}

	if revision != "" && revision != v.Revision {
		return nil, notFound(driver.ResourceTypePackageVersion, version, "Version %s has no revision %s", version, revision)
	}

	if v.Status == driver.VersionStatusArchived {
		return nil, conflict(driver.ResourceTypePackageVersion, version, "Version %s is archived and cannot be downloaded", version)
	}

	a, ok := v.Assets[asset]
	if !ok || !a.Stored {
		return nil, notFound(driver.ResourceTypeAsset, asset, "Asset %s not found in version %s", asset, version)
	}

	return &driver.AssetContent{
		Asset: a.toDriver(), Content: append([]byte(nil), a.Content...), Version: v.Version, Revision: v.Revision,
	}, nil
}

func validateAssetRequest(asset, revision string) error {
	if !validAssetName(asset) {
		return validation("asset name %q is invalid", asset)
	}

	if revision != "" && (len(revision) > revisionMaxLen || hasSpace(revision)) {
		return validation("revision must be 1-%d characters without whitespace", revisionMaxLen)
	}

	return nil
}

// revisionMaxLen is the longest revision string the API accepts.
const revisionMaxLen = 50

// GetPackageVersionReadme returns the identity of a version with no readme: a
// generic package version carries no readme metadata.
//
//nolint:gocritic // ref is passed by value to implement driver.PackageVersions.
func (m *Mock) GetPackageVersionReadme(_ context.Context, ref driver.PackageRef,
	version string) (*driver.VersionIdentity, error) {
	return m.versionIdentity(&ref, version)
}

// ListPackageVersionDependencies returns the identity of a version with no
// dependencies: a generic package version carries no dependency metadata.
//
//nolint:gocritic // ref is passed by value to implement driver.PackageVersions.
func (m *Mock) ListPackageVersionDependencies(_ context.Context, ref driver.PackageRef,
	version string) (*driver.VersionIdentity, error) {
	return m.versionIdentity(&ref, version)
}

func (m *Mock) versionIdentity(ref *driver.PackageRef, version string) (*driver.VersionIdentity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, v, err := m.versionLocked(ref, version)
	if err != nil {
		return nil, err
	}

	id := v.identity(p)

	return &id, nil
}
