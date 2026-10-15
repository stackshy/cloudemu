package aws

import (
	"context"
	"net/http"
	"testing"

	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

const (
	eksPath   = "/clusters"
	eksARN    = "arn:aws:eks:us-east-1:123456789012:"
	eksSigned = "eks"
	jsonCT    = "application/json"
)

func newEKSCluster(t *testing.T, cloud *awsprovider.Provider, name string, tags map[string]string) {
	t.Helper()

	ctx := context.Background()

	if _, err := cloud.EKS.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name: name, RoleArn: "arn:aws:iam::123456789012:role/eks", Tags: tags,
		AccessConfig: eksdriver.AccessConfigRequest{AuthenticationMode: "API"},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := cloud.EKS.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName: name, NodegroupName: "ng", NodeRole: "arn:aws:iam::123456789012:role/node", Subnets: []string{"subnet-1"},
	}); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}
}

func nodegroupExists(cloud *awsprovider.Provider, cluster, name string) bool {
	_, err := cloud.EKS.DescribeNodegroup(context.Background(), cluster, name)
	return err == nil
}

// TestAuthzMatrixEKS covers cluster and nodegroup ARNs, a single Deny, a
// missing nodegroup reaching its 404, tag and version condition keys, and
// DescribeUpdate authorized on the nodegroup it names.
func TestAuthzMatrixEKS(t *testing.T) {
	ts, cloud := matrixServer(t, nil)
	newEKSCluster(t, cloud, "prod", map[string]string{"env": "prod"})
	newEKSCluster(t, cloud, "dev", map[string]string{"env": "dev"})

	get := func(path string) sreq { return sreq{method: http.MethodGet, path: eksPath + path, service: eksSigned} }

	t.Run("one cluster in scope", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "devops", policyDoc(
			stmt("Allow", "eks:DescribeCluster", eksARN+"cluster/dev"),
			stmt("Allow", "eks:*Nodegroup*", eksARN+"nodegroup/dev/*")))

		status, body := doSigned(t, ts, u, get("/dev"))
		wantNotDenied(t, status, body)

		status, body = doSigned(t, ts, u, get("/prod"))
		wantDenied(t, status, body, "eks:DescribeCluster on resource: "+eksARN+"cluster/prod")

		status, body = doSigned(t, ts, u, get("/dev/node-groups/ng"))
		wantNotDenied(t, status, body)

		status, body = doSigned(t, ts, u, get("/prod/node-groups/ng"))
		wantDenied(t, status, body, accessDeny)

		if status, body = doSigned(t, ts, u, get("/dev/node-groups/gone")); status != http.StatusNotFound {
			t.Fatalf("missing nodegroup in scope: %d %s, want 404", status, body)
		}
	})

	t.Run("deny one nodegroup delete", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "denyng", policyDoc(stmt("Allow", "*", "*"),
			stmt("Deny", "eks:DeleteNodegroup", eksARN+"nodegroup/prod/*")))

		status, body := doSigned(t, ts, u, sreq{method: http.MethodDelete, path: eksPath + "/prod/node-groups/ng", service: eksSigned})
		wantDenied(t, status, body, "with an explicit deny")

		if !nodegroupExists(cloud, "prod", "ng") {
			t.Fatal("a denied DeleteNodegroup removed the nodegroup")
		}

		status, body = doSigned(t, ts, u, sreq{method: http.MethodDelete, path: eksPath + "/dev/node-groups/ng", service: eksSigned})
		wantNotDenied(t, status, body)
	})

	t.Run("resource tag and version conditions", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "ekstags", policyDoc(
			stmtCond("Allow", "eks:*", "*", `{"StringEquals":{"aws:ResourceTag/env":"dev"}}`),
			stmtCond("Allow", "eks:CreateCluster", "*", `{"StringEquals":{"eks:kubernetesVersion":"1.35"}}`)))

		status, body := doSigned(t, ts, u, get("/dev"))
		wantNotDenied(t, status, body)

		status, body = doSigned(t, ts, u, get("/prod"))
		wantDenied(t, status, body, accessDeny)

		create := func(name, version string) sreq {
			return sreq{path: eksPath, ctype: jsonCT, service: eksSigned, body: `{"name":"` + name + `","version":"` + version +
				`","roleArn":"arn:aws:iam::123456789012:role/eks","resourcesVpcConfig":{"subnetIds":["subnet-1"]}}`}
		}

		status, body = doSigned(t, ts, u, create("v135", "1.35"))
		wantNotDenied(t, status, body)

		status, body = doSigned(t, ts, u, create("v134", "1.34"))
		wantDenied(t, status, body, "eks:CreateCluster")
	})

	t.Run("DescribeUpdate is checked on the nodegroup it names", func(t *testing.T) {
		upd, err := cloud.EKS.UpdateNodegroupVersion(context.Background(), "prod", "ng", eksdriver.NodegroupVersionUpdate{})
		if err != nil {
			t.Fatalf("UpdateNodegroupVersion: %v", err)
		}

		clusterOnly := userWithPolicy(t, cloud, "clusteronly", policyDoc(stmt("Allow", "eks:DescribeUpdate", eksARN+"cluster/prod")))

		status, body := doSigned(t, ts, clusterOnly, get("/prod/updates/"+upd.ID+"?nodegroupName=ng"))
		wantDenied(t, status, body, "eks:DescribeUpdate on resource: "+eksARN+"nodegroup/prod/ng")

		if status, body = doSigned(t, ts, clusterOnly, get("/prod/updates/"+upd.ID)); status != http.StatusNotFound {
			t.Fatalf("nodegroup update read through the cluster: %d %s, want 404", status, body)
		}
	})

	t.Run("tagging ARN of another account", func(t *testing.T) {
		u := userWithPolicy(t, cloud, "ekstagger", allow("eks:ListTagsForResource"))

		status, body := doSigned(t, ts, u, sreq{method: http.MethodGet, service: eksSigned,
			path: "/tags/arn:aws:eks:us-east-1:999999999999:cluster/prod"})
		wantDenied(t, status, body, accessDeny)

		status, body = doSigned(t, ts, u, sreq{method: http.MethodGet, service: eksSigned, path: "/tags/" + eksARN + "cluster/prod"})
		wantNotDenied(t, status, body)
	})
}
