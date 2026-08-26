// Package acm provides an in-memory mock implementation of AWS Certificate
// Manager, issuing real self-signed X.509 certificates so GetCertificate and
// ExportCertificate return usable PEM material.
package acm

import (
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

// Compile-time check that Mock implements driver.ACM.
var _ driver.ACM = (*Mock)(nil)

const (
	defaultDaysBeforeExpiry = 45
	// maxDomains is ACM's cap on domains (CN + SANs) per certificate.
	maxDomains = 100
	// maxTags is ACM's cap on tags per certificate.
	maxTags = 50
	// maxDomainNameLen / maxLabelLen bound a valid FQDN (RFC 5280 / ACM pattern).
	maxDomainNameLen = 253
	maxLabelLen      = 63
	// minTLDLen is the minimum length of the final (top-level) label; minLabels is
	// the minimum number of dot-separated labels an FQDN must have.
	minTLDLen = 2
	minLabels = 2
)

// Mock is an in-memory implementation of AWS ACM.
type Mock struct {
	certs *memstore.Store[*certData]

	cfgMu     sync.RWMutex
	accountFg driver.AccountConfiguration

	opts *config.Options
}

// certData is a certificate plus its own lock.
type certData struct {
	cert driver.Certificate
	// settle overlays a PENDING_VALIDATION window over the stored (ISSUED) status
	// on the Describe/List surface under AsyncSettle; zero-value reports ISSUED
	// immediately. While pending, each domain-validation option also reports
	// PENDING_VALIDATION so a caller sees the CNAME it must create.
	settle settle.Window
	mu     sync.RWMutex
}

// observeCert returns a deep copy of cert with the PENDING_VALIDATION window
// overlaid: while the window is unelapsed the certificate and every domain
// validation report PENDING_VALIDATION; once elapsed the stored (ISSUED) status
// shows through.
func observeCert(cert *driver.Certificate, w settle.Window, now time.Time) driver.Certificate {
	out := copyCert(cert)

	if observed := w.Observe(now, out.Status); observed != out.Status {
		out.Status = observed
		for i := range out.DomainValidationOptions {
			out.DomainValidationOptions[i].ValidationStatus = observed
		}
	}

	return out
}

// New creates a new ACM mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		certs:     memstore.New[*certData](),
		accountFg: driver.AccountConfiguration{DaysBeforeExpiry: defaultDaysBeforeExpiry},
		opts:      opts,
	}
}

func (m *Mock) certARN() string {
	return idgen.AWSARN("acm", m.opts.Region, m.opts.AccountID, "certificate/"+idgen.GenerateID(""))
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

func (m *Mock) getCert(arn string) (*certData, error) {
	if !validCertARN(arn) {
		return nil, invalidArn("%q is not a valid ACM certificate ARN", arn)
	}

	cd, ok := m.certs.Get(arn)
	if !ok {
		return nil, errNotFound(arn)
	}

	return cd, nil
}

// validCertARN reports whether arn has the ACM certificate ARN shape
// (arn:aws:acm:<region>:<account>:certificate/<id>). A malformed ARN is an
// InvalidArnException in real ACM, distinct from a well-formed-but-absent ARN.
func validCertARN(arn string) bool {
	const parts = 6

	seg := strings.SplitN(arn, ":", parts)
	if len(seg) != parts {
		return false
	}

	return seg[0] == "arn" && seg[2] == "acm" && strings.HasPrefix(seg[5], "certificate/") &&
		strings.TrimPrefix(seg[5], "certificate/") != ""
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// copyCert returns a deep copy of a certificate so callers can read reference
// fields (Tags map, SANs / DomainValidationOptions / InUseBy slices) without
// racing concurrent mutations of the stored value under its lock.
func copyCert(c *driver.Certificate) driver.Certificate {
	out := *c
	out.Tags = copyTags(c.Tags)
	out.SubjectAlternativeNames = append([]string(nil), c.SubjectAlternativeNames...)
	out.InUseBy = append([]string(nil), c.InUseBy...)
	out.DomainValidationOptions = append([]driver.DomainValidation(nil), c.DomainValidationOptions...)

	return out
}
