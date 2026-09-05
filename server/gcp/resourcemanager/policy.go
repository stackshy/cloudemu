package resourcemanager

import "encoding/base64"

const (
	// iamEtagTag is the protobuf tag byte for field 1 (varint wire type). Real
	// cloudresourcemanager IAM policy etags are base64 of a single-field
	// protobuf message carrying a monotonically increasing version, e.g. "CAE="
	// decodes to tag 0x08 followed by the varint value 1. cloudemu mints the
	// same shape for GCS bucket and Pub/Sub IAM policies, so project policies
	// stay consistent with the rest of the surface.
	iamEtagTag = 0x08
	// iamEtagInitialVersion is the version reported for a project whose IAM
	// policy was never explicitly set (etag "CAE=").
	iamEtagInitialVersion = 1
)

// policy mirrors the cloudresourcemanager v1 Policy resource. Every field is
// stored verbatim so a getIamPolicy → modify → setIamPolicy round-trips
// bindings (with conditions) and audit configs unchanged — what
// google_project_iam_member / _binding / _policy / _audit_config all rely on.
type policy struct {
	Version      int           `json:"version,omitempty"`
	Bindings     []binding     `json:"bindings,omitempty"`
	AuditConfigs []auditConfig `json:"auditConfigs,omitempty"`
	Etag         string        `json:"etag,omitempty"`
}

type binding struct {
	Role      string     `json:"role"`
	Members   []string   `json:"members,omitempty"`
	Condition *condition `json:"condition,omitempty"`
}

type condition struct {
	Expression  string `json:"expression,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type auditConfig struct {
	Service         string           `json:"service,omitempty"`
	AuditLogConfigs []auditLogConfig `json:"auditLogConfigs,omitempty"`
}

type auditLogConfig struct {
	LogType         string   `json:"logType,omitempty"`
	ExemptedMembers []string `json:"exemptedMembers,omitempty"`
}

// getIamPolicyRequest carries the (ignored) requestedPolicyVersion option.
type getIamPolicyRequest struct {
	Options struct {
		RequestedPolicyVersion int `json:"requestedPolicyVersion,omitempty"`
	} `json:"options,omitempty"`
}

type setIamPolicyRequest struct {
	Policy     policy `json:"policy"`
	UpdateMask string `json:"updateMask,omitempty"`
}

type testIamPermissionsRequest struct {
	Permissions []string `json:"permissions,omitempty"`
}

type testIamPermissionsResponse struct {
	Permissions []string `json:"permissions,omitempty"`
}

const (
	// varintContinuationBit marks that a varint byte is followed by more bytes.
	varintContinuationBit = 0x80
	// varintPayloadMask extracts the 7 payload bits of one varint byte.
	varintPayloadMask = 0x7f
	// varintPayloadBits is how many payload bits one varint byte carries.
	varintPayloadBits = 7
)

// encodeEtag base64-encodes a protobuf field-1 varint carrying version,
// matching the etag shape real cloudresourcemanager (and cloudemu's GCS /
// Pub/Sub IAM) hands back.
func encodeEtag(version uint64) string {
	b := []byte{iamEtagTag}

	for version >= varintContinuationBit {
		b = append(b, byte(version&varintPayloadMask|varintContinuationBit)) //nolint:gosec // masked to one byte
		version >>= varintPayloadBits
	}

	b = append(b, byte(version))

	return base64.StdEncoding.EncodeToString(b)
}
