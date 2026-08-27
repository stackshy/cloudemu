package redshift_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
)

// TestSDKRedshiftGetClusterCredentials asserts GetClusterCredentials returns a
// temporary DbUser (prefixed IAM:), a DbPassword and an Expiration for an
// existing cluster.
func TestSDKRedshiftGetClusterCredentials(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("creds"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := client.GetClusterCredentials(ctx, &awsredshift.GetClusterCredentialsInput{
		ClusterIdentifier: aws.String("creds"),
		DbUser:            aws.String("analyst"),
	})
	if err != nil {
		t.Fatalf("GetClusterCredentials: %v", err)
	}

	if !strings.HasPrefix(aws.ToString(out.DbUser), "IAM:") {
		t.Fatalf("DbUser=%q, want an IAM: prefix", aws.ToString(out.DbUser))
	}

	if aws.ToString(out.DbPassword) == "" {
		t.Fatal("DbPassword empty; want a temporary password")
	}

	if out.Expiration == nil {
		t.Fatal("Expiration nil; want a credential expiry")
	}
}

// TestSDKRedshiftGetClusterCredentialsMissing asserts an unknown cluster is a
// ClusterNotFound fault, not an empty credential.
func TestSDKRedshiftGetClusterCredentialsMissing(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.GetClusterCredentials(ctx, &awsredshift.GetClusterCredentialsInput{
		ClusterIdentifier: aws.String("nope"),
		DbUser:            aws.String("analyst"),
	}); err == nil {
		t.Fatal("expected an error for GetClusterCredentials on a missing cluster")
	}
}

// TestSDKRedshiftPauseResume asserts PauseCluster and ResumeCluster move the
// cluster between paused and available.
func TestSDKRedshiftPauseResume(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("pausable"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	paused, err := client.PauseCluster(ctx, &awsredshift.PauseClusterInput{
		ClusterIdentifier: aws.String("pausable"),
	})
	if err != nil {
		t.Fatalf("PauseCluster: %v", err)
	}

	if aws.ToString(paused.Cluster.ClusterStatus) != "paused" {
		t.Fatalf("after pause status=%q, want paused", aws.ToString(paused.Cluster.ClusterStatus))
	}

	resumed, err := client.ResumeCluster(ctx, &awsredshift.ResumeClusterInput{
		ClusterIdentifier: aws.String("pausable"),
	})
	if err != nil {
		t.Fatalf("ResumeCluster: %v", err)
	}

	if aws.ToString(resumed.Cluster.ClusterStatus) != "available" {
		t.Fatalf("after resume status=%q, want available", aws.ToString(resumed.Cluster.ClusterStatus))
	}
}
