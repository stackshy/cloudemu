package iam

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// awsManagedPolicyPrefix marks the policies AWS itself publishes. They exist
// in every account without anyone creating them, which is why callers attach
// them without a preceding CreatePolicy.
const awsManagedPolicyPrefix = "arn:aws:iam::aws:policy/"

// awsManagedPolicyDocument is the document reported for a managed policy. The
// emulator does not evaluate policy documents, and reproducing the real
// contents of these policies would be fiction presented as fact — so this is
// an explicit placeholder rather than a plausible-looking guess.
const awsManagedPolicyDocument = `{"Version":"2012-10-17","Statement":[]}`

// awsManagedPolicies is the catalog of AWS-published policies this emulator
// recognizes, keyed by the part of the ARN after the prefix (so the path is
// included where the real policy has one).
//
// AWS publishes a finite, fixed set, and an ARN outside it is NoSuchEntity in
// a real account. Honoring any well-formed ARN would accept typos and
// invented names — the emulator would happily attach
// AmazonEKSClusterPolicyy — so unknown names are rejected. That makes a
// missing entry a loud, one-line fix here rather than a silent divergence
// from the account the caller will really run against.
//
//nolint:gochecknoglobals // static lookup table
var awsManagedPolicies = map[string]bool{
	// Broad access
	"AdministratorAccess": true,
	"PowerUserAccess":     true,
	"ReadOnlyAccess":      true,

	// EC2 / VPC / autoscaling
	"AmazonEC2FullAccess":            true,
	"AmazonEC2ReadOnlyAccess":        true,
	"AmazonVPCFullAccess":            true,
	"AmazonVPCReadOnlyAccess":        true,
	"AutoScalingFullAccess":          true,
	"ElasticLoadBalancingFullAccess": true,

	// EKS
	"AmazonEKSClusterPolicy":         true,
	"AmazonEKSWorkerNodePolicy":      true,
	"AmazonEKSServicePolicy":         true,
	"AmazonEKS_CNI_Policy":           true,
	"AmazonEKSVPCResourceController": true,

	// ECR / ECS
	"AmazonEC2ContainerRegistryReadOnly":            true,
	"AmazonEC2ContainerRegistryPowerUser":           true,
	"AmazonEC2ContainerRegistryFullAccess":          true,
	"AmazonECS_FullAccess":                          true,
	"service-role/AmazonECSTaskExecutionRolePolicy": true,

	// Systems Manager
	"AmazonSSMManagedInstanceCore":         true,
	"AmazonSSMFullAccess":                  true,
	"AmazonSSMReadOnlyAccess":              true,
	"service-role/AmazonEC2RoleforSSM":     true,
	"service-role/AmazonSSMAutomationRole": true,

	// Storage / databases
	"AmazonS3FullAccess":                    true,
	"AmazonS3ReadOnlyAccess":                true,
	"AmazonRDSFullAccess":                   true,
	"AmazonRDSReadOnlyAccess":               true,
	"AmazonDynamoDBFullAccess":              true,
	"AmazonDynamoDBReadOnlyAccess":          true,
	"AmazonElastiCacheFullAccess":           true,
	"service-role/AmazonEBSCSIDriverPolicy": true,
	"SecretsManagerReadWrite":               true,

	// Lambda
	"AWSLambda_FullAccess":                         true,
	"service-role/AWSLambdaBasicExecutionRole":     true,
	"service-role/AWSLambdaVPCAccessExecutionRole": true,

	// Observability / messaging / DNS
	"CloudWatchAgentServerPolicy": true,
	"CloudWatchFullAccess":        true,
	"CloudWatchLogsFullAccess":    true,
	"CloudWatchReadOnlyAccess":    true,
	"AWSXRayDaemonWriteAccess":    true,
	"AmazonSQSFullAccess":         true,
	"AmazonSNSFullAccess":         true,
	"AmazonRoute53FullAccess":     true,

	// IAM
	"IAMFullAccess":     true,
	"IAMReadOnlyAccess": true,
}

// isAWSManagedPolicyARN reports whether the ARN names a policy in the
// catalog above.
func isAWSManagedPolicyARN(arn string) bool {
	if !strings.HasPrefix(arn, awsManagedPolicyPrefix) {
		return false
	}

	return awsManagedPolicies[strings.TrimPrefix(arn, awsManagedPolicyPrefix)]
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

	name := strings.TrimPrefix(arn, awsManagedPolicyPrefix)

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
		PolicyDocument: awsManagedPolicyDocument,
		Description:    "AWS managed policy",
	}
	seedInitialVersion(p, awsManagedPolicyDocument, m.opts.Clock.Now().UTC().Format(timeFormat))

	m.policies.SetIfAbsent(arn, p)

	return true
}
