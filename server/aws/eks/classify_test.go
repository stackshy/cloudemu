package eks

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassify(t *testing.T) {
	const (
		c    = "/clusters"
		role = "arn%3Aaws%3Aiam%3A%3A123456789012%3Arole%2Fdev"
		arn  = "arn:aws:iam::123456789012:role/dev"
		pol  = "arn%3Aaws%3Aeks%3A%3Aaws%3Acluster-access-policy%2FAmazonEKSViewPolicy"
	)

	cases := []struct {
		method, target       string
		op                   opID
		cluster, child, poli string
		fail                 failKind
	}{
		{method: "GET", target: "/access-policies", op: opListAccessPolicies},
		{method: "GET", target: "/addons/supported-versions", op: opDescribeAddonVersions},
		{method: "GET", target: "/addons/configuration-schemas", op: opDescribeAddonConfiguration},
		{method: "POST", target: "/access-policies", op: opUnknown, fail: failMethod},

		{method: "POST", target: "/tags/arn:aws:eks:us-east-1:1:cluster/a", op: opTagResource},
		{method: "DELETE", target: "/tags/arn:aws:eks:us-east-1:1:cluster/a?tagKeys=k", op: opUntagResource},
		{method: "GET", target: "/tags/arn:aws:eks:us-east-1:1:cluster/a", op: opListTagsForResource},
		{method: "PUT", target: "/tags/arn:aws:eks:us-east-1:1:cluster/a", op: opUnknown, fail: failTagsMethod},

		{method: "POST", target: c, op: opCreateCluster},
		{method: "GET", target: c + "/", op: opListClusters},
		{method: "PUT", target: c, op: opUnknown, fail: failMethod},
		{method: "GET", target: c + "/a", op: opDescribeCluster, cluster: "a"},
		{method: "DELETE", target: c + "/a", op: opDeleteCluster, cluster: "a"},
		{method: "POST", target: c + "/a", op: opUnknown, cluster: "a", fail: failMethod},
		{method: "POST", target: c + "/a/update-config", op: opUpdateClusterConfig, cluster: "a"},
		{method: "GET", target: c + "/a/update-config", op: opUnknown, cluster: "a", fail: failMethod},
		{method: "POST", target: c + "/a/updates", op: opUpdateClusterVersion, cluster: "a"},
		{method: "GET", target: c + "/a/updates?nodegroupName=n", op: opListUpdates, cluster: "a"},
		{method: "GET", target: c + "/a/updates/u1", op: opDescribeUpdate, cluster: "a", child: "u1"},
		{method: "GET", target: c + "/a/other", op: opUnknown, cluster: "a", fail: failNotFound},

		{method: "POST", target: c + "/a/node-groups", op: opCreateNodegroup, cluster: "a"},
		{method: "GET", target: c + "/a/node-groups", op: opListNodegroups, cluster: "a"},
		{method: "GET", target: c + "/a/node-groups/n", op: opDescribeNodegroup, cluster: "a", child: "n"},
		{method: "DELETE", target: c + "/a/node-groups/n", op: opDeleteNodegroup, cluster: "a", child: "n"},
		{method: "POST", target: c + "/a/node-groups/n/update-config", op: opUpdateNodegroupConfig, cluster: "a", child: "n"},
		{method: "POST", target: c + "/a/node-groups/n/update-version", op: opUpdateNodegroupVersion, cluster: "a", child: "n"},
		{method: "GET", target: c + "/a/node-groups/n/update-config", op: opUnknown, cluster: "a", child: "n", fail: failMethod},
		{method: "POST", target: c + "/a/node-groups/n/other", op: opUnknown, cluster: "a", child: "n", fail: failNotFound},

		{method: "POST", target: c + "/a/fargate-profiles", op: opCreateFargateProfile, cluster: "a"},
		{method: "GET", target: c + "/a/fargate-profiles/f", op: opDescribeFargateProfile, cluster: "a", child: "f"},
		{method: "DELETE", target: c + "/a/fargate-profiles/f", op: opDeleteFargateProfile, cluster: "a", child: "f"},

		{method: "POST", target: c + "/a/addons", op: opCreateAddon, cluster: "a"},
		{method: "GET", target: c + "/a/addons", op: opListAddons, cluster: "a"},
		{method: "GET", target: c + "/a/addons/vpc-cni", op: opDescribeAddon, cluster: "a", child: "vpc-cni"},
		{method: "POST", target: c + "/a/addons/vpc-cni/update", op: opUpdateAddon, cluster: "a", child: "vpc-cni"},
		{method: "DELETE", target: c + "/a/addons/vpc-cni", op: opDeleteAddon, cluster: "a", child: "vpc-cni"},

		{method: "POST", target: c + "/a/access-entries", op: opCreateAccessEntry, cluster: "a"},
		{method: "GET", target: c + "/a/access-entries", op: opListAccessEntries, cluster: "a"},
		{method: "GET", target: c + "/a/access-entries/" + role, op: opDescribeAccessEntry, cluster: "a", child: arn},
		{method: "POST", target: c + "/a/access-entries/" + role, op: opUpdateAccessEntry, cluster: "a", child: arn},
		{method: "DELETE", target: c + "/a/access-entries/" + role, op: opDeleteAccessEntry, cluster: "a", child: arn},
		{method: "POST", target: c + "/a/access-entries/" + role + "/access-policies", op: opAssociateAccessPolicy, cluster: "a", child: arn},
		{method: "GET", target: c + "/a/access-entries/" + role + "/access-policies", op: opListAssociatedAccessPolicies,
			cluster: "a", child: arn},
		{method: "DELETE", target: c + "/a/access-entries/" + role + "/access-policies/" + pol, op: opDisassociateAccessPolicy,
			cluster: "a", child: arn, poli: "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy"},
		{method: "GET", target: c + "/a/access-entries/" + role + "/access-policies/" + pol, op: opUnknown,
			cluster: "a", child: arn, poli: "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy", fail: failMethod},
		{method: "DELETE", target: c + "/a/node-groups/n/x/y", op: opUnknown, cluster: "a", child: "n", poli: "y", fail: failNotFound},
		{method: "GET", target: c + "/a/b/c/d/e/f", op: opUnknown, fail: failNotFound},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.target, http.NoBody)

		op, a := classify(r)
		if op != tc.op || a.cluster != tc.cluster || a.child != tc.child || a.policyARN != tc.poli ||
			(op == opUnknown && a.fail != tc.fail) {
			t.Errorf("%s %s: got op %q cluster %q child %q policy %q fail %d",
				tc.method, tc.target, op, a.cluster, a.child, a.policyARN, a.fail)
		}
	}
}

