package acm

import (
	"context"
	"crypto/x509"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

// ImportCertificate imports an externally-issued certificate. With an ARN it
// re-imports (updates) the existing certificate; otherwise it creates a new
// IMPORTED certificate. The certificate PEM is validated as parseable X.509.
func (m *Mock) ImportCertificate(_ context.Context, in driver.ImportCertificateInput) (string, error) {
	if in.CertificatePEM == "" || in.PrivateKeyPEM == "" {
		return "", errors.New(errors.InvalidArgument, "Certificate and PrivateKey are required")
	}

	leaf, err := parseCertificatePEM(in.CertificatePEM)
	if err != nil {
		return "", err
	}

	now := m.now()

	if in.ARN != "" {
		err := m.mutate(in.ARN, func(cd *certData) error {
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
	c.KeyAlgorithm = driver.KeyAlgRSA2048
	c.SignatureAlgorithm = "SHA256WITHRSA"
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
		return "", "", "", errors.New(errors.InvalidArgument, "Passphrase is required")
	}

	cd, err := m.getCert(arn)
	if err != nil {
		return "", "", "", err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	if cd.cert.PrivateKeyPEM == "" {
		return "", "", "", errors.Newf(errors.FailedPrecondition, "certificate %q has no exportable key", arn)
	}

	return cd.cert.CertificatePEM, cd.cert.ChainPEM, cd.cert.PrivateKeyPEM, nil
}

// RenewCertificate re-issues an Amazon-managed certificate, resetting its
// validity window.
func (m *Mock) RenewCertificate(_ context.Context, arn string) error {
	return m.mutate(arn, func(cd *certData) error {
		if cd.cert.Type != driver.TypeAmazonIssued {
			return errors.New(errors.InvalidArgument, "only Amazon-issued certificates can be renewed")
		}

		now := m.now()

		mat, err := generateCertificate(cd.cert.DomainName, cd.cert.SubjectAlternativeNames, now)
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
		return errors.New(errors.InvalidArgument, "CertificateTransparencyLoggingPreference must be ENABLED or DISABLED")
	}

	return m.mutate(arn, func(cd *certData) error {
		cd.cert.CTLoggingPreference = ctLoggingPreference

		return nil
	})
}

// RevokeCertificate revokes a certificate, returning its ARN.
func (m *Mock) RevokeCertificate(_ context.Context, arn, _ string) (string, error) {
	err := m.mutate(arn, func(cd *certData) error {
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
