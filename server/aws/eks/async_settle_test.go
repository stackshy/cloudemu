package eks_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/stackshy/cloudemu/v2"
	cloudconfig "github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newAsyncSDKClient builds an EKS SDK client backed by a cloudemu server with
// AsyncSettle enabled and a FakeClock the test can advance, so create/update
// report their real transient states deterministically over the wire.
func newAsyncSDKClient(t *testing.T) (*awseks.Client, *cloudconfig.FakeClock) {
	t.Helper()

	fc := cloudconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(cloudconfig.WithClock(fc), cloudconfig.WithAsyncSettle())

	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{EKS: cloud.EKS}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awseks.NewFromConfig(cfg, func(o *awseks.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return client, fc
}

// TestAsyncSettleWireEKSClusterAndNodegroup pins that a real SDK client sees a
// cluster as CREATING (then ACTIVE), rejects a nodegroup on a cluster that
// isn't ACTIVE yet, and sees a nodegroup as CREATING (then ACTIVE) too — all
// over the wire, driven purely by a FakeClock.
func TestAsyncSettleWireEKSClusterAndNodegroup(t *testing.T) {
	client, fc := newAsyncSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		RoleArn:            aws.String("arn:aws:iam::1:role/r"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if created.Cluster.Status != ekstypes.ClusterStatusCreating {
		t.Fatalf("create status = %q, want CREATING", created.Cluster.Status)
	}

	desc, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c1")})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if desc.Cluster.Status != ekstypes.ClusterStatusCreating {
		t.Fatalf("describe status = %q, want CREATING", desc.Cluster.Status)
	}

	// A managed node group cannot be created while the cluster is still
	// CREATING — real EKS requires ACTIVE.
	_, err = client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::1:role/r"),
		Subnets:       []string{"subnet-1"},
	})

	var inUse *ekstypes.ResourceInUseException
	if !errors.As(err, &inUse) {
		t.Fatalf("CreateNodegroup(cluster CREATING) err = %v, want ResourceInUseException", err)
	}

	fc.Advance(settle.DefaultClusterSettle)

	desc, err = client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c1")})
	if err != nil {
		t.Fatalf("DescribeCluster after settle: %v", err)
	}

	if desc.Cluster.Status != ekstypes.ClusterStatusActive {
		t.Fatalf("settled status = %q, want ACTIVE", desc.Cluster.Status)
	}

	// Now that the cluster is ACTIVE, node group creation succeeds and itself
	// starts in CREATING.
	ngOut, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::1:role/r"),
		Subnets:       []string{"subnet-1"},
	})
	if err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	if ngOut.Nodegroup.Status != ekstypes.NodegroupStatusCreating {
		t.Fatalf("nodegroup create status = %q, want CREATING", ngOut.Nodegroup.Status)
	}

	ngDesc, err := client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName: aws.String("c1"), NodegroupName: aws.String("ng1"),
	})
	if err != nil {
		t.Fatalf("DescribeNodegroup: %v", err)
	}

	if ngDesc.Nodegroup.Status != ekstypes.NodegroupStatusCreating {
		t.Fatalf("nodegroup describe status = %q, want CREATING", ngDesc.Nodegroup.Status)
	}

	fc.Advance(settle.DefaultClusterSettle)

	ngDesc, err = client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName: aws.String("c1"), NodegroupName: aws.String("ng1"),
	})
	if err != nil {
		t.Fatalf("DescribeNodegroup after settle: %v", err)
	}

	if ngDesc.Nodegroup.Status != ekstypes.NodegroupStatusActive {
		t.Fatalf("nodegroup settled status = %q, want ACTIVE", ngDesc.Nodegroup.Status)
	}
}
