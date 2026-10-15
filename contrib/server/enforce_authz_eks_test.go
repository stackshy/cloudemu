package main

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

func eksClient(t *testing.T, endpoint string, c aws.Credentials) *eks.Client {
	t.Helper()

	return eks.NewFromConfig(credsConfig(t, c), func(o *eks.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

// TestEnforceAuthEKS drives the real EKS SDK against cloudemu serve with
// --enforce-auth: cluster and nodegroup operations are authorized on their
// ARNs, a missing nodegroup in scope still answers ResourceNotFoundException,
// and a nodegroup update is read only through its nodegroup.
func TestEnforceAuthEKS(t *testing.T) {
	endpoint, stop := enforceAuthServer(t)
	defer stop()

	ctx := context.Background()
	boot := clientsFor(t, endpoint, seedBootUser(t, endpoint))
	admin := eksClient(t, endpoint, boot.cred)

	for _, name := range []string{"team", "other"} {
		_, err := admin.CreateCluster(ctx, &eks.CreateClusterInput{
			Name: aws.String(name), RoleArn: aws.String("arn:aws:iam::000000000000:role/eks"),
			ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
		})
		wantOK(t, "CreateCluster "+name+" as boot", err)
	}

	cl, err := admin.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String("team")})
	wantOK(t, "DescribeCluster as boot", err)

	arn := aws.ToString(cl.Cluster.Arn)
	ngARN := strings.Replace(arn, ":cluster/team", ":nodegroup/team/*", 1)
	user := eksClient(t, endpoint, boot.newUser(t, "teamops", `{"Version":"2012-10-17","Statement":[`+
		`{"Effect":"Allow","Action":["eks:DescribeCluster","eks:CreateNodegroup","eks:ListNodegroups"],"Resource":"`+arn+`"},`+
		`{"Effect":"Allow","Action":["eks:DescribeNodegroup","eks:UpdateNodegroupConfig","eks:DescribeUpdate"],"Resource":"`+ngARN+`"}]}`))

	_, err = user.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String("team")})
	wantOK(t, "DescribeCluster", err)

	_, err = user.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String("other")})
	wantCode(t, "DescribeCluster of another cluster", err, "AccessDeniedException")

	_, err = user.CreateNodegroup(ctx, &eks.CreateNodegroupInput{
		ClusterName: aws.String("team"), NodegroupName: aws.String("ng"),
		NodeRole: aws.String("arn:aws:iam::000000000000:role/node"), Subnets: []string{"subnet-1"},
	})
	wantOK(t, "CreateNodegroup", err)

	_, err = user.DeleteNodegroup(ctx, &eks.DeleteNodegroupInput{ClusterName: aws.String("team"), NodegroupName: aws.String("ng")})
	wantCode(t, "DeleteNodegroup", err, "AccessDeniedException")

	if !strings.Contains(err.Error(), "eks:DeleteNodegroup on resource: "+strings.TrimSuffix(ngARN, "*")+"ng") {
		t.Fatalf("deny message = %v", err)
	}

	_, err = user.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{ClusterName: aws.String("team"), NodegroupName: aws.String("gone")})
	wantCode(t, "DescribeNodegroup of a missing nodegroup", err, "ResourceNotFoundException")

	upd, err := user.UpdateNodegroupConfig(ctx, &eks.UpdateNodegroupConfigInput{
		ClusterName: aws.String("team"), NodegroupName: aws.String("ng"),
		Labels: &ekstypes.UpdateLabelsPayload{AddOrUpdateLabels: map[string]string{"k": "v"}},
	})
	wantOK(t, "UpdateNodegroupConfig", err)

	_, err = user.DescribeUpdate(ctx, &eks.DescribeUpdateInput{
		Name: aws.String("team"), UpdateId: upd.Update.Id, NodegroupName: aws.String("ng"),
	})
	wantOK(t, "DescribeUpdate with its nodegroup", err)

	_, err = admin.DescribeUpdate(ctx, &eks.DescribeUpdateInput{Name: aws.String("team"), UpdateId: upd.Update.Id})
	wantCode(t, "DescribeUpdate of a nodegroup update without its nodegroup", err, "ResourceNotFoundException")
}
