package redshift_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
)

// TestSDKRedshiftClusterAttributes asserts a created cluster reports the node
// shape Terraform and the console read — NodeType, NumberOfNodes, Encrypted,
// PubliclyAccessible, AvailabilityZone, the parameter-group membership and the
// synthesized LEADER/COMPUTE node list.
func TestSDKRedshiftClusterAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:         aws.String("attrs"),
		MasterUsername:            aws.String("admin"),
		MasterUserPassword:        aws.String("Sup3rSecret!"),
		NodeType:                  aws.String("ra3.xlplus"),
		NumberOfNodes:             aws.Int32(2),
		Encrypted:                 aws.Bool(true),
		PubliclyAccessible:        aws.Bool(true),
		AvailabilityZone:          aws.String("us-east-1a"),
		ClusterParameterGroupName: aws.String("pg-custom"),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	cl := out.Cluster

	if aws.ToString(cl.NodeType) != "ra3.xlplus" {
		t.Fatalf("NodeType=%q, want ra3.xlplus", aws.ToString(cl.NodeType))
	}

	if aws.ToInt32(cl.NumberOfNodes) != 2 {
		t.Fatalf("NumberOfNodes=%d, want 2", aws.ToInt32(cl.NumberOfNodes))
	}

	if !aws.ToBool(cl.Encrypted) {
		t.Fatal("Encrypted=false, want true")
	}

	if !aws.ToBool(cl.PubliclyAccessible) {
		t.Fatal("PubliclyAccessible=false, want true")
	}

	if aws.ToString(cl.AvailabilityZone) != "us-east-1a" {
		t.Fatalf("AvailabilityZone=%q, want us-east-1a", aws.ToString(cl.AvailabilityZone))
	}

	if len(cl.ClusterParameterGroups) != 1 ||
		aws.ToString(cl.ClusterParameterGroups[0].ParameterGroupName) != "pg-custom" {
		t.Fatalf("ClusterParameterGroups=%+v, want one pg-custom membership", cl.ClusterParameterGroups)
	}

	// One LEADER plus NumberOfNodes COMPUTE nodes.
	if len(cl.ClusterNodes) != 3 {
		t.Fatalf("ClusterNodes=%d, want 3 (1 leader + 2 compute)", len(cl.ClusterNodes))
	}

	if aws.ToString(cl.ClusterNodes[0].NodeRole) != "LEADER" {
		t.Fatalf("first node role=%q, want LEADER", aws.ToString(cl.ClusterNodes[0].NodeRole))
	}
}
