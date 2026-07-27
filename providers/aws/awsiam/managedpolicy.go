package awsiam

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// awsManagedPolicyPrefix marks the policies AWS itself publishes. They exist
// in every account without anyone creating them, which is exactly why callers
// attach them without a preceding CreatePolicy.
const awsManagedPolicyPrefix = "arn:aws:iam::aws:policy/"

// awsManagedPolicyDocument is the document reported for a materialised
// AWS-managed policy. The emulator does not evaluate policy documents, and
// inventing the real contents of AmazonSSMManagedInstanceCore et al. would be
// fiction presented as fact — so this is deliberately an explicit placeholder
// rather than a plausible-looking guess.
const awsManagedPolicyDocument = `{"Version":"2012-10-17","Statement":[]}`

// isAWSManagedPolicyARN reports whether the ARN names an AWS-managed policy.
func isAWSManagedPolicyARN(arn string) bool {
	return strings.HasPrefix(arn, awsManagedPolicyPrefix)
}

// ensureAWSManagedPolicy materialises an AWS-managed policy on first
// reference and reports whether the ARN is now known.
//
// Real AWS ships hundreds of these and every account has all of them, so
// requiring CreatePolicy first — as the emulator otherwise does — makes a
// perfectly ordinary AttachRolePolicy fail with NoSuchEntity. Seeding a fixed
// list instead would just move the failure to the first policy nobody thought
// to list, so any well-formed managed ARN is honoured.
func (m *Mock) ensureAWSManagedPolicy(arn string) bool {
	if m.policies.Has(arn) {
		return true
	}

	if !isAWSManagedPolicyARN(arn) {
		return false
	}

	name := strings.TrimPrefix(arn, awsManagedPolicyPrefix)
	if name == "" {
		return false
	}

	// Managed policies may be pathed (service-role/AmazonEC2RoleforSSM); the
	// policy name is the last segment and the path is everything before it.
	path := "/"
	if i := strings.LastIndex(name, "/"); i >= 0 {
		path = "/" + name[:i+1]
		name = name[i+1:]
	}

	p := &policyData{
		Name:           name,
		ID:             idgen.GenerateID("ANPA"),
		ARN:            arn,
		Path:           path,
		PolicyDocument: awsManagedPolicyDocument,
		Description:    "AWS managed policy",
	}
	seedInitialVersion(p, awsManagedPolicyDocument, m.opts.Clock.Now().UTC().Format(timeFormat))

	m.policies.Set(arn, p)

	return true
}