func TestParseTagARN(t *testing.T) {
	const pre = "arn:aws:eks:us-east-1:123456789012:"

	cases := []struct {
		arn  string
		want tagRef
	}{
		{pre + "cluster/c", tagRef{region: "us-east-1", account: "123456789012", kind: "cluster", cluster: "c"}},
		{pre + "nodegroup/c/ng", tagRef{region: "us-east-1", account: "123456789012", kind: "nodegroup", cluster: "c", name: "ng"}},
		{pre + "nodegroup/c/ng/uuid", tagRef{region: "us-east-1", account: "123456789012", kind: "nodegroup", cluster: "c", name: "ng"}},
		{pre + "fargateprofile/c/fp", tagRef{region: "us-east-1", account: "123456789012", kind: "fargateprofile", cluster: "c", name: "fp"}},
		{pre + "addon/c/vpc-cni/id", tagRef{region: "us-east-1", account: "123456789012", kind: "addon", cluster: "c", name: "vpc-cni"}},
		{pre + "access-entry/c/role/123456789012/r/id", tagRef{region: "us-east-1", account: "123456789012", kind: "access-entry", cluster: "c"}},
		{pre + "cluster/c/extra", tagRef{}},
		{pre + "nodegroup/c", tagRef{}},
		{pre + "podidentityassociation/c/a", tagRef{}},
		{"arn:aws:eks::123456789012:cluster/c", tagRef{}},
		{"arn:aws:s3:::b", tagRef{}},
		{"c", tagRef{}},
	}

	for _, tc := range cases {
		if got := parseTagARN(tc.arn); got != tc.want {
			t.Errorf("%q: got %+v, want %+v", tc.arn, got, tc.want)
		}
	}
}
