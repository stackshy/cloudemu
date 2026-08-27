package redshift_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
)

// TestSDKRedshiftEncryptedClusterKmsKeyId proves an encrypted cluster round-trips
// its KmsKeyId on create and DescribeClusters — without it a Terraform
// aws_redshift_cluster.kms_key_id reads back empty and drifts.
func TestSDKRedshiftEncryptedClusterKmsKeyId(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const kmsARN = "arn:aws:kms:us-east-1:123456789012:key/abc-123"

	out, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("enc"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
		Encrypted:          aws.Bool(true),
		KmsKeyId:           aws.String(kmsARN),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if !aws.ToBool(out.Cluster.Encrypted) || aws.ToString(out.Cluster.KmsKeyId) != kmsARN {
		t.Fatalf("create Encrypted/KmsKeyId = %v/%q, want true/%q",
			aws.ToBool(out.Cluster.Encrypted), aws.ToString(out.Cluster.KmsKeyId), kmsARN)
	}

	got, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{
		ClusterIdentifier: aws.String("enc"),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if aws.ToString(got.Clusters[0].KmsKeyId) != kmsARN {
		t.Fatalf("describe KmsKeyId = %q, want %q", aws.ToString(got.Clusters[0].KmsKeyId), kmsARN)
	}
}
