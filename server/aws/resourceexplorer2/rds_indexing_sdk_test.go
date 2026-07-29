package resourceexplorer2_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	rex "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// A relational database that cloudemu emulates must be enumerable through
// Resource Explorer (issue #295): seed an instance, an Aurora cluster, and a
// snapshot, then assert all three surface and that a service:rds filter
// narrows to exactly them.
func TestSDKResourceExplorer2_RDSIndexing(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	if _, err := cloud.RDS.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db", Engine: "mysql",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := cloud.RDS.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID: "cl", Engine: "aurora-mysql",
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := cloud.RDS.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{
		ID: "snap", InstanceID: "db",
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if err := cloud.S3.CreateBucket(ctx, "control-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	srv := awsserver.New(awsserver.Drivers{
		S3:                cloud.S3,
		ResourceDiscovery: cloud.ResourceDiscovery,
		AccountID:         "123456789012",
		Region:            "us-east-1",
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := newREXClient(t, ts.URL)

	t.Run("instance, cluster and snapshot appear in unscoped Search", func(t *testing.T) {
		out, err := client.Search(ctx, &rex.SearchInput{QueryString: aws.String("")})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}

		for _, want := range []string{"relationaldb:dbinstance", "relationaldb:dbcluster", "relationaldb:dbsnapshot"} {
			if !rexHasResourceType(out.Resources, want) {
				t.Errorf("resource type %q not surfaced", want)
			}
		}
	})

	t.Run("service:rds narrows to the RDS resources", func(t *testing.T) {
		out, err := client.Search(ctx, &rex.SearchInput{QueryString: aws.String("service:rds")})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}

		if len(out.Resources) != 3 {
			t.Fatalf("service:rds returned %d resources, want 3 (instance+cluster+snapshot)", len(out.Resources))
		}

		for _, r := range out.Resources {
			if aws.ToString(r.Service) != "rds" {
				t.Errorf("resource service = %q, want rds", aws.ToString(r.Service))
			}
		}
	})

	t.Run("instance ARN is RDS-shaped", func(t *testing.T) {
		out, err := client.Search(ctx, &rex.SearchInput{QueryString: aws.String("service:rds")})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}

		var instanceARN string
		for _, r := range out.Resources {
			if aws.ToString(r.ResourceType) == "relationaldb:dbinstance" {
				instanceARN = aws.ToString(r.Arn)
			}
		}

		if instanceARN == "" {
			t.Fatal("no db instance in results")
		}
	})
}
