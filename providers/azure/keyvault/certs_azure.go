package keyvault

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // x5t is defined by Key Vault as the SHA-1 thumbprint of the DER cert
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	certRSABits           = 2048
	defaultValidityMonths = 12
	certSerialBits        = 128
)

// certVersion is one stored certificate version: the generated DER certificate
// plus the Key Vault attributes and the verbatim policy for round-tripping.
type certVersion struct {
	versionID   string
	cer         []byte
	thumbprint  []byte
	contentType string
	policyRaw   []byte
	tags        map[string]string
	enabled     bool
	expires     int64
	notBefore   int64
	created     time.Time
	updated     time.Time
	current     bool
}

type certData struct {
	name           string
	versions       []certVersion
	deletedAt      time.Time
	scheduledPurge time.Time
	mu             sync.RWMutex
}

func (v *certVersion) toKV(name string) driver.KVCertificate {
	return driver.KVCertificate{
		Name:        name,
		Version:     v.versionID,
		CER:         copyBytes(v.cer),
		Thumbprint:  copyBytes(v.thumbprint),
		ContentType: v.contentType,
		Tags:        copyTags(v.tags),
		Enabled:     v.enabled,
		Expires:     v.expires,
		NotBefore:   v.notBefore,
		Created:     v.created.Unix(),
		Updated:     v.updated.Unix(),
		PolicyRaw:   copyBytes(v.policyRaw),
	}
}

// liveCert returns the stored certificate from store if it exists and is not
// soft-deleted.
func liveCert(store *memstore.Store[*certData], name string) *certData {
	cd, ok := store.Get(name)
	if !ok {
		return nil
	}

	cd.mu.RLock()
	deleted := !cd.deletedAt.IsZero()
	cd.mu.RUnlock()

	if deleted {
		return nil
	}

	return cd
}

func findCertVersion(cd *certData, version string) *certVersion {
	for i := range cd.versions {
		v := &cd.versions[i]
		if version == "" && v.current {
			return v
		}

		if v.versionID == version {
			return v
		}
	}

	return nil
}

// commonName extracts the CN component from an X.509 distinguished name such as
// "CN=example.com, O=Contoso". It returns fallback when the DN carries no CN.
func commonName(subject, fallback string) string {
	for _, rdn := range strings.Split(subject, ",") {
		rdn = strings.TrimSpace(rdn)
		if cn, ok := strings.CutPrefix(rdn, "CN="); ok {
			return cn
		}
	}

	return fallback
}

// generateSelfSigned builds a self-signed X.509 certificate for the subject and
// SAN DNS names, valid for months from now, returning its DER encoding and the
// not-before/not-after instants.
func generateSelfSigned(
	subject, name string, dnsNames []string, months int, now time.Time,
) (der []byte, notBefore, notAfter time.Time, err error) {
	key, err := rsa.GenerateKey(rand.Reader, certRSABits)
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), certSerialBits)

	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}

	notBefore = now
	notAfter = now.AddDate(0, months, 0)

	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName(subject, name)},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
	}

	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}

	return der, notBefore, notAfter, nil
}

// CreateCertificate generates a self-signed certificate and stores it as the
// current version, appending a new version when the name already exists.
func (m *Mock) CreateCertificate(
	_ context.Context, vault, name string, params *driver.KVCreateCertificateParams,
) (*driver.KVCertificate, error) {
	if name == "" {
		return nil, errors.New(errors.InvalidArgument, "certificate name is required")
	}

	months := params.ValidityMonths
	if months <= 0 {
		months = defaultValidityMonths
	}

	now := m.opts.Clock.Now().UTC()

	der, notBefore, notAfter, err := generateSelfSigned(params.Subject, name, params.DNSNames, months, now)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "generate certificate: %v", err)
	}

	sum := sha1.Sum(der) //nolint:gosec // x5t is the SHA-1 thumbprint by Key Vault's definition

	v := certVersion{
		versionID:   hexVersion(),
		cer:         der,
		thumbprint:  sum[:],
		contentType: params.ContentType,
		policyRaw:   copyBytes(params.PolicyRaw),
		tags:        copyTags(params.Tags),
		enabled:     params.Attributes.Enabled,
		expires:     notAfter.Unix(),
		notBefore:   notBefore.Unix(),
		created:     now,
		updated:     now,
		current:     true,
	}

	store := m.vault(vault).certs

	if cd, ok := store.Get(name); ok {
		cd.mu.Lock()
		defer cd.mu.Unlock()

		if !cd.deletedAt.IsZero() {
			return nil, errors.Newf(errors.AlreadyExists, "certificate %q is in a deleted but recoverable state", name)
		}

		for i := range cd.versions {
			cd.versions[i].current = false
		}

		cd.versions = append(cd.versions, v)
		kv := v.toKV(name)

		return &kv, nil
	}

	cd := &certData{name: name, versions: []certVersion{v}}
	store.Set(name, cd)

	kv := v.toKV(name)

	return &kv, nil
}

