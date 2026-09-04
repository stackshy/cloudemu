package iam

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// awsManagedPolicyPrefix marks the policies AWS itself publishes. They exist
// in every account without anyone creating them, which is why callers attach
// them without a preceding CreatePolicy.
const awsManagedPolicyPrefix = "arn:aws:iam::aws:policy/"

// isAWSManagedPolicyARN reports whether the ARN names a policy in the
// catalog (see awsManagedPolicyDocuments in managed_policy_documents.go).
//
// AWS publishes a finite, fixed set, and an ARN outside it is NoSuchEntity in
// a real account. Honoring any well-formed ARN would accept typos and
// invented names — the emulator would happily attach
// AmazonEKSClusterPolicyy — so unknown names are rejected. That makes a
// missing entry a loud, one-line fix here rather than a silent divergence
// from the account the caller will really run against.
func isAWSManagedPolicyARN(arn string) bool {
	if !strings.HasPrefix(arn, awsManagedPolicyPrefix) {
		return false
	}

	_, ok := awsManagedPolicyDocuments[strings.TrimPrefix(arn, awsManagedPolicyPrefix)]

	return ok
}

// ensureAWSManagedPolicy materializes a recognized AWS-managed policy on first
// reference and reports whether the ARN is now known.
//
// Real accounts already have these, so requiring CreatePolicy first turns an
// ordinary AttachRolePolicy into NoSuchEntity. Materializing on demand keeps
// the catalog cheap — no policy exists until something asks for it.
func (m *Mock) ensureAWSManagedPolicy(arn string) bool {
	if m.policies.Has(arn) {
		return true
	}

	if !isAWSManagedPolicyARN(arn) {
		return false
	}

	catalogKey := strings.TrimPrefix(arn, awsManagedPolicyPrefix)
	document := awsManagedPolicyDocuments[catalogKey]

	name := catalogKey

	// Managed policies may be pathed (service-role/AmazonEBSCSIDriverPolicy);
	// the policy name is the last segment, the path everything before it.
	path := "/"
	if i := strings.LastIndex(name, "/"); i >= 0 {
		path = "/" + name[:i+1]
		name = name[i+1:]
	}

	// SetIfAbsent rather than Set: two concurrent first-references would
	// otherwise each materialize the policy and the second would overwrite the
	// first, handing out an ARN whose ID no longer matches what the earlier
	// caller was told.
	p := &policyData{
		Name:           name,
		ID:             idgen.GenerateID("ANPA"),
		ARN:            arn,
		Path:           path,
		PolicyDocument: document,
		Description:    "AWS managed policy",
	}
	seedInitialVersion(p, document, m.opts.Clock.Now().UTC().Format(timeFormat))

	m.policies.SetIfAbsent(arn, p)

	return true
}
