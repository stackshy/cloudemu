package acm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"time"

	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

// ImportCertificate imports an externally-issued certificate. With an ARN it
// re-imports (updates) the existing certificate; otherwise it creates a new
// IMPORTED certificate. The certificate PEM is validated as parseable X.509.
func (m *Mock) ImportCertificate(_ context.Context, in driver.ImportCertificateInput) (string, error) {
	if in.CertificatePEM == "" || in.PrivateKeyPEM == "" {
		return "", invalidParameter("Certificate and PrivateKey are required")
	}

	leaf, err := parseCertificatePEM(in.CertificatePEM)
	if err != nil {
		return "", err
	}

	// Validate the private key parses and matches the certificate — the most
	// common real-ACM import error. tls.X509KeyPair does both checks at once.
	if _, err := tls.X509KeyPair(pemBytes(in.CertificatePEM), pemBytes(in.PrivateKeyPEM)); err != nil {
		return "", invalidParameter("private key could not be parsed or does not match the certificate: %v", err)
	}

	now := m.now()

	if in.ARN != "" {
		err := m.mutate(in.ARN, func(cd *certData) error {
			// Re-import replaces an existing IMPORTED certificate's material; it
			// can't convert a managed (AMAZON_ISSUED) cert into an imported one.
			if cd.cert.Type != driver.TypeImported {
				return invalidParameter(
					"certificate %q is %s and cannot be replaced by import", in.ARN, cd.cert.Type)
			}

			applyImported(&cd.cert, in, leaf, now)

			return nil
		})
		if err != nil {
			return "", err
		}

		return in.ARN, nil
	}

	arn := m.certARN()
	cert := driver.Certificate{ARN: arn, Type: driver.TypeImported, Tags: copyTags(in.Tags)}
	applyImported(&cert, in, leaf, now)
	m.certs.Set(arn, &certData{cert: cert})

	return arn, nil
}

func applyImported(c *driver.Certificate, in driver.ImportCertificateInput, leaf *x509.Certificate, now time.Time) {
	c.DomainName = leaf.Subject.CommonName
	c.SubjectAlternativeNames = leaf.DNSNames
	c.Serial = formatSerial(leaf.SerialNumber)
	c.Subject = leaf.Subject.String()
	c.Issuer = leaf.Issuer.CommonName
	c.CreatedAt = now
	c.ImportedAt = now
	c.NotBefore = leaf.NotBefore
	c.NotAfter = leaf.NotAfter
	c.Status = driver.StatusIssued
	// Report the algorithms actually present in the imported certificate rather
	// than assuming RSA-2048/SHA256WITHRSA.
	c.KeyAlgorithm = keyAlgorithmOf(leaf)
	c.SignatureAlgorithm = signatureAlgorithmOf(leaf)
	c.Type = driver.TypeImported
	c.RenewalEligibility = driver.RenewalIneligible
	c.CertificatePEM = in.CertificatePEM
	c.PrivateKeyPEM = in.PrivateKeyPEM
	c.ChainPEM = chainOrSelf(in.ChainPEM, in.CertificatePEM)
}

func chainOrSelf(chain, cert string) string {
	if chain != "" {
		return chain
	}

	return cert
}

