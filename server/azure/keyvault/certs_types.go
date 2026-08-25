package keyvault

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// certBundleJSON is a full Key Vault certificate bundle: identifiers, the DER
// certificate (cer, standard base64), the SHA-1 thumbprint (x5t, base64url),
// attributes and the verbatim policy.
type certBundleJSON struct {
	ID          string            `json:"id"`
	KID         string            `json:"kid,omitempty"`
	SID         string            `json:"sid,omitempty"`
	X5T         string            `json:"x5t,omitempty"`
	CER         string            `json:"cer,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Attributes  attributesJSON    `json:"attributes"`
	Policy      json.RawMessage   `json:"policy,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// certItemJSON is a list entry: identifier, thumbprint, attributes and tags.
type certItemJSON struct {
	ID         string            `json:"id"`
	X5T        string            `json:"x5t,omitempty"`
	Attributes attributesJSON    `json:"attributes"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type certListResponseJSON struct {
	Value    []certItemJSON `json:"value"`
	NextLink *string        `json:"nextLink"`
}

// deletedCertBundleJSON extends a bundle with soft-delete scheduling.
type deletedCertBundleJSON struct {
	certBundleJSON

	RecoveryID         string `json:"recoveryId"`
	DeletedDate        int64  `json:"deletedDate,omitempty"`
	ScheduledPurgeDate int64  `json:"scheduledPurgeDate,omitempty"`
}

// deletedCertItemJSON is a deleted-list entry.
type deletedCertItemJSON struct {
	certItemJSON

	RecoveryID         string `json:"recoveryId"`
	DeletedDate        int64  `json:"deletedDate,omitempty"`
	ScheduledPurgeDate int64  `json:"scheduledPurgeDate,omitempty"`
}

type deletedCertListResponseJSON struct {
	Value    []deletedCertItemJSON `json:"value"`
	NextLink *string               `json:"nextLink"`
}

// certOperationJSON is the CertificateOperation returned by create and pending.
type certOperationJSON struct {
	ID                    string          `json:"id"`
	Issuer                json.RawMessage `json:"issuer,omitempty"`
	CancellationRequested bool            `json:"cancellation_requested"`
	Status                string          `json:"status"`
	StatusDetails         string          `json:"status_details,omitempty"`
	RequestID             string          `json:"request_id,omitempty"`
	Target                string          `json:"target,omitempty"`
}

// createCertRequest is the CreateCertificate body. Policy is kept as raw JSON so
// it round-trips verbatim on GetCertificate; the parsed sub-fields drive
// self-signed generation.
type createCertRequest struct {
	Policy     json.RawMessage          `json:"policy"`
	Attributes *setSecretAttributesJSON `json:"attributes"`
	Tags       map[string]string        `json:"tags"`
}

// certPolicyParse is the subset of a certificate policy the emulator reads to
// generate a self-signed certificate.
type certPolicyParse struct {
	SecretProps *struct {
		ContentType string `json:"contentType"`
	} `json:"secret_props"`
	X509Props *struct {
		Subject string `json:"subject"`
		Sans    *struct {
			DNSNames []string `json:"dns_names"`
		} `json:"sans"`
		ValidityMonths int `json:"validity_months"`
	} `json:"x509_props"`
	Issuer json.RawMessage `json:"issuer"`
}

// certID builds "{vault}/certificates/{name}[/{version}]".
func certID(r *http.Request, name, version string) string {
	id := vaultBaseURL(r) + certsPrefix + "/" + name
	if version != "" {
		id += "/" + version
	}

	return id
}

func deletedCertID(r *http.Request, name string) string {
	return vaultBaseURL(r) + deletedCertsPrefix + "/" + name
}

func certAttributes(c *secretsdriver.KVCertificate) attributesJSON {
	return attributesJSON{
		Enabled:         c.Enabled,
		Created:         c.Created,
		Updated:         c.Updated,
		Expires:         c.Expires,
		NotBefore:       c.NotBefore,
		RecoverableDays: recoverableDays,
		RecoveryLevel:   recoveryLevel,
	}
}

func toCertBundle(r *http.Request, c *secretsdriver.KVCertificate) certBundleJSON {
	b := certBundleJSON{
		ID:          certID(r, c.Name, c.Version),
		KID:         keyID(r, c.Name, c.Version),
		SID:         secretID(r, c.Name, c.Version),
		ContentType: c.ContentType,
		Attributes:  certAttributes(c),
		Tags:        c.Tags,
	}

	if len(c.Thumbprint) > 0 {
		b.X5T = base64.RawURLEncoding.EncodeToString(c.Thumbprint)
	}

	if len(c.CER) > 0 {
		b.CER = base64.StdEncoding.EncodeToString(c.CER)
	}

	if len(c.PolicyRaw) > 0 {
		b.Policy = json.RawMessage(c.PolicyRaw)
	}

	return b
}

func toCertItem(r *http.Request, c *secretsdriver.KVCertificate) certItemJSON {
	item := certItemJSON{
		ID:         certID(r, c.Name, c.Version),
		Attributes: certAttributes(c),
		Tags:       c.Tags,
	}

	if len(c.Thumbprint) > 0 {
		item.X5T = base64.RawURLEncoding.EncodeToString(c.Thumbprint)
	}

	return item
}

func toDeletedCertBundle(r *http.Request, d *secretsdriver.KVDeletedCertificate) deletedCertBundleJSON {
	return deletedCertBundleJSON{
		certBundleJSON:     toCertBundle(r, &d.KVCertificate),
		RecoveryID:         deletedCertID(r, d.Name),
		DeletedDate:        d.DeletedDate,
		ScheduledPurgeDate: d.ScheduledPurgeDate,
	}
}

func toDeletedCertItem(r *http.Request, d *secretsdriver.KVDeletedCertificate) deletedCertItemJSON {
	return deletedCertItemJSON{
		certItemJSON:       toCertItem(r, &d.KVCertificate),
		RecoveryID:         deletedCertID(r, d.Name),
		DeletedDate:        d.DeletedDate,
		ScheduledPurgeDate: d.ScheduledPurgeDate,
	}
}
