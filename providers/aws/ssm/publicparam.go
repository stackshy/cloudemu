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

// awsPublicAMIParamPrefixes are the published parameter trees whose leaves
// carry an AMI id. A path under one of these ending in /ami-id resolves; the
// value is synthesized because a real AMI id changes as the publisher rebuilds
// the image, so there is no fixed answer to copy.
//
// Restricting to known trees matters: AWS answers ParameterNotFound for a path
// it does not publish, and accepting anything ending in /ami-id would resolve
// typos and invented distros — the caller would launch an instance from an
// image that does not exist anywhere but here.
//
//nolint:gochecknoglobals // static lookup table
var awsPublicAMIParamPrefixes = []string{
	"/aws/service/ami-amazon-linux-latest/",
	"/aws/service/ami-windows-latest/",
	"/aws/service/canonical/ubuntu/",
	"/aws/service/debian/release/",
	"/aws/service/suse/",
	"/aws/service/marketplace/prod-",
	"/aws/service/bottlerocket/",
	"/aws/service/eks/optimized-ami/",
}

// The trailing segments that carry an image id. The EKS-optimized tree names
// its leaf image_id rather than ami-id, so matching only the latter would leave
// every EKS AMI lookup unresolved.
var amiIDLeaves = []string{"/ami-id", "/image_id"} //nolint:gochecknoglobals // static lookup table

// isPublicAMIParam reports whether the name is an AMI-id parameter in a tree
// AWS publishes.
func isPublicAMIParam(name string) bool {
	if !strings.HasPrefix(name, awsPublicParamPrefix) {
		return false
	}

	leaf := false

	for _, suffix := range amiIDLeaves {
		if strings.HasSuffix(name, suffix) {
			leaf = true
			break
		}
	}

	if !leaf {
		return false
	}

	for _, prefix := range awsPublicAMIParamPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}

// ensurePublicParameter materializes an AWS-published parameter on first read
// and reports whether the name is now known.
//
// Only the AMI-id family is synthesized. Other published parameters carry
// payloads that cannot be derived — the ECS-optimized family holds a JSON blob
// — and inventing those would be fiction presented as fact, so they stay
// NotFound.
func (m *Mock) ensurePublicParameter(name string) bool {
	if m.params.Has(name) {
		return true
	}

	if !isPublicAMIParam(name) {
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
