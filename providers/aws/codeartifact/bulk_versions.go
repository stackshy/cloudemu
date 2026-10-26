package codeartifact

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// bulkOp applies one operation to one package version that passed the common
// checks. It returns the success entry, or a failure entry, and optionally an
// event.
type bulkOp func(repo *driver.Repository, p *packageRecord, v *versionRecord) (
	driver.SuccessfulVersion, *driver.FailedVersion, *versionEvent)

func validateVersionList(versions []string, revisions map[string]string, expectedStatus string) error {
	if len(versions) > maxBulkVersions || len(revisions) > maxBulkVersions {
		return validation("at most %d package versions can be named in one request", maxBulkVersions)
	}

	for _, v := range versions {
		if err := validateVersion(v); err != nil {
			return err
		}
	}

	if err := validateRevisionPins(revisions); err != nil {
		return err
	}

	if expectedStatus != "" && !validVersionStatus(expectedStatus) {
		return validation("expectedStatus %q is not a package version status", expectedStatus)
	}

	return nil
}

// validateRevisionPins checks the version -> revision map of a request.
func validateRevisionPins(revisions map[string]string) error {
	for v, rev := range revisions {
		if err := validateVersion(v); err != nil {
			return err
		}

		if rev == "" || len(rev) > revisionMaxLen || hasSpace(rev) {
			return validation("revision for version %s must be 1-%d characters without whitespace", v, revisionMaxLen)
		}
	}

	return nil
}

// precheckVersion applies the checks every bulk operation shares: the version
// exists, has the expected status and sits at the pinned revision. It returns
// the failure to report, or nil when the version may be operated on.
func precheckVersion(in *driver.VersionsInput, name string, v *versionRecord) *driver.FailedVersion {
	switch {
	case v == nil:
		return &driver.FailedVersion{ErrorCode: driver.VersionErrNotFound, ErrorMessage: "Version " + name + " not found"}
	case in.ExpectedStatus != "" && v.Status != in.ExpectedStatus:
		return &driver.FailedVersion{ErrorCode: driver.VersionErrMismatchedStatus, ErrorMessage: "Version " + name + " is " + v.Status}
	case pinnedRevisionMismatch(in.VersionRevisions, name, v):
		return &driver.FailedVersion{ErrorCode: driver.VersionErrMismatchedRev, ErrorMessage: "Version " + name + " is at revision " + v.Revision}
	default:
		return nil
	}
}

// runBulk applies op to each named version, collecting per-version outcomes.
// Missing versions, a status other than expectedStatus and a revision other than
// the pinned one are reported as failures without calling op. A package left with
// no versions is removed unless its origin controls were set explicitly.
func (m *Mock) runBulk(in *driver.VersionsInput, op bulkOp) (*driver.VersionsResult, []versionEvent, error) {
	if err := validatePackageRef(&in.Ref); err != nil {
		return nil, nil, err
	}

	if err := validateVersionList(in.Versions, in.VersionRevisions, in.ExpectedStatus); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	repo, p, err := m.packageLocked(&in.Ref)
	if err != nil {
		return nil, nil, err
	}

	res := &driver.VersionsResult{
		Successful: map[string]driver.SuccessfulVersion{}, Failed: map[string]driver.FailedVersion{},
	}
	events := make([]versionEvent, 0, len(in.Versions))

	for _, name := range uniqueStrings(in.Versions) {
		v := p.Versions[name]

		if fail := precheckVersion(in, name, v); fail != nil {
			res.Failed[name] = *fail

			continue
		}

		succ, fail, ev := op(&repo, p, v)
		if fail != nil {
			res.Failed[name] = *fail

			continue
		}

		res.Successful[name] = succ

		if ev != nil {
			events = append(events, *ev)
		}
	}

	if len(p.Versions) == 0 && !p.ExplicitOrigin {
		m.packages.Delete(refKey(&in.Ref))
	}

	return res, events, nil
}

// uniqueStrings drops repeats, keeping first-seen order, so a version named twice
// in one request is applied and reported once.
func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		if seen[s] {
			continue
		}

		seen[s] = true

		out = append(out, s)
	}

	return out
}

