package keyvault

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // x5t is defined by Key Vault as the SHA-1 thumbprint of the DER cert
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
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
) (der []byte, key *rsa.PrivateKey, notBefore, notAfter time.Time, err error) {
	key, err = rsa.GenerateKey(rand.Reader, certRSABits)
	if err != nil {
		return nil, nil, time.Time{}, time.Time{}, err
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), certSerialBits)

	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, time.Time{}, time.Time{}, err
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
		return nil, nil, time.Time{}, time.Time{}, err
	}

	return der, key, notBefore, notAfter, nil
}

// certAndKeyPEM encodes the certificate and its RSA private key as a PEM bundle,
// the form Key Vault's addressable secret returns for an exportable certificate.
func certAndKeyPEM(der []byte, key *rsa.PrivateKey) []byte {
	var buf bytes.Buffer

	_ = pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	_ = pem.Encode(&buf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	return buf.Bytes()
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

	der, key, notBefore, notAfter, err := generateSelfSigned(params.Subject, name, params.DNSNames, months, now)
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

	if err := m.storeCertVersion(vault, name, &v); err != nil {
		return nil, err
	}

	// A Key Vault certificate also creates an addressable key and secret with
	// the same name and version: the secret returns the certificate value, the
	// key exposes the certificate's key operations. Their attributes mirror the
	// certificate's, so SID/KID on the certificate bundle resolve to real
	// objects (Terraform azurerm_key_vault_certificate.secret_id, azcertificates
	// CertificateBundle.SID/KID).
	m.mintCertSecret(vault, name, &v, certAndKeyPEM(der, key))
	m.mintCertKey(vault, name, &v, key)

	kv := v.toKV(name)

	return &kv, nil
}

// storeCertVersion appends v as the current version of name, creating the
// certificate if it does not yet exist. A soft-deleted name cannot be reused
// until recovered.
func (m *Mock) storeCertVersion(vault, name string, v *certVersion) error {
	store := m.vault(vault).certs

	if cd, ok := store.Get(name); ok {
		cd.mu.Lock()
		defer cd.mu.Unlock()

		if !cd.deletedAt.IsZero() {
			return errors.Newf(errors.AlreadyExists, "certificate %q is in a deleted but recoverable state", name)
		}

		for i := range cd.versions {
			cd.versions[i].current = false
		}

		cd.versions = append(cd.versions, *v)

		return nil
	}

	store.Set(name, &certData{name: name, versions: []certVersion{*v}})

	return nil
}

// mintCertSecret inserts the managed addressable secret backing a certificate
// version, carrying the certificate as a PEM bundle. The secret shares the
// certificate's version id so the certificate's SID resolves to it.
func (m *Mock) mintCertSecret(vault, name string, v *certVersion, pemBundle []byte) {
	sv := secretVersion{
		versionID:   v.versionID,
		value:       pemBundle,
		contentType: "application/x-pem-file",
		enabled:     v.enabled,
		expires:     v.expires,
		notBefore:   v.notBefore,
		created:     v.created,
		updated:     v.updated,
		current:     true,
		managed:     true,
	}

	store := m.vault(vault).secrets

	if sd, ok := store.Get(name); ok {
		sd.mu.Lock()
		defer sd.mu.Unlock()

		for i := range sd.versions {
			sd.versions[i].current = false
		}

		sd.versions = append(sd.versions, sv)
		sd.info.UpdatedAt = v.updated.Format(time.RFC3339)

		return
	}

	store.Set(name, &secretData{
		info: driver.SecretInfo{
			ID:         idgen.GenerateID("secret-"),
			Name:       name,
			ResourceID: idgen.AzureID(m.opts.AccountID, "rg-default", "Microsoft.KeyVault", "vaults/default/secrets", name),
			CreatedAt:  v.created.Format(time.RFC3339),
			UpdatedAt:  v.updated.Format(time.RFC3339),
		},
		versions: []secretVersion{sv},
	})
}

// mintCertKey inserts the managed addressable key backing a certificate
// version, reusing the certificate's RSA key material. The key shares the
// certificate's version id so the certificate's KID resolves to it.
func (m *Mock) mintCertKey(vault, name string, v *certVersion, key *rsa.PrivateKey) {
	kvv := keyVersion{
		versionID: v.versionID,
		kty:       ktyRSA,
		keyOps:    defaultKeyOps(ktyRSA),
		rsaKey:    key,
		enabled:   v.enabled,
		expires:   v.expires,
		notBefore: v.notBefore,
		created:   v.created,
		updated:   v.updated,
		current:   true,
		managed:   true,
	}

	store := m.vault(vault).keys

	if kd, ok := store.Get(name); ok {
		kd.mu.Lock()
		defer kd.mu.Unlock()

		for i := range kd.versions {
			kd.versions[i].current = false
		}

		kd.versions = append(kd.versions, kvv)

		return
	}

	store.Set(name, &keyData{name: name, versions: []keyVersion{kvv}})
}

// GetCertificate returns one certificate version. Empty version returns the
// current version.
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
