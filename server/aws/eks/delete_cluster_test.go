package eks_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// TestSDKDeleteClusterWithNodegroupInUse guards that deleting a cluster that
// still has a managed node group attached returns ResourceInUseException with
// HTTP status 409 (per the DeleteCluster API reference), and that the surfaced
// message does not leak the internal canonical-code prefix. Deleting the node
// group first must then let the cluster delete.
func TestSDKDeleteClusterWithNodegroupInUse(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		RoleArn:            aws.String("arn:aws:iam::1:role/r"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::1:role/r"),
		Subnets:       []string{"subnet-1"},
	}); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	_, err := client.DeleteCluster(ctx, &awseks.DeleteClusterInput{Name: aws.String("c1")})

	var inUse *ekstypes.ResourceInUseException
	if !errors.As(err, &inUse) {
		t.Fatalf("DeleteCluster(with nodegroup) err = %v, want ResourceInUseException", err)
	}

	// HTTP 409 Conflict, not 400.
	var httpErr interface{ HTTPStatusCode() int }
	if !errors.As(err, &httpErr) {
		t.Fatalf("DeleteCluster error carries no HTTP status: %v", err)
	}

	if got := httpErr.HTTPStatusCode(); got != 409 {
		t.Fatalf("DeleteCluster(with nodegroup) HTTP status = %d, want 409", got)
	}

	// The message must read like real AWS, not leak the "FailedPrecondition:"
	// canonical-code prefix.
	if msg := inUse.ErrorMessage(); strings.Contains(msg, "FailedPrecondition") {
		t.Fatalf("DeleteCluster message leaks canonical-code prefix: %q", msg)
	}

	// Removing the node group unblocks the delete.
	if _, err := client.DeleteNodegroup(ctx, &awseks.DeleteNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
	}); err != nil {
		t.Fatalf("DeleteNodegroup: %v", err)
	}

	if _, err := client.DeleteCluster(ctx, &awseks.DeleteClusterInput{Name: aws.String("c1")}); err != nil {
		t.Fatalf("DeleteCluster after draining nodegroup: %v", err)
	}
}
