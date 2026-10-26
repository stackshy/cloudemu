package codeartifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// validSHA256Hex reports whether s is a 64-character lowercase hex digest.
func validSHA256Hex(s string) bool {
	if len(s) != sha256HexLen {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

// validatePublish checks a PublishPackageVersion request before any state is read.
func validatePublish(in *driver.PublishInput) error {
	if err := validatePackageRef(&in.Ref); err != nil {
		return err
	}

	if err := validateVersion(in.Version); err != nil {
		return err
	}

	if !validAssetName(in.AssetName) {
		return validation("asset name %q is invalid", in.AssetName)
	}

	if !validSHA256Hex(in.SHA256) {
		return validation("x-amz-content-sha256 must be a 64-character lowercase hex digest")
	}

	if in.Ref.Format != driver.FormatGeneric {
		return validation("only generic packages can be published with PublishPackageVersion, not %s", in.Ref.Format)
	}

	if in.Ref.Namespace == "" {
		return validation("a generic package needs a namespace")
	}

	if len(in.Content) > driver.MaxAssetBytes {
		return quotaExceeded(driver.ResourceTypeAsset, in.AssetName,
			"Asset %s exceeds the emulator's %d-byte asset limit", in.AssetName, driver.MaxAssetBytes)
	}

	sum := sha256.Sum256(in.Content)
	if hex.EncodeToString(sum[:]) != in.SHA256 {
		return validation("the SHA-256 of the asset content does not match x-amz-content-sha256")
	}

	return nil
}

// PublishPackageVersion adds an asset to a generic package version, creating the
// package and version as needed. Without Unfinished the version ends Published; a
// Published version cannot take more assets. The same asset name and content on
// an Unfinished version is a no-op, different content is a ConflictException.
func (m *Mock) PublishPackageVersion(ctx context.Context, in *driver.PublishInput) (*driver.PublishResult, error) {
	res, events, err := m.publish(in)
	m.publishVersionEvents(ctx, events)

	return res, err
}

func (m *Mock) publish(in *driver.PublishInput) (*driver.PublishResult, []versionEvent, error) {
	if err := validatePublish(in); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, repo, err := m.repositoryLocked(driver.RepositoryRef{Domain: in.Ref.Domain, Owner: in.Ref.Owner, Repository: in.Ref.Repository})
	if err != nil {
		return nil, nil, err
	}

	key := refKey(&in.Ref)
	p, existed := m.packages.Get(key)

	if reason := m.publishBlockedLocked(in.Ref.Domain, in.Ref.Repository, in.Ref.Format, in.Ref.Namespace, in.Ref.Package, p); reason != "" {
		return nil, nil, conflict(driver.ResourceTypePackage, in.Ref.Package, "Publishing %s is not allowed: %s", in.Ref.Package, reason)
	}

	if existed && p.Restrictions.Upstream == driver.OriginAllow && m.versionInUpstreamsLocked(&repo, &in.Ref, in.Version) {
		return nil, nil, conflict(driver.ResourceTypePackageVersion, in.Version,
			"Version %s of %s is present in an upstream repository, so it cannot be published here", in.Version, in.Ref.Package)
	}

	if !existed {
		p = &packageRecord{
			Domain: in.Ref.Domain, Repository: in.Ref.Repository, Format: in.Ref.Format, Namespace: in.Ref.Namespace,
			Name: in.Ref.Package, Versions: map[string]*versionRecord{},
			Restrictions: m.defaultRestrictionsLocked(in.Ref.Domain, in.Ref.Format, in.Ref.Namespace, in.Ref.Package),
		}
	}

	v, ev, err := m.addAssetLocked(&repo, p, in)
	if err != nil {
		return nil, nil, err
	}

	m.packages.Set(key, p)

	res := &driver.PublishResult{
		VersionIdentity: v.identity(p),
		Status:          v.Status,
		Asset:           v.Assets[in.AssetName].toDriver(),
	}

	if ev == nil {
		return res, nil, nil
	}

	return res, []versionEvent{*ev}, nil
}

// versionInUpstreamsLocked reports whether a version of the package is held by a
// repository reachable through the repository's upstreams. The caller holds mu.
func (m *Mock) versionInUpstreamsLocked(repo *driver.Repository, ref *driver.PackageRef, version string) bool {
	for _, u := range repo.Upstreams {
		if v := m.findVersionLocked(repo.DomainName, u.RepositoryName, ref.Format, ref.Namespace, ref.Package, version, true); v != nil {
			return true
		}
	}

	return false
}

// checkAssetAddableLocked reports why an asset cannot be added to the existing
// version, and whether the request changes nothing (the same asset again on an
// Unfinished version).
func checkAssetAddableLocked(v *versionRecord, in *driver.PublishInput) (noop bool, err error) {
	if v.Status != driver.VersionStatusUnfinished {
		return false, conflict(driver.ResourceTypePackageVersion, in.Version,
			"Version %s of %s is %s and cannot take more assets", in.Version, in.Ref.Package, v.Status)
	}

	cur, ok := v.Assets[in.AssetName]
	if !ok {
		if len(v.Assets) >= maxAssetsPerVersion {
			return false, quotaExceeded(driver.ResourceTypeAsset, in.AssetName,
				"A package version can have at most %d assets", maxAssetsPerVersion)
		}

		return false, nil
	}

	if cur.Hashes[driver.HashSHA256] != in.SHA256 {
		return false, conflict(driver.ResourceTypeAsset, in.AssetName, "Asset %s already exists with different content", in.AssetName)
	}

	return in.Unfinished, nil
}

// addAssetLocked adds the published asset to the package's version, creating the
// version when needed, and builds the event. A nil event means the request
// changed nothing. The caller holds mu.
func (m *Mock) addAssetLocked(repo *driver.Repository, p *packageRecord, in *driver.PublishInput) (*versionRecord, *versionEvent, error) {
	v, exists := p.Versions[in.Version]
	operation := operationUpdated

	if exists {
		noop, err := checkAssetAddableLocked(v, in)
		if err != nil {
			return nil, nil, err
		}

		if noop {
			return v, nil, nil
		}
	} else {
		operation = operationCreated
		v = &versionRecord{
			Version: in.Version, Status: driver.VersionStatusUnfinished, Assets: map[string]*assetRecord{},
			Origin: driver.VersionOrigin{OriginType: driver.OriginTypeInternal, RepositoryName: repo.Name},
		}
		p.Versions[in.Version] = v
	}

	added := 0

	if _, replaced := v.Assets[in.AssetName]; !replaced {
		added = 1
		v.Assets[in.AssetName] = &assetRecord{
			Name: in.AssetName, Size: int64(len(in.Content)), Hashes: assetHashes(in.Content),
			Content: append([]byte(nil), in.Content...), Stored: true,
		}
	}

	statusChanged := !exists

	if !in.Unfinished && v.Status == driver.VersionStatusUnfinished {
		v.Status = driver.VersionStatusPublished
		v.PublishedTime = m.now()
		statusChanged = true
	}

	v.Revision = computeRevision(v.Assets)

	ev := m.newVersionEventLocked(repo, p, v, operation, versionChanges{AssetsAdded: added, StatusChanged: statusChanged})

	return v, &ev, nil
}
