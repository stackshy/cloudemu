package kms

import "github.com/stackshy/cloudemu/v2/providers/gcp/resourceiam"

// Cloud KMS keyRings and cryptoKeys each carry an IAM policy. cloudemu does not
// enforce IAM; it stores the policy verbatim so a getIamPolicy → modify →
// setIamPolicy round-trips (what google_kms_*_iam_* Terraform resources rely
// on). Policies are keyed on the owning resource, not a separate store. Etags
// follow the shared resourceiam scheme so they match every other GCP policy.

// iamState holds a resource's IAM policy, nil until the first set.
type iamState struct {
	policy *iamPolicyJSON
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
		return iamPolicyJSON{Version: 1, Etag: resourceiam.InitialEtag()}, nil
	}

	return *h.policy, nil
}

// setIAMPolicy stores pol. An empty etag is a blind overwrite; an etag that no
// longer matches the stored policy is resourceiam.ErrAborted (real 409
// ABORTED).
func (s *store) setIAMPolicy(rt *route, pol iamPolicyJSON) (iamPolicyJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h, err := s.iamHolder(rt)
	if err != nil {
		return iamPolicyJSON{}, err
	}

	cur := resourceiam.InitialEtag()
	if h.policy != nil {
		cur = h.policy.Etag
	}

	if pol.Etag != "" && pol.Etag != cur {
		return iamPolicyJSON{}, resourceiam.ErrAborted
	}

	if pol.Version == 0 {
		pol.Version = 1
	}

	pol.Etag = resourceiam.NextEtag(cur)
	h.policy = &pol

	return pol, nil
}
