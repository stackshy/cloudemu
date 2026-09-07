package privateca

import (
	"context"
	"encoding/json"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	pcadriver "github.com/stackshy/cloudemu/v2/services/privateca/driver"
)

// CreateCertificateAuthority provisions a new certificate authority under a CA
// pool. A self-signed CA is created STAGED with deterministic self-signed PEM
// material (a follow-up :enable moves it to ENABLED); a subordinate CA is created
// AWAITING_USER_ACTIVATION with no signed certificate until :activate is called.
// The parent pool must exist.
func (m *Mock) CreateCertificateAuthority(_ context.Context, cfg *pcadriver.Config) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	return m.create(m.authorities, authoritiesColl, cfg, m.seedAuthority)
}

// GetCertificateAuthority returns a certificate authority by identity.
func (m *Mock) GetCertificateAuthority(_ context.Context, project, location, caPool, id string) (
	*pcadriver.Resource, error,
) {
	return m.get(m.authorities, authoritiesColl, project, location, caPool, id)
}

// ListCertificateAuthorities returns every certificate authority in a CA pool.
func (m *Mock) ListCertificateAuthorities(_ context.Context, project, location, caPool string) (
	[]pcadriver.Resource, error,
) {
	return m.list(m.authorities, authoritiesColl, project, location, caPool)
}

// PatchCertificateAuthority applies a masked update to a certificate authority.
func (m *Mock) PatchCertificateAuthority(_ context.Context, cfg *pcadriver.Config, mask []string) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	return m.patch(m.authorities, authoritiesColl, cfg, mask)
}

// DeleteCertificateAuthority soft-deletes a certificate authority, transitioning
// it to DELETED (recoverable via :undelete within the real grace window) rather
// than removing it, matching real CA Service.
func (m *Mock) DeleteCertificateAuthority(_ context.Context, project, location, caPool, id string) (
	*pcadriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceKey(authoritiesColl, project, location, caPool, id)

	r, ok := m.authorities.Get(key)
	if !ok {
		return nil, notFoundErr(authoritiesColl, project, location, caPool, id)
	}

	if caState(&r) == stateDeleted {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "certificate authority %q is already DELETED", key)
	}

	setState(&r, stateDeleted)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	m.authorities.Set(key, r)

	return m.newOp(project, location, "delete", key), nil
}

// EnableCertificateAuthority transitions a DISABLED or STAGED CA to ENABLED.
func (m *Mock) EnableCertificateAuthority(_ context.Context, project, location, caPool, id string) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	return m.transitionCA(project, location, caPool, id,
		map[string]bool{stateDisabled: true, stateStaged: true}, stateEnabled, "enable", nil)
}

// DisableCertificateAuthority transitions an ENABLED or STAGED CA to DISABLED.
func (m *Mock) DisableCertificateAuthority(_ context.Context, project, location, caPool, id string) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	return m.transitionCA(project, location, caPool, id,
		map[string]bool{stateEnabled: true, stateStaged: true}, stateDisabled, "disable", nil)
}

// UndeleteCertificateAuthority restores a DELETED CA to DISABLED.
func (m *Mock) UndeleteCertificateAuthority(_ context.Context, project, location, caPool, id string) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	return m.transitionCA(project, location, caPool, id,
		map[string]bool{stateDeleted: true}, stateDisabled, "undelete", nil)
}

// ActivateCertificateAuthority activates a subordinate CA awaiting activation,
// storing the caller-supplied signed pemCaCertificate and moving it to ENABLED.
func (m *Mock) ActivateCertificateAuthority(_ context.Context, project, location, caPool, id, pemCaCertificate string) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	if pemCaCertificate == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "pemCaCertificate is required to activate a subordinate CA")
	}

	return m.transitionCA(project, location, caPool, id,
		map[string]bool{stateAwaitingUser: true}, stateEnabled, "activate", func(r *pcadriver.Resource) {
			chain, _ := json.Marshal([]string{pemCaCertificate})
			r.Fields["pemCaCertificates"] = chain
		})
}

