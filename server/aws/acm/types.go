package acm

import (
	"time"

	acmdriver "github.com/stackshy/cloudemu/v2/services/acm/driver"
)

type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func tagsToMap(tags []tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func mapToTags(m map[string]string) []tag {
	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{Key: k, Value: v})
	}

	return out
}

func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

type resourceRecordJSON struct {
	Name  string `json:"Name"`
	Type  string `json:"Type"`
	Value string `json:"Value"`
}

type domainValidationJSON struct {
	DomainName       string              `json:"DomainName"`
	ValidationDomain string              `json:"ValidationDomain,omitempty"`
	ValidationStatus string              `json:"ValidationStatus,omitempty"`
	ValidationMethod string              `json:"ValidationMethod,omitempty"`
	ResourceRecord   *resourceRecordJSON `json:"ResourceRecord,omitempty"`
}

type certOptionsJSON struct {
	CertificateTransparencyLoggingPreference string `json:"CertificateTransparencyLoggingPreference,omitempty"`
}

// certificateDetailJSON is the ACM CertificateDetail wire shape.
type certificateDetailJSON struct {
	CertificateArn          string                 `json:"CertificateArn"`
	DomainName              string                 `json:"DomainName"`
	SubjectAlternativeNames []string               `json:"SubjectAlternativeNames,omitempty"`
	DomainValidationOptions []domainValidationJSON `json:"DomainValidationOptions,omitempty"`
	Serial                  string                 `json:"Serial,omitempty"`
	Subject                 string                 `json:"Subject,omitempty"`
	Issuer                  string                 `json:"Issuer,omitempty"`
	CreatedAt               *float64               `json:"CreatedAt,omitempty"`
	IssuedAt                *float64               `json:"IssuedAt,omitempty"`
	ImportedAt              *float64               `json:"ImportedAt,omitempty"`
	NotBefore               *float64               `json:"NotBefore,omitempty"`
	NotAfter                *float64               `json:"NotAfter,omitempty"`
	Status                  string                 `json:"Status"`
	KeyAlgorithm            string                 `json:"KeyAlgorithm,omitempty"`
	SignatureAlgorithm      string                 `json:"SignatureAlgorithm,omitempty"`
	Type                    string                 `json:"Type,omitempty"`
	RenewalEligibility      string                 `json:"RenewalEligibility,omitempty"`
	InUseBy                 []string               `json:"InUseBy,omitempty"`
	Options                 *certOptionsJSON       `json:"Options,omitempty"`
}

func certToWire(c *acmdriver.Certificate) certificateDetailJSON {
	dvo := make([]domainValidationJSON, 0, len(c.DomainValidationOptions))

	for i := range c.DomainValidationOptions {
		d := c.DomainValidationOptions[i]
		j := domainValidationJSON{
			DomainName: d.DomainName, ValidationDomain: d.ValidationDomain,
			ValidationStatus: d.ValidationStatus, ValidationMethod: d.ValidationMethod,
		}

		if d.ResourceRecordN != "" {
			j.ResourceRecord = &resourceRecordJSON{Name: d.ResourceRecordN, Type: d.ResourceRecordT, Value: d.ResourceRecordV}
		}

		dvo = append(dvo, j)
	}

	return certificateDetailJSON{
		CertificateArn:          c.ARN,
		DomainName:              c.DomainName,
		SubjectAlternativeNames: c.SubjectAlternativeNames,
		DomainValidationOptions: dvo,
		Serial:                  c.Serial,
		Subject:                 c.Subject,
		Issuer:                  c.Issuer,
		CreatedAt:               epochOrNil(c.CreatedAt),
		IssuedAt:                epochOrNil(c.IssuedAt),
		ImportedAt:              epochOrNil(c.ImportedAt),
		NotBefore:               epochOrNil(c.NotBefore),
		NotAfter:                epochOrNil(c.NotAfter),
		Status:                  c.Status,
		KeyAlgorithm:            c.KeyAlgorithm,
		SignatureAlgorithm:      c.SignatureAlgorithm,
		Type:                    c.Type,
		RenewalEligibility:      c.RenewalEligibility,
		InUseBy:                 c.InUseBy,
		Options:                 &certOptionsJSON{CertificateTransparencyLoggingPreference: c.CTLoggingPreference},
	}
}

