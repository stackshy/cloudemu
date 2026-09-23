package aws_test

import (
	"context"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	"github.com/stackshy/cloudemu/v2/providers/aws/s3"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

const (
	regionWest = "us-west-2"
	regionEast = "us-east-1"
)

// twoRegions builds a default (us-east-1) provider plus a us-west-2 regional
// provider that shares the default's global services, the arrangement the
// region mux creates.
func twoRegions(t *testing.T) (east, west *awsprovider.Provider) {
	t.Helper()

	east = cloudemu.NewAWS(config.WithRegion(regionEast))
	west = awsprovider.NewRegional(east.Globals(), config.WithRegion(regionWest))

	return east, west
}

// TestGlobalServicesShared proves NewRegional injects the SAME global instances
// (not copies), so a region provider's global services and the S3 name namespace
// are common with the bundle owner, while regional services are distinct.
func TestGlobalServicesShared(t *testing.T) {
	east, west := twoRegions(t)

	if east.IAM != west.IAM {
		t.Error("IAM is not shared across regions")
	}
	if east.Route53 != west.Route53 {
		t.Error("Route53 is not shared across regions")
	}
	if east.CloudFront != west.CloudFront {
		t.Error("CloudFront is not shared across regions")
	}
	if east.GlobalAccelerator != west.GlobalAccelerator {
		t.Error("GlobalAccelerator is not shared across regions")
	}
	if east.S3.NameReservation() != west.S3.NameReservation() {
		t.Error("S3 bucket-name namespace is not shared across regions")
	}

	// Regional services must be distinct instances.
	if east.DynamoDB == west.DynamoDB || east.SQS == west.SQS || east.EC2 == west.EC2 || east.S3 == west.S3 {
		t.Error("regional services must not be shared across regions")
	}
	if east.Region != regionEast || west.Region != regionWest {
		t.Errorf("region stamps: east=%q west=%q", east.Region, west.Region)
	}
}

// TestRegionalIsolation locks that regional resources created in one region are
// invisible in another and present in their own.
func TestRegionalIsolation(t *testing.T) {
	ctx := context.Background()
	east, west := twoRegions(t)

	if err := west.DynamoDB.CreateTable(ctx, dbdriver.TableConfig{Name: "orders", PartitionKey: "id"}); err != nil {
		t.Fatalf("create table west: %v", err)
	}
	if _, err := west.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "jobs"}); err != nil {
		t.Fatalf("create queue west: %v", err)
	}

	// Absent in us-east-1.
	if _, err := east.DynamoDB.DescribeTable(ctx, "orders"); !cerrors.IsNotFound(err) {
		t.Errorf("east DescribeTable(orders) err = %v, want NotFound", err)
	}
	if tables, err := east.DynamoDB.ListTables(ctx); err != nil || len(tables) != 0 {
		t.Errorf("east ListTables = %v (err %v), want empty", tables, err)
	}

	// Present in us-west-2.
	if _, err := west.DynamoDB.DescribeTable(ctx, "orders"); err != nil {
		t.Errorf("west DescribeTable(orders) err = %v, want found", err)
	}
}

// TestS3NameReservationCrossRegion proves the global S3 name namespace: a name
// taken in one region blocks a create in another, and DeleteBucket frees it.
func TestS3NameReservationCrossRegion(t *testing.T) {
	ctx := context.Background()
	east, west := twoRegions(t)

	if err := east.S3.CreateBucket(ctx, "shared-name"); err != nil {
		t.Fatalf("create bucket east: %v", err)
	}

	// Same name in another region is rejected (real S3 global namespace).
	err := west.S3.CreateBucket(ctx, "shared-name")
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("west CreateBucket(shared-name) err = %v, want AlreadyExists", err)
	}

	// A different name is fine.
	if err := west.S3.CreateBucket(ctx, "west-only"); err != nil {
		t.Fatalf("west CreateBucket(west-only): %v", err)
	}

	// Deleting in east frees the name; west can now take it.
	if err := east.S3.DeleteBucket(ctx, "shared-name"); err != nil {
		t.Fatalf("delete bucket east: %v", err)
	}
	if err := west.S3.CreateBucket(ctx, "shared-name"); err != nil {
		t.Fatalf("west CreateBucket(shared-name) after east delete: %v", err)
	}
}