// FetchCertificateAuthorityCSR returns the deterministic PEM-encoded CSR a
// subordinate CA awaiting activation must have signed by an issuer.
func (m *Mock) FetchCertificateAuthorityCSR(_ context.Context, project, location, caPool, id string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := resourceKey(authoritiesColl, project, location, caPool, id)

	r, ok := m.authorities.Get(key)
	if !ok {
		return "", notFoundErr(authoritiesColl, project, location, caPool, id)
	}

	if caState(&r) != stateAwaitingUser {
		return "", cerrors.Newf(cerrors.FailedPrecondition,
			"certificate authority %q is not AWAITING_USER_ACTIVATION; no CSR to fetch", key)
	}

	return deterministicCSR(key), nil
}

// transitionCA applies a guarded CA lifecycle state transition. It rejects a
// transition from a state outside allowed with FAILED_PRECONDITION (the real
// error for enabling an already-ENABLED or a DELETED CA), applies an optional
// mutate hook, moves the CA to `to`, and returns the completed LRO.
func (m *Mock) transitionCA(
	project, location, caPool, id string, allowed map[string]bool, to, opType string,
	mutate func(r *pcadriver.Resource),
) (*pcadriver.Resource, *pcadriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceKey(authoritiesColl, project, location, caPool, id)

	r, ok := m.authorities.Get(key)
	if !ok {
		return nil, nil, notFoundErr(authoritiesColl, project, location, caPool, id)
	}

	cur := caState(&r)
	if !allowed[cur] {
		return nil, nil, cerrors.Newf(cerrors.FailedPrecondition,
			"certificate authority %q is in state %s; cannot %s", key, cur, opType)
	}

	if mutate != nil {
		mutate(&r)
	}

	setState(&r, to)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	m.authorities.Set(key, r)

	op := m.newOp(project, location, opType, key)
	out := cloneResource(&r)

	return &out, op, nil
}

// seedAuthority mints a certificate authority's computed fields at create: its
// tier is inherited from the parent pool (which must exist), and its initial
// state plus deterministic PEM material follow from its type — a self-signed CA
// lands ENABLED with a signed self certificate, a subordinate CA lands
// AWAITING_USER_ACTIVATION with no signed certificate yet. Runs under the write
// lock, so the parent pool read is consistent.
func (m *Mock) seedAuthority(r *pcadriver.Resource) error {
	if r.Fields == nil {
		r.Fields = map[string]json.RawMessage{}
	}

	poolKey := resourceKey(caPoolsColl, r.Project, r.Location, "", r.CaPool)

	pool, ok := m.pools.Get(poolKey)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "ca pool %q not found", poolKey)
	}

	if tier, has := pool.Fields["tier"]; has {
		r.Fields["tier"] = append(json.RawMessage(nil), tier...)
	} else {
		r.Fields["tier"] = json.RawMessage(`"` + defaultTier + `"`)
	}

	key := resourceKey(authoritiesColl, r.Project, r.Location, r.CaPool, r.ID)

	if stringField(r.Fields, "type") == typeSubordinate {
		r.Fields["state"] = json.RawMessage(`"` + stateAwaitingUser + `"`)
		r.Fields["pemCaCertificates"] = json.RawMessage(`[]`)

		return nil
	}

	// A self-signed CA is signed at creation but lands STAGED, not ENABLED: real
	// CA Service (and so the Terraform provider) issues a follow-up :enable to
	// reach the desired ENABLED state.
	r.Fields["state"] = json.RawMessage(`"` + stateStaged + `"`)
	chain, _ := json.Marshal([]string{deterministicCACert(key)})
	r.Fields["pemCaCertificates"] = chain

	return nil
}

// caState reads a certificate authority's stored lifecycle state.
func caState(r *pcadriver.Resource) string { return stringField(r.Fields, "state") }

// setState writes a certificate authority's lifecycle state.
func setState(r *pcadriver.Resource, s string) {
	r.Fields["state"] = json.RawMessage(`"` + s + `"`)
}

// stringField decodes a top-level string body field, returning "" when it is
// absent or not a string.
func stringField(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}

	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}

	return s
}
