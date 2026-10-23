package eks

import (
	"context"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestIAMChecks(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ctx := context.Background()

	cl, err := cloud.EKS.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name: "prod", RoleArn: "arn:aws:iam::123456789012:role/eks", Tags: map[string]string{"env": "prod"},
		AccessConfig: eksdriver.AccessConfigRequest{AuthenticationMode: "API"},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := cloud.EKS.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName: "prod", NodegroupName: "ng", NodeRole: "arn:aws:iam::123456789012:role/node",
		Subnets: []string{"subnet-1"}, Tags: map[string]string{"team": "a"},
	}); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	const principal = "arn:aws:iam::123456789012:role/dev"

	entry, err := cloud.EKS.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{
		ClusterName: "prod", PrincipalArn: principal, Username: "dev", KubernetesGroups: []string{"g1"},
	})
	if err != nil {
		t.Fatalf("CreateAccessEntry: %v", err)
	}

	parts := strings.SplitN(cl.ARN, ":", 6)
	s := awsauthz.Scope{Partition: "aws", Region: parts[3], AccountID: parts[4]}
	pre := "arn:aws:eks:" + s.Region + ":" + s.AccountID + ":"
	ng := pre + "nodegroup/prod/ng"
	esc := url.PathEscape(principal)

	type check struct{ action, resource string }

	cases := []struct {
		name, method, target, body string
		checks                     []check
		cond                       map[string]string
		unknown                    bool
	}{
		{name: "list clusters", method: "GET", target: "/clusters", checks: []check{{"eks:ListClusters", "*"}}},
		{name: "create cluster", method: "POST", target: "/clusters",
			body: `{"name":"c2","version":"1.35","resourcesVpcConfig":{"endpointPublicAccess":false},` +
				`"accessConfig":{"authenticationMode":"API","bootstrapClusterCreatorAdminPermissions":true},` +
				`"logging":{"clusterLogging":[{"types":["api","audit"],"enabled":true}]},"tags":{"k":"v"}}`,
			checks: []check{{"eks:CreateCluster", "*"}},
			cond: map[string]string{
				condKubernetesVersion: "1.35", condEndpointPublic: "false", condAuthMode: "API", condBootstrapAdmin: "true",
				condLoggingTypePrefix + "api": "true", condLoggingTypePrefix + "audit": "true",
				"aws:RequestTag/k": "v", condTagKeys: "k",
			}},
		{name: "create cluster with a bad body", method: "POST", target: "/clusters", body: `{`, unknown: true},
		{name: "describe cluster", method: "GET", target: "/clusters/prod", checks: []check{{"eks:DescribeCluster", cl.ARN}},
			cond: map[string]string{"aws:ResourceTag/env": "prod"}},
		{name: "update version", method: "POST", target: "/clusters/prod/updates", body: `{"version":"1.36"}`,
			checks: []check{{"eks:UpdateClusterVersion", cl.ARN}}, cond: map[string]string{condKubernetesVersion: "1.36"}},
		{name: "update config with tags", method: "POST", target: "/clusters/prod/update-config", body: `{"tags":{"a":"b"}}`,
			checks: []check{{"eks:UpdateClusterConfig", cl.ARN}, {"eks:TagResource", cl.ARN}}},
		{name: "list updates", method: "GET", target: "/clusters/prod/updates", checks: []check{{"eks:ListUpdates", cl.ARN}}},
		{name: "list nodegroup updates", method: "GET", target: "/clusters/prod/updates?nodegroupName=ng",
			checks: []check{{"eks:ListUpdates", ng}}},
		{name: "describe nodegroup update", method: "GET", target: "/clusters/prod/updates/u1?nodegroupName=ng",
			checks: []check{{"eks:DescribeUpdate", ng}}},
		{name: "describe add-on update", method: "GET", target: "/clusters/prod/updates/u1?addonName=vpc-cni",
			checks: []check{{"eks:DescribeUpdate", pre + "addon/prod/vpc-cni"}}},
		{name: "create nodegroup", method: "POST", target: "/clusters/prod/node-groups", body: `{"nodegroupName":"n2","tags":{"x":"y"}}`,
			checks: []check{{"eks:CreateNodegroup", cl.ARN}},
			cond:   map[string]string{"aws:RequestTag/x": "y", "aws:ResourceTag/env": "prod"}},
		{name: "describe nodegroup", method: "GET", target: "/clusters/prod/node-groups/ng", checks: []check{{"eks:DescribeNodegroup", ng}},
			cond: map[string]string{"aws:ResourceTag/team": "a"}},
		{name: "delete missing nodegroup", method: "DELETE", target: "/clusters/prod/node-groups/gone",
			checks: []check{{"eks:DeleteNodegroup", pre + "nodegroup/prod/gone"}}},
		{name: "update nodegroup version", method: "POST", target: "/clusters/prod/node-groups/ng/update-version",
			checks: []check{{"eks:UpdateNodegroupVersion", ng}}},
		{name: "describe fargate profile", method: "GET", target: "/clusters/prod/fargate-profiles/fp",
			checks: []check{{"eks:DescribeFargateProfile", pre + "fargateprofile/prod/fp"}}},
		{name: "update add-on", method: "POST", target: "/clusters/prod/addons/vpc-cni/update",
			checks: []check{{"eks:UpdateAddon", pre + "addon/prod/vpc-cni"}}},
		{name: "create access entry", method: "POST", target: "/clusters/prod/access-entries",
			body:   `{"principalArn":"arn:aws:iam::123456789012:role/ops","type":"STANDARD","username":"ops","kubernetesGroups":["a","b"]}`,
			checks: []check{{"eks:CreateAccessEntry", cl.ARN}},
			cond: map[string]string{condPrincipalARN: "arn:aws:iam::123456789012:role/ops", condAccessEntryType: "STANDARD",
				condUsername: "ops", condKubernetesGroups: "a" + iamdriver.ConditionValueSeparator + "b"}},
		{name: "describe access entry", method: "GET", target: "/clusters/prod/access-entries/" + esc,
			checks: []check{{"eks:DescribeAccessEntry", entry.ARN}},
			cond:   map[string]string{condClusterName: "prod", condPrincipalARN: principal, condUsername: "dev", condKubernetesGroups: "g1"}},
		{name: "delete missing access entry", method: "DELETE", target: "/clusters/prod/access-entries/" + url.PathEscape("arn:aws:iam::1:role/x"),
			checks: []check{{"eks:DeleteAccessEntry", pre + "access-entry/prod/*"}}},
		{name: "associate policy", method: "POST", target: "/clusters/prod/access-entries/" + esc + "/access-policies",
			body:   `{"policyArn":"arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy","accessScope":{"type":"namespace","namespaces":["ns"]}}`,
			checks: []check{{"eks:AssociateAccessPolicy", entry.ARN}},
			cond: map[string]string{condPolicyARN: "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy",
				condAccessScope: "namespace", condNamespaces: "ns"}},
		{name: "disassociate policy", method: "DELETE",
			target: "/clusters/prod/access-entries/" + esc + "/access-policies/" + url.PathEscape("arn:aws:eks::aws:cluster-access-policy/P"),
			checks: []check{{"eks:DisassociateAccessPolicy", entry.ARN}},
			cond:   map[string]string{condPolicyARN: "arn:aws:eks::aws:cluster-access-policy/P"}},
		{name: "list access policies", method: "GET", target: "/access-policies", checks: []check{{"eks:ListAccessPolicies", "*"}}},
		{name: "addon versions", method: "GET", target: "/addons/supported-versions", checks: []check{{"eks:DescribeAddonVersions", "*"}}},
		{name: "tag cluster", method: "POST", target: "/tags/" + cl.ARN, body: `{"tags":{"k":"v"}}`,
			checks: []check{{"eks:TagResource", cl.ARN}}, cond: map[string]string{"aws:RequestTag/k": "v", "aws:ResourceTag/env": "prod"}},
		{name: "untag nodegroup with an id", method: "DELETE", target: "/tags/" + ng + "/1234?tagKeys=team",
			checks: []check{{"eks:UntagResource", ng}}, cond: map[string]string{condTagKeys: "team"}},
		{name: "list access entry tags", method: "GET", target: "/tags/" + entry.ARN, checks: []check{{"eks:ListTagsForResource", entry.ARN}}},
		{name: "tags of another account", method: "GET", target: "/tags/" + strings.Replace(cl.ARN, s.AccountID, "999999999999", 1), unknown: true},
		{name: "tags of another region", method: "GET", target: "/tags/" + strings.Replace(cl.ARN, s.Region, "xx-west-9", 1), unknown: true},
		{name: "tags of a cluster name", method: "GET", target: "/tags/prod", unknown: true},
		{name: "unknown sub-resource", method: "GET", target: "/clusters/prod/other", unknown: true},
		{name: "wrong method", method: "PUT", target: "/clusters/prod", unknown: true},
	}

	h := New(cloud.EKS)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))

			checks, cond, ok := h.IAMChecksWithContext(r, s)
			if ok == tc.unknown {
				t.Fatalf("ok = %v, want %v", ok, !tc.unknown)
			}

			got := make([]check, 0, len(checks))
			for _, c := range checks {
				got = append(got, check{c.Action, c.Resource})
			}

			if !tc.unknown && !reflect.DeepEqual(got, tc.checks) {
				t.Errorf("checks = %v, want %v", got, tc.checks)
			}

			for k, v := range tc.cond {
				if cond[k] != v {
					t.Errorf("cond %s = %q, want %q", k, cond[k], v)
				}
			}
		})
	}
}
