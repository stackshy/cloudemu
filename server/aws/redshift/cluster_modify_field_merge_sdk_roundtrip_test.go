package redshift_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
)

// TestSDKRedshiftModifyClusterFieldLevelMerge proves ModifyCluster is a
// field-level merge over the wire: a first call sets VpcSecurityGroupIds,
// ClusterParameterGroupName, AllowVersionUpgrade, PubliclyAccessible,
// Encrypted, MaintenanceTrackName and ElasticIp; a second call that only
// changes NumberOfNodes must not revert any of them. Before this fix,
// ModifyCluster silently dropped every one of these fields: a caller that
// set them via ModifyCluster saw the change accepted but never applied.
func TestSDKRedshiftModifyClusterFieldLevelMerge(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("merge-wh"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("dc2.large"),
		ClusterType:        aws.String("multi-node"),
		NumberOfNodes:      aws.Int32(2),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	first, err := client.ModifyCluster(ctx, &awsredshift.ModifyClusterInput{
		ClusterIdentifier:         aws.String("merge-wh"),
		VpcSecurityGroupIds:       []string{"sg-aaa", "sg-bbb"},
		ClusterParameterGroupName: aws.String("custom-pg"),
		AllowVersionUpgrade:       aws.Bool(false),
		PubliclyAccessible:        aws.Bool(true),
		Encrypted:                 aws.Bool(true),
		MaintenanceTrackName:      aws.String("trailing"),
		ElasticIp:                 aws.String("192.0.2.20"),
	})
	if err != nil {
		t.Fatalf("first ModifyCluster: %v", err)
	}

	if len(first.Cluster.VpcSecurityGroups) != 2 {
		t.Fatalf("first modify VpcSecurityGroups = %+v, want 2 entries", first.Cluster.VpcSecurityGroups)
	}
	if aws.ToString(first.Cluster.ClusterParameterGroups[0].ParameterGroupName) != "custom-pg" {
		t.Fatalf("first modify ClusterParameterGroupName = %+v, want custom-pg", first.Cluster.ClusterParameterGroups)
	}
	if aws.ToBool(first.Cluster.AllowVersionUpgrade) {
		t.Fatal("first modify AllowVersionUpgrade = true, want false")
	}
	if !aws.ToBool(first.Cluster.PubliclyAccessible) || !aws.ToBool(first.Cluster.Encrypted) {
		t.Fatalf("first modify PubliclyAccessible/Encrypted = %v/%v, want true/true",
			aws.ToBool(first.Cluster.PubliclyAccessible), aws.ToBool(first.Cluster.Encrypted))
	}
	if aws.ToString(first.Cluster.MaintenanceTrackName) != "trailing" {
		t.Fatalf("first modify MaintenanceTrackName = %q, want trailing", aws.ToString(first.Cluster.MaintenanceTrackName))
	}
	if first.Cluster.ElasticIpStatus == nil || aws.ToString(first.Cluster.ElasticIpStatus.ElasticIp) != "192.0.2.20" {
		t.Fatalf("first modify ElasticIpStatus = %+v, want 192.0.2.20", first.Cluster.ElasticIpStatus)
	}

	// Second call touches only NumberOfNodes; every field set above must survive.
	second, err := client.ModifyCluster(ctx, &awsredshift.ModifyClusterInput{
		ClusterIdentifier: aws.String("merge-wh"),
		NumberOfNodes:     aws.Int32(4),
	})
	if err != nil {
		t.Fatalf("second ModifyCluster: %v", err)
	}

	if aws.ToInt32(second.Cluster.NumberOfNodes) != 4 {
		t.Fatalf("second modify NumberOfNodes = %d, want 4", aws.ToInt32(second.Cluster.NumberOfNodes))
	}
	if len(second.Cluster.VpcSecurityGroups) != 2 {
		t.Fatalf("second modify dropped VpcSecurityGroups: %+v", second.Cluster.VpcSecurityGroups)
	}
	if aws.ToString(second.Cluster.ClusterParameterGroups[0].ParameterGroupName) != "custom-pg" {
		t.Fatalf("second modify dropped ClusterParameterGroupName: %+v", second.Cluster.ClusterParameterGroups)
	}
	if aws.ToBool(second.Cluster.AllowVersionUpgrade) {
		t.Fatal("second modify reverted AllowVersionUpgrade to true")
	}
	if !aws.ToBool(second.Cluster.PubliclyAccessible) || !aws.ToBool(second.Cluster.Encrypted) {
		t.Fatalf("second modify reverted PubliclyAccessible/Encrypted: %v/%v",
			aws.ToBool(second.Cluster.PubliclyAccessible), aws.ToBool(second.Cluster.Encrypted))
	}
	if aws.ToString(second.Cluster.MaintenanceTrackName) != "trailing" {
		t.Fatalf("second modify reverted MaintenanceTrackName: %q", aws.ToString(second.Cluster.MaintenanceTrackName))
	}
	if second.Cluster.ElasticIpStatus == nil || aws.ToString(second.Cluster.ElasticIpStatus.ElasticIp) != "192.0.2.20" {
		t.Fatalf("second modify reverted ElasticIpStatus: %+v", second.Cluster.ElasticIpStatus)
	}

	// DescribeClusters must reflect the same preserved state.
	desc, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("merge-wh"),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	got := desc.Clusters[0]
	if aws.ToInt32(got.NumberOfNodes) != 4 {
		t.Fatalf("describe NumberOfNodes = %d, want 4", aws.ToInt32(got.NumberOfNodes))
	}
	if len(got.VpcSecurityGroups) != 2 {
		t.Fatalf("describe dropped VpcSecurityGroups: %+v", got.VpcSecurityGroups)
	}
	if aws.ToString(got.ClusterParameterGroups[0].ParameterGroupName) != "custom-pg" {
		t.Fatalf("describe dropped ClusterParameterGroupName: %+v", got.ClusterParameterGroups)
	}
	if got.ElasticIpStatus == nil || aws.ToString(got.ElasticIpStatus.ElasticIp) != "192.0.2.20" {
		t.Fatalf("describe dropped ElasticIpStatus: %+v", got.ElasticIpStatus)
	}
}

// TestSDKRedshiftModifyClusterSecurityGroups proves the legacy (EC2-Classic)
// ClusterSecurityGroups list round-trips through ModifyCluster and
// DescribeClusters instead of being silently dropped.
func TestSDKRedshiftModifyClusterSecurityGroups(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("classic-wh"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("dc2.large"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.ModifyCluster(ctx, &awsredshift.ModifyClusterInput{
		ClusterIdentifier:     aws.String("classic-wh"),
		ClusterSecurityGroups: []string{"classic-sg-1"},
	}); err != nil {
		t.Fatalf("ModifyCluster: %v", err)
	}

	desc, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("classic-wh"),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(desc.Clusters[0].ClusterSecurityGroups) != 1 ||
		aws.ToString(desc.Clusters[0].ClusterSecurityGroups[0].ClusterSecurityGroupName) != "classic-sg-1" {
		t.Fatalf("ClusterSecurityGroups = %+v, want [classic-sg-1]", desc.Clusters[0].ClusterSecurityGroups)
	}
}