// TestS3NotificationNonDefaultRegion proves a bucket in a NON-default region
// delivers its object events to THAT region's queue, not another region's, the
// S3→SQS wire is same-region because both are regional services of one provider.
func TestS3NotificationNonDefaultRegion(t *testing.T) {
	ctx := context.Background()
	east, west := twoRegions(t)

	// A queue in each region.
	eastQ, err := east.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "events"})
	if err != nil {
		t.Fatalf("create east queue: %v", err)
	}
	westQ, err := west.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "events"})
	if err != nil {
		t.Fatalf("create west queue: %v", err)
	}

	if err := west.S3.CreateBucket(ctx, "west-bucket"); err != nil {
		t.Fatalf("create west bucket: %v", err)
	}
	if err := west.S3.PutBucketNotification(ctx, "west-bucket", []s3.BucketNotification{{
		ID: "n1", Target: s3.NotifyQueue, ARN: westQ.ARN, Events: []string{"s3:ObjectCreated:*"},
	}}); err != nil {
		t.Fatalf("put notification: %v", err)
	}

	if err := west.S3.PutObject(ctx, "west-bucket", "k", []byte("v"), "text/plain", nil); err != nil {
		t.Fatalf("put object: %v", err)
	}

	// The west queue got the event; the east queue (same name, other region) did not.
	westMsgs, err := west.SQS.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: westQ.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("receive west: %v", err)
	}
	if len(westMsgs) != 1 {
		t.Fatalf("west queue got %d messages, want 1", len(westMsgs))
	}

	eastMsgs, err := east.SQS.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: eastQ.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("receive east: %v", err)
	}
	if len(eastMsgs) != 0 {
		t.Fatalf("east queue got %d messages, want 0 (cross-region leak)", len(eastMsgs))
	}
}

// TestRegionalToGlobalInstanceProfile proves a regional EC2 launch in us-west-2
// resolves an instance profile from the SHARED IAM, the one regional→global
// wire the design must preserve.
func TestRegionalToGlobalInstanceProfile(t *testing.T) {
	ctx := context.Background()
	east, west := twoRegions(t)

	// Create the role + instance profile on the shared IAM (via the east handle;
	// it is the same instance the west provider is wired to).
	if _, err := east.IAM.CreateRole(ctx, iamdriver.RoleConfig{Name: "app-role"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	prof, err := east.IAM.CreateInstanceProfile(ctx, iamdriver.InstanceProfileConfig{Name: "app-profile"})
	if err != nil {
		t.Fatalf("create instance profile: %v", err)
	}
	if err := east.IAM.AddRoleToInstanceProfile(ctx, "app-profile", "app-role"); err != nil {
		t.Fatalf("add role to profile: %v", err)
	}

	insts, err := west.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-1", InstanceType: "t3.micro", IamInstanceProfileName: "app-profile",
	}, 1)
	if err != nil {
		t.Fatalf("run instances west: %v", err)
	}
	if got := insts[0].IamInstanceProfile; got == nil || got.ARN != prof.ARN {
		t.Fatalf("west instance profile = %+v, want ARN %q from shared IAM", got, prof.ARN)
	}
}

// TestCrossServiceMetricsSameRegion proves EC2's auto-metrics land in the SAME
// region's CloudWatch and are absent in another region's.
func TestCrossServiceMetricsSameRegion(t *testing.T) {
	ctx := context.Background()
	east, west := twoRegions(t)

	if _, err := west.EC2.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "ami-1", InstanceType: "t3.micro"}, 1); err != nil {
		t.Fatalf("run instances west: %v", err)
	}

	westMetrics, err := west.CloudWatch.ListMetrics(ctx, "AWS/EC2")
	if err != nil {
		t.Fatalf("west ListMetrics: %v", err)
	}
	if len(westMetrics) == 0 {
		t.Fatal("west CloudWatch has no AWS/EC2 metrics after RunInstances")
	}

	eastMetrics, err := east.CloudWatch.ListMetrics(ctx, "AWS/EC2")
	if err != nil {
		t.Fatalf("east ListMetrics: %v", err)
	}
	if len(eastMetrics) != 0 {
		t.Fatalf("east CloudWatch has %d AWS/EC2 metrics, want 0 (cross-region leak)", len(eastMetrics))
	}
}

// TestSnapshotServicesSplit guards the Global/Regional partition: the two subsets
// are disjoint, their union is the full set, and the global keys are exactly the
// classified ones that are snapshottable.
func TestSnapshotServicesSplit(t *testing.T) {
	p := cloudemu.NewAWS()

	all := p.SnapshotServices()
	global := p.GlobalSnapshotServices()
	regional := p.RegionalSnapshotServices()

	if len(global)+len(regional) != len(all) {
		t.Fatalf("split sizes: global %d + regional %d != all %d", len(global), len(regional), len(all))
	}

	for k := range global {
		if _, dup := regional[k]; dup {
			t.Errorf("key %q appears in both global and regional", k)
		}
		if _, ok := all[k]; !ok {
			t.Errorf("global key %q missing from full set", k)
		}
	}

	// IAM is global; S3 and DynamoDB are regional.
	if _, ok := global["iam"]; !ok {
		t.Error("iam should be a global snapshot service")
	}
	if _, ok := regional["s3"]; !ok {
		t.Error("s3 should be a regional snapshot service")
	}
	if _, ok := regional["dynamodb"]; !ok {
		t.Error("dynamodb should be a regional snapshot service")
	}
	if _, ok := global["s3"]; ok {
		t.Error("s3 must not be classified global")
	}
}