// GetCertificate returns one certificate version. Empty version returns the
// current version.
//
//nolint:dupl // parallel certificate/secret version accessor; the shared shape is intentional
func (m *Mock) GetCertificate(_ context.Context, vault, name, version string) (*driver.KVCertificate, error) {
	cd := liveCert(m.vault(vault).certs, name)
	if cd == nil {
		return nil, errors.Newf(errors.NotFound, "certificate %q not found", name)
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	v := findCertVersion(cd, version)
	if v == nil {
		return nil, errors.Newf(errors.NotFound, "version %q not found for certificate %q", version, name)
	}

	kv := v.toKV(name)

	return &kv, nil
}

// ListCertificates returns the current version of each live certificate.
func (m *Mock) ListCertificates(_ context.Context, vault string) ([]driver.KVCertificate, error) {
	all := m.vault(vault).certs.All()

	out := make([]driver.KVCertificate, 0, len(all))

	for _, cd := range all {
		cd.mu.RLock()
		if cd.deletedAt.IsZero() {
			if v := findCertVersion(cd, ""); v != nil {
				out = append(out, v.toKV(cd.name))
			}
		}
		cd.mu.RUnlock()
	}

	return out, nil
}

// ListCertificateVersions returns every version of a certificate.
func (m *Mock) ListCertificateVersions(_ context.Context, vault, name string) ([]driver.KVCertificate, error) {
	cd := liveCert(m.vault(vault).certs, name)
	if cd == nil {
		return nil, errors.Newf(errors.NotFound, "certificate %q not found", name)
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := make([]driver.KVCertificate, len(cd.versions))
	for i := range cd.versions {
		out[i] = cd.versions[i].toKV(name)
	}

	return out, nil
}

// DeleteCertificate soft-deletes a certificate and returns its deleted view.
//
//nolint:dupl // parallel certificate/key soft-delete; the shared shape is intentional
func (m *Mock) DeleteCertificate(_ context.Context, vault, name string) (*driver.KVDeletedCertificate, error) {
	cd := liveCert(m.vault(vault).certs, name)
	if cd == nil {
		return nil, errors.Newf(errors.NotFound, "certificate %q not found", name)
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	now := m.opts.Clock.Now().UTC()
	cd.deletedAt = now
	cd.scheduledPurge = now.AddDate(0, 0, purgeWindowDays)

	return deletedCertView(cd), nil
}

func deletedCertView(cd *certData) *driver.KVDeletedCertificate {
	v := findCertVersion(cd, "")
	if v == nil && len(cd.versions) > 0 {
		v = &cd.versions[len(cd.versions)-1]
	}

	var kv driver.KVCertificate
	if v != nil {
		kv = v.toKV(cd.name)
	}

	return &driver.KVDeletedCertificate{
		KVCertificate:      kv,
		DeletedDate:        cd.deletedAt.Unix(),
		ScheduledPurgeDate: cd.scheduledPurge.Unix(),
	}
}

// GetDeletedCertificate returns a soft-deleted certificate by name.
func (m *Mock) GetDeletedCertificate(_ context.Context, vault, name string) (*driver.KVDeletedCertificate, error) {
	cd, ok := m.vault(vault).certs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "deleted certificate %q not found", name)
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	if cd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "deleted certificate %q not found", name)
	}

	return deletedCertView(cd), nil
}

// ListDeletedCertificates returns all soft-deleted certificates.
func (m *Mock) ListDeletedCertificates(_ context.Context, vault string) ([]driver.KVDeletedCertificate, error) {
	all := m.vault(vault).certs.All()

	out := make([]driver.KVDeletedCertificate, 0, len(all))

	for _, cd := range all {
		cd.mu.RLock()
		if !cd.deletedAt.IsZero() {
			out = append(out, *deletedCertView(cd))
		}
		cd.mu.RUnlock()
	}

	return out, nil
}

// RecoverDeletedCertificate clears the soft-delete state of a certificate.
//
//nolint:dupl // parallel certificate/secret recover; the shared shape is intentional
func (m *Mock) RecoverDeletedCertificate(_ context.Context, vault, name string) (*driver.KVCertificate, error) {
	cd, ok := m.vault(vault).certs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "deleted certificate %q not found", name)
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	if cd.deletedAt.IsZero() {
		return nil, errors.Newf(errors.NotFound, "deleted certificate %q not found", name)
	}

	cd.deletedAt = time.Time{}
	cd.scheduledPurge = time.Time{}

	v := findCertVersion(cd, "")

	var kv driver.KVCertificate
	if v != nil {
		kv = v.toKV(name)
	}

	return &kv, nil
}

// PurgeDeletedCertificate permanently removes a soft-deleted certificate.
func (m *Mock) PurgeDeletedCertificate(_ context.Context, vault, name string) error {
	store := m.vault(vault).certs

	cd, ok := store.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "deleted certificate %q not found", name)
	}

	cd.mu.RLock()
	deleted := !cd.deletedAt.IsZero()
	cd.mu.RUnlock()

	if !deleted {
		return errors.Newf(errors.NotFound, "deleted certificate %q not found", name)
	}

	store.Delete(name)

	return nil
}
