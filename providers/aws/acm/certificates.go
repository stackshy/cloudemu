package acm

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
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

	if err := validateDomainName(in.DomainName); err != nil {
		return "", err
	}

	for _, san := range in.SubjectAlternativeNames {
		if err := validateDomainName(san); err != nil {
			return "", err
		}
	}

	sans := dedupeDomains(in.DomainName, in.SubjectAlternativeNames)
	if len(sans) > maxDomains {
		return "", invalidParameter("a certificate may cover at most %d domains, got %d", maxDomains, len(sans))
	}

	method := in.ValidationMethod
	if method == "" {
		method = driver.ValidationDNS
	}

	keyAlg := in.KeyAlgorithm
	if keyAlg == "" {
		keyAlg = driver.KeyAlgRSA2048
	}

	ct := in.CTLoggingPreference
	if ct == "" {
		ct = driver.CTLoggingEnabled
	}

	now := m.now()

	// generateCertificate issues real key material for the requested algorithm
	// (RSA/EC) and rejects a genuinely-unsupported one with
	// InvalidParameterException, so Describe reflects the actual key + signature.
	mat, err := generateCertificate(keyAlg, in.DomainName, in.SubjectAlternativeNames, now)
	if err != nil {
		return "", err
	}

	arn := m.certARN()

	cert := driver.Certificate{
		ARN:                     arn,
		DomainName:              in.DomainName,
		SubjectAlternativeNames: sans,
		DomainValidationOptions: validationOptions(sans, method, in.DomainValidationOptions),
		Serial:                  mat.serial,
		Subject:                 mat.subject,
		Issuer:                  mat.issuer,
		CreatedAt:               now,
		IssuedAt:                now,
		NotBefore:               now,
		NotAfter:                mat.notAfter,
		Status:                  driver.StatusIssued,
		KeyAlgorithm:            mat.keyAlgorithm,
		SignatureAlgorithm:      mat.sigAlgorithm,
		Type:                    driver.TypeAmazonIssued,
		RenewalEligibility:      driver.RenewalEligible,
		ValidationMethod:        method,
		CTLoggingPreference:     ct,
		Tags:                    copyTags(in.Tags),
		CertificatePEM:          mat.certPEM,
		ChainPEM:                mat.chainPEM,
		PrivateKeyPEM:           mat.keyPEM,
	}

	window := settle.Pending(driver.StatusPendingValidation, now,
		m.opts.SettleDuration(settle.DefaultCertificateSettle))
	m.certs.Set(arn, &certData{cert: cert, settle: window})

	return arn, nil
}

// wellKnownMailboxes are the deterministic approver addresses ACM emails for
// EMAIL validation (real ACM also adds WHOIS-derived contacts, which an emulator
// cannot resolve). Each is prefixed onto the domain's validation domain.
//
//nolint:gochecknoglobals // fixed lookup table for EMAIL validation mailboxes
var wellKnownMailboxes = []string{"admin", "administrator", "hostmaster", "postmaster", "webmaster"}

// validationOptions builds the per-domain validation records for a set of
// domains. DNS validation exposes a CNAME record to add; EMAIL validation
// exposes the approver mailbox list rooted at each domain's validation domain.
//
// A wildcard domain ("*.example.com") is validated against its BASE domain, so
// the DNS record (or email domain) is rooted at "example.com" (never a literal
// "*", which Route53 rejects as a leftmost label). A wildcard and its apex SAN
// ("*.example.com" and "example.com") share a single DNS validation record,
// matching real ACM, so they collapse to one DomainValidation rather than
// emitting a duplicate CNAME.
//
// reqOpts carries any caller-supplied DomainValidationOptions, letting an EMAIL
// request route approval to a superdomain (validation domain defaults to the
// domain itself, with any wildcard label stripped).
func validationOptions(domains []string, method string, reqOpts []driver.DomainValidationOption) []driver.DomainValidation {
	overrides := make(map[string]string, len(reqOpts))

	for _, o := range reqOpts {
		if o.ValidationDomain != "" {
			overrides[o.DomainName] = o.ValidationDomain
		}
	}

	out := make([]driver.DomainValidation, 0, len(domains))
	seenRecord := make(map[string]bool)

	for _, d := range domains {
		base := strings.TrimPrefix(d, "*.")

		if method == driver.ValidationDNS && seenRecord[base] {
			continue
		}

		dv := driver.DomainValidation{
			DomainName:       d,
			ValidationDomain: d,
			ValidationStatus: driver.StatusIssued, // auto-validated in the emulator
			ValidationMethod: method,
		}

		if method == driver.ValidationDNS {
			seenRecord[base] = true
			dv.ResourceRecordN = "_acm-validations." + base + "."
			dv.ResourceRecordT = "CNAME"
			dv.ResourceRecordV = "_" + strings.ReplaceAll(base, ".", "-") + ".acm-validations.aws."
		} else {
			validationDomain := base
			if v, ok := overrides[d]; ok {
				validationDomain = v
			}

			dv.ValidationDomain = validationDomain
			dv.ValidationEmails = validationEmails(validationDomain)
		}

		out = append(out, dv)
	}

	return out
}