func pinnedRevisionMismatch(revisions map[string]string, name string, v *versionRecord) bool {
	rev, ok := revisions[name]

	return ok && rev != v.Revision
}

// statusUpdateFailure returns why a version cannot move to target, or nil.
func statusUpdateFailure(v *versionRecord, target string) *driver.FailedVersion {
	switch v.Status {
	case target:
		return &driver.FailedVersion{ErrorCode: driver.VersionErrSkipped, ErrorMessage: "Version " + v.Version + " is already " + v.Status}
	case driver.VersionStatusDisposed:
		return &driver.FailedVersion{
			ErrorCode: driver.VersionErrNotAllowed, ErrorMessage: "Version " + v.Version + " is disposed and cannot change status",
		}
	default:
		return nil
	}
}

// UpdatePackageVersionsStatus moves versions to Archived, Published or Unlisted.
// Disposed versions cannot be revived; a version already in the target status is
// skipped.
func (m *Mock) UpdatePackageVersionsStatus(ctx context.Context, in *driver.UpdateStatusInput) (*driver.VersionsResult, error) {
	switch in.TargetStatus {
	case driver.VersionStatusPublished, driver.VersionStatusUnlisted, driver.VersionStatusArchived:
	default:
		return nil, validation("targetStatus %q must be Published, Unlisted or Archived", in.TargetStatus)
	}

	res, events, err := m.runBulk(&in.VersionsInput, func(repo *driver.Repository, p *packageRecord, v *versionRecord) (
		driver.SuccessfulVersion, *driver.FailedVersion, *versionEvent) {
		if fail := statusUpdateFailure(v, in.TargetStatus); fail != nil {
			return driver.SuccessfulVersion{}, fail, nil
		}

		v.Status = in.TargetStatus
		if in.TargetStatus == driver.VersionStatusPublished && v.PublishedTime.IsZero() {
			v.PublishedTime = m.now()
		}

		ev := m.newVersionEventLocked(repo, p, v, operationUpdated, versionChanges{StatusChanged: true})

		return driver.SuccessfulVersion{Revision: v.Revision, Status: v.Status}, nil, &ev
	})

	m.publishVersionEvents(ctx, events)

	return res, err
}

// DisposePackageVersions deletes the assets of versions and sets them Disposed.
func (m *Mock) DisposePackageVersions(ctx context.Context, in *driver.VersionsInput) (*driver.VersionsResult, error) {
	res, events, err := m.runBulk(in, func(repo *driver.Repository, p *packageRecord, v *versionRecord) (
		driver.SuccessfulVersion, *driver.FailedVersion, *versionEvent) {
		if v.Status == driver.VersionStatusDisposed {
			return driver.SuccessfulVersion{}, &driver.FailedVersion{
				ErrorCode: driver.VersionErrSkipped, ErrorMessage: "Version " + v.Version + " is already disposed",
			}, nil
		}

		removed := len(v.Assets)
		v.Assets = map[string]*assetRecord{}
		v.Status = driver.VersionStatusDisposed
		v.Revision = computeRevision(v.Assets)

		ev := m.newVersionEventLocked(repo, p, v, operationUpdated, versionChanges{AssetsRemoved: removed, StatusChanged: true})

		return driver.SuccessfulVersion{Revision: v.Revision, Status: v.Status}, nil, &ev
	})

	m.publishVersionEvents(ctx, events)

	return res, err
}

// DeletePackageVersions removes versions and their assets for good.
func (m *Mock) DeletePackageVersions(ctx context.Context, in *driver.VersionsInput) (*driver.VersionsResult, error) {
	res, events, err := m.runBulk(in, func(repo *driver.Repository, p *packageRecord, v *versionRecord) (
		driver.SuccessfulVersion, *driver.FailedVersion, *versionEvent) {
		ev := m.newVersionEventLocked(repo, p, v, operationDeleted, versionChanges{AssetsRemoved: len(v.Assets), StatusChanged: true})

		delete(p.Versions, v.Version)

		return driver.SuccessfulVersion{Revision: v.Revision, Status: driver.VersionStatusDeleted}, nil, &ev
	})

	m.publishVersionEvents(ctx, events)

	return res, err
}