// certSummaryJSON is the lighter CertificateSummary used in list responses.
type certSummaryJSON struct {
	CertificateArn          string   `json:"CertificateArn"`
	DomainName              string   `json:"DomainName"`
	SubjectAlternativeNames []string `json:"SubjectAlternativeNameSummaries,omitempty"`
	Status                  string   `json:"Status,omitempty"`
	Type                    string   `json:"Type,omitempty"`
	KeyAlgorithm            string   `json:"KeyAlgorithm,omitempty"`
	InUse                   bool     `json:"InUse"`
	Exported                bool     `json:"Exported"`
}

func certToSummary(c *acmdriver.Certificate) certSummaryJSON {
	return certSummaryJSON{
		CertificateArn:          c.ARN,
		DomainName:              c.DomainName,
		SubjectAlternativeNames: c.SubjectAlternativeNames,
		Status:                  c.Status,
		Type:                    c.Type,
		KeyAlgorithm:            c.KeyAlgorithm,
		InUse:                   len(c.InUseBy) > 0,
	}
}

// --- request shapes ---

type requestCertificateRequest struct {
	DomainName              string           `json:"DomainName"`
	SubjectAlternativeNames []string         `json:"SubjectAlternativeNames"`
	ValidationMethod        string           `json:"ValidationMethod"`
	KeyAlgorithm            string           `json:"KeyAlgorithm"`
	IdempotencyToken        string           `json:"IdempotencyToken"`
	Options                 *certOptionsJSON `json:"Options"`
	Tags                    []tag            `json:"Tags"`
}

type importCertificateRequest struct {
	CertificateArn   string `json:"CertificateArn"`
	Certificate      []byte `json:"Certificate"`
	PrivateKey       []byte `json:"PrivateKey"`
	CertificateChain []byte `json:"CertificateChain"`
	Tags             []tag  `json:"Tags"`
}

type certArnRequest struct {
	CertificateArn string `json:"CertificateArn"`
}

type listFiltersJSON struct {
	KeyTypes []string `json:"keyTypes"`
}

type listCertificatesRequest struct {
	CertificateStatuses []string         `json:"CertificateStatuses"`
	Includes            *listFiltersJSON `json:"Includes"`
	MaxItems            *int32           `json:"MaxItems"`
	NextToken           string           `json:"NextToken"`
}

func (r *listCertificatesRequest) keyTypes() []string {
	if r.Includes == nil {
		return nil
	}

	return r.Includes.KeyTypes
}

type exportCertificateRequest struct {
	CertificateArn string `json:"CertificateArn"`
	Passphrase     []byte `json:"Passphrase"`
}

type updateOptionsRequest struct {
	CertificateArn string          `json:"CertificateArn"`
	Options        certOptionsJSON `json:"Options"`
}

type revokeCertificateRequest struct {
	CertificateArn   string `json:"CertificateArn"`
	RevocationReason string `json:"RevocationReason"`
}

type addTagsRequest struct {
	CertificateArn string `json:"CertificateArn"`
	Tags           []tag  `json:"Tags"`
}

type removeTagsRequest struct {
	CertificateArn string `json:"CertificateArn"`
	Tags           []tag  `json:"Tags"`
}

type putAccountConfigRequest struct {
	ExpiryEvents     *expiryEventsJSON `json:"ExpiryEvents"`
	IdempotencyToken string            `json:"IdempotencyToken"`
}

type expiryEventsJSON struct {
	DaysBeforeExpiry int32 `json:"DaysBeforeExpiry"`
}

// --- response shapes ---

type requestCertificateResponse struct {
	CertificateArn string `json:"CertificateArn"`
}

type describeCertificateResponse struct {
	Certificate certificateDetailJSON `json:"Certificate"`
}

type listCertificatesResponse struct {
	CertificateSummaryList []certSummaryJSON `json:"CertificateSummaryList"`
	NextToken              string            `json:"NextToken,omitempty"`
}

type getCertificateResponse struct {
	Certificate      string `json:"Certificate"`
	CertificateChain string `json:"CertificateChain,omitempty"`
}

type exportCertificateResponse struct {
	Certificate      string `json:"Certificate"`
	CertificateChain string `json:"CertificateChain,omitempty"`
	PrivateKey       string `json:"PrivateKey"`
}

type listTagsResponse struct {
	Tags []tag `json:"Tags"`
}

type revokeCertificateResponse struct {
	CertificateArn string `json:"CertificateArn"`
}

type getAccountConfigResponse struct {
	ExpiryEvents *expiryEventsJSON `json:"ExpiryEvents,omitempty"`
}
