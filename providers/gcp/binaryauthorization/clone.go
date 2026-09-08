package binaryauthorization

import "github.com/stackshy/cloudemu/v2/services/binaryauthorization/driver"

// clonePolicy returns a deep copy of a policy so the store never hands out a
// pointer into its own state (copy-on-write at the driver boundary). The opaque
// JSON blocks are copied so a caller can never mutate stored bytes.
func clonePolicy(p *driver.Policy) *driver.Policy {
	cp := *p
	cp.AdmissionWhitelistPatterns = cloneBytes(p.AdmissionWhitelistPatterns)
	cp.DefaultAdmissionRule = cloneBytes(p.DefaultAdmissionRule)
	cp.ClusterAdmissionRules = cloneBytes(p.ClusterAdmissionRules)

	return &cp
}

// cloneAttestor returns a deep copy of an attestor.
func cloneAttestor(a *driver.Attestor) *driver.Attestor {
	cp := *a
	cp.UserOwnedGrafeasNote = cloneNote(a.UserOwnedGrafeasNote)
	cp.IAMPolicy = clonePolicyIAM(a.IAMPolicy)

	return &cp
}

func cloneNote(in *driver.UserOwnedGrafeasNote) *driver.UserOwnedGrafeasNote {
	if in == nil {
		return nil
	}

	cp := *in
	cp.PublicKeys = cloneBytes(in.PublicKeys)

	return &cp
}

// clonePolicyIAM deep-copies an IAM policy so stored and returned values don't
// share backing slices.
func clonePolicyIAM(p *driver.IAMPolicy) *driver.IAMPolicy {
	if p == nil {
		return nil
	}

	out := &driver.IAMPolicy{Version: p.Version, Etag: p.Etag}
	for _, b := range p.Bindings {
		out.Bindings = append(out.Bindings, driver.IAMBinding{Role: b.Role, Members: cloneStrings(b.Members)})
	}

	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}

	out := make([]byte, len(in))
	copy(out, in)

	return out
}
