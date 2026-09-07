package certificatemanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// managedProvisioningState is the state a managed certificate is minted with at
// create. Real Certificate Manager provisions a managed certificate through
// PROVISIONING before (given valid DNS authorization) reaching ACTIVE; CloudEmu
// has no issuance data plane, so it reports a stable PROVISIONING. The value is
// output-only/computed in both the API and the Terraform schema, so a stable
// value never drifts a refresh.
const managedProvisioningState = "PROVISIONING"

// acmeChallengePrefix is the DNS label a Certificate Manager DNS authorization's
// validation record is published under, per the real API's dnsResourceRecord.
const acmeChallengePrefix = "_acme-challenge."

// errCertificateBothTypes and errCertificateNoType surface the certificate
// managed/self_managed oneof violations as a 400, matching the real API.
var (
	errCertificateBothTypes = errors.New("certificate must set exactly one of managed or self_managed, not both")
	errCertificateNoType    = errors.New("certificate must set one of managed or self_managed")
)

// validateCertificate enforces the certificate managed/self_managed oneof:
// exactly one must be present, matching the real API (a 400 otherwise).
func validateCertificate(fields map[string]json.RawMessage) error {
	_, hasManaged := nonNull(fields, "managed")
	_, hasSelf := nonNull(fields, "selfManaged")

	switch {
	case hasManaged && hasSelf:
		return errCertificateBothTypes
	case !hasManaged && !hasSelf:
		return errCertificateNoType
	default:
		return nil
	}
}

// seedCertificate injects the output-only managed.state so a GET reports a
// stable provisioning state (self-managed certificates carry no state). A
// caller-supplied state is left untouched.
func seedCertificate(fields map[string]json.RawMessage) {
	managed, ok := objectField(fields, "managed")
	if !ok {
		return
	}

	if _, has := managed["state"]; has {
		return
	}

	managed["state"] = json.RawMessage(`"` + managedProvisioningState + `"`)
	putObject(fields, "managed", managed)
}

// stripCertificate removes a self-managed certificate's write-only
// pemPrivateKey from a rendered resource, matching the real API, which stores
// but never echoes the private key (the Terraform provider treats it as a
// sensitive input it never reads back, so echoing it would drift).
func stripCertificate(fields map[string]json.RawMessage) {
	self, ok := objectField(fields, "selfManaged")
	if !ok {
		return
	}

	if _, has := self["pemPrivateKey"]; !has {
		return
	}

	delete(self, "pemPrivateKey")
	putObject(fields, "selfManaged", self)
}

// seedDNSAuthorization mints the output-only dnsResourceRecord{name,type,data}
// once, deterministically from the authorization's domain, so a GET reports it
// stably across refreshes. This is the classic Certificate Manager drift point:
// the record must be identical on the create response and every later read.
func seedDNSAuthorization(fields map[string]json.RawMessage) {
	if _, has := fields["dnsResourceRecord"]; has {
		return
	}

	var domain string
	if raw, ok := fields["domain"]; ok {
		_ = json.Unmarshal(raw, &domain)
	}

	if domain == "" {
		return
	}

	record := map[string]string{
		"name": acmeChallengePrefix + domain,
		"type": "CNAME",
		"data": challengeData(domain),
	}

	if raw, err := json.Marshal(record); err == nil {
		fields["dnsResourceRecord"] = raw
	}
}

// challengeData derives the stable CNAME target a DNS authorization's validation
// record points at, deterministically from the domain so it never changes across
// reads. The shape mirrors the real API's
// `<token>.<shard>.authorize.certificatemanager.goog.` target.
func challengeData(domain string) string {
	sum := sha256.Sum256([]byte(domain))
	h := hex.EncodeToString(sum[:])

	return h[:32] + "." + h[32:40] + ".authorize.certificatemanager.goog."
}

// nonNull reports whether key is present in fields with a non-null value.
func nonNull(fields map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	v, ok := fields[key]
	if !ok || len(v) == 0 || string(v) == "null" {
		return nil, false
	}

	return v, true
}

// objectField decodes a top-level body field as a JSON object, reporting
// ok=false when it is absent, null, or not an object.
func objectField(fields map[string]json.RawMessage, key string) (map[string]json.RawMessage, bool) {
	raw, ok := nonNull(fields, key)
	if !ok {
		return nil, false
	}

	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, false
	}

	return obj, true
}

// putObject re-marshals obj back into fields under key, leaving fields unchanged
// on a marshal error.
func putObject(fields map[string]json.RawMessage, key string, obj map[string]json.RawMessage) {
	if raw, err := json.Marshal(obj); err == nil {
		fields[key] = raw
	}
}
