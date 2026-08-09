package acm

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

// RequestCertificate issues a new Amazon-managed certificate. It generates a
// real self-signed X.509 cert and, since the emulator can't perform real
// domain validation, auto-issues it (status ISSUED) so it is immediately
// usable — the local-dev analog of a validated public cert.
//
//nolint:gocritic // in is the public RequestCertificate input, taken by value to match the driver API
func (m *Mock) RequestCertificate(_ context.Context, in driver.RequestCertificateInput) (string, error) {
	if in.DomainName == "" {
		return "", invalidParameter("DomainName is required")
	}

	sans := dedupeDomains(in.DomainName, in.SubjectAlternativeNames)
	if len(sans) > maxDomains {
		return "", invalidParameter("a certificate may cover at most %d domains, got %d", maxDomains, len(sans))
	}

	method := in.ValidationMethod
	if method == "" {
		method = driver.ValidationDNS
	}

	// The emulator issues RSA-2048 material, so it can only honestly report
	// RSA_2048. Reject a request for a different algorithm rather than echoing an
	// algorithm the generated PEM won't match.
	keyAlg := in.KeyAlgorithm
	if keyAlg == "" {
		keyAlg = driver.KeyAlgRSA2048
	}

	if keyAlg != driver.KeyAlgRSA2048 {
		return "", invalidParameter(
			"KeyAlgorithm %q is not supported; the emulator issues RSA_2048 certificates", keyAlg)
	}

	ct := in.CTLoggingPreference
	if ct == "" {
		ct = driver.CTLoggingEnabled
	}

	now := m.now()

	mat, err := generateCertificate(in.DomainName, in.SubjectAlternativeNames, now)
	if err != nil {
		return "", err
	}

	arn := m.certARN()

	cert := driver.Certificate{
		ARN:                     arn,
		DomainName:              in.DomainName,
		SubjectAlternativeNames: sans,
		DomainValidationOptions: validationOptions(sans, method),
		Serial:                  mat.serial,
		Subject:                 mat.subject,
		Issuer:                  mat.issuer,
		CreatedAt:               now,
		IssuedAt:                now,
		NotBefore:               now,
		NotAfter:                mat.notAfter,
		Status:                  driver.StatusIssued,
		KeyAlgorithm:            keyAlg,
		SignatureAlgorithm:      "SHA256WITHRSA",
		Type:                    driver.TypeAmazonIssued,
		RenewalEligibility:      driver.RenewalEligible,
		ValidationMethod:        method,
		CTLoggingPreference:     ct,
		Tags:                    copyTags(in.Tags),
		CertificatePEM:          mat.certPEM,
		ChainPEM:                mat.chainPEM,
		PrivateKeyPEM:           mat.keyPEM,
	}

	m.certs.Set(arn, &certData{cert: cert})

	return arn, nil
}

// validationOptions builds the per-domain validation records for a set of
// domains (DNS validation exposes a CNAME record to add).
func validationOptions(domains []string, method string) []driver.DomainValidation {
	out := make([]driver.DomainValidation, 0, len(domains))

	for _, d := range domains {
		dv := driver.DomainValidation{
			DomainName:       d,
			ValidationDomain: d,
			ValidationStatus: driver.StatusIssued, // auto-validated in the emulator
			ValidationMethod: method,
		}

		if method == driver.ValidationDNS {
			dv.ResourceRecordN = "_acm-validations." + d + "."
			dv.ResourceRecordT = "CNAME"
			dv.ResourceRecordV = "_" + strings.ReplaceAll(d, ".", "-") + ".acm-validations.aws."
		}

		out = append(out, dv)
	}

	return out
}

// DescribeCertificate returns a certificate's metadata.
func (m *Mock) DescribeCertificate(_ context.Context, arn string) (*driver.Certificate, error) {
	cd, err := m.getCert(arn)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := copyCert(&cd.cert)

	return &out, nil
}

// ListCertificates returns all certificates, optionally filtered by status.
func (m *Mock) ListCertificates(_ context.Context, filter driver.ListFilter) ([]driver.Certificate, error) {
	all := m.certs.All()
	out := make([]driver.Certificate, 0, len(all))

	for _, cd := range all {
		cd.mu.RLock()
		if statusMatches(cd.cert.Status, filter.Statuses) {
			out = append(out, copyCert(&cd.cert))
		}
		cd.mu.RUnlock()
	}

	return out, nil
}

func statusMatches(status string, want []string) bool {
	if len(want) == 0 {
		return true
	}

	for _, s := range want {
		if s == status {
			return true
		}
	}

	return false
}

// DeleteCertificate removes a certificate.
func (m *Mock) DeleteCertificate(_ context.Context, arn string) error {
	if _, err := m.getCert(arn); err != nil {
		return err
	}

	m.certs.Delete(arn)

	return nil
}

// GetCertificate returns the certificate and chain PEM for an issued cert.
func (m *Mock) GetCertificate(_ context.Context, arn string) (certPEM, chainPEM string, err error) {
	cd, err := m.getCert(arn)
	if err != nil {
		return "", "", err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	if cd.cert.CertificatePEM == "" {
		return "", "", errors.Newf(errors.FailedPrecondition, "certificate %q has no issued material yet", arn)
	}

	return cd.cert.CertificatePEM, cd.cert.ChainPEM, nil
}

// AddTagsToCertificate adds or overwrites tags, enforcing ACM's per-certificate
// tag cap.
func (m *Mock) AddTagsToCertificate(_ context.Context, arn string, tags map[string]string) error {
	return m.mutate(arn, func(cd *certData) error {
		merged := len(cd.cert.Tags)

		for k := range tags {
			if _, exists := cd.cert.Tags[k]; !exists {
				merged++
			}
		}

		if merged > maxTags {
			return tooManyTags("a certificate may have at most %d tags", maxTags)
		}

		if cd.cert.Tags == nil {
			cd.cert.Tags = map[string]string{}
		}

		for k, v := range tags {
			cd.cert.Tags[k] = v
		}

		return nil
	})
}

// RemoveTagsFromCertificate removes tags by key.
func (m *Mock) RemoveTagsFromCertificate(_ context.Context, arn string, tagKeys []string) error {
	return m.mutate(arn, func(cd *certData) error {
		for _, k := range tagKeys {
			delete(cd.cert.Tags, k)
		}

		return nil
	})
}

// ListTagsForCertificate returns a copy of a certificate's tags.
func (m *Mock) ListTagsForCertificate(_ context.Context, arn string) (map[string]string, error) {
	cd, err := m.getCert(arn)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	return copyTags(cd.cert.Tags), nil
}

// mutate resolves a certificate and runs fn under its write lock.
func (m *Mock) mutate(arn string, fn func(*certData) error) error {
	cd, err := m.getCert(arn)
	if err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	return fn(cd)
}