// ExportCertificate returns the cert, chain, and (passphrase-protected in real
// ACM; returned as-is here) private key PEM.
func (m *Mock) ExportCertificate(
	_ context.Context, arn string, passphrase []byte,
) (certPEM, chainPEM, keyPEM string, err error) {
	if len(passphrase) == 0 {
		return "", "", "", invalidParameter("Passphrase is required")
	}

	cd, err := m.getCert(arn)
	if err != nil {
		return "", "", "", err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	// Real ACM refuses to export the private key of a public AMAZON_ISSUED
	// certificate — only imported and private-CA certs are exportable. The key
	// exists server-side (we need it to serve GetCertificate), so the gate is on
	// Type, matching GetCertificate's own key-withholding.
	if cd.cert.Type == driver.TypeAmazonIssued {
		return "", "", "", invalidState(
			"certificate %q is a public AMAZON_ISSUED certificate and cannot be exported", arn)
	}

	if cd.cert.PrivateKeyPEM == "" {
		return "", "", "", invalidState("certificate %q has no exportable key", arn)
	}

	return cd.cert.CertificatePEM, cd.cert.ChainPEM, cd.cert.PrivateKeyPEM, nil
}

// RenewCertificate re-issues an Amazon-managed certificate, resetting its
// validity window.
func (m *Mock) RenewCertificate(_ context.Context, arn string) error {
	return m.mutate(arn, func(cd *certData) error {
		if cd.cert.Type != driver.TypeAmazonIssued {
			return invalidState("only Amazon-issued certificates can be renewed")
		}

		now := m.now()

		mat, err := generateCertificate(cd.cert.KeyAlgorithm, cd.cert.DomainName, cd.cert.SubjectAlternativeNames, now)
		if err != nil {
			return err
		}

		cd.cert.Serial = mat.serial
		cd.cert.IssuedAt = now
		cd.cert.NotBefore = now
		cd.cert.NotAfter = mat.notAfter
		cd.cert.CertificatePEM = mat.certPEM
		cd.cert.ChainPEM = mat.chainPEM
		cd.cert.PrivateKeyPEM = mat.keyPEM
		// Re-issuing produces valid material; a previously revoked cert becomes
		// usable again, so the status must return to ISSUED.
		cd.cert.Status = driver.StatusIssued

		return nil
	})
}

// ResendValidationEmail is a no-op success in the emulator (certs auto-validate)
// but validates the certificate exists and is pending.
func (m *Mock) ResendValidationEmail(_ context.Context, arn string) error {
	cd, err := m.getCert(arn)
	if err != nil {
		return err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	return nil
}

// UpdateCertificateOptions changes the certificate-transparency logging pref.
func (m *Mock) UpdateCertificateOptions(_ context.Context, arn, ctLoggingPreference string) error {
	if ctLoggingPreference != driver.CTLoggingEnabled && ctLoggingPreference != driver.CTLoggingDisabled {
		return invalidParameter("CertificateTransparencyLoggingPreference must be ENABLED or DISABLED")
	}

	return m.mutate(arn, func(cd *certData) error {
		cd.cert.CTLoggingPreference = ctLoggingPreference

		return nil
	})
}

// RevokeCertificate revokes an issued certificate, returning its ARN. Only an
// ISSUED certificate can be revoked; revoking a pending/failed/already-revoked
// certificate is an InvalidStateException in real ACM.
func (m *Mock) RevokeCertificate(_ context.Context, arn, _ string) (string, error) {
	err := m.mutate(arn, func(cd *certData) error {
		if cd.cert.Status != driver.StatusIssued {
			return invalidState("certificate %q is in state %s and cannot be revoked", arn, cd.cert.Status)
		}

		cd.cert.Status = driver.StatusRevoked

		return nil
	})
	if err != nil {
		return "", err
	}

	return arn, nil
}

// SearchCertificates returns certificates matching the filter (same backing as
// ListCertificates in the emulator).
func (m *Mock) SearchCertificates(ctx context.Context, filter driver.ListFilter) ([]driver.Certificate, error) {
	return m.ListCertificates(ctx, filter)
}

// GetAccountConfiguration returns the account-level ACM configuration.
func (m *Mock) GetAccountConfiguration(_ context.Context) (*driver.AccountConfiguration, error) {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()

	cfg := m.accountFg

	return &cfg, nil
}

// PutAccountConfiguration sets the account-level ACM configuration.
func (m *Mock) PutAccountConfiguration(_ context.Context, cfg driver.AccountConfiguration) error {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()

	m.accountFg = cfg

	return nil
}
