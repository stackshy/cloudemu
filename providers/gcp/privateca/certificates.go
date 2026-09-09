package privateca

import (
	"context"
	"encoding/json"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	pcadriver "github.com/stackshy/cloudemu/v2/services/privateca/driver"
)

// defaultRevocationReason is applied when :revoke omits a reason, matching the
// real API's REVOCATION_REASON_UNSPECIFIED handling with a concrete default.
const defaultRevocationReason = "REVOCATION_REASON_UNSPECIFIED"

// CreateCertificate issues a certificate under a CA pool. Issuance completes
// synchronously (the real API returns the Certificate, not an operation): the
// deterministic pemCertificate, pemCertificateChain, and issuer are minted once
// and stored so a later read is byte-stable. The parent pool must exist.
func (m *Mock) CreateCertificate(_ context.Context, cfg *pcadriver.Config) (*pcadriver.Resource, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "certificate id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	poolKey := resourceKey(caPoolsColl, cfg.Project, cfg.Location, "", cfg.CaPool)
	if !m.pools.Has(poolKey) {
		return nil, cerrors.Newf(cerrors.NotFound, "ca pool %q not found", poolKey)
	}

	key := resourceKey(certificatesColl, cfg.Project, cfg.Location, cfg.CaPool, cfg.ID)
	if m.certificates.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "certificate %q already exists", cfg.ID)
	}

	now := m.opts.Clock.Now().UTC()
	res := pcadriver.Resource{
		Project: cfg.Project, Location: cfg.Location, CaPool: cfg.CaPool, ID: cfg.ID,
		CreateTime: now, UpdateTime: now, Fields: cloneRawMap(cfg.Fields),
	}
	seedCertificate(&res, key)
	m.certificates.Set(key, res)

	out := cloneResource(&res)

	return &out, nil
}

// GetCertificate returns a certificate by identity.
func (m *Mock) GetCertificate(_ context.Context, project, location, caPool, id string) (*pcadriver.Resource, error) {
	return m.get(m.certificates, certificatesColl, project, location, caPool, id)
}

// ListCertificates returns every certificate in a CA pool.
func (m *Mock) ListCertificates(_ context.Context, project, location, caPool string) ([]pcadriver.Resource, error) {
	return m.list(m.certificates, certificatesColl, project, location, caPool)
}

// PatchCertificate applies a masked update to a certificate (labels only in
// practice) synchronously, returning the updated resource.
func (m *Mock) PatchCertificate(_ context.Context, cfg *pcadriver.Config, mask []string) (*pcadriver.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceKey(certificatesColl, cfg.Project, cfg.Location, cfg.CaPool, cfg.ID)

	r, ok := m.certificates.Get(key)
	if !ok {
		return nil, notFoundErr(certificatesColl, cfg.Project, cfg.Location, cfg.CaPool, cfg.ID)
	}

	applyMask(&r, cfg.Fields, mask)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	m.certificates.Set(key, r)

	out := cloneResource(&r)

	return &out, nil
}

// RevokeCertificate records a certificate's revocation, setting revocationDetails
// (revocationState + revocationTime) and returning the updated resource. A
// second revoke is rejected with FAILED_PRECONDITION, as the real API does.
func (m *Mock) RevokeCertificate(_ context.Context, project, location, caPool, id, reason string) (
	*pcadriver.Resource, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceKey(certificatesColl, project, location, caPool, id)

	r, ok := m.certificates.Get(key)
	if !ok {
		return nil, notFoundErr(certificatesColl, project, location, caPool, id)
	}

	if _, revoked := r.Fields["revocationDetails"]; revoked {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "certificate %q is already revoked", key)
	}

	if reason == "" {
		reason = defaultRevocationReason
	}

	details, _ := json.Marshal(map[string]string{
		"revocationState": reason,
		"revocationTime":  m.opts.Clock.Now().UTC().Format(time.RFC3339Nano),
	})
	r.Fields["revocationDetails"] = details
	r.UpdateTime = m.opts.Clock.Now().UTC()
	m.certificates.Set(key, r)

	out := cloneResource(&r)

	return &out, nil
}

// seedCertificate mints a certificate's computed output fields once at create so a
// later read is byte-stable: a deterministic issuer, PEM certificate, and chain.
func seedCertificate(r *pcadriver.Resource, key string) {
	if r.Fields == nil {
		r.Fields = map[string]json.RawMessage{}
	}

	r.Fields["issuerCertificateAuthority"] = json.RawMessage(`"` + deterministicIssuer(key) + `"`)

	pem, _ := json.Marshal(deterministicCert(key))
	r.Fields["pemCertificate"] = pem

	chain, _ := json.Marshal([]string{deterministicCACert(key)})
	r.Fields["pemCertificateChain"] = chain
}
