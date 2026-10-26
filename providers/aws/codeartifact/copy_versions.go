package codeartifact

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// validateCopy checks a CopyPackageVersions request before any state is read.
func validateCopy(in *driver.CopyInput) error {
	src := in.Ref
	src.Repository = in.SourceRepository

	if err := validatePackageRef(&src); err != nil {
		return err
	}

	if err := validateRepositoryName(in.DestinationRepository); err != nil {
		return err
	}

	if in.SourceRepository == in.DestinationRepository {
		return validation("the source and destination repositories must differ")
	}

	hasVersions, hasRevisions := len(in.Versions) > 0, len(in.VersionRevisions) > 0
	if hasVersions == hasRevisions {
		return validation("specify versions or versionRevisions, not both and not neither")
	}

	return validateVersionList(in.Versions, in.VersionRevisions, "")
}

// copyTargets returns the versions a copy names, in a deterministic order.
func copyTargets(in *driver.CopyInput) []string {
	if len(in.Versions) > 0 {
		return in.Versions
	}

	out := make([]string, 0, len(in.VersionRevisions))
	for v := range in.VersionRevisions {
		out = append(out, v)
	}

	sort.Strings(out)

	return out
}

// CopyPackageVersions copies versions between two repositories of one domain.
// With IncludeFromUpstream the source's upstream repositories are searched too.
// A version already in the destination is an ALREADY_EXISTS failure unless
// AllowOverwrite is set.
func (m *Mock) CopyPackageVersions(ctx context.Context, in *driver.CopyInput) (*driver.VersionsResult, error) {
	res, events, err := m.copyVersions(in)
	m.publishVersionEvents(ctx, events)

	return res, err
}

func (m *Mock) copyVersions(in *driver.CopyInput) (*driver.VersionsResult, []versionEvent, error) {
	if err := validateCopy(in); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ref := in.Ref
	owner := ref.Owner

	if _, _, err := m.repositoryLocked(driver.RepositoryRef{Domain: ref.Domain, Owner: owner, Repository: in.SourceRepository}); err != nil {
		return nil, nil, err
	}

	_, destRepo, err := m.repositoryLocked(driver.RepositoryRef{Domain: ref.Domain, Owner: owner, Repository: in.DestinationRepository})
	if err != nil {
		return nil, nil, err
	}

	res := &driver.VersionsResult{Successful: map[string]driver.SuccessfulVersion{}, Failed: map[string]driver.FailedVersion{}}

	targets := uniqueStrings(copyTargets(in))
	events := make([]versionEvent, 0, len(targets))

	for _, name := range targets {
		src := m.findVersionLocked(ref.Domain, in.SourceRepository, ref.Format, ref.Namespace, ref.Package, name, in.IncludeFromUpstream)

		if fail := copyPrecheck(in, name, src); fail != nil {
			res.Failed[name] = *fail

			continue
		}

		dp, existed := m.packages.Get(packageKey(ref.Domain, in.DestinationRepository, ref.Format, ref.Namespace, ref.Package))
		if !existed {
			dp = &packageRecord{
				Domain: ref.Domain, Repository: in.DestinationRepository, Format: ref.Format, Namespace: ref.Namespace,
				Name: ref.Package, Versions: map[string]*versionRecord{},
				Restrictions: m.defaultRestrictionsLocked(ref.Domain, ref.Format, ref.Namespace, ref.Package),
			}
			m.packages.Set(packageKey(ref.Domain, in.DestinationRepository, ref.Format, ref.Namespace, ref.Package), dp)
		}

		if _, dup := dp.Versions[name]; dup && !in.AllowOverwrite {
			res.Failed[name] = driver.FailedVersion{
				ErrorCode:    driver.VersionErrAlreadyExists,
				ErrorMessage: "Version " + name + " already exists in repository " + in.DestinationRepository,
			}

			continue
		}

		operation := operationCreated
		if _, dup := dp.Versions[name]; dup {
			operation = operationUpdated
		}

		copied := cloneVersion(src, in.DestinationRepository)
		dp.Versions[name] = copied
		res.Successful[name] = driver.SuccessfulVersion{Revision: copied.Revision, Status: copied.Status}

		events = append(events, m.newVersionEventLocked(&destRepo, dp, copied, operation,
			versionChanges{AssetsAdded: len(copied.Assets), StatusChanged: true}))
	}

	return res, events, nil
}

// copyPrecheck returns the failure for a version that cannot be copied, or nil.
func copyPrecheck(in *driver.CopyInput, name string, src *versionRecord) *driver.FailedVersion {
	switch {
	case src == nil:
		return &driver.FailedVersion{ErrorCode: driver.VersionErrNotFound, ErrorMessage: "Version " + name + " not found"}
	case pinnedRevisionMismatch(in.VersionRevisions, name, src):
		return &driver.FailedVersion{
			ErrorCode: driver.VersionErrMismatchedRev, ErrorMessage: "Version " + name + " is at revision " + src.Revision,
		}
	case src.Status == driver.VersionStatusDisposed:
		return &driver.FailedVersion{
			ErrorCode: driver.VersionErrNotAllowed, ErrorMessage: "Version " + name + " is disposed and has no assets to copy",
		}
	default:
		return nil
	}
}

// cloneVersion copies a version into a repository. Asset bytes are shared: they
// are never modified once stored.
func cloneVersion(src *versionRecord, repo string) *versionRecord {
	out := *src
	out.Origin = driver.VersionOrigin{OriginType: driver.OriginTypeInternal, RepositoryName: repo}
	out.Assets = make(map[string]*assetRecord, len(src.Assets))

	for n, a := range src.Assets {
		c := *a
		c.Hashes = copyTags(a.Hashes)
		out.Assets[n] = &c
	}

	return &out
}