// validationEmails returns the well-known approver mailbox addresses for a
// validation domain.
func validationEmails(validationDomain string) []string {
	out := make([]string, 0, len(wellKnownMailboxes))
	for _, mbox := range wellKnownMailboxes {
		out = append(out, mbox+"@"+validationDomain)
	}

	return out
}

// validateDomainName reports whether name is a valid ACM fully qualified domain
// name, mirroring the RequestCertificate DomainName/SAN pattern
// `(\*\.)?(label\.)+tld`. A single leading "*." wildcard label is allowed; the
// remaining name must be a dotted FQDN whose top-level label is at least two
// characters. A malformed name is an InvalidParameterException in real ACM.
func validateDomainName(name string) error {
	if name == "" || len(name) > maxDomainNameLen {
		return invalidParameter("DomainName %q is not a valid fully qualified domain name", name)
	}

	labels := strings.Split(strings.TrimPrefix(name, "*."), ".")
	if len(labels) < minLabels {
		return invalidParameter("DomainName %q is not a valid fully qualified domain name", name)
	}

	for i, label := range labels {
		minLen := 1
		if i == len(labels)-1 {
			minLen = minTLDLen
		}

		if !validLabel(label, minLen) {
			return invalidParameter("DomainName %q is not a valid fully qualified domain name", name)
		}
	}

	return nil
}

// validLabel reports whether label is a valid DNS label of at least minLen
// characters: alphanumeric or hyphen, never starting or ending with a hyphen.
func validLabel(label string, minLen int) bool {
	if len(label) < minLen || len(label) > maxLabelLen {
		return false
	}

	if label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}

	for i := 0; i < len(label); i++ {
		if !isLabelChar(label[i]) {
			return false
		}
	}

	return true
}

func isLabelChar(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
}

// DescribeCertificate returns a certificate's metadata.
func (m *Mock) DescribeCertificate(_ context.Context, arn string) (*driver.Certificate, error) {
	cd, err := m.getCert(arn)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := observeCert(&cd.cert, cd.settle, m.now())

	return &out, nil
}

// ListCertificates returns all certificates, filtered by status and key type.
// Following real ACM, the default (empty KeyTypes) returns only RSA_2048
// certificates; other key types appear only when explicitly requested.
func (m *Mock) ListCertificates(_ context.Context, filter driver.ListFilter) ([]driver.Certificate, error) {
	all := m.certs.All()
	out := make([]driver.Certificate, 0, len(all))

	keyTypes := filter.KeyTypes
	if len(keyTypes) == 0 {
		keyTypes = []string{driver.KeyAlgRSA2048}
	}

	now := m.now()

	for _, cd := range all {
		cd.mu.RLock()
		oc := observeCert(&cd.cert, cd.settle, now)
		cd.mu.RUnlock()

		if statusMatches(oc.Status, filter.Statuses) && contains(keyTypes, oc.KeyAlgorithm) {
			out = append(out, oc)
		}
	}

	return out, nil
}

func statusMatches(status string, want []string) bool {
	if len(want) == 0 {
		return true
	}

	return contains(want, status)
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
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

	// While the certificate is still observably PENDING_VALIDATION, its material
	// is not yet retrievable — real ACM answers RequestInProgressException.
	if !cd.settle.Settled(m.now()) {
		return "", "", errors.Newf(errors.FailedPrecondition,
			"certificate %q is pending validation and has no issued material yet", arn)
	}

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
