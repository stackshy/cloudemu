package identity

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// The portable policy-version operations map onto revisions of a policy's
// statements: OCI stamps a policy with a versionDate rather than keeping
// addressable documents, so each statement change is recorded as a revision.

// CreatePolicyVersion records a new revision of a policy's statements.
func (m *Mock) CreatePolicyVersion(
	_ context.Context, cfg driver.PolicyVersionConfig,
) (*driver.PolicyVersionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.policies.Get(cfg.PolicyARN)
	if !ok {
		return nil, policyNotFound(cfg.PolicyARN)
	}

	statements := splitStatements(cfg.PolicyDocument)

	parsed, err := parseStatements(statements)
	if err != nil {
		return nil, err
	}

	now := m.now()
	rev := p.addRevision(statements, now, cfg.SetAsDefault)

	if cfg.SetAsDefault {
		p.Statements = statements
		p.parsed = parsed
		p.VersionDate = now
	}

	m.policies.Set(cfg.PolicyARN, p)

	return toPolicyVersionInfo(rev), nil
}

// GetPolicyVersion returns one revision of a policy's statements.
func (m *Mock) GetPolicyVersion(_ context.Context, policyARN, versionID string) (*driver.PolicyVersionInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.policies.Get(policyARN)
	if !ok {
		return nil, policyNotFound(policyARN)
	}

	rev, found := findRevision(p.versions, versionID)
	if !found {
		return nil, versionNotFound(versionID, policyARN)
	}

	return toPolicyVersionInfo(rev), nil
}

// ListPolicyVersions returns every recorded revision of a policy.
func (m *Mock) ListPolicyVersions(_ context.Context, policyARN string) ([]driver.PolicyVersionInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.policies.Get(policyARN)
	if !ok {
		return nil, policyNotFound(policyARN)
	}

	out := make([]driver.PolicyVersionInfo, 0, len(p.versions))
	for _, rev := range p.versions {
		out = append(out, *toPolicyVersionInfo(rev))
	}

	return out, nil
}

// DeletePolicyVersion removes a revision that is not the current one.
func (m *Mock) DeletePolicyVersion(_ context.Context, policyARN, versionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.policies.Get(policyARN)
	if !ok {
		return policyNotFound(policyARN)
	}

	for idx, rev := range p.versions {
		if rev.VersionID != versionID {
			continue
		}

		if rev.IsDefault {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"revision %q is the current version of policy %q", versionID, policyARN)
		}

		p.versions = append(p.versions[:idx], p.versions[idx+1:]...)
		m.policies.Set(policyARN, p)

		return nil
	}

	return versionNotFound(versionID, policyARN)
}

// SetDefaultPolicyVersion makes a recorded revision the policy's current
// statements.
func (m *Mock) SetDefaultPolicyVersion(_ context.Context, policyARN, versionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.policies.Get(policyARN)
	if !ok {
		return policyNotFound(policyARN)
	}

	rev, found := findRevision(p.versions, versionID)
	if !found {
		return versionNotFound(versionID, policyARN)
	}

	parsed, err := parseStatements(rev.Statements)
	if err != nil {
		return err
	}

	clearDefaults(p.versions)

	rev.IsDefault = true
	p.Statements = copyStrings(rev.Statements)
	p.parsed = parsed
	p.VersionDate = m.now()
	m.policies.Set(policyARN, p)

	return nil
}

func findRevision(versions []*policyRevision, versionID string) (*policyRevision, bool) {
	for _, rev := range versions {
		if rev.VersionID == versionID {
			return rev, true
		}
	}

	return nil, false
}

func versionNotFound(versionID, policyARN string) error {
	return cerrors.Newf(cerrors.NotFound, "revision %q not found for policy %q", versionID, policyARN)
}

func toPolicyVersionInfo(rev *policyRevision) *driver.PolicyVersionInfo {
	return &driver.PolicyVersionInfo{
		VersionID:        rev.VersionID,
		PolicyDocument:   strings.Join(rev.Statements, statementSeparator),
		IsDefaultVersion: rev.IsDefault,
		CreatedAt:        rev.CreatedAt,
	}
}
