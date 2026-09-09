package kms

import (
	"encoding/base64"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Cloud KMS keyRings and cryptoKeys each carry an IAM policy. cloudemu does not
// enforce IAM; it stores the policy verbatim so a getIamPolicy → modify →
// setIamPolicy round-trips (what google_kms_*_iam_* Terraform resources rely
// on). Policies are keyed on the owning resource, not a separate store.

// iamState holds a resource's IAM policy plus a monotonic write counter that
// seeds successive etags.
type iamState struct {
	policy  *iamPolicyJSON
	version uint64
}

const (
	// iamEtagTag is the protobuf field-1 varint tag byte. Real IAM policy etags
	// are base64 of a single-field protobuf carrying a version, e.g. "CAE="
	// decodes to tag 0x08 then varint 1 — the same shape cloudemu mints for its
	// other GCP IAM policies.
	iamEtagTag = 0x08
	// iamEtagInitialVersion is the version an unset policy reports (etag "CAE=").
	iamEtagInitialVersion = 1

	varintContinuationBit = 0x80
	varintPayloadMask     = 0x7f
	varintPayloadBits     = 7
)

// encodeIAMEtag base64-encodes a protobuf field-1 varint carrying version.
func encodeIAMEtag(version uint64) string {
	b := []byte{iamEtagTag}

	for version >= varintContinuationBit {
		b = append(b, byte(version&varintPayloadMask|varintContinuationBit)) //nolint:gosec // masked to one byte
		version >>= varintPayloadBits
	}

	b = append(b, byte(version))

	return base64.StdEncoding.EncodeToString(b)
}

// iamHolder resolves the IAM state for the resource a route addresses (a
// keyRing or a cryptoKey). Callers hold s.mu.
func (s *store) iamHolder(rt *route) (*iamState, error) {
	if rt.kind == kindCryptoKey {
		ck, err := s.findCryptoKey(rt)
		if err != nil {
			return nil, err
		}

		return &ck.iam, nil
	}

	kr, err := s.findKeyRing(rt)
	if err != nil {
		return nil, err
	}

	return &kr.iam, nil
}

func (s *store) getIAMPolicy(rt *route) (iamPolicyJSON, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	h, err := s.iamHolder(rt)
	if err != nil {
		return iamPolicyJSON{}, err
	}

	if h.policy == nil {
		return iamPolicyJSON{Version: 1, Etag: encodeIAMEtag(iamEtagInitialVersion)}, nil
	}

	return *h.policy, nil
}

func (s *store) setIAMPolicy(rt *route, pol iamPolicyJSON) (iamPolicyJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h, err := s.iamHolder(rt)
	if err != nil {
		return iamPolicyJSON{}, err
	}

	if h.policy != nil && pol.Etag != h.policy.Etag {
		return iamPolicyJSON{}, cerrors.New(cerrors.FailedPrecondition,
			"there were concurrent policy changes; please retry the whole read-modify-write with the new etag")
	}

	if pol.Version == 0 {
		pol.Version = 1
	}

	// Advance past the initial version reported by an unset-policy read so the
	// first write mints a distinct etag.
	if h.version == 0 {
		h.version = iamEtagInitialVersion
	}

	h.version++
	pol.Etag = encodeIAMEtag(h.version)
	h.policy = &pol

	return pol, nil
}
