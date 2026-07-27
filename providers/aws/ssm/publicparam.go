package ssm

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// awsPublicParamPrefix marks the parameters AWS itself publishes. They are
// readable from every account without anyone putting them, which is why
// callers read them directly.
const awsPublicParamPrefix = "/aws/service/"

// amiIDLeaf is the trailing segment of the public parameters that carry an AMI
// id — the family callers use to resolve "current image for this distro"
// without pinning an id that goes stale.
const amiIDLeaf = "/ami-id"

// ensurePublicParameter materialises an AWS-published parameter on first read
// and reports whether the name is now known.
//
// Deliberately narrow: only the /ami-id family is synthesised, because an AMI
// id has a well-defined shape that can be generated truthfully. Other
// /aws/service/ parameters carry payloads whose contents cannot be derived
// (the ECS-optimised family holds a JSON blob, for instance), and inventing
// those would be fiction presented as fact — so they stay NotFound.
func (m *Mock) ensurePublicParameter(name string) bool {
	if m.params.Has(name) {
		return true
	}

	if !strings.HasPrefix(name, awsPublicParamPrefix) || !strings.HasSuffix(name, amiIDLeaf) {
		return false
	}

	now := m.now()

	pd := &paramData{
		name:        name,
		description: "AWS public parameter",
		tier:        "Standard",
		latest:      1,
		versions: []*version{{
			value:        syntheticAMIID(name),
			typ:          "String",
			dataType:     "text",
			version:      1,
			lastModified: now,
		}},
	}

	m.params.Set(name, pd)

	return true
}

// syntheticAMIID derives a stable AMI id from the parameter name, so repeated
// reads of the same parameter resolve to the same image and two different
// distros never collide on one id.
func syntheticAMIID(paramName string) string {
	sum := sha256.Sum256([]byte(paramName))

	return "ami-" + hex.EncodeToString(sum[:])[:17]
}
