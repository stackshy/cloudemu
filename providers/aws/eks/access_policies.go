package eks

import (
	"context"
	"sort"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// accessPolicyARNPrefix is the shared prefix of every EKS access policy ARN.
// Access policies are AWS owned, so the ARN has no region or account.
const accessPolicyARNPrefix = "arn:aws:eks::aws:cluster-access-policy/"

// accessPolicyDef is one entry of the fixed access policy catalog.
// serviceLinkedOnly marks policies AWS reserves for service-linked roles.
type accessPolicyDef struct {
	name              string
	serviceLinkedOnly bool
}

// accessPolicyCatalog lists the access policies real EKS publishes, taken
// from the EKS user guide "Review access policy permissions" page.
func accessPolicyCatalog() []accessPolicyDef {
	return []accessPolicyDef{
		{name: "AWSBackupFullAccessPolicyForBackup"},
		{name: "AWSBackupFullAccessPolicyForRestore"},
		{name: "AmazonAIOpsAssistantPolicy"},
		{name: "AmazonARCRegionSwitchScalingPolicy"},
		{name: "AmazonEKSACKPolicy"},
		{name: "AmazonEKSAdminPolicy"},
		{name: "AmazonEKSAdminViewPolicy"},
		{name: "AmazonEKSArgoCDClusterPolicy"},
		{name: "AmazonEKSArgoCDPolicy"},
		{name: "AmazonEKSAutoNodePolicy"},
		{name: "AmazonEKSBlockStorageClusterPolicy", serviceLinkedOnly: true},
		{name: "AmazonEKSBlockStoragePolicy", serviceLinkedOnly: true},
		{name: "AmazonEKSClusterAdminPolicy"},
		{name: "AmazonEKSClusterInsightsPolicy", serviceLinkedOnly: true},
		{name: "AmazonEKSComputeClusterPolicy", serviceLinkedOnly: true},
		{name: "AmazonEKSComputePolicy", serviceLinkedOnly: true},
		{name: "AmazonEKSEditPolicy"},
		{name: "AmazonEKSHybridPolicy", serviceLinkedOnly: true},
		{name: "AmazonEKSKROPolicy"},
		{name: "AmazonEKSLoadBalancingClusterPolicy"},
		{name: "AmazonEKSLoadBalancingPolicy"},
		{name: "AmazonEKSNetworkingClusterPolicy"},
		{name: "AmazonEKSNetworkingPolicy"},
		{name: "AmazonEKSPodIdentityPolicy", serviceLinkedOnly: true},
		{name: "AmazonEKSSecretAdminPolicy"},
		{name: "AmazonEKSSecretReaderPolicy"},
		{name: "AmazonEKSViewPolicy"},
	}
}

// lookupAccessPolicy finds a catalog policy by ARN.
func lookupAccessPolicy(arn string) (accessPolicyDef, bool) {
	for _, p := range accessPolicyCatalog() {
		if accessPolicyARNPrefix+p.name == arn {
			return p, true
		}
	}

	return accessPolicyDef{}, false
}

// ListAccessPolicies returns the access policy catalog sorted by name.
func (*Mock) ListAccessPolicies(_ context.Context) ([]eksdriver.AccessPolicy, error) {
	defs := accessPolicyCatalog()
	out := make([]eksdriver.AccessPolicy, 0, len(defs))

	for _, p := range defs {
		out = append(out, eksdriver.AccessPolicy{Name: p.name, ARN: accessPolicyARNPrefix + p.name})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}
