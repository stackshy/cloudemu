package redshift_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/aws/smithy-go"
)

// paramOverride builds a single-parameter override list for
// ModifyClusterParameterGroup.
func paramOverride(name, value string) []redshifttypes.Parameter {
	return []redshifttypes.Parameter{
		{ParameterName: aws.String(name), ParameterValue: aws.String(value)},
	}
}

// TestSDKRedshiftTerraformReadFields asserts the cluster-read fields the real
// aws_redshift_cluster resource depends on during its create/read cycle.
// ClusterAvailabilityStatus drives the create/update waiters (target
// "Available"); AvailabilityZoneRelocationStatus is waited on unconditionally;
// MultiAZ is parsed as "Enabled"/"Disabled"; MaintenanceTrackName and the
// snapshot-retention / version-upgrade defaults must match the provider schema
// defaults or every plan drifts. Omitting any of these blocks Terraform.
func TestSDKRedshiftTerraformReadFields(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("tf-fields"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("dc2.large"),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	cl := out.Cluster

	if got := aws.ToString(cl.ClusterAvailabilityStatus); got != "Available" {
		t.Fatalf("ClusterAvailabilityStatus=%q, want Available", got)
	}

	if got := aws.ToString(cl.AvailabilityZoneRelocationStatus); got != "disabled" {
		t.Fatalf("AvailabilityZoneRelocationStatus=%q, want disabled", got)
	}

	if got := aws.ToString(cl.MultiAZ); got != "Disabled" {
		t.Fatalf("MultiAZ=%q, want Disabled", got)
	}

	if got := aws.ToString(cl.MaintenanceTrackName); got != "current" {
		t.Fatalf("MaintenanceTrackName=%q, want current", got)
	}

	if !aws.ToBool(cl.AllowVersionUpgrade) {
		t.Fatal("AllowVersionUpgrade=false, want true (AWS default)")
	}

	if got := aws.ToInt32(cl.AutomatedSnapshotRetentionPeriod); got != 1 {
		t.Fatalf("AutomatedSnapshotRetentionPeriod=%d, want 1", got)
	}

	if got := aws.ToInt32(cl.ManualSnapshotRetentionPeriod); got != -1 {
		t.Fatalf("ManualSnapshotRetentionPeriod=%d, want -1", got)
	}

	// A cluster is always attached to a parameter group; the provider indexes
	// ClusterParameterGroups[0] unconditionally, so an empty list crashes it.
	if len(cl.ClusterParameterGroups) != 1 ||
		aws.ToString(cl.ClusterParameterGroups[0].ParameterGroupName) != "default.redshift-1.0" {
		t.Fatalf("ClusterParameterGroups=%+v, want one default.redshift-1.0 membership", cl.ClusterParameterGroups)
	}
}

// TestSDKRedshiftSingleNodeNodeShape asserts a single-node cluster reports
// exactly one SHARED node. Terraform derives cluster_type from
// len(ClusterNodes) > 1, so a leader+compute pair would mislabel a single-node
// cluster as multi-node and drift forever.
func TestSDKRedshiftSingleNodeNodeShape(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("one-node"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("dc2.large"),
		NumberOfNodes:      aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	nodes := out.Cluster.ClusterNodes
	if len(nodes) != 1 {
		t.Fatalf("single-node ClusterNodes=%d, want 1", len(nodes))
	}

	if got := aws.ToString(nodes[0].NodeRole); got != "SHARED" {
		t.Fatalf("single-node role=%q, want SHARED", got)
	}
}

// TestSDKRedshiftDescribeLoggingStatus asserts DescribeLoggingStatus reports
// logging disabled for an existing cluster (Terraform reads it on every cluster
// read) and ClusterNotFound for an unknown one.
func TestSDKRedshiftDescribeLoggingStatus(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("logged"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("dc2.large"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	ls, err := client.DescribeLoggingStatus(ctx, &awsredshift.DescribeLoggingStatusInput{
		ClusterIdentifier: aws.String("logged"),
	})
	if err != nil {
		t.Fatalf("DescribeLoggingStatus: %v", err)
	}

	if aws.ToBool(ls.LoggingEnabled) {
		t.Fatal("LoggingEnabled=true, want false")
	}

	_, err = client.DescribeLoggingStatus(ctx, &awsredshift.DescribeLoggingStatusInput{
		ClusterIdentifier: aws.String("ghost"),
	})
	if err == nil {
		t.Fatal("expected error for unknown cluster")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ClusterNotFound" {
		t.Fatalf("DescribeLoggingStatus(missing) ErrorCode = %v, want ClusterNotFound", err)
	}
}

// TestSDKRedshiftDescribeClusterParametersSourceFilter asserts the Source
// filter is honored: Terraform's aws_redshift_parameter_group read requests
// Source=user and expects only user-overridden parameters, otherwise it plans
// to delete every engine default on the next run.
func TestSDKRedshiftDescribeClusterParametersSourceFilter(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateClusterParameterGroup(ctx, &awsredshift.CreateClusterParameterGroupInput{
		ParameterGroupName:   aws.String("filtpg"),
		ParameterGroupFamily: aws.String("redshift-1.0"),
		Description:          aws.String("filter pg"),
	}); err != nil {
		t.Fatalf("CreateClusterParameterGroup: %v", err)
	}

	// Before any override, a user-source query returns nothing.
	userOnly, err := client.DescribeClusterParameters(ctx, &awsredshift.DescribeClusterParametersInput{
		ParameterGroupName: aws.String("filtpg"),
		Source:             aws.String("user"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterParameters(user): %v", err)
	}
	if len(userOnly.Parameters) != 0 {
		t.Fatalf("user params before override = %d, want 0", len(userOnly.Parameters))
	}

	if _, err := client.ModifyClusterParameterGroup(ctx, &awsredshift.ModifyClusterParameterGroupInput{
		ParameterGroupName: aws.String("filtpg"),
		Parameters:         paramOverride("require_ssl", "true"),
	}); err != nil {
		t.Fatalf("ModifyClusterParameterGroup: %v", err)
	}

	// After the override, only the one user parameter comes back under Source=user.
	userOnly, err = client.DescribeClusterParameters(ctx, &awsredshift.DescribeClusterParametersInput{
		ParameterGroupName: aws.String("filtpg"),
		Source:             aws.String("user"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterParameters(user after): %v", err)
	}
	if len(userOnly.Parameters) != 1 || aws.ToString(userOnly.Parameters[0].ParameterName) != "require_ssl" {
		t.Fatalf("user params after override = %+v, want only require_ssl", userOnly.Parameters)
	}
}
