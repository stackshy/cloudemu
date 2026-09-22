package redshift_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
)

// TestSDKRedshiftPauseResumePreconditions asserts Pause/Resume enforce their
// source state the way AWS does: ResumeCluster on an available cluster and
// PauseCluster on an already-paused cluster fail with InvalidClusterState
// rather than succeeding as a no-op.
func TestSDKRedshiftPauseResumePreconditions(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("strict"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	_, err := client.ResumeCluster(ctx, &awsredshift.ResumeClusterInput{ClusterIdentifier: aws.String("strict")})
	if code := errorCode(err); code != "InvalidClusterState" {
		t.Fatalf("ResumeCluster on available cluster: code=%q err=%v, want InvalidClusterState", code, err)
	}

	if _, err = client.PauseCluster(ctx, &awsredshift.PauseClusterInput{ClusterIdentifier: aws.String("strict")}); err != nil {
		t.Fatalf("PauseCluster: %v", err)
	}

	_, err = client.PauseCluster(ctx, &awsredshift.PauseClusterInput{ClusterIdentifier: aws.String("strict")})
	if code := errorCode(err); code != "InvalidClusterState" {
		t.Fatalf("PauseCluster on paused cluster: code=%q err=%v, want InvalidClusterState", code, err)
	}
}
