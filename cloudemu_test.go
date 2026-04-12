package cloudemu

import (
	"context"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/compute"
	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/cost"
	"github.com/stackshy/cloudemu/database/driver"
	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	iamdriver "github.com/stackshy/cloudemu/iam/driver"
	"github.com/stackshy/cloudemu/inject"
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
	"github.com/stackshy/cloudemu/metrics"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	serverlessdriver "github.com/stackshy/cloudemu/serverless/driver"
	"github.com/stackshy/cloudemu/storage"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"

	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"

	cachedriver "github.com/stackshy/cloudemu/cache/driver"
	crdriver "github.com/stackshy/cloudemu/containerregistry/driver"
	ebdriver "github.com/stackshy/cloudemu/eventbus/driver"
	loggingdriver "github.com/stackshy/cloudemu/logging/driver"
	notifdriver "github.com/stackshy/cloudemu/notification/driver"
	secretsdriver "github.com/stackshy/cloudemu/secrets/driver"
)

func TestStorageLifecycle(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	// Create bucket
	if err := p.S3.CreateBucket(ctx, "my-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	// Put object
	if err := p.S3.PutObject(ctx, "my-bucket", "docs/hello.txt", []byte("hello world"), "text/plain", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// Get object
	obj, err := p.S3.GetObject(ctx, "my-bucket", "docs/hello.txt")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if string(obj.Data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(obj.Data))
	}

	// List with prefix
	result, err := p.S3.ListObjects(ctx, "my-bucket", storagedriver.ListOptions{Prefix: "docs/"})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(result.Objects) != 1 {
		t.Errorf("expected 1 object, got %d", len(result.Objects))
	}

	// List with delimiter
	if err := p.S3.PutObject(ctx, "my-bucket", "images/photo.jpg", []byte("jpg"), "image/jpeg", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	result, err = p.S3.ListObjects(ctx, "my-bucket", storagedriver.ListOptions{Delimiter: "/"})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(result.CommonPrefixes) != 2 {
		t.Errorf("expected 2 common prefixes, got %d: %v", len(result.CommonPrefixes), result.CommonPrefixes)
	}

	// Delete object
	if err := p.S3.DeleteObject(ctx, "my-bucket", "docs/hello.txt"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	// Verify not found
	_, err = p.S3.GetObject(ctx, "my-bucket", "docs/hello.txt")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestStoragePagination(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	if err := p.S3.CreateBucket(ctx, "pag-bucket"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		key := "key" + string(rune('A'+i))
		p.S3.PutObject(ctx, "pag-bucket", key, []byte("data"), "text/plain", nil)
	}

	// Page 1: 2 items
	result, err := p.S3.ListObjects(ctx, "pag-bucket", storagedriver.ListOptions{MaxKeys: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Objects) != 2 {
		t.Errorf("page 1: expected 2 objects, got %d", len(result.Objects))
	}
	if !result.IsTruncated {
		t.Error("page 1: expected truncated")
	}

	// Page 2
	result, err = p.S3.ListObjects(ctx, "pag-bucket", storagedriver.ListOptions{MaxKeys: 2, PageToken: result.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Objects) != 2 {
		t.Errorf("page 2: expected 2 objects, got %d", len(result.Objects))
	}

	// Page 3
	result, err = p.S3.ListObjects(ctx, "pag-bucket", storagedriver.ListOptions{MaxKeys: 2, PageToken: result.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Objects) != 1 {
		t.Errorf("page 3: expected 1 object, got %d", len(result.Objects))
	}
	if result.IsTruncated {
		t.Error("page 3: expected not truncated")
	}
}

func TestComputeLifecycle(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	// Create VM
	instances, err := p.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-123", InstanceType: "t2.micro",
		Tags: map[string]string{"env": "test"},
	}, 1)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	id := instances[0].ID
	if instances[0].State != compute.StateRunning {
		t.Errorf("expected running, got %s", instances[0].State)
	}

	// Stop VM
	if err := p.EC2.StopInstances(ctx, []string{id}); err != nil {
		t.Fatalf("StopInstances: %v", err)
	}

	// Verify stopped
	descs, _ := p.EC2.DescribeInstances(ctx, []string{id}, nil)
	if descs[0].State != compute.StateStopped {
		t.Errorf("expected stopped, got %s", descs[0].State)
	}

	// Start VM
	if err := p.EC2.StartInstances(ctx, []string{id}); err != nil {
		t.Fatalf("StartInstances: %v", err)
	}

	// Terminate
	if err := p.EC2.TerminateInstances(ctx, []string{id}); err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}

	// Verify can't stop terminated
	err = p.EC2.StopInstances(ctx, []string{id})
	if err == nil {
		t.Error("expected error stopping terminated instance")
	}
}

func TestDatabaseLifecycle(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	// Create table
	if err := p.DynamoDB.CreateTable(ctx, driver.TableConfig{
		Name: "users", PartitionKey: "pk", SortKey: "sk",
	}); err != nil {
		t.Fatal(err)
	}

	// Put items
	items := []map[string]interface{}{
		{"pk": "user1", "sk": "profile", "name": "Alice"},
		{"pk": "user1", "sk": "settings", "theme": "dark"},
		{"pk": "user2", "sk": "profile", "name": "Bob"},
	}
	for _, item := range items {
		if err := p.DynamoDB.PutItem(ctx, "users", item); err != nil {
			t.Fatal(err)
		}
	}

	// Get item
	item, err := p.DynamoDB.GetItem(ctx, "users", map[string]interface{}{"pk": "user1", "sk": "profile"})
	if err != nil {
		t.Fatal(err)
	}
	if item["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", item["name"])
	}

	// Query by key condition
	result, err := p.DynamoDB.Query(ctx, driver.QueryInput{
		Table: "users",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "pk", PartitionVal: "user1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 {
		t.Errorf("expected 2 items for user1, got %d", result.Count)
	}

	// Scan with filter
	scanResult, err := p.DynamoDB.Scan(ctx, driver.ScanInput{
		Table: "users",
		Filters: []driver.ScanFilter{
			{Field: "name", Op: "=", Value: "Bob"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanResult.Count != 1 {
		t.Errorf("expected 1 item matching Bob, got %d", scanResult.Count)
	}

	// Batch operations
	batchItems := []map[string]interface{}{
		{"pk": "user3", "sk": "profile", "name": "Charlie"},
		{"pk": "user4", "sk": "profile", "name": "Diana"},
	}
	if err := p.DynamoDB.BatchPutItems(ctx, "users", batchItems); err != nil {
		t.Fatal(err)
	}
	batchResults, err := p.DynamoDB.BatchGetItems(ctx, "users", []map[string]interface{}{
		{"pk": "user3", "sk": "profile"},
		{"pk": "user4", "sk": "profile"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(batchResults) != 2 {
		t.Errorf("expected 2 batch results, got %d", len(batchResults))
	}
}

func TestErrorInjection(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Now())
	p := NewAWS(config.WithClock(clock))

	rec := recorder.New()
	inj := inject.NewInjector()

	bucket := storage.NewBucket(p.S3,
		storage.WithRecorder(rec),
		storage.WithErrorInjection(inj),
	)

	// Set up: inject NotFound on 3rd call
	inj.Set("storage", "GetObject", cerrors.New(cerrors.NotFound, "injected"), inject.NewNthCall(3))

	bucket.CreateBucket(ctx, "test-bucket")
	bucket.PutObject(ctx, "test-bucket", "key1", []byte("data"), "text/plain", nil)

	// First 2 GetObject succeed
	if _, err := bucket.GetObject(ctx, "test-bucket", "key1"); err != nil {
		t.Errorf("call 1 should succeed: %v", err)
	}
	if _, err := bucket.GetObject(ctx, "test-bucket", "key1"); err != nil {
		t.Errorf("call 2 should succeed: %v", err)
	}
	// 3rd fails
	_, err := bucket.GetObject(ctx, "test-bucket", "key1")
	if !cerrors.IsNotFound(err) {
		t.Errorf("call 3 should fail with NotFound, got: %v", err)
	}
}

func TestRateLimiting(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Now())
	p := NewAWS(config.WithClock(clock))

	// Create bucket without rate limiter
	p.S3.CreateBucket(ctx, "rl-bucket")

	limiter := ratelimit.New(2, 2, clock) // 2 req/sec, burst 2
	bucket := storage.NewBucket(p.S3, storage.WithRateLimiter(limiter))

	// First 2 calls succeed (burst)
	if err := bucket.PutObject(ctx, "rl-bucket", "k1", []byte("d"), "", nil); err != nil {
		t.Errorf("call 1 should succeed: %v", err)
	}
	if err := bucket.PutObject(ctx, "rl-bucket", "k2", []byte("d"), "", nil); err != nil {
		t.Errorf("call 2 should succeed: %v", err)
	}

	// 3rd should be throttled
	err := bucket.PutObject(ctx, "rl-bucket", "k3", []byte("d"), "", nil)
	if !cerrors.IsThrottled(err) {
		t.Errorf("call 3 should be throttled, got: %v", err)
	}

	// Advance clock, should succeed again
	clock.Advance(time.Second)
	if err := bucket.PutObject(ctx, "rl-bucket", "k4", []byte("d"), "", nil); err != nil {
		t.Errorf("after advance, call should succeed: %v", err)
	}
}

func TestRecorder(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	rec := recorder.New()
	bucket := storage.NewBucket(p.S3, storage.WithRecorder(rec))

	bucket.CreateBucket(ctx, "rec-bucket")
	bucket.PutObject(ctx, "rec-bucket", "key1", []byte("data"), "", nil)
	bucket.GetObject(ctx, "rec-bucket", "key1")

	if rec.CallCount() != 3 {
		t.Errorf("expected 3 calls, got %d", rec.CallCount())
	}
	if rec.CallCountFor("storage", "PutObject") != 1 {
		t.Errorf("expected 1 PutObject call, got %d", rec.CallCountFor("storage", "PutObject"))
	}
}

func TestMetrics(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	mc := metrics.NewCollector()
	bucket := storage.NewBucket(p.S3, storage.WithMetrics(mc))

	bucket.CreateBucket(ctx, "met-bucket")
	bucket.PutObject(ctx, "met-bucket", "key1", []byte("data"), "", nil)
	bucket.GetObject(ctx, "met-bucket", "key1")

	q := metrics.NewQuery(mc).ByName("calls_total")
	if q.Count() != 3 {
		t.Errorf("expected 3 call metrics, got %d", q.Count())
	}
	if q.Sum() != 3 {
		t.Errorf("expected sum of 3, got %f", q.Sum())
	}
}

func TestCrossProvider(t *testing.T) {
	ctx := context.Background()

	// Use the top-level NewAWS/NewAzure/NewGCP entry points
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name    string
		storage storagedriver.Bucket
	}{
		{"aws", awsP.S3},
		{"azure", azureP.BlobStorage},
		{"gcp", gcpP.GCS},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			bkt := storage.NewBucket(p.storage)

			if err := bkt.CreateBucket(ctx, "cross-bucket"); err != nil {
				t.Fatalf("CreateBucket: %v", err)
			}
			if err := bkt.PutObject(ctx, "cross-bucket", "hello.txt", []byte("hello"), "text/plain", nil); err != nil {
				t.Fatalf("PutObject: %v", err)
			}
			obj, err := bkt.GetObject(ctx, "cross-bucket", "hello.txt")
			if err != nil {
				t.Fatalf("GetObject: %v", err)
			}
			if string(obj.Data) != "hello" {
				t.Errorf("expected 'hello', got %q", string(obj.Data))
			}
			if err := bkt.DeleteObject(ctx, "cross-bucket", "hello.txt"); err != nil {
				t.Fatalf("DeleteObject: %v", err)
			}
			if err := bkt.DeleteBucket(ctx, "cross-bucket"); err != nil {
				t.Fatalf("DeleteBucket: %v", err)
			}
		})
	}
}

// ==============================================================================
// Real-World Scenario Tests
// These simulate what a real user would do: create a cloud environment,
// seed resources, then perform operations — all without real cloud resources.
// ==============================================================================

// TestRealWorldAWS_InfraSetup simulates setting up a full AWS infrastructure:
// VPC → Subnets → Security Groups → EC2 instances → S3 buckets → DNS → Monitoring
func TestRealWorldAWS_InfraSetup(t *testing.T) {
	ctx := context.Background()
	aws := NewAWS()

	// 1. Create VPC and networking
	vpc, err := aws.VPC.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16",
		Tags:      map[string]string{"env": "production"},
	})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	subnet, err := aws.VPC.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: vpc.ID, CIDRBlock: "10.0.1.0/24", AvailabilityZone: "us-east-1a",
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	sg, err := aws.VPC.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
		Name: "web-sg", Description: "Web servers", VPCID: vpc.ID,
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}

	// Add firewall rules
	if err := aws.VPC.AddIngressRule(ctx, sg.ID, netdriver.SecurityRule{
		Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0",
	}); err != nil {
		t.Fatalf("AddIngressRule: %v", err)
	}

	// 2. Launch EC2 instances
	instances, err := aws.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-web-server", InstanceType: "t3.large",
		SubnetID: subnet.ID, SecurityGroups: []string{sg.ID},
		Tags: map[string]string{"app": "web", "env": "production"},
	}, 3)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if len(instances) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(instances))
	}

	// 3. List running instances — like a real dashboard would
	allInstances, err := aws.EC2.DescribeInstances(ctx, nil, []computedriver.DescribeFilter{
		{Name: "instance-state-name", Values: []string{compute.StateRunning}},
	})
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}
	if len(allInstances) != 3 {
		t.Errorf("expected 3 running instances, got %d", len(allInstances))
	}

	// 4. Stop one instance for maintenance
	if err := aws.EC2.StopInstances(ctx, []string{instances[0].ID}); err != nil {
		t.Fatalf("StopInstances: %v", err)
	}
	desc, _ := aws.EC2.DescribeInstances(ctx, []string{instances[0].ID}, nil)
	if desc[0].State != compute.StateStopped {
		t.Errorf("expected stopped, got %s", desc[0].State)
	}

	// 5. Modify stopped instance (resize)
	if err := aws.EC2.ModifyInstance(ctx, instances[0].ID, computedriver.ModifyInstanceInput{
		InstanceType: "t3.xlarge",
	}); err != nil {
		t.Fatalf("ModifyInstance: %v", err)
	}

	// 6. Start it back
	if err := aws.EC2.StartInstances(ctx, []string{instances[0].ID}); err != nil {
		t.Fatalf("StartInstances: %v", err)
	}

	// 7. Verify the resize took effect
	desc, _ = aws.EC2.DescribeInstances(ctx, []string{instances[0].ID}, nil)
	if desc[0].InstanceType != "t3.xlarge" {
		t.Errorf("expected t3.xlarge, got %s", desc[0].InstanceType)
	}
	if desc[0].State != compute.StateRunning {
		t.Errorf("expected running, got %s", desc[0].State)
	}

	// 8. Create S3 bucket and upload app configs
	if err := aws.S3.CreateBucket(ctx, "app-configs"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := aws.S3.PutObject(ctx, "app-configs", "prod/config.json", []byte(`{"db":"rds-prod"}`), "application/json", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if err := aws.S3.PutObject(ctx, "app-configs", "prod/secrets.enc", []byte("encrypted"), "application/octet-stream", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// 9. List objects in bucket — like S3 console
	listResult, err := aws.S3.ListObjects(ctx, "app-configs", storagedriver.ListOptions{Prefix: "prod/"})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(listResult.Objects) != 2 {
		t.Errorf("expected 2 objects, got %d", len(listResult.Objects))
	}

	// 10. Set up DNS
	zone, err := aws.Route53.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "myapp.com"})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	_, err = aws.Route53.CreateRecord(ctx, dnsdriver.RecordConfig{
		ZoneID: zone.ID, Name: "api.myapp.com", Type: "A", TTL: 300,
		Values: []string{instances[0].PrivateIP},
	})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	// 11. Verify DNS record exists
	rec, err := aws.Route53.GetRecord(ctx, zone.ID, "api.myapp.com", "A")
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.Values[0] != instances[0].PrivateIP {
		t.Errorf("DNS record IP mismatch: got %s, want %s", rec.Values[0], instances[0].PrivateIP)
	}

	// 12. Push metrics (like a monitoring agent would)
	now := time.Now()
	if err := aws.CloudWatch.PutMetricData(ctx, []mondriver.MetricDatum{
		{Namespace: "App/Web", MetricName: "CPUUtilization", Value: 45.2, Timestamp: now, Dimensions: map[string]string{"InstanceId": instances[0].ID}},
		{Namespace: "App/Web", MetricName: "CPUUtilization", Value: 72.8, Timestamp: now, Dimensions: map[string]string{"InstanceId": instances[1].ID}},
		{Namespace: "App/Web", MetricName: "RequestCount", Value: 1500, Timestamp: now},
	}); err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	// 13. Query metrics — like CloudWatch dashboard
	cpuResult, err := aws.CloudWatch.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace: "App/Web", MetricName: "CPUUtilization",
		Dimensions: map[string]string{"InstanceId": instances[1].ID},
		StartTime:  now.Add(-time.Minute), EndTime: now.Add(time.Minute),
		Period: 60, Stat: "Average",
	})
	if err != nil {
		t.Fatalf("GetMetricData: %v", err)
	}
	if len(cpuResult.Values) != 1 || cpuResult.Values[0] != 72.8 {
		t.Errorf("expected CPU 72.8, got %v", cpuResult.Values)
	}

	// 14. Create alarm for high CPU
	if err := aws.CloudWatch.CreateAlarm(ctx, mondriver.AlarmConfig{
		Name: "high-cpu", Namespace: "App/Web", MetricName: "CPUUtilization",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 80,
		Period: 300, EvaluationPeriods: 2, Stat: "Average",
	}); err != nil {
		t.Fatalf("CreateAlarm: %v", err)
	}

	alarms, err := aws.CloudWatch.DescribeAlarms(ctx, []string{"high-cpu"})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}
	if len(alarms) != 1 {
		t.Errorf("expected 1 alarm, got %d", len(alarms))
	}

	// 15. Terminate all instances
	ids := make([]string, len(instances))
	for i, inst := range instances {
		ids[i] = inst.ID
	}
	if err := aws.EC2.TerminateInstances(ctx, ids); err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}

	// 16. Verify VPC resources still exist after instance termination
	vpcs, err := aws.VPC.DescribeVPCs(ctx, []string{vpc.ID})
	if err != nil {
		t.Fatalf("DescribeVPCs: %v", err)
	}
	if len(vpcs) != 1 {
		t.Errorf("VPC should still exist, got %d", len(vpcs))
	}
}

// TestRealWorldAzure_InfraSetup simulates the same flow on Azure.
func TestRealWorldAzure_InfraSetup(t *testing.T) {
	ctx := context.Background()
	az := NewAzure()

	// 1. Create VNet + subnet
	vnet, err := az.VNet.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16",
		Tags:      map[string]string{"env": "staging"},
	})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	_, err = az.VNet.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID: vnet.ID, CIDRBlock: "10.0.1.0/24",
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	// 2. Launch VMs
	instances, err := az.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "UbuntuServer-20.04", InstanceType: "Standard_D2s_v3",
		Tags: map[string]string{"app": "api"},
	}, 2)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("expected 2 VMs, got %d", len(instances))
	}

	// 3. Upload to Blob Storage
	if err := az.BlobStorage.CreateBucket(ctx, "deployments"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := az.BlobStorage.PutObject(ctx, "deployments", "v1.2.3/app.zip", []byte("binary"), "application/zip", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// 4. Verify data
	obj, err := az.BlobStorage.GetObject(ctx, "deployments", "v1.2.3/app.zip")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if string(obj.Data) != "binary" {
		t.Errorf("expected 'binary', got %q", string(obj.Data))
	}

	// 5. Stop and restart VMs
	if err := az.VirtualMachines.StopInstances(ctx, []string{instances[0].ID}); err != nil {
		t.Fatalf("StopInstances: %v", err)
	}
	if err := az.VirtualMachines.StartInstances(ctx, []string{instances[0].ID}); err != nil {
		t.Fatalf("StartInstances: %v", err)
	}
	desc, _ := az.VirtualMachines.DescribeInstances(ctx, []string{instances[0].ID}, nil)
	if desc[0].State != compute.StateRunning {
		t.Errorf("expected running after restart, got %s", desc[0].State)
	}
}

// TestRealWorldGCP_InfraSetup simulates the same flow on GCP.
func TestRealWorldGCP_InfraSetup(t *testing.T) {
	ctx := context.Background()
	gcp := NewGCP()

	// 1. Create VPC
	vpc, err := gcp.VPC.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16",
		Tags:      map[string]string{"project": "my-gcp-project"},
	})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	// 2. Launch Compute Engine instances
	instances, err := gcp.GCE.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "debian-11", InstanceType: "n2-standard-4",
		Tags: map[string]string{"tier": "backend"},
	}, 2)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	// 3. Store data in Cloud Storage
	if err := gcp.GCS.CreateBucket(ctx, "my-gcp-data"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := gcp.GCS.PutObject(ctx, "my-gcp-data", "models/trained.bin", []byte("model-data"), "application/octet-stream", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// 4. List all resources
	buckets, err := gcp.GCS.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	if len(buckets) != 1 {
		t.Errorf("expected 1 bucket, got %d", len(buckets))
	}

	allVMs, err := gcp.GCE.DescribeInstances(ctx, nil, nil)
	if err != nil {
		t.Fatalf("DescribeInstances: %v", err)
	}
	if len(allVMs) != 2 {
		t.Errorf("expected 2 VMs, got %d", len(allVMs))
	}

	vpcs, err := gcp.VPC.DescribeVPCs(ctx, nil)
	if err != nil {
		t.Fatalf("DescribeVPCs: %v", err)
	}
	if len(vpcs) != 1 {
		t.Errorf("expected 1 VPC, got %d", len(vpcs))
	}

	// 5. Set up DNS
	zone, err := gcp.CloudDNS.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "myapp.dev"})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	_, err = gcp.CloudDNS.CreateRecord(ctx, dnsdriver.RecordConfig{
		ZoneID: zone.ID, Name: "api.myapp.dev", Type: "A", TTL: 60,
		Values: []string{instances[0].PrivateIP},
	})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	// 6. Push and query metrics
	now := time.Now()
	gcp.CloudMonitoring.PutMetricData(ctx, []mondriver.MetricDatum{
		{Namespace: "custom", MetricName: "requests_per_sec", Value: 250, Timestamp: now},
	})
	result, err := gcp.CloudMonitoring.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace: "custom", MetricName: "requests_per_sec",
		StartTime: now.Add(-time.Minute), EndTime: now.Add(time.Minute),
		Period: 60, Stat: "Sum",
	})
	if err != nil {
		t.Fatalf("GetMetricData: %v", err)
	}
	if len(result.Values) != 1 || result.Values[0] != 250 {
		t.Errorf("expected 250, got %v", result.Values)
	}

	// 7. Terminate instances
	if err := gcp.GCE.TerminateInstances(ctx, []string{instances[0].ID, instances[1].ID}); err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}

	// VPC still exists
	vpcs, _ = gcp.VPC.DescribeVPCs(ctx, []string{vpc.ID})
	if len(vpcs) != 1 {
		t.Errorf("VPC should still exist")
	}
}

// ==============================================================================
// New Tests: Fixing 6 gaps to make CloudEmu behave like real cloud
// ==============================================================================

func TestScanMissingOperators(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	if err := p.DynamoDB.CreateTable(ctx, driver.TableConfig{
		Name: "products", PartitionKey: "pk", SortKey: "sk",
	}); err != nil {
		t.Fatal(err)
	}

	items := []map[string]interface{}{
		{"pk": "cat1", "sk": "item1", "price": 5, "name": "alpha-one"},
		{"pk": "cat1", "sk": "item2", "price": 10, "name": "alpha-two"},
		{"pk": "cat1", "sk": "item3", "price": 15, "name": "beta-one"},
		{"pk": "cat1", "sk": "item4", "price": 20, "name": "beta-two"},
	}
	for _, item := range items {
		if err := p.DynamoDB.PutItem(ctx, "products", item); err != nil {
			t.Fatal(err)
		}
	}

	// Test <= operator
	result, err := p.DynamoDB.Scan(ctx, driver.ScanInput{
		Table:   "products",
		Filters: []driver.ScanFilter{{Field: "price", Op: "<=", Value: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 {
		t.Errorf("<= filter: expected 2 items, got %d", result.Count)
	}

	// Test >= operator
	result, err = p.DynamoDB.Scan(ctx, driver.ScanInput{
		Table:   "products",
		Filters: []driver.ScanFilter{{Field: "price", Op: ">=", Value: 15}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 {
		t.Errorf(">= filter: expected 2 items, got %d", result.Count)
	}

	// Test BEGINS_WITH operator
	result, err = p.DynamoDB.Scan(ctx, driver.ScanInput{
		Table:   "products",
		Filters: []driver.ScanFilter{{Field: "name", Op: "BEGINS_WITH", Value: "alpha"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 {
		t.Errorf("BEGINS_WITH filter: expected 2 items, got %d", result.Count)
	}
}

func TestNumericComparison(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	if err := p.DynamoDB.CreateTable(ctx, driver.TableConfig{
		Name: "numbers", PartitionKey: "pk", SortKey: "sk",
	}); err != nil {
		t.Fatal(err)
	}

	// Insert items with numeric values that would sort wrong as strings
	// String sort: "10" < "9" (wrong), Numeric sort: 10 > 9 (correct)
	items := []map[string]interface{}{
		{"pk": "g1", "sk": "a", "val": 1},
		{"pk": "g1", "sk": "b", "val": 5},
		{"pk": "g1", "sk": "c", "val": 9},
		{"pk": "g1", "sk": "d", "val": 10},
		{"pk": "g1", "sk": "e", "val": 20},
	}
	for _, item := range items {
		if err := p.DynamoDB.PutItem(ctx, "numbers", item); err != nil {
			t.Fatal(err)
		}
	}

	// Scan: val > 9 should return items with val=10 and val=20
	result, err := p.DynamoDB.Scan(ctx, driver.ScanInput{
		Table:   "numbers",
		Filters: []driver.ScanFilter{{Field: "val", Op: ">", Value: 9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 {
		t.Errorf("numeric > filter: expected 2 items (10, 20), got %d", result.Count)
	}

	// Scan: val < 10 should return items with val=1, 5, 9
	result, err = p.DynamoDB.Scan(ctx, driver.ScanInput{
		Table:   "numbers",
		Filters: []driver.ScanFilter{{Field: "val", Op: "<", Value: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 3 {
		t.Errorf("numeric < filter: expected 3 items (1, 5, 9), got %d", result.Count)
	}
}

func TestFIFODeduplication(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	p := NewAWS(config.WithClock(clock))

	// Create FIFO queue
	qInfo, err := p.SQS.CreateQueue(ctx, mqdriver.QueueConfig{
		Name: "test-queue.fifo",
		FIFO: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Send first message
	out1, err := p.SQS.SendMessage(ctx, mqdriver.SendMessageInput{
		QueueURL:        qInfo.URL,
		Body:            "hello",
		GroupID:         "group1",
		DeduplicationID: "dedup-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Send duplicate within 5-min window — should return same message ID
	out2, err := p.SQS.SendMessage(ctx, mqdriver.SendMessageInput{
		QueueURL:        qInfo.URL,
		Body:            "hello again",
		GroupID:         "group1",
		DeduplicationID: "dedup-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out1.MessageID != out2.MessageID {
		t.Errorf("duplicate within 5 min should return same MessageID: got %s and %s", out1.MessageID, out2.MessageID)
	}

	// Verify only 1 message in queue
	info, _ := p.SQS.GetQueueInfo(ctx, qInfo.URL)
	if info.ApproxMessageCount != 1 {
		t.Errorf("expected 1 message in queue, got %d", info.ApproxMessageCount)
	}

	// Advance clock past 5-minute window
	clock.Advance(6 * time.Minute)

	// Send same dedup ID again — should be accepted as new message
	out3, err := p.SQS.SendMessage(ctx, mqdriver.SendMessageInput{
		QueueURL:        qInfo.URL,
		Body:            "hello after window",
		GroupID:         "group1",
		DeduplicationID: "dedup-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out3.MessageID == out1.MessageID {
		t.Error("message after 5 min window should have new MessageID")
	}
}

func TestIAMCheckPermission(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	// Create user
	_, err := p.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	// No policies attached — should deny
	allowed, err := p.IAM.CheckPermission(ctx, "alice", "s3:GetObject", "arn:aws:s3:::my-bucket/*")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Error("expected deny with no policies attached")
	}

	// Create Allow policy for s3:*
	allowPolicy, err := p.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{
		Name: "s3-allow",
		PolicyDocument: `{
			"Version": "2012-10-17",
			"Statement": [
				{"Effect": "Allow", "Action": "s3:*", "Resource": "*"}
			]
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Attach and check
	if err := p.IAM.AttachUserPolicy(ctx, "alice", allowPolicy.ARN); err != nil {
		t.Fatal(err)
	}

	allowed, err = p.IAM.CheckPermission(ctx, "alice", "s3:GetObject", "arn:aws:s3:::my-bucket/key")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Error("expected allow with s3:* policy")
	}

	// Non-matching action should deny
	allowed, err = p.IAM.CheckPermission(ctx, "alice", "ec2:DescribeInstances", "arn:aws:ec2:::*")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Error("expected deny for ec2 action with only s3 policy")
	}

	// Create explicit Deny policy for s3:DeleteObject
	denyPolicy, err := p.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{
		Name: "s3-deny-delete",
		PolicyDocument: `{
			"Version": "2012-10-17",
			"Statement": [
				{"Effect": "Deny", "Action": "s3:DeleteObject", "Resource": "*"}
			]
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.IAM.AttachUserPolicy(ctx, "alice", denyPolicy.ARN); err != nil {
		t.Fatal(err)
	}

	// Explicit Deny should win even though Allow s3:* is also attached
	allowed, err = p.IAM.CheckPermission(ctx, "alice", "s3:DeleteObject", "arn:aws:s3:::my-bucket/key")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Error("expected deny: explicit Deny should override Allow")
	}
}

func TestAlarmAutoEvaluation(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC))
	p := NewAWS(config.WithClock(clock))

	// Create alarm: trigger if Average CPU > 80
	if err := p.CloudWatch.CreateAlarm(ctx, mondriver.AlarmConfig{
		Name: "high-cpu", Namespace: "AWS/EC2", MetricName: "CPUUtilization",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 80,
		Period: 300, EvaluationPeriods: 1, Stat: "Average",
	}); err != nil {
		t.Fatal(err)
	}

	// Verify initial state is INSUFFICIENT_DATA
	alarms, _ := p.CloudWatch.DescribeAlarms(ctx, []string{"high-cpu"})
	if alarms[0].State != "INSUFFICIENT_DATA" {
		t.Errorf("expected INSUFFICIENT_DATA, got %s", alarms[0].State)
	}

	// Push metric data below threshold
	now := clock.Now()
	if err := p.CloudWatch.PutMetricData(ctx, []mondriver.MetricDatum{
		{Namespace: "AWS/EC2", MetricName: "CPUUtilization", Value: 50, Timestamp: now},
	}); err != nil {
		t.Fatal(err)
	}

	// Alarm should transition to OK
	alarms, _ = p.CloudWatch.DescribeAlarms(ctx, []string{"high-cpu"})
	if alarms[0].State != "OK" {
		t.Errorf("expected OK after below-threshold data, got %s", alarms[0].State)
	}

	// Advance clock past the evaluation window so the old data point falls out
	clock.Advance(10 * time.Minute)
	now = clock.Now()

	// Push metric data above threshold
	if err := p.CloudWatch.PutMetricData(ctx, []mondriver.MetricDatum{
		{Namespace: "AWS/EC2", MetricName: "CPUUtilization", Value: 95, Timestamp: now},
	}); err != nil {
		t.Fatal(err)
	}

	// Alarm should transition to ALARM
	alarms, _ = p.CloudWatch.DescribeAlarms(ctx, []string{"high-cpu"})
	if alarms[0].State != "ALARM" {
		t.Errorf("expected ALARM after above-threshold data, got %s", alarms[0].State)
	}
}

func TestAutoMetricGeneration(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	// Launch an instance
	_, err := p.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Verify auto-generated metrics exist in CloudWatch
	metricNames, err := p.CloudWatch.ListMetrics(ctx, "AWS/EC2")
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"CPUUtilization", "DiskReadOps", "DiskWriteOps", "NetworkIn", "NetworkOut"}
	sort.Strings(metricNames)
	if len(metricNames) != len(expected) {
		t.Fatalf("expected %d metrics, got %d: %v", len(expected), len(metricNames), metricNames)
	}
	for i, name := range expected {
		if metricNames[i] != name {
			t.Errorf("expected metric %q, got %q", name, metricNames[i])
		}
	}
}

func TestAlarmTriggeredByAutoMetrics(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC))
	p := NewAWS(config.WithClock(clock))

	// Create alarm: CPU > 0 (any CPU should trigger since auto-metrics emit 25.0)
	if err := p.CloudWatch.CreateAlarm(ctx, mondriver.AlarmConfig{
		Name: "any-cpu", Namespace: "AWS/EC2", MetricName: "CPUUtilization",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 0,
		Period: 600, EvaluationPeriods: 1, Stat: "Average",
	}); err != nil {
		t.Fatal(err)
	}

	// Verify initial state
	alarms, _ := p.CloudWatch.DescribeAlarms(ctx, []string{"any-cpu"})
	if alarms[0].State != "INSUFFICIENT_DATA" {
		t.Errorf("expected INSUFFICIENT_DATA, got %s", alarms[0].State)
	}

	// Launch instance — auto-metrics should trigger alarm evaluation
	_, err := p.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Alarm should now be in ALARM state (CPU 25.0 > 0)
	alarms, _ = p.CloudWatch.DescribeAlarms(ctx, []string{"any-cpu"})
	if alarms[0].State != "ALARM" {
		t.Errorf("expected ALARM after auto-metrics (CPU=25 > 0), got %s", alarms[0].State)
	}
}

func TestLifecycleStopEmitsZeroMetrics(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC))
	p := NewAWS(config.WithClock(clock))

	// Create alarm: CPU < 1 (LessThanThreshold)
	if err := p.CloudWatch.CreateAlarm(ctx, mondriver.AlarmConfig{
		Name: "low-cpu", Namespace: "AWS/EC2", MetricName: "CPUUtilization",
		ComparisonOperator: "LessThanThreshold", Threshold: 1,
		Period: 300, EvaluationPeriods: 1, Stat: "Average",
	}); err != nil {
		t.Fatal(err)
	}

	// RunInstances → CPU=25 → alarm stays OK (25 is not < 1)
	instances, err := p.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	id := instances[0].ID

	alarms, _ := p.CloudWatch.DescribeAlarms(ctx, []string{"low-cpu"})
	if alarms[0].State != "OK" {
		t.Errorf("expected OK after RunInstances (CPU=25, not < 1), got %s", alarms[0].State)
	}

	// Advance clock past evaluation window so old datapoints fall out
	clock.Advance(11 * time.Minute)

	// StopInstances → CPU=0 → alarm fires ALARM (0 < 1)
	if err := p.EC2.StopInstances(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}

	alarms, _ = p.CloudWatch.DescribeAlarms(ctx, []string{"low-cpu"})
	if alarms[0].State != "ALARM" {
		t.Errorf("expected ALARM after StopInstances (CPU=0 < 1), got %s", alarms[0].State)
	}
}

func TestLifecycleStartEmitsRunningMetrics(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC))
	p := NewAWS(config.WithClock(clock))

	// Create alarm: CPU > 0
	if err := p.CloudWatch.CreateAlarm(ctx, mondriver.AlarmConfig{
		Name: "any-cpu-start", Namespace: "AWS/EC2", MetricName: "CPUUtilization",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 0,
		Period: 300, EvaluationPeriods: 1, Stat: "Average",
	}); err != nil {
		t.Fatal(err)
	}

	// RunInstances → stop → advance clock → start
	instances, err := p.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t2.micro",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	id := instances[0].ID

	if err := p.EC2.StopInstances(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}

	// Advance clock past evaluation window so old datapoints fall out
	clock.Advance(11 * time.Minute)

	// Stop emitted zeros, so after window expiry only zeros remain → alarm should be OK
	// But we need fresh data, so push a zero to re-evaluate
	// Actually, StopInstances already pushed zeros at t0+0. After advancing 11min,
	// those zeros are outside the 5min window. We need StartInstances to push new running data.

	// StartInstances → CPU=25 → alarm fires (25 > 0)
	if err := p.EC2.StartInstances(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}

	alarms, _ := p.CloudWatch.DescribeAlarms(ctx, []string{"any-cpu-start"})
	if alarms[0].State != "ALARM" {
		t.Errorf("expected ALARM after StartInstances (CPU=25 > 0), got %s", alarms[0].State)
	}
}

// ==============================================================================
// Feature: Dead-Letter Queue Tests
// ==============================================================================

func TestDeadLetterQueue(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	p := NewAWS(config.WithClock(clock))

	// 1. Create the DLQ first
	dlq, err := p.SQS.CreateQueue(ctx, mqdriver.QueueConfig{
		Name: "my-queue-dlq",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Create main queue with DLQ configured (maxReceiveCount=2)
	mainQ, err := p.SQS.CreateQueue(ctx, mqdriver.QueueConfig{
		Name:              "my-queue",
		VisibilityTimeout: 1,
		DeadLetterQueue: &mqdriver.DeadLetterConfig{
			TargetQueueURL:  dlq.URL,
			MaxReceiveCount: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3. Send a message
	_, err = p.SQS.SendMessage(ctx, mqdriver.SendMessageInput{
		QueueURL: mainQ.URL,
		Body:     "process me",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 4. Receive the message twice (simulating failed processing — not deleting it)
	for i := 0; i < 2; i++ {
		msgs, err := p.SQS.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{
			QueueURL: mainQ.URL,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("receive %d: expected 1 message, got %d", i+1, len(msgs))
		}
		// Don't delete — simulating failure. Make it visible again.
		clock.Advance(2 * time.Second)
	}

	// 5. Third receive should trigger DLQ move (receiveCount exceeds maxReceiveCount=2)
	msgs, err := p.SQS.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{
		QueueURL: mainQ.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages in main queue after DLQ move, got %d", len(msgs))
	}

	// 6. Verify message is now in the DLQ
	dlqInfo, err := p.SQS.GetQueueInfo(ctx, dlq.URL)
	if err != nil {
		t.Fatal(err)
	}
	if dlqInfo.ApproxMessageCount != 1 {
		t.Errorf("expected 1 message in DLQ, got %d", dlqInfo.ApproxMessageCount)
	}

	// 7. Receive from DLQ to verify message body
	dlqMsgs, err := p.SQS.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{
		QueueURL: dlq.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dlqMsgs) != 1 {
		t.Fatalf("expected 1 DLQ message, got %d", len(dlqMsgs))
	}
	if dlqMsgs[0].Body != "process me" {
		t.Errorf("expected DLQ message body 'process me', got %q", dlqMsgs[0].Body)
	}
}

func TestDeadLetterQueueAzure(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	p := NewAzure(config.WithClock(clock))

	dlq, err := p.ServiceBus.CreateQueue(ctx, mqdriver.QueueConfig{Name: "sb-dlq"})
	if err != nil {
		t.Fatal(err)
	}

	mainQ, err := p.ServiceBus.CreateQueue(ctx, mqdriver.QueueConfig{
		Name:              "sb-main",
		VisibilityTimeout: 1,
		DeadLetterQueue:   &mqdriver.DeadLetterConfig{TargetQueueURL: dlq.URL, MaxReceiveCount: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	p.ServiceBus.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: mainQ.URL, Body: "hello"})

	// First receive succeeds
	msgs, _ := p.ServiceBus.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: mainQ.URL})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	clock.Advance(2 * time.Second)

	// Second receive moves to DLQ (receiveCount=2 > maxReceiveCount=1)
	msgs, _ = p.ServiceBus.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: mainQ.URL})
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after DLQ move, got %d", len(msgs))
	}

	dlqMsgs, _ := p.ServiceBus.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: dlq.URL})
	if len(dlqMsgs) != 1 || dlqMsgs[0].Body != "hello" {
		t.Errorf("expected DLQ message with body 'hello', got %v", dlqMsgs)
	}
}

func TestDeadLetterQueueGCP(t *testing.T) {
	ctx := context.Background()
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	p := NewGCP(config.WithClock(clock))

	dlq, err := p.PubSub.CreateQueue(ctx, mqdriver.QueueConfig{Name: "ps-dlq"})
	if err != nil {
		t.Fatal(err)
	}

	mainQ, err := p.PubSub.CreateQueue(ctx, mqdriver.QueueConfig{
		Name:              "ps-main",
		VisibilityTimeout: 1,
		DeadLetterQueue:   &mqdriver.DeadLetterConfig{TargetQueueURL: dlq.URL, MaxReceiveCount: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	p.PubSub.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: mainQ.URL, Body: "gcp-msg"})

	msgs, _ := p.PubSub.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: mainQ.URL})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	clock.Advance(2 * time.Second)

	msgs, _ = p.PubSub.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: mainQ.URL})
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after DLQ move, got %d", len(msgs))
	}

	dlqMsgs, _ := p.PubSub.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: dlq.URL})
	if len(dlqMsgs) != 1 || dlqMsgs[0].Body != "gcp-msg" {
		t.Errorf("expected DLQ message with body 'gcp-msg', got %v", dlqMsgs)
	}
}

// ==============================================================================
// Feature: Cost Simulation Tests
// ==============================================================================

func TestCostTracker(t *testing.T) {
	tracker := cost.New()

	// Simulate some cloud operations
	tracker.Record("compute", "RunInstances", 3)
	tracker.Record("storage", "PutObject", 100)
	tracker.Record("storage", "GetObject", 500)
	tracker.Record("database", "PutItem", 1000)
	tracker.Record("serverless", "Invoke", 10000)
	tracker.Record("messagequeue", "SendMessage", 5000)

	// Verify total cost is > 0
	total := tracker.TotalCost()
	if total <= 0 {
		t.Errorf("expected total cost > 0, got %f", total)
	}

	// Verify cost by service
	byService := tracker.CostByService()
	if byService["compute"] <= 0 {
		t.Error("expected compute cost > 0")
	}
	if byService["storage"] <= 0 {
		t.Error("expected storage cost > 0")
	}
	if byService["database"] <= 0 {
		t.Error("expected database cost > 0")
	}

	// Verify cost by operation
	byOp := tracker.CostByOperation()
	if byOp["compute:RunInstances"] <= 0 {
		t.Error("expected RunInstances cost > 0")
	}

	// Verify all costs recorded
	allCosts := tracker.AllCosts()
	if len(allCosts) != 6 {
		t.Errorf("expected 6 cost records, got %d", len(allCosts))
	}
}

func TestCostTrackerCustomRates(t *testing.T) {
	tracker := cost.New()

	// Set custom rate
	tracker.SetRate("compute", "RunInstances", 0.50)

	tracker.Record("compute", "RunInstances", 10)

	total := tracker.TotalCost()
	expected := 5.0 // 0.50 * 10
	if total != expected {
		t.Errorf("expected cost %f, got %f", expected, total)
	}
}

func TestCostTrackerReset(t *testing.T) {
	tracker := cost.New()

	tracker.Record("compute", "RunInstances", 5)
	if tracker.TotalCost() <= 0 {
		t.Error("expected cost > 0 before reset")
	}

	tracker.Reset()
	if tracker.TotalCost() != 0 {
		t.Errorf("expected 0 cost after reset, got %f", tracker.TotalCost())
	}
}

// ==============================================================================
// Feature: Lambda-SQS Trigger Tests
// ==============================================================================

func TestLambdaSQSTrigger(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	// 1. Create a Lambda function
	p.Lambda.RegisterHandler("processor", func(_ context.Context, payload []byte) ([]byte, error) {
		return []byte("processed: " + string(payload)), nil
	})
	_, err := p.Lambda.CreateFunction(ctx, serverlessdriver.FunctionConfig{
		Name: "processor", Runtime: "go1.x", Handler: "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Create SQS queue
	q, err := p.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "trigger-queue"})
	if err != nil {
		t.Fatal(err)
	}

	// 3. Wire the trigger: SQS → Lambda
	var triggerCount int64
	p.SQS.SetTrigger(q.URL, func(queueURL string, msg mqdriver.Message) {
		// Invoke lambda with the message body
		_, invokeErr := p.Lambda.Invoke(ctx, serverlessdriver.InvokeInput{
			FunctionName: "processor",
			Payload:      []byte(msg.Body),
		})
		if invokeErr != nil {
			t.Errorf("trigger invoke failed: %v", invokeErr)
		}
		atomic.AddInt64(&triggerCount, 1)
	})

	// 4. Send messages — Lambda should be triggered automatically
	for i := 0; i < 5; i++ {
		_, err := p.SQS.SendMessage(ctx, mqdriver.SendMessageInput{
			QueueURL: q.URL,
			Body:     "message-body",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// 5. Verify Lambda was triggered 5 times
	if atomic.LoadInt64(&triggerCount) != 5 {
		t.Errorf("expected 5 trigger invocations, got %d", triggerCount)
	}
}

func TestLambdaSQSTriggerRemove(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	q, err := p.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "removable-trigger"})
	if err != nil {
		t.Fatal(err)
	}

	var triggerCount int64
	p.SQS.SetTrigger(q.URL, func(_ string, _ mqdriver.Message) {
		atomic.AddInt64(&triggerCount, 1)
	})

	// Send one message — trigger fires
	p.SQS.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: q.URL, Body: "first"})
	if atomic.LoadInt64(&triggerCount) != 1 {
		t.Errorf("expected 1 trigger, got %d", triggerCount)
	}

	// Remove trigger
	p.SQS.RemoveTrigger(q.URL)

	// Send another message — trigger should NOT fire
	p.SQS.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: q.URL, Body: "second"})
	if atomic.LoadInt64(&triggerCount) != 1 {
		t.Errorf("expected still 1 trigger after removal, got %d", triggerCount)
	}
}

func TestAzureFunctionServiceBusTrigger(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()

	q, err := p.ServiceBus.CreateQueue(ctx, mqdriver.QueueConfig{Name: "az-trigger-queue"})
	if err != nil {
		t.Fatal(err)
	}

	var received []string
	p.ServiceBus.SetTrigger(q.URL, func(_ string, msg mqdriver.Message) {
		received = append(received, msg.Body)
	})

	p.ServiceBus.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: q.URL, Body: "azure-msg-1"})
	p.ServiceBus.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: q.URL, Body: "azure-msg-2"})

	if len(received) != 2 {
		t.Errorf("expected 2 triggered messages, got %d", len(received))
	}
	if received[0] != "azure-msg-1" || received[1] != "azure-msg-2" {
		t.Errorf("unexpected messages: %v", received)
	}
}

func TestGCPCloudFunctionPubSubTrigger(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()

	q, err := p.PubSub.CreateQueue(ctx, mqdriver.QueueConfig{Name: "gcp-trigger-topic"})
	if err != nil {
		t.Fatal(err)
	}

	var received []string
	p.PubSub.SetTrigger(q.URL, func(_ string, msg mqdriver.Message) {
		received = append(received, msg.Body)
	})

	p.PubSub.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: q.URL, Body: "gcp-event-1"})
	p.PubSub.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: q.URL, Body: "gcp-event-2"})
	p.PubSub.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: q.URL, Body: "gcp-event-3"})

	if len(received) != 3 {
		t.Errorf("expected 3 triggered messages, got %d", len(received))
	}
}

// ==============================================================================
// Integration Tests: Secrets (AWS SecretsManager, Azure KeyVault, GCP SecretManager)
// ==============================================================================

func TestAWSSecretsManagerOperations(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testSecretsWithDriver(t, ctx, p.SecretsManager)
}

func TestAzureKeyVaultOperations(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testSecretsWithDriver(t, ctx, p.KeyVault)
}

func TestGCPSecretManagerOperations(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testSecretsWithDriver(t, ctx, p.SecretManager)
}

func testSecretsWithDriver(t *testing.T, ctx context.Context, d secretsdriver.Secrets) {
	t.Helper()

	// Create secret
	info, err := d.CreateSecret(ctx, secretsdriver.SecretConfig{
		Name:        "db-password",
		Description: "Database password",
		Tags:        map[string]string{"env": "production"},
	}, []byte("s3cret-v1"))
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if info.Name != "db-password" {
		t.Errorf("expected name 'db-password', got %q", info.Name)
	}
	if info.ResourceID == "" {
		t.Error("expected non-empty ResourceID")
	}

	// Get secret
	got, err := d.GetSecret(ctx, "db-password")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got.Name != "db-password" {
		t.Errorf("expected 'db-password', got %q", got.Name)
	}

	// Get secret value
	ver, err := d.GetSecretValue(ctx, "db-password", "")
	if err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}
	if string(ver.Value) != "s3cret-v1" {
		t.Errorf("expected 's3cret-v1', got %q", string(ver.Value))
	}

	// Put new version
	ver2, err := d.PutSecretValue(ctx, "db-password", []byte("s3cret-v2"))
	if err != nil {
		t.Fatalf("PutSecretValue: %v", err)
	}
	if !ver2.Current {
		t.Error("expected new version to be current")
	}

	// Verify new version is returned as current
	latest, err := d.GetSecretValue(ctx, "db-password", "")
	if err != nil {
		t.Fatalf("GetSecretValue (latest): %v", err)
	}
	if string(latest.Value) != "s3cret-v2" {
		t.Errorf("expected 's3cret-v2', got %q", string(latest.Value))
	}

	// List versions
	versions, err := d.ListSecretVersions(ctx, "db-password")
	if err != nil {
		t.Fatalf("ListSecretVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(versions))
	}

	// List secrets
	secrets, err := d.ListSecrets(ctx)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(secrets) != 1 {
		t.Errorf("expected 1 secret, got %d", len(secrets))
	}

	// Delete secret (soft-delete)
	if err := d.DeleteSecret(ctx, "db-password"); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	// Verify deleted secret is not accessible
	_, err = d.GetSecret(ctx, "db-password")
	if err == nil {
		t.Error("expected error getting deleted secret")
	}

	// Verify not found for non-existent
	_, err = d.GetSecret(ctx, "does-not-exist")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// ==============================================================================
// Integration Tests: Cache (AWS ElastiCache, Azure Cache, GCP Memorystore)
// ==============================================================================

func TestAWSElastiCacheOperations(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testCacheWithDriver(t, ctx, p.ElastiCache)
}

func TestAzureCacheOperations(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testCacheWithDriver(t, ctx, p.Cache)
}

func TestGCPMemorystoreOperations(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testCacheWithDriver(t, ctx, p.Memorystore)
}

func testCacheWithDriver(t *testing.T, ctx context.Context, d cachedriver.Cache) {
	t.Helper()

	// Create cache
	info, err := d.CreateCache(ctx, cachedriver.CacheConfig{
		Name:   "session-cache",
		Engine: "redis",
	})
	if err != nil {
		t.Fatalf("CreateCache: %v", err)
	}
	if info.Name == "" {
		t.Error("expected non-empty cache name")
	}
	if info.Endpoint == "" {
		t.Error("expected non-empty endpoint")
	}

	// Set values
	if err := d.Set(ctx, "session-cache", "user:1:session", []byte("token-abc"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := d.Set(ctx, "session-cache", "user:2:session", []byte("token-def"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := d.Set(ctx, "session-cache", "config:app", []byte("settings"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get value
	item, err := d.Get(ctx, "session-cache", "user:1:session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(item.Value) != "token-abc" {
		t.Errorf("expected 'token-abc', got %q", string(item.Value))
	}

	// Keys with glob pattern (middle wildcard)
	keys, err := d.Keys(ctx, "session-cache", "user:*:session")
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys matching 'user:*:session', got %d: %v", len(keys), keys)
	}

	// Keys with prefix pattern
	allKeys, err := d.Keys(ctx, "session-cache", "*")
	if err != nil {
		t.Fatalf("Keys(*): %v", err)
	}
	if len(allKeys) != 3 {
		t.Errorf("expected 3 total keys, got %d", len(allKeys))
	}

	// Delete key
	if err := d.Delete(ctx, "session-cache", "user:1:session"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = d.Get(ctx, "session-cache", "user:1:session")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got %v", err)
	}

	// FlushAll
	if err := d.FlushAll(ctx, "session-cache"); err != nil {
		t.Fatalf("FlushAll: %v", err)
	}
	remainingKeys, _ := d.Keys(ctx, "session-cache", "*")
	if len(remainingKeys) != 0 {
		t.Errorf("expected 0 keys after flush, got %d", len(remainingKeys))
	}

	// List caches
	caches, err := d.ListCaches(ctx)
	if err != nil {
		t.Fatalf("ListCaches: %v", err)
	}
	if len(caches) != 1 {
		t.Errorf("expected 1 cache, got %d", len(caches))
	}

	// Delete cache
	if err := d.DeleteCache(ctx, "session-cache"); err != nil {
		t.Fatalf("DeleteCache: %v", err)
	}
	_, err = d.GetCache(ctx, "session-cache")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// ==============================================================================
// Integration Tests: Logging (AWS CloudWatch Logs, Azure Log Analytics, GCP Cloud Logging)
// ==============================================================================

func TestAWSCloudWatchLogsOperations(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testLoggingWithDriver(t, ctx, p.CloudWatchLogs)
}

func TestAzureLogAnalyticsOperations(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testLoggingWithDriver(t, ctx, p.LogAnalytics)
}

func TestGCPCloudLoggingOperations(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testLoggingWithDriver(t, ctx, p.CloudLogging)
}

func testLoggingWithDriver(t *testing.T, ctx context.Context, d loggingdriver.Logging) {
	t.Helper()

	// Create log group
	groupInfo, err := d.CreateLogGroup(ctx, loggingdriver.LogGroupConfig{
		Name:          "app-logs",
		RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}
	if groupInfo.Name != "app-logs" {
		t.Errorf("expected name 'app-logs', got %q", groupInfo.Name)
	}
	if groupInfo.ResourceID == "" {
		t.Error("expected non-empty ResourceID")
	}
	if groupInfo.RetentionDays != 7 {
		t.Errorf("expected retention 7, got %d", groupInfo.RetentionDays)
	}

	// Create log stream
	streamInfo, err := d.CreateLogStream(ctx, "app-logs", "web-server")
	if err != nil {
		t.Fatalf("CreateLogStream: %v", err)
	}
	if streamInfo.Name != "web-server" {
		t.Errorf("expected 'web-server', got %q", streamInfo.Name)
	}

	// Put log events
	now := time.Now()
	events := []loggingdriver.LogEvent{
		{Timestamp: now.Add(-2 * time.Hour), Message: "Starting server"},
		{Timestamp: now.Add(-time.Hour), Message: "Request received: GET /api/health"},
		{Timestamp: now, Message: "Error: connection timeout"},
	}
	if err := d.PutLogEvents(ctx, "app-logs", "web-server", events); err != nil {
		t.Fatalf("PutLogEvents: %v", err)
	}

	// Get all events
	allEvents, err := d.GetLogEvents(ctx, &loggingdriver.LogQueryInput{
		LogGroup: "app-logs",
	})
	if err != nil {
		t.Fatalf("GetLogEvents (all): %v", err)
	}
	if len(allEvents) != 3 {
		t.Errorf("expected 3 events, got %d", len(allEvents))
	}

	// Get events from specific stream
	streamEvents, err := d.GetLogEvents(ctx, &loggingdriver.LogQueryInput{
		LogGroup:  "app-logs",
		LogStream: "web-server",
	})
	if err != nil {
		t.Fatalf("GetLogEvents (stream): %v", err)
	}
	if len(streamEvents) != 3 {
		t.Errorf("expected 3 events from stream, got %d", len(streamEvents))
	}

	// Filter by pattern
	errorEvents, err := d.GetLogEvents(ctx, &loggingdriver.LogQueryInput{
		LogGroup: "app-logs",
		Pattern:  "Error",
	})
	if err != nil {
		t.Fatalf("GetLogEvents (pattern): %v", err)
	}
	if len(errorEvents) != 1 {
		t.Errorf("expected 1 error event, got %d", len(errorEvents))
	}

	// Filter by time range
	recentEvents, err := d.GetLogEvents(ctx, &loggingdriver.LogQueryInput{
		LogGroup:  "app-logs",
		StartTime: now.Add(-90 * time.Minute),
		EndTime:   now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("GetLogEvents (time range): %v", err)
	}
	if len(recentEvents) != 2 {
		t.Errorf("expected 2 recent events, got %d", len(recentEvents))
	}

	// List log streams
	streams, err := d.ListLogStreams(ctx, "app-logs")
	if err != nil {
		t.Fatalf("ListLogStreams: %v", err)
	}
	if len(streams) != 1 {
		t.Errorf("expected 1 stream, got %d", len(streams))
	}

	// List log groups
	groups, err := d.ListLogGroups(ctx)
	if err != nil {
		t.Fatalf("ListLogGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Errorf("expected 1 group, got %d", len(groups))
	}

	// Delete stream
	if err := d.DeleteLogStream(ctx, "app-logs", "web-server"); err != nil {
		t.Fatalf("DeleteLogStream: %v", err)
	}

	// Delete group
	if err := d.DeleteLogGroup(ctx, "app-logs"); err != nil {
		t.Fatalf("DeleteLogGroup: %v", err)
	}
	_, err = d.GetLogGroup(ctx, "app-logs")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// ==============================================================================
// Integration Tests: FilterLogEvents and MetricFilters
// ==============================================================================

func TestFilterLogEventsAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testFilterLogEvents(t, ctx, p.CloudWatchLogs)
}

func TestFilterLogEventsAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testFilterLogEvents(t, ctx, p.LogAnalytics)
}

func TestFilterLogEventsGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testFilterLogEvents(t, ctx, p.CloudLogging)
}

func testFilterLogEvents(
	t *testing.T,
	ctx context.Context,
	d loggingdriver.Logging,
) {
	t.Helper()

	_, err := d.CreateLogGroup(ctx, loggingdriver.LogGroupConfig{
		Name: "filter-test",
	})
	if err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	_, err = d.CreateLogStream(ctx, "filter-test", "stream-a")
	if err != nil {
		t.Fatalf("CreateLogStream (a): %v", err)
	}

	_, err = d.CreateLogStream(ctx, "filter-test", "stream-b")
	if err != nil {
		t.Fatalf("CreateLogStream (b): %v", err)
	}

	now := time.Now().UTC()

	eventsA := []loggingdriver.LogEvent{
		{Timestamp: now.Add(-3 * time.Minute), Message: "INFO starting"},
		{Timestamp: now.Add(-2 * time.Minute), Message: "ERROR disk full"},
		{Timestamp: now.Add(-time.Minute), Message: "INFO recovered"},
	}

	eventsB := []loggingdriver.LogEvent{
		{Timestamp: now.Add(-2 * time.Minute), Message: "ERROR timeout"},
		{Timestamp: now, Message: "INFO healthy"},
	}

	err = d.PutLogEvents(ctx, "filter-test", "stream-a", eventsA)
	if err != nil {
		t.Fatalf("PutLogEvents (a): %v", err)
	}

	err = d.PutLogEvents(ctx, "filter-test", "stream-b", eventsB)
	if err != nil {
		t.Fatalf("PutLogEvents (b): %v", err)
	}

	// Filter by pattern across all streams.
	results, err := d.FilterLogEvents(ctx, &loggingdriver.FilterLogEventsInput{
		LogGroup:      "filter-test",
		FilterPattern: "ERROR",
	})
	if err != nil {
		t.Fatalf("FilterLogEvents (ERROR): %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 ERROR events, got %d", len(results))
	}

	for _, r := range results {
		if r.LogStream == "" {
			t.Error("expected non-empty LogStream on filtered event")
		}
	}

	// Filter by specific stream.
	streamResults, err := d.FilterLogEvents(
		ctx, &loggingdriver.FilterLogEventsInput{
			LogGroup:      "filter-test",
			LogStream:     "stream-a",
			FilterPattern: "ERROR",
		},
	)
	if err != nil {
		t.Fatalf("FilterLogEvents (stream-a ERROR): %v", err)
	}

	if len(streamResults) != 1 {
		t.Errorf("expected 1 event from stream-a, got %d", len(streamResults))
	}

	// Filter by time range (-2.5min to -0.5min captures 3 events).
	timeResults, err := d.FilterLogEvents(
		ctx, &loggingdriver.FilterLogEventsInput{
			LogGroup:  "filter-test",
			StartTime: now.Add(-150 * time.Second),
			EndTime:   now.Add(-30 * time.Second),
		},
	)
	if err != nil {
		t.Fatalf("FilterLogEvents (time range): %v", err)
	}

	if len(timeResults) != 3 {
		t.Errorf("expected 3 events in time range, got %d", len(timeResults))
	}

	// Filter with limit.
	limitResults, err := d.FilterLogEvents(
		ctx, &loggingdriver.FilterLogEventsInput{
			LogGroup: "filter-test",
			Limit:    2,
		},
	)
	if err != nil {
		t.Fatalf("FilterLogEvents (limit): %v", err)
	}

	if len(limitResults) > 2 {
		t.Errorf("expected at most 2 events, got %d", len(limitResults))
	}

	// Empty result for non-matching pattern.
	emptyResults, err := d.FilterLogEvents(
		ctx, &loggingdriver.FilterLogEventsInput{
			LogGroup:      "filter-test",
			FilterPattern: "CRITICAL",
		},
	)
	if err != nil {
		t.Fatalf("FilterLogEvents (no match): %v", err)
	}

	if len(emptyResults) != 0 {
		t.Errorf("expected 0 events, got %d", len(emptyResults))
	}

	// Not found log group.
	_, err = d.FilterLogEvents(ctx, &loggingdriver.FilterLogEventsInput{
		LogGroup: "nonexistent",
	})
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestMetricFiltersAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	d := p.CloudWatchLogs

	_, err := d.CreateLogGroup(ctx, loggingdriver.LogGroupConfig{
		Name: "mf-test",
	})
	if err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	// Put a metric filter.
	err = d.PutMetricFilter(ctx, &loggingdriver.MetricFilterConfig{
		Name:            "error-count",
		LogGroup:        "mf-test",
		FilterPattern:   "ERROR",
		MetricName:      "ErrorCount",
		MetricNamespace: "App/Errors",
		MetricValue:     "1",
	})
	if err != nil {
		t.Fatalf("PutMetricFilter: %v", err)
	}

	// Describe metric filters.
	filters, err := d.DescribeMetricFilters(ctx, "mf-test")
	if err != nil {
		t.Fatalf("DescribeMetricFilters: %v", err)
	}

	if len(filters) != 1 {
		t.Fatalf("expected 1 filter, got %d", len(filters))
	}

	f := filters[0]
	if f.Name != "error-count" {
		t.Errorf("expected name 'error-count', got %q", f.Name)
	}

	if f.FilterPattern != "ERROR" {
		t.Errorf("expected pattern 'ERROR', got %q", f.FilterPattern)
	}

	if f.MetricName != "ErrorCount" {
		t.Errorf("expected metric 'ErrorCount', got %q", f.MetricName)
	}

	if f.MetricNamespace != "App/Errors" {
		t.Errorf(
			"expected namespace 'App/Errors', got %q",
			f.MetricNamespace,
		)
	}

	if f.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}

	// Update existing filter by name.
	err = d.PutMetricFilter(ctx, &loggingdriver.MetricFilterConfig{
		Name:            "error-count",
		LogGroup:        "mf-test",
		FilterPattern:   "FATAL",
		MetricName:      "FatalCount",
		MetricNamespace: "App/Errors",
		MetricValue:     "1",
	})
	if err != nil {
		t.Fatalf("PutMetricFilter (update): %v", err)
	}

	filters, err = d.DescribeMetricFilters(ctx, "mf-test")
	if err != nil {
		t.Fatalf("DescribeMetricFilters after update: %v", err)
	}

	if len(filters) != 1 {
		t.Fatalf("expected 1 filter after update, got %d", len(filters))
	}

	if filters[0].FilterPattern != "FATAL" {
		t.Errorf(
			"expected updated pattern 'FATAL', got %q",
			filters[0].FilterPattern,
		)
	}

	// Delete metric filter.
	err = d.DeleteMetricFilter(ctx, "mf-test", "error-count")
	if err != nil {
		t.Fatalf("DeleteMetricFilter: %v", err)
	}

	filters, err = d.DescribeMetricFilters(ctx, "mf-test")
	if err != nil {
		t.Fatalf("DescribeMetricFilters after delete: %v", err)
	}

	if len(filters) != 0 {
		t.Errorf("expected 0 filters after delete, got %d", len(filters))
	}

	// Delete non-existent filter.
	err = d.DeleteMetricFilter(ctx, "mf-test", "nonexistent")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}

	// Describe on non-existent group.
	_, err = d.DescribeMetricFilters(ctx, "no-such-group")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// ==============================================================================
// Integration Tests: Notification (AWS SNS, Azure Notification Hubs, GCP FCM)
// ==============================================================================

func TestAWSSNSOperations(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testNotificationWithDriver(t, ctx, p.SNS)
}

func TestAzureNotificationHubsOperations(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testNotificationWithDriver(t, ctx, p.NotificationHubs)
}

func TestGCPFCMOperations(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testNotificationWithDriver(t, ctx, p.FCM)
}

func testNotificationWithDriver(t *testing.T, ctx context.Context, d notifdriver.Notification) {
	t.Helper()

	// Create topic
	topic, err := d.CreateTopic(ctx, notifdriver.TopicConfig{
		Name:        "order-events",
		DisplayName: "Order Events",
		Tags:        map[string]string{"team": "commerce"},
	})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if topic.Name != "order-events" {
		t.Errorf("expected 'order-events', got %q", topic.Name)
	}
	if topic.ResourceID == "" {
		t.Error("expected non-empty ResourceID")
	}

	// Get topic
	got, err := d.GetTopic(ctx, "order-events")
	if err != nil {
		t.Fatalf("GetTopic: %v", err)
	}
	if got.Name != "order-events" {
		t.Errorf("expected 'order-events', got %q", got.Name)
	}

	// Subscribe
	sub, err := d.Subscribe(ctx, notifdriver.SubscriptionConfig{
		TopicID:  "order-events",
		Protocol: "email",
		Endpoint: "admin@example.com",
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if sub.ID == "" {
		t.Error("expected non-empty subscription ID")
	}
	if sub.Status != "confirmed" {
		t.Errorf("expected 'confirmed', got %q", sub.Status)
	}

	// Subscribe another
	sub2, err := d.Subscribe(ctx, notifdriver.SubscriptionConfig{
		TopicID:  "order-events",
		Protocol: "https",
		Endpoint: "https://webhook.example.com/orders",
	})
	if err != nil {
		t.Fatalf("Subscribe (2): %v", err)
	}

	// List subscriptions
	subs, err := d.ListSubscriptions(ctx, "order-events")
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("expected 2 subscriptions, got %d", len(subs))
	}

	// Verify topic subscription count
	topicInfo, _ := d.GetTopic(ctx, "order-events")
	if topicInfo.SubscriptionCount != 2 {
		t.Errorf("expected subscription count 2, got %d", topicInfo.SubscriptionCount)
	}

	// Publish message
	pubOut, err := d.Publish(ctx, notifdriver.PublishInput{
		TopicID: "order-events",
		Subject: "New Order",
		Message: `{"orderId": "12345", "total": 99.99}`,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if pubOut.MessageID == "" {
		t.Error("expected non-empty message ID")
	}

	// Unsubscribe
	if err := d.Unsubscribe(ctx, sub2.ID); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	subs, _ = d.ListSubscriptions(ctx, "order-events")
	if len(subs) != 1 {
		t.Errorf("expected 1 subscription after unsubscribe, got %d", len(subs))
	}

	// List topics
	topics, err := d.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	if len(topics) != 1 {
		t.Errorf("expected 1 topic, got %d", len(topics))
	}

	// Delete topic
	if err := d.DeleteTopic(ctx, "order-events"); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	_, err = d.GetTopic(ctx, "order-events")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// ==============================================================================
// Cross-Provider Integration: All 4 new services work consistently
// ==============================================================================

func TestCrossProviderNewServices(t *testing.T) {
	ctx := context.Background()

	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	// Test secrets across providers
	secretsProviders := []struct {
		name string
		d    secretsdriver.Secrets
	}{
		{"aws", awsP.SecretsManager},
		{"azure", azureP.KeyVault},
		{"gcp", gcpP.SecretManager},
	}

	for _, sp := range secretsProviders {
		t.Run("secrets/"+sp.name, func(t *testing.T) {
			info, err := sp.d.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "api-key"}, []byte("key-123"))
			if err != nil {
				t.Fatalf("CreateSecret: %v", err)
			}
			if info.ResourceID == "" {
				t.Error("expected non-empty ResourceID")
			}

			ver, _ := sp.d.GetSecretValue(ctx, "api-key", "")
			if string(ver.Value) != "key-123" {
				t.Errorf("expected 'key-123', got %q", string(ver.Value))
			}
		})
	}

	// Test caches across providers (including tags)
	cacheProviders := []struct {
		name string
		d    cachedriver.Cache
	}{
		{"aws", awsP.ElastiCache},
		{"azure", azureP.Cache},
		{"gcp", gcpP.Memorystore},
	}

	for _, cp := range cacheProviders {
		t.Run("cache/"+cp.name, func(t *testing.T) {
			info, err := cp.d.CreateCache(ctx, cachedriver.CacheConfig{
				Name: "test-cache",
				Tags: map[string]string{"env": "staging", "team": "backend"},
			})
			if err != nil {
				t.Fatalf("CreateCache: %v", err)
			}

			// Verify tags are persisted in CacheInfo
			if info.Tags["env"] != "staging" {
				t.Errorf("expected tag env=staging, got %q", info.Tags["env"])
			}
			if info.Tags["team"] != "backend" {
				t.Errorf("expected tag team=backend, got %q", info.Tags["team"])
			}

			// Verify tags survive GetCache
			got, err := cp.d.GetCache(ctx, "test-cache")
			if err != nil {
				t.Fatalf("GetCache: %v", err)
			}
			if got.Tags["env"] != "staging" {
				t.Errorf("GetCache: expected tag env=staging, got %q", got.Tags["env"])
			}

			err = cp.d.Set(ctx, "test-cache", "key", []byte("value"), 0)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			item, _ := cp.d.Get(ctx, "test-cache", "key")
			if string(item.Value) != "value" {
				t.Errorf("expected 'value', got %q", string(item.Value))
			}
		})
	}

	// Test notification across providers — all use name-based topic lookup
	notifProviders := []struct {
		name string
		d    notifdriver.Notification
	}{
		{"aws", awsP.SNS},
		{"azure", azureP.NotificationHubs},
		{"gcp", gcpP.FCM},
	}

	for _, np := range notifProviders {
		t.Run("notification/"+np.name, func(t *testing.T) {
			topic, err := np.d.CreateTopic(ctx, notifdriver.TopicConfig{Name: "alerts"})
			if err != nil {
				t.Fatalf("CreateTopic: %v", err)
			}
			if topic.ResourceID == "" {
				t.Error("expected non-empty ResourceID")
			}

			// All providers use name as key — portable API contract
			got, err := np.d.GetTopic(ctx, "alerts")
			if err != nil {
				t.Fatalf("GetTopic by name: %v", err)
			}
			if got.Name != "alerts" {
				t.Errorf("expected 'alerts', got %q", got.Name)
			}
		})
	}
}

// ==============================================================================
// Integration Tests: CacheInfo Tags Persistence
// ==============================================================================

func TestCacheTagsPersistence(t *testing.T) {
	ctx := context.Background()

	providers := []struct {
		name string
		d    cachedriver.Cache
	}{
		{"aws/elasticache", NewAWS().ElastiCache},
		{"azure/cache", NewAzure().Cache},
		{"gcp/memorystore", NewGCP().Memorystore},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			tags := map[string]string{"env": "production", "team": "platform", "cost-center": "eng-42"}

			info, err := p.d.CreateCache(ctx, cachedriver.CacheConfig{
				Name:   "tagged-cache",
				Engine: "redis",
				Tags:   tags,
			})
			if err != nil {
				t.Fatalf("CreateCache: %v", err)
			}

			// Verify all tags are present in the returned CacheInfo
			for k, v := range tags {
				if info.Tags[k] != v {
					t.Errorf("CreateCache: expected tag %s=%s, got %q", k, v, info.Tags[k])
				}
			}

			// Verify tags survive GetCache round-trip
			got, err := p.d.GetCache(ctx, "tagged-cache")
			if err != nil {
				t.Fatalf("GetCache: %v", err)
			}
			for k, v := range tags {
				if got.Tags[k] != v {
					t.Errorf("GetCache: expected tag %s=%s, got %q", k, v, got.Tags[k])
				}
			}

			// Verify tags appear in ListCaches
			caches, err := p.d.ListCaches(ctx)
			if err != nil {
				t.Fatalf("ListCaches: %v", err)
			}
			if len(caches) != 1 {
				t.Fatalf("expected 1 cache, got %d", len(caches))
			}
			for k, v := range tags {
				if caches[0].Tags[k] != v {
					t.Errorf("ListCaches: expected tag %s=%s, got %q", k, v, caches[0].Tags[k])
				}
			}

			// Verify tag isolation (modifying original map doesn't affect stored tags)
			tags["env"] = "modified"
			got2, _ := p.d.GetCache(ctx, "tagged-cache")
			if got2.Tags["env"] != "production" {
				t.Error("tag isolation broken: modifying input map affected stored tags")
			}

			// Verify empty tags work fine
			info2, err := p.d.CreateCache(ctx, cachedriver.CacheConfig{Name: "no-tags-cache"})
			if err != nil {
				t.Fatalf("CreateCache (no tags): %v", err)
			}
			if info2.Tags == nil {
				t.Error("expected non-nil empty tags map")
			}
			if len(info2.Tags) != 0 {
				t.Errorf("expected 0 tags, got %d", len(info2.Tags))
			}
		})
	}
}

// ==============================================================================
// Integration Tests: LogQueryInput Pointer Interface
// ==============================================================================

func TestLogQueryInputPointer(t *testing.T) {
	ctx := context.Background()

	providers := []struct {
		name string
		d    loggingdriver.Logging
	}{
		{"aws/cloudwatchlogs", NewAWS().CloudWatchLogs},
		{"azure/loganalytics", NewAzure().LogAnalytics},
		{"gcp/cloudlogging", NewGCP().CloudLogging},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			// Setup: create log group and stream
			_, err := p.d.CreateLogGroup(ctx, loggingdriver.LogGroupConfig{Name: "ptr-test-group"})
			if err != nil {
				t.Fatalf("CreateLogGroup: %v", err)
			}
			_, err = p.d.CreateLogStream(ctx, "ptr-test-group", "stream-1")
			if err != nil {
				t.Fatalf("CreateLogStream: %v", err)
			}

			now := time.Now().UTC()
			events := []loggingdriver.LogEvent{
				{Timestamp: now.Add(-2 * time.Minute), Message: "info: starting up"},
				{Timestamp: now.Add(-1 * time.Minute), Message: "error: connection failed"},
				{Timestamp: now, Message: "info: recovered"},
			}
			if err := p.d.PutLogEvents(ctx, "ptr-test-group", "stream-1", events); err != nil {
				t.Fatalf("PutLogEvents: %v", err)
			}

			// Query with pointer — basic query
			results, err := p.d.GetLogEvents(ctx, &loggingdriver.LogQueryInput{
				LogGroup: "ptr-test-group",
			})
			if err != nil {
				t.Fatalf("GetLogEvents (all): %v", err)
			}
			if len(results) != 3 {
				t.Errorf("expected 3 events, got %d", len(results))
			}

			// Query with pointer — pattern filter
			filtered, err := p.d.GetLogEvents(ctx, &loggingdriver.LogQueryInput{
				LogGroup: "ptr-test-group",
				Pattern:  "error",
			})
			if err != nil {
				t.Fatalf("GetLogEvents (pattern): %v", err)
			}
			if len(filtered) != 1 {
				t.Errorf("expected 1 error event, got %d", len(filtered))
			}

			// Query with pointer — specific stream
			streamResults, err := p.d.GetLogEvents(ctx, &loggingdriver.LogQueryInput{
				LogGroup:  "ptr-test-group",
				LogStream: "stream-1",
			})
			if err != nil {
				t.Fatalf("GetLogEvents (stream): %v", err)
			}
			if len(streamResults) != 3 {
				t.Errorf("expected 3 events from stream-1, got %d", len(streamResults))
			}

			// Query with pointer — limit
			limited, err := p.d.GetLogEvents(ctx, &loggingdriver.LogQueryInput{
				LogGroup: "ptr-test-group",
				Limit:    1,
			})
			if err != nil {
				t.Fatalf("GetLogEvents (limit): %v", err)
			}
			if len(limited) != 1 {
				t.Errorf("expected 1 event with limit=1, got %d", len(limited))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Container Registry Tests
// ---------------------------------------------------------------------------

func TestContainerRegistryOperations(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    crdriver.ContainerRegistry
	}{
		{"AWS", awsP.ECR},
		{"Azure", azureP.ACR},
		{"GCP", gcpP.ArtifactRegistry},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			// CreateRepository
			repo, err := p.d.CreateRepository(ctx, crdriver.RepositoryConfig{
				Name:               "my-app",
				Tags:               map[string]string{"env": "test"},
				ImageTagMutability: "MUTABLE",
			})
			if err != nil {
				t.Fatalf("CreateRepository: %v", err)
			}
			if repo == nil {
				t.Fatalf("expected non-nil repo")
			}

			// GetRepository
			got, err := p.d.GetRepository(ctx, "my-app")
			if err != nil {
				t.Fatalf("GetRepository: %v", err)
			}
			if got == nil {
				t.Fatalf("expected non-nil repo from Get")
			}

			// ListRepositories
			repos, err := p.d.ListRepositories(ctx)
			if err != nil {
				t.Fatalf("ListRepositories: %v", err)
			}
			if len(repos) != 1 {
				t.Errorf("expected 1 repo, got %d", len(repos))
			}

			// PutImage
			img, err := p.d.PutImage(ctx, &crdriver.ImageManifest{
				Repository: "my-app",
				Tag:        "v1.0",
				Digest:     "sha256:abc123",
				MediaType:  "application/vnd.docker.distribution.manifest.v2+json",
				SizeBytes:  1024,
			})
			if err != nil {
				t.Fatalf("PutImage: %v", err)
			}
			if len(img.Tags) == 0 {
				t.Errorf("expected at least one tag on image")
			}

			// GetImage
			imgDetail, err := p.d.GetImage(ctx, "my-app", "v1.0")
			if err != nil {
				t.Fatalf("GetImage: %v", err)
			}
			if imgDetail.Digest != "sha256:abc123" {
				t.Errorf("expected digest sha256:abc123, got %s", imgDetail.Digest)
			}

			// ListImages
			images, err := p.d.ListImages(ctx, "my-app")
			if err != nil {
				t.Fatalf("ListImages: %v", err)
			}
			if len(images) != 1 {
				t.Errorf("expected 1 image, got %d", len(images))
			}

			// TagImage
			err = p.d.TagImage(ctx, "my-app", "v1.0", "latest")
			if err != nil {
				t.Fatalf("TagImage: %v", err)
			}
			imgDetail2, err := p.d.GetImage(ctx, "my-app", "latest")
			if err != nil {
				t.Fatalf("GetImage after TagImage: %v", err)
			}
			if imgDetail2.Digest != "sha256:abc123" {
				t.Errorf("expected same digest after tagging, got %s", imgDetail2.Digest)
			}

			// DeleteImage
			err = p.d.DeleteImage(ctx, "my-app", "v1.0")
			if err != nil {
				t.Fatalf("DeleteImage: %v", err)
			}

			// DeleteRepository
			err = p.d.DeleteRepository(ctx, "my-app", true)
			if err != nil {
				t.Fatalf("DeleteRepository: %v", err)
			}
			_, err = p.d.GetRepository(ctx, "my-app")
			if err == nil {
				t.Errorf("expected error after deleting repo, got nil")
			}
		})
	}
}

func TestContainerRegistryImmutableTags(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    crdriver.ContainerRegistry
	}{
		{"AWS", awsP.ECR},
		{"Azure", azureP.ACR},
		{"GCP", gcpP.ArtifactRegistry},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			_, err := p.d.CreateRepository(ctx, crdriver.RepositoryConfig{
				Name:               "immutable-repo",
				ImageTagMutability: "IMMUTABLE",
			})
			if err != nil {
				t.Fatalf("CreateRepository: %v", err)
			}

			_, err = p.d.PutImage(ctx, &crdriver.ImageManifest{
				Repository: "immutable-repo",
				Tag:        "v1.0",
				Digest:     "sha256:first",
				SizeBytes:  512,
			})
			if err != nil {
				t.Fatalf("PutImage first: %v", err)
			}

			// Push duplicate tag should fail on IMMUTABLE
			_, err = p.d.PutImage(ctx, &crdriver.ImageManifest{
				Repository: "immutable-repo",
				Tag:        "v1.0",
				Digest:     "sha256:second",
				SizeBytes:  512,
			})
			if err == nil {
				t.Errorf("expected error pushing duplicate tag to immutable repo, got nil")
			}
		})
	}
}

func TestContainerRegistryLifecyclePolicy(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    crdriver.ContainerRegistry
	}{
		{"AWS", awsP.ECR},
		{"Azure", azureP.ACR},
		{"GCP", gcpP.ArtifactRegistry},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			_, err := p.d.CreateRepository(ctx, crdriver.RepositoryConfig{
				Name:               "lifecycle-repo",
				ImageTagMutability: "MUTABLE",
			})
			if err != nil {
				t.Fatalf("CreateRepository: %v", err)
			}

			policy := crdriver.LifecyclePolicy{
				Rules: []crdriver.LifecycleRule{
					{
						Priority:    1,
						Description: "expire untagged",
						TagStatus:   "untagged",
						CountType:   "imageCountMoreThan",
						CountValue:  5,
						Action:      "expire",
					},
				},
			}
			err = p.d.PutLifecyclePolicy(ctx, "lifecycle-repo", policy)
			if err != nil {
				t.Fatalf("PutLifecyclePolicy: %v", err)
			}

			got, err := p.d.GetLifecyclePolicy(ctx, "lifecycle-repo")
			if err != nil {
				t.Fatalf("GetLifecyclePolicy: %v", err)
			}
			if len(got.Rules) != 1 {
				t.Errorf("expected 1 lifecycle rule, got %d", len(got.Rules))
			}

			_, err = p.d.EvaluateLifecyclePolicy(ctx, "lifecycle-repo")
			if err != nil {
				t.Fatalf("EvaluateLifecyclePolicy: %v", err)
			}
		})
	}
}

func TestContainerRegistryImageScan(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    crdriver.ContainerRegistry
	}{
		{"AWS", awsP.ECR},
		{"Azure", azureP.ACR},
		{"GCP", gcpP.ArtifactRegistry},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			_, err := p.d.CreateRepository(ctx, crdriver.RepositoryConfig{
				Name:               "scan-repo",
				ImageTagMutability: "MUTABLE",
				ImageScanOnPush:    true,
			})
			if err != nil {
				t.Fatalf("CreateRepository: %v", err)
			}

			_, err = p.d.PutImage(ctx, &crdriver.ImageManifest{
				Repository: "scan-repo",
				Tag:        "latest",
				Digest:     "sha256:scanme",
				SizeBytes:  2048,
			})
			if err != nil {
				t.Fatalf("PutImage: %v", err)
			}

			scanResult, err := p.d.StartImageScan(ctx, "scan-repo", "latest")
			if err != nil {
				t.Fatalf("StartImageScan: %v", err)
			}
			if scanResult.Status == "" {
				t.Errorf("expected scan status, got empty")
			}

			results, err := p.d.GetImageScanResults(ctx, "scan-repo", "latest")
			if err != nil {
				t.Fatalf("GetImageScanResults: %v", err)
			}
			if results.Repository != "scan-repo" {
				t.Errorf("expected repository scan-repo, got %s", results.Repository)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Event Bus Tests
// ---------------------------------------------------------------------------

func TestEventBusOperations(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    ebdriver.EventBus
	}{
		{"AWS", awsP.EventBridge},
		{"Azure", azureP.EventGrid},
		{"GCP", gcpP.Eventarc},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			// CreateEventBus
			bus, err := p.d.CreateEventBus(ctx, ebdriver.EventBusConfig{
				Name: "test-bus",
				Tags: map[string]string{"env": "test"},
			})
			if err != nil {
				t.Fatalf("CreateEventBus: %v", err)
			}
			if bus.Name != "test-bus" {
				t.Errorf("expected bus name test-bus, got %s", bus.Name)
			}

			// GetEventBus
			gotBus, err := p.d.GetEventBus(ctx, "test-bus")
			if err != nil {
				t.Fatalf("GetEventBus: %v", err)
			}
			if gotBus.Name != "test-bus" {
				t.Errorf("expected test-bus, got %s", gotBus.Name)
			}

			// ListEventBuses
			buses, err := p.d.ListEventBuses(ctx)
			if err != nil {
				t.Fatalf("ListEventBuses: %v", err)
			}
			if len(buses) < 1 {
				t.Errorf("expected at least 1 bus, got %d", len(buses))
			}

			// PutRule
			rule, err := p.d.PutRule(ctx, &ebdriver.RuleConfig{
				Name:         "test-rule",
				EventBus:     "test-bus",
				Description:  "test rule",
				EventPattern: `{"source":["my.app"]}`,
				State:        "ENABLED",
			})
			if err != nil {
				t.Fatalf("PutRule: %v", err)
			}
			if rule.Name != "test-rule" {
				t.Errorf("expected rule name test-rule, got %s", rule.Name)
			}

			// GetRule
			gotRule, err := p.d.GetRule(ctx, "test-bus", "test-rule")
			if err != nil {
				t.Fatalf("GetRule: %v", err)
			}
			if gotRule.State != "ENABLED" {
				t.Errorf("expected ENABLED, got %s", gotRule.State)
			}

			// ListRules
			rules, err := p.d.ListRules(ctx, "test-bus")
			if err != nil {
				t.Fatalf("ListRules: %v", err)
			}
			if len(rules) != 1 {
				t.Errorf("expected 1 rule, got %d", len(rules))
			}

			// DisableRule
			err = p.d.DisableRule(ctx, "test-bus", "test-rule")
			if err != nil {
				t.Fatalf("DisableRule: %v", err)
			}
			gotRule, err = p.d.GetRule(ctx, "test-bus", "test-rule")
			if err != nil {
				t.Fatalf("GetRule after disable: %v", err)
			}
			if gotRule.State != "DISABLED" {
				t.Errorf("expected DISABLED, got %s", gotRule.State)
			}

			// EnableRule
			err = p.d.EnableRule(ctx, "test-bus", "test-rule")
			if err != nil {
				t.Fatalf("EnableRule: %v", err)
			}

			// PutTargets
			err = p.d.PutTargets(ctx, "test-bus", "test-rule", []ebdriver.Target{
				{ID: "target-1", ARN: "arn:aws:lambda:us-east-1:123456:function:my-func"},
			})
			if err != nil {
				t.Fatalf("PutTargets: %v", err)
			}

			// ListTargets
			targets, err := p.d.ListTargets(ctx, "test-bus", "test-rule")
			if err != nil {
				t.Fatalf("ListTargets: %v", err)
			}
			if len(targets) != 1 {
				t.Errorf("expected 1 target, got %d", len(targets))
			}

			// RemoveTargets
			err = p.d.RemoveTargets(ctx, "test-bus", "test-rule", []string{"target-1"})
			if err != nil {
				t.Fatalf("RemoveTargets: %v", err)
			}
			targets, err = p.d.ListTargets(ctx, "test-bus", "test-rule")
			if err != nil {
				t.Fatalf("ListTargets after remove: %v", err)
			}
			if len(targets) != 0 {
				t.Errorf("expected 0 targets after remove, got %d", len(targets))
			}

			// DeleteRule
			err = p.d.DeleteRule(ctx, "test-bus", "test-rule")
			if err != nil {
				t.Fatalf("DeleteRule: %v", err)
			}

			// DeleteEventBus
			err = p.d.DeleteEventBus(ctx, "test-bus")
			if err != nil {
				t.Fatalf("DeleteEventBus: %v", err)
			}
			_, err = p.d.GetEventBus(ctx, "test-bus")
			if err == nil {
				t.Errorf("expected error after deleting bus, got nil")
			}
		})
	}
}

func TestEventPatternMatching(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    ebdriver.EventBus
	}{
		{"AWS", awsP.EventBridge},
		{"Azure", azureP.EventGrid},
		{"GCP", gcpP.Eventarc},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			_, err := p.d.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "pattern-bus"})
			if err != nil {
				t.Fatalf("CreateEventBus: %v", err)
			}

			_, err = p.d.PutRule(ctx, &ebdriver.RuleConfig{
				Name:         "pattern-rule",
				EventBus:     "pattern-bus",
				EventPattern: `{"source":["my.app"]}`,
				State:        "ENABLED",
			})
			if err != nil {
				t.Fatalf("PutRule: %v", err)
			}

			result, err := p.d.PutEvents(ctx, []ebdriver.Event{
				{
					Source:     "my.app",
					DetailType: "OrderCreated",
					Detail:     `{"orderId":"123"}`,
					EventBus:   "pattern-bus",
				},
			})
			if err != nil {
				t.Fatalf("PutEvents: %v", err)
			}
			if result.SuccessCount != 1 {
				t.Errorf("expected 1 success, got %d", result.SuccessCount)
			}

			history, err := p.d.GetEventHistory(ctx, "pattern-bus", 10)
			if err != nil {
				t.Fatalf("GetEventHistory: %v", err)
			}
			if len(history) < 1 {
				t.Errorf("expected at least 1 event in history, got %d", len(history))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Storage Enhancement Tests
// ---------------------------------------------------------------------------

func TestPresignedURLs(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    storagedriver.Bucket
	}{
		{"AWS", awsP.S3},
		{"Azure", azureP.BlobStorage},
		{"GCP", gcpP.GCS},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			err := p.d.CreateBucket(ctx, "presign-bucket")
			if err != nil {
				t.Fatalf("CreateBucket: %v", err)
			}
			err = p.d.PutObject(ctx, "presign-bucket", "test.txt", []byte("hello"), "text/plain", nil)
			if err != nil {
				t.Fatalf("PutObject: %v", err)
			}

			// GET presigned URL
			getURL, err := p.d.GeneratePresignedURL(ctx, storagedriver.PresignedURLRequest{
				Bucket:    "presign-bucket",
				Key:       "test.txt",
				Method:    "GET",
				ExpiresIn: 15 * time.Minute,
			})
			if err != nil {
				t.Fatalf("GeneratePresignedURL GET: %v", err)
			}
			if getURL.URL == "" {
				t.Errorf("expected non-empty presigned URL")
			}
			if getURL.Method != "GET" {
				t.Errorf("expected method GET, got %s", getURL.Method)
			}

			// PUT presigned URL
			putURL, err := p.d.GeneratePresignedURL(ctx, storagedriver.PresignedURLRequest{
				Bucket:    "presign-bucket",
				Key:       "upload.txt",
				Method:    "PUT",
				ExpiresIn: 30 * time.Minute,
			})
			if err != nil {
				t.Fatalf("GeneratePresignedURL PUT: %v", err)
			}
			if putURL.URL == "" {
				t.Errorf("expected non-empty presigned URL")
			}
			if putURL.Method != "PUT" {
				t.Errorf("expected method PUT, got %s", putURL.Method)
			}
		})
	}
}

func TestBucketLifecycle(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	awsP := NewAWS(config.WithClock(clk))
	azureP := NewAzure(config.WithClock(clk))
	gcpP := NewGCP(config.WithClock(clk))

	providers := []struct {
		name string
		d    storagedriver.Bucket
	}{
		{"AWS", awsP.S3},
		{"Azure", azureP.BlobStorage},
		{"GCP", gcpP.GCS},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			err := p.d.CreateBucket(ctx, "lc-bucket")
			if err != nil {
				t.Fatalf("CreateBucket: %v", err)
			}

			// Put objects
			err = p.d.PutObject(ctx, "lc-bucket", "old-file.txt", []byte("old"), "text/plain", nil)
			if err != nil {
				t.Fatalf("PutObject: %v", err)
			}

			// Set lifecycle with 1-day expiration
			err = p.d.PutLifecycleConfig(ctx, "lc-bucket", storagedriver.LifecycleConfig{
				Rules: []storagedriver.LifecycleRule{
					{
						ID:             "expire-old",
						Enabled:        true,
						Prefix:         "",
						ExpirationDays: 1,
					},
				},
			})
			if err != nil {
				t.Fatalf("PutLifecycleConfig: %v", err)
			}

			gotLC, err := p.d.GetLifecycleConfig(ctx, "lc-bucket")
			if err != nil {
				t.Fatalf("GetLifecycleConfig: %v", err)
			}
			if len(gotLC.Rules) != 1 {
				t.Errorf("expected 1 lifecycle rule, got %d", len(gotLC.Rules))
			}

			// Advance time past expiration
			clk.Advance(48 * time.Hour)

			expired, err := p.d.EvaluateLifecycle(ctx, "lc-bucket")
			if err != nil {
				t.Fatalf("EvaluateLifecycle: %v", err)
			}
			if len(expired) < 1 {
				t.Errorf("expected at least 1 expired object, got %d", len(expired))
			}
		})
	}
}

func TestMultipartUpload(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    storagedriver.Bucket
	}{
		{"AWS", awsP.S3},
		{"Azure", azureP.BlobStorage},
		{"GCP", gcpP.GCS},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			err := p.d.CreateBucket(ctx, "mp-bucket")
			if err != nil {
				t.Fatalf("CreateBucket: %v", err)
			}

			// Create multipart upload
			upload, err := p.d.CreateMultipartUpload(ctx, "mp-bucket", "big-file.bin", "application/octet-stream")
			if err != nil {
				t.Fatalf("CreateMultipartUpload: %v", err)
			}
			if upload.UploadID == "" {
				t.Fatalf("expected non-empty upload ID")
			}

			// Upload parts
			part1, err := p.d.UploadPart(ctx, "mp-bucket", "big-file.bin", upload.UploadID, 1, []byte("part-one-"))
			if err != nil {
				t.Fatalf("UploadPart 1: %v", err)
			}
			part2, err := p.d.UploadPart(ctx, "mp-bucket", "big-file.bin", upload.UploadID, 2, []byte("part-two"))
			if err != nil {
				t.Fatalf("UploadPart 2: %v", err)
			}

			// List multipart uploads
			uploads, err := p.d.ListMultipartUploads(ctx, "mp-bucket")
			if err != nil {
				t.Fatalf("ListMultipartUploads: %v", err)
			}
			if len(uploads) < 1 {
				t.Errorf("expected at least 1 upload, got %d", len(uploads))
			}

			// Complete
			err = p.d.CompleteMultipartUpload(ctx, "mp-bucket", "big-file.bin", upload.UploadID, []storagedriver.UploadPart{*part1, *part2})
			if err != nil {
				t.Fatalf("CompleteMultipartUpload: %v", err)
			}

			// Verify final object
			obj, err := p.d.GetObject(ctx, "mp-bucket", "big-file.bin")
			if err != nil {
				t.Fatalf("GetObject after multipart: %v", err)
			}
			if string(obj.Data) != "part-one-part-two" {
				t.Errorf("expected 'part-one-part-two', got '%s'", string(obj.Data))
			}
		})
	}
}

func TestMultipartUploadAbort(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    storagedriver.Bucket
	}{
		{"AWS", awsP.S3},
		{"Azure", azureP.BlobStorage},
		{"GCP", gcpP.GCS},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			err := p.d.CreateBucket(ctx, "abort-bucket")
			if err != nil {
				t.Fatalf("CreateBucket: %v", err)
			}

			upload, err := p.d.CreateMultipartUpload(ctx, "abort-bucket", "aborted.bin", "application/octet-stream")
			if err != nil {
				t.Fatalf("CreateMultipartUpload: %v", err)
			}

			_, err = p.d.UploadPart(ctx, "abort-bucket", "aborted.bin", upload.UploadID, 1, []byte("data"))
			if err != nil {
				t.Fatalf("UploadPart: %v", err)
			}

			err = p.d.AbortMultipartUpload(ctx, "abort-bucket", "aborted.bin", upload.UploadID)
			if err != nil {
				t.Fatalf("AbortMultipartUpload: %v", err)
			}

			// Object should not exist
			_, err = p.d.GetObject(ctx, "abort-bucket", "aborted.bin")
			if err == nil {
				t.Errorf("expected error getting object after abort, got nil")
			}
		})
	}
}

func TestBucketVersioning(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    storagedriver.Bucket
	}{
		{"AWS", awsP.S3},
		{"Azure", azureP.BlobStorage},
		{"GCP", gcpP.GCS},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			err := p.d.CreateBucket(ctx, "ver-bucket")
			if err != nil {
				t.Fatalf("CreateBucket: %v", err)
			}

			// Default should be disabled
			enabled, err := p.d.GetBucketVersioning(ctx, "ver-bucket")
			if err != nil {
				t.Fatalf("GetBucketVersioning: %v", err)
			}
			if enabled {
				t.Errorf("expected versioning disabled by default")
			}

			// Enable
			err = p.d.SetBucketVersioning(ctx, "ver-bucket", true)
			if err != nil {
				t.Fatalf("SetBucketVersioning enable: %v", err)
			}

			enabled, err = p.d.GetBucketVersioning(ctx, "ver-bucket")
			if err != nil {
				t.Fatalf("GetBucketVersioning after enable: %v", err)
			}
			if !enabled {
				t.Errorf("expected versioning enabled")
			}

			// Disable
			err = p.d.SetBucketVersioning(ctx, "ver-bucket", false)
			if err != nil {
				t.Fatalf("SetBucketVersioning disable: %v", err)
			}
			enabled, err = p.d.GetBucketVersioning(ctx, "ver-bucket")
			if err != nil {
				t.Fatalf("GetBucketVersioning after disable: %v", err)
			}
			if enabled {
				t.Errorf("expected versioning disabled")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Database Enhancement Tests
// ---------------------------------------------------------------------------

func TestDatabaseTTL(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	awsP := NewAWS(config.WithClock(clk))
	azureP := NewAzure(config.WithClock(clk))
	gcpP := NewGCP(config.WithClock(clk))

	providers := []struct {
		name string
		d    driver.Database
		pk   string
	}{
		{"AWS", awsP.DynamoDB, "pk"},
		{"Azure", azureP.CosmosDB, "pk"},
		{"GCP", gcpP.Firestore, "pk"},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			err := p.d.CreateTable(ctx, driver.TableConfig{
				Name:         "ttl-table",
				PartitionKey: p.pk,
			})
			if err != nil {
				t.Fatalf("CreateTable: %v", err)
			}

			// Enable TTL
			err = p.d.UpdateTTL(ctx, "ttl-table", driver.TTLConfig{
				Enabled:       true,
				AttributeName: "expiresAt",
			})
			if err != nil {
				t.Fatalf("UpdateTTL: %v", err)
			}

			ttlConfig, err := p.d.DescribeTTL(ctx, "ttl-table")
			if err != nil {
				t.Fatalf("DescribeTTL: %v", err)
			}
			if !ttlConfig.Enabled {
				t.Errorf("expected TTL enabled")
			}
			if ttlConfig.AttributeName != "expiresAt" {
				t.Errorf("expected TTL attribute expiresAt, got %s", ttlConfig.AttributeName)
			}

			// Put items with TTL (epoch seconds)
			ttlTime := clk.Now().Add(1 * time.Hour).Unix()
			err = p.d.PutItem(ctx, "ttl-table", map[string]any{
				p.pk:        "item-1",
				"expiresAt": ttlTime,
				"data":      "should expire",
			})
			if err != nil {
				t.Fatalf("PutItem: %v", err)
			}

			// Item should be visible before TTL
			item, err := p.d.GetItem(ctx, "ttl-table", map[string]any{p.pk: "item-1"})
			if err != nil {
				t.Fatalf("GetItem before TTL: %v", err)
			}
			if item == nil {
				t.Fatalf("expected item before TTL expiry")
			}

			// Advance past TTL
			clk.Advance(2 * time.Hour)

			_, err = p.d.GetItem(ctx, "ttl-table", map[string]any{p.pk: "item-1"})
			if err == nil {
				t.Errorf("expected error or nil for expired item")
			}
		})
	}
}

func TestDatabaseStreams(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    driver.Database
		pk   string
	}{
		{"AWS", awsP.DynamoDB, "pk"},
		{"Azure", azureP.CosmosDB, "pk"},
		{"GCP", gcpP.Firestore, "pk"},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			err := p.d.CreateTable(ctx, driver.TableConfig{
				Name:         "stream-table",
				PartitionKey: p.pk,
			})
			if err != nil {
				t.Fatalf("CreateTable: %v", err)
			}

			// Enable streams
			err = p.d.UpdateStreamConfig(ctx, "stream-table", driver.StreamConfig{
				Enabled:  true,
				ViewType: "NEW_AND_OLD_IMAGES",
			})
			if err != nil {
				t.Fatalf("UpdateStreamConfig: %v", err)
			}

			// Put item -> INSERT
			err = p.d.PutItem(ctx, "stream-table", map[string]any{
				p.pk:   "stream-1",
				"data": "initial",
			})
			if err != nil {
				t.Fatalf("PutItem: %v", err)
			}

			// Modify item -> MODIFY
			err = p.d.PutItem(ctx, "stream-table", map[string]any{
				p.pk:   "stream-1",
				"data": "modified",
			})
			if err != nil {
				t.Fatalf("PutItem modify: %v", err)
			}

			// Delete item -> REMOVE
			err = p.d.DeleteItem(ctx, "stream-table", map[string]any{p.pk: "stream-1"})
			if err != nil {
				t.Fatalf("DeleteItem: %v", err)
			}

			// Get stream records
			iter, err := p.d.GetStreamRecords(ctx, "stream-table", 10, "")
			if err != nil {
				t.Fatalf("GetStreamRecords: %v", err)
			}
			if len(iter.Records) < 3 {
				t.Errorf("expected at least 3 stream records (INSERT/MODIFY/REMOVE), got %d", len(iter.Records))
			}

			// Verify event types
			eventTypes := make(map[string]bool)
			for _, r := range iter.Records {
				eventTypes[r.EventType] = true
			}
			for _, expected := range []string{"INSERT", "MODIFY", "REMOVE"} {
				if !eventTypes[expected] {
					t.Errorf("expected event type %s in stream records", expected)
				}
			}

			// Verify sequence numbers are ordered
			for i := 1; i < len(iter.Records); i++ {
				if iter.Records[i].SequenceNumber <= iter.Records[i-1].SequenceNumber {
					t.Errorf("expected increasing sequence numbers, got %s <= %s",
						iter.Records[i].SequenceNumber, iter.Records[i-1].SequenceNumber)
				}
			}
		})
	}
}

func TestTransactWriteItems(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    driver.Database
		pk   string
	}{
		{"AWS", awsP.DynamoDB, "pk"},
		{"Azure", azureP.CosmosDB, "pk"},
		{"GCP", gcpP.Firestore, "pk"},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			err := p.d.CreateTable(ctx, driver.TableConfig{
				Name:         "txn-table",
				PartitionKey: p.pk,
			})
			if err != nil {
				t.Fatalf("CreateTable: %v", err)
			}

			// Seed an item to delete
			err = p.d.PutItem(ctx, "txn-table", map[string]any{
				p.pk:   "to-delete",
				"data": "will be removed",
			})
			if err != nil {
				t.Fatalf("PutItem seed: %v", err)
			}

			// Transact: put 2 items and delete 1
			err = p.d.TransactWriteItems(ctx, "txn-table",
				[]map[string]any{
					{p.pk: "txn-1", "data": "first"},
					{p.pk: "txn-2", "data": "second"},
				},
				[]map[string]any{
					{p.pk: "to-delete"},
				},
			)
			if err != nil {
				t.Fatalf("TransactWriteItems: %v", err)
			}

			// Verify puts
			item1, err := p.d.GetItem(ctx, "txn-table", map[string]any{p.pk: "txn-1"})
			if err != nil {
				t.Fatalf("GetItem txn-1: %v", err)
			}
			if item1["data"] != "first" {
				t.Errorf("expected data=first, got %v", item1["data"])
			}

			item2, err := p.d.GetItem(ctx, "txn-table", map[string]any{p.pk: "txn-2"})
			if err != nil {
				t.Fatalf("GetItem txn-2: %v", err)
			}
			if item2["data"] != "second" {
				t.Errorf("expected data=second, got %v", item2["data"])
			}

			// Verify delete
			_, err = p.d.GetItem(ctx, "txn-table", map[string]any{p.pk: "to-delete"})
			if err == nil {
				t.Errorf("expected error for deleted item, got nil")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Compute Enhancement Tests
// ---------------------------------------------------------------------------

func TestAutoScalingGroup(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    computedriver.Compute
	}{
		{"AWS", awsP.EC2},
		{"Azure", azureP.VirtualMachines},
		{"GCP", gcpP.GCE},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			asg, err := p.d.CreateAutoScalingGroup(ctx, computedriver.AutoScalingGroupConfig{
				Name:            "test-asg",
				MinSize:         1,
				MaxSize:         5,
				DesiredCapacity: 2,
				InstanceConfig: computedriver.InstanceConfig{
					ImageID:      "ami-test",
					InstanceType: "t2.micro",
				},
				Tags: map[string]string{"env": "test"},
			})
			if err != nil {
				t.Fatalf("CreateAutoScalingGroup: %v", err)
			}
			if asg.Name != "test-asg" {
				t.Errorf("expected ASG name test-asg, got %s", asg.Name)
			}
			if asg.DesiredCapacity != 2 {
				t.Errorf("expected desired capacity 2, got %d", asg.DesiredCapacity)
			}

			// Verify instances launched
			got, err := p.d.GetAutoScalingGroup(ctx, "test-asg")
			if err != nil {
				t.Fatalf("GetAutoScalingGroup: %v", err)
			}
			if len(got.InstanceIDs) != 2 {
				t.Errorf("expected 2 instances, got %d", len(got.InstanceIDs))
			}

			// Scale up
			err = p.d.SetDesiredCapacity(ctx, "test-asg", 4)
			if err != nil {
				t.Fatalf("SetDesiredCapacity up: %v", err)
			}
			got, err = p.d.GetAutoScalingGroup(ctx, "test-asg")
			if err != nil {
				t.Fatalf("GetAutoScalingGroup after scale up: %v", err)
			}
			if len(got.InstanceIDs) != 4 {
				t.Errorf("expected 4 instances after scale up, got %d", len(got.InstanceIDs))
			}

			// Scale down
			err = p.d.SetDesiredCapacity(ctx, "test-asg", 1)
			if err != nil {
				t.Fatalf("SetDesiredCapacity down: %v", err)
			}
			got, err = p.d.GetAutoScalingGroup(ctx, "test-asg")
			if err != nil {
				t.Fatalf("GetAutoScalingGroup after scale down: %v", err)
			}
			if len(got.InstanceIDs) != 1 {
				t.Errorf("expected 1 instance after scale down, got %d", len(got.InstanceIDs))
			}

			// List
			asgs, err := p.d.ListAutoScalingGroups(ctx)
			if err != nil {
				t.Fatalf("ListAutoScalingGroups: %v", err)
			}
			if len(asgs) != 1 {
				t.Errorf("expected 1 ASG, got %d", len(asgs))
			}

			// Delete
			err = p.d.DeleteAutoScalingGroup(ctx, "test-asg", true)
			if err != nil {
				t.Fatalf("DeleteAutoScalingGroup: %v", err)
			}
			_, err = p.d.GetAutoScalingGroup(ctx, "test-asg")
			if err == nil {
				t.Errorf("expected error after deleting ASG, got nil")
			}
		})
	}
}

func TestScalingPolicy(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    computedriver.Compute
	}{
		{"AWS", awsP.EC2},
		{"Azure", azureP.VirtualMachines},
		{"GCP", gcpP.GCE},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			_, err := p.d.CreateAutoScalingGroup(ctx, computedriver.AutoScalingGroupConfig{
				Name:            "policy-asg",
				MinSize:         1,
				MaxSize:         10,
				DesiredCapacity: 2,
				InstanceConfig: computedriver.InstanceConfig{
					ImageID:      "ami-test",
					InstanceType: "t2.micro",
				},
			})
			if err != nil {
				t.Fatalf("CreateAutoScalingGroup: %v", err)
			}

			err = p.d.PutScalingPolicy(ctx, computedriver.ScalingPolicy{
				Name:              "scale-out",
				AutoScalingGroup:  "policy-asg",
				PolicyType:        "SimpleScaling",
				AdjustmentType:    "ChangeInCapacity",
				ScalingAdjustment: 2,
				Cooldown:          60,
			})
			if err != nil {
				t.Fatalf("PutScalingPolicy: %v", err)
			}

			// Execute policy
			err = p.d.ExecuteScalingPolicy(ctx, "policy-asg", "scale-out")
			if err != nil {
				t.Fatalf("ExecuteScalingPolicy: %v", err)
			}

			asg, err := p.d.GetAutoScalingGroup(ctx, "policy-asg")
			if err != nil {
				t.Fatalf("GetAutoScalingGroup after execute: %v", err)
			}
			if asg.DesiredCapacity != 4 {
				t.Errorf("expected desired capacity 4, got %d", asg.DesiredCapacity)
			}

			// Delete policy
			err = p.d.DeleteScalingPolicy(ctx, "policy-asg", "scale-out")
			if err != nil {
				t.Fatalf("DeleteScalingPolicy: %v", err)
			}
		})
	}
}

func TestSpotInstances(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    computedriver.Compute
	}{
		{"AWS", awsP.EC2},
		{"Azure", azureP.VirtualMachines},
		{"GCP", gcpP.GCE},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			reqs, err := p.d.RequestSpotInstances(ctx, computedriver.SpotRequestConfig{
				InstanceConfig: computedriver.InstanceConfig{
					ImageID:      "ami-spot",
					InstanceType: "m4.large",
				},
				MaxPrice: 0.5,
				Count:    2,
				Type:     "one-time",
			})
			if err != nil {
				t.Fatalf("RequestSpotInstances: %v", err)
			}
			if len(reqs) != 2 {
				t.Fatalf("expected 2 spot requests, got %d", len(reqs))
			}

			// Describe spot requests
			ids := make([]string, len(reqs))
			for i, r := range reqs {
				ids[i] = r.ID
			}
			described, err := p.d.DescribeSpotRequests(ctx, ids)
			if err != nil {
				t.Fatalf("DescribeSpotRequests: %v", err)
			}
			if len(described) != 2 {
				t.Errorf("expected 2 described requests, got %d", len(described))
			}

			// Cancel
			err = p.d.CancelSpotRequests(ctx, ids)
			if err != nil {
				t.Fatalf("CancelSpotRequests: %v", err)
			}

			described, err = p.d.DescribeSpotRequests(ctx, ids)
			if err != nil {
				t.Fatalf("DescribeSpotRequests after cancel: %v", err)
			}
			for _, d := range described {
				if d.Status != "canceled" && d.Status != "cancelled" {
					t.Errorf("expected canceled status, got %s", d.Status)
				}
			}
		})
	}
}

func TestLaunchTemplates(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    computedriver.Compute
	}{
		{"AWS", awsP.EC2},
		{"Azure", azureP.VirtualMachines},
		{"GCP", gcpP.GCE},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			lt, err := p.d.CreateLaunchTemplate(ctx, computedriver.LaunchTemplateConfig{
				Name: "test-template",
				InstanceConfig: computedriver.InstanceConfig{
					ImageID:      "ami-template",
					InstanceType: "t2.micro",
				},
			})
			if err != nil {
				t.Fatalf("CreateLaunchTemplate: %v", err)
			}
			if lt.Name != "test-template" {
				t.Errorf("expected name test-template, got %s", lt.Name)
			}
			if lt.Version != 1 {
				t.Errorf("expected version 1, got %d", lt.Version)
			}

			// Get
			got, err := p.d.GetLaunchTemplate(ctx, "test-template")
			if err != nil {
				t.Fatalf("GetLaunchTemplate: %v", err)
			}
			if got.Name != "test-template" {
				t.Errorf("expected test-template, got %s", got.Name)
			}

			// List
			templates, err := p.d.ListLaunchTemplates(ctx)
			if err != nil {
				t.Fatalf("ListLaunchTemplates: %v", err)
			}
			if len(templates) != 1 {
				t.Errorf("expected 1 template, got %d", len(templates))
			}

			// Delete
			err = p.d.DeleteLaunchTemplate(ctx, "test-template")
			if err != nil {
				t.Fatalf("DeleteLaunchTemplate: %v", err)
			}
			_, err = p.d.GetLaunchTemplate(ctx, "test-template")
			if err == nil {
				t.Errorf("expected error after deleting template, got nil")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Serverless Enhancement Tests
// ---------------------------------------------------------------------------

func TestFunctionVersions(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    serverlessdriver.Serverless
	}{
		{"AWS", awsP.Lambda},
		{"Azure", azureP.Functions},
		{"GCP", gcpP.CloudFunctions},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			_, err := p.d.CreateFunction(ctx, serverlessdriver.FunctionConfig{
				Name:    "version-func",
				Runtime: "go1.x",
				Handler: "main",
				Memory:  128,
				Timeout: 30,
			})
			if err != nil {
				t.Fatalf("CreateFunction: %v", err)
			}

			// Publish version
			v1, err := p.d.PublishVersion(ctx, "version-func", "first version")
			if err != nil {
				t.Fatalf("PublishVersion: %v", err)
			}
			if v1.Version != "1" {
				t.Errorf("expected version 1, got %s", v1.Version)
			}

			// Publish another
			v2, err := p.d.PublishVersion(ctx, "version-func", "second version")
			if err != nil {
				t.Fatalf("PublishVersion 2: %v", err)
			}
			if v2.Version != "2" {
				t.Errorf("expected version 2, got %s", v2.Version)
			}

			// List versions
			versions, err := p.d.ListVersions(ctx, "version-func")
			if err != nil {
				t.Fatalf("ListVersions: %v", err)
			}
			if len(versions) < 2 {
				t.Errorf("expected at least 2 versions, got %d", len(versions))
			}
		})
	}
}

func TestFunctionAliases(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    serverlessdriver.Serverless
	}{
		{"AWS", awsP.Lambda},
		{"Azure", azureP.Functions},
		{"GCP", gcpP.CloudFunctions},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			_, err := p.d.CreateFunction(ctx, serverlessdriver.FunctionConfig{
				Name:    "alias-func",
				Runtime: "go1.x",
				Handler: "main",
				Memory:  128,
				Timeout: 30,
			})
			if err != nil {
				t.Fatalf("CreateFunction: %v", err)
			}

			v1, err := p.d.PublishVersion(ctx, "alias-func", "v1")
			if err != nil {
				t.Fatalf("PublishVersion: %v", err)
			}

			// Create alias
			alias, err := p.d.CreateAlias(ctx, serverlessdriver.AliasConfig{
				FunctionName:    "alias-func",
				Name:            "prod",
				FunctionVersion: v1.Version,
				Description:     "production alias",
			})
			if err != nil {
				t.Fatalf("CreateAlias: %v", err)
			}
			if alias.Name != "prod" {
				t.Errorf("expected alias name prod, got %s", alias.Name)
			}

			// Get alias
			gotAlias, err := p.d.GetAlias(ctx, "alias-func", "prod")
			if err != nil {
				t.Fatalf("GetAlias: %v", err)
			}
			if gotAlias.FunctionVersion != v1.Version {
				t.Errorf("expected version %s, got %s", v1.Version, gotAlias.FunctionVersion)
			}

			// Update alias
			v2, err := p.d.PublishVersion(ctx, "alias-func", "v2")
			if err != nil {
				t.Fatalf("PublishVersion v2: %v", err)
			}
			_, err = p.d.UpdateAlias(ctx, serverlessdriver.AliasConfig{
				FunctionName:    "alias-func",
				Name:            "prod",
				FunctionVersion: v2.Version,
			})
			if err != nil {
				t.Fatalf("UpdateAlias: %v", err)
			}

			gotAlias, err = p.d.GetAlias(ctx, "alias-func", "prod")
			if err != nil {
				t.Fatalf("GetAlias after update: %v", err)
			}
			if gotAlias.FunctionVersion != v2.Version {
				t.Errorf("expected updated version %s, got %s", v2.Version, gotAlias.FunctionVersion)
			}

			// List aliases
			aliases, err := p.d.ListAliases(ctx, "alias-func")
			if err != nil {
				t.Fatalf("ListAliases: %v", err)
			}
			if len(aliases) != 1 {
				t.Errorf("expected 1 alias, got %d", len(aliases))
			}

			// Delete alias
			err = p.d.DeleteAlias(ctx, "alias-func", "prod")
			if err != nil {
				t.Fatalf("DeleteAlias: %v", err)
			}
			_, err = p.d.GetAlias(ctx, "alias-func", "prod")
			if err == nil {
				t.Errorf("expected error after deleting alias, got nil")
			}
		})
	}
}

func TestLayers(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    serverlessdriver.Serverless
	}{
		{"AWS", awsP.Lambda},
		{"Azure", azureP.Functions},
		{"GCP", gcpP.CloudFunctions},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			// Publish layer version 1
			lv1, err := p.d.PublishLayerVersion(ctx, serverlessdriver.LayerConfig{
				Name:               "my-layer",
				Description:        "first version",
				Content:            []byte("layer-content-v1"),
				CompatibleRuntimes: []string{"go1.x", "python3.9"},
			})
			if err != nil {
				t.Fatalf("PublishLayerVersion: %v", err)
			}
			if lv1.Version != 1 {
				t.Errorf("expected layer version 1, got %d", lv1.Version)
			}

			// Publish version 2
			lv2, err := p.d.PublishLayerVersion(ctx, serverlessdriver.LayerConfig{
				Name:               "my-layer",
				Description:        "second version",
				Content:            []byte("layer-content-v2"),
				CompatibleRuntimes: []string{"go1.x"},
			})
			if err != nil {
				t.Fatalf("PublishLayerVersion 2: %v", err)
			}
			if lv2.Version != 2 {
				t.Errorf("expected layer version 2, got %d", lv2.Version)
			}

			// Get layer version
			got, err := p.d.GetLayerVersion(ctx, "my-layer", 1)
			if err != nil {
				t.Fatalf("GetLayerVersion: %v", err)
			}
			if got.Description != "first version" {
				t.Errorf("expected description 'first version', got '%s'", got.Description)
			}

			// List layer versions
			versions, err := p.d.ListLayerVersions(ctx, "my-layer")
			if err != nil {
				t.Fatalf("ListLayerVersions: %v", err)
			}
			if len(versions) != 2 {
				t.Errorf("expected 2 layer versions, got %d", len(versions))
			}

			// List layers
			layers, err := p.d.ListLayers(ctx)
			if err != nil {
				t.Fatalf("ListLayers: %v", err)
			}
			if len(layers) < 1 {
				t.Errorf("expected at least 1 layer, got %d", len(layers))
			}

			// Delete layer version
			err = p.d.DeleteLayerVersion(ctx, "my-layer", 1)
			if err != nil {
				t.Fatalf("DeleteLayerVersion: %v", err)
			}
			_, err = p.d.GetLayerVersion(ctx, "my-layer", 1)
			if err == nil {
				t.Errorf("expected error after deleting layer version, got nil")
			}
		})
	}
}

func TestFunctionConcurrency(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    serverlessdriver.Serverless
	}{
		{"AWS", awsP.Lambda},
		{"Azure", azureP.Functions},
		{"GCP", gcpP.CloudFunctions},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			_, err := p.d.CreateFunction(ctx, serverlessdriver.FunctionConfig{
				Name:    "conc-func",
				Runtime: "go1.x",
				Handler: "main",
				Memory:  128,
				Timeout: 30,
			})
			if err != nil {
				t.Fatalf("CreateFunction: %v", err)
			}

			// Put concurrency
			err = p.d.PutFunctionConcurrency(ctx, serverlessdriver.ConcurrencyConfig{
				FunctionName:                 "conc-func",
				ReservedConcurrentExecutions: 10,
			})
			if err != nil {
				t.Fatalf("PutFunctionConcurrency: %v", err)
			}

			// Get concurrency
			conc, err := p.d.GetFunctionConcurrency(ctx, "conc-func")
			if err != nil {
				t.Fatalf("GetFunctionConcurrency: %v", err)
			}
			if conc.ReservedConcurrentExecutions != 10 {
				t.Errorf("expected 10 reserved concurrency, got %d", conc.ReservedConcurrentExecutions)
			}

			// Delete concurrency
			err = p.d.DeleteFunctionConcurrency(ctx, "conc-func")
			if err != nil {
				t.Fatalf("DeleteFunctionConcurrency: %v", err)
			}

			conc, err = p.d.GetFunctionConcurrency(ctx, "conc-func")
			if err != nil {
				// NotFound after delete is acceptable
				return
			}
			if conc.ReservedConcurrentExecutions != 0 {
				t.Errorf("expected 0 after delete, got %d", conc.ReservedConcurrentExecutions)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Message Queue Enhancement Tests
// ---------------------------------------------------------------------------

func TestBatchSendMessages(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    mqdriver.MessageQueue
	}{
		{"AWS", awsP.SQS},
		{"Azure", azureP.ServiceBus},
		{"GCP", gcpP.PubSub},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			q, err := p.d.CreateQueue(ctx, mqdriver.QueueConfig{Name: "batch-send-q"})
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}

			result, err := p.d.SendMessageBatch(ctx, q.URL, []mqdriver.BatchSendEntry{
				{ID: "1", Body: "message one"},
				{ID: "2", Body: "message two"},
				{ID: "3", Body: "message three"},
			})
			if err != nil {
				t.Fatalf("SendMessageBatch: %v", err)
			}
			if len(result.Successful) != 3 {
				t.Errorf("expected 3 successful, got %d", len(result.Successful))
			}

			// Receive all messages
			var received []mqdriver.Message
			msgs, err := p.d.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{
				QueueURL:    q.URL,
				MaxMessages: 10,
			})
			if err != nil {
				t.Fatalf("ReceiveMessages: %v", err)
			}
			received = append(received, msgs...)
			if len(received) < 3 {
				t.Errorf("expected at least 3 messages, got %d", len(received))
			}
		})
	}
}

func TestBatchDeleteMessages(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    mqdriver.MessageQueue
	}{
		{"AWS", awsP.SQS},
		{"Azure", azureP.ServiceBus},
		{"GCP", gcpP.PubSub},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			q, err := p.d.CreateQueue(ctx, mqdriver.QueueConfig{Name: "batch-del-q"})
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}

			// Send messages
			for i := 0; i < 3; i++ {
				_, err = p.d.SendMessage(ctx, mqdriver.SendMessageInput{
					QueueURL: q.URL,
					Body:     "msg",
				})
				if err != nil {
					t.Fatalf("SendMessage %d: %v", i, err)
				}
			}

			// Receive
			msgs, err := p.d.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{
				QueueURL:    q.URL,
				MaxMessages: 10,
			})
			if err != nil {
				t.Fatalf("ReceiveMessages: %v", err)
			}

			entries := make([]mqdriver.BatchDeleteEntry, len(msgs))
			for i, m := range msgs {
				entries[i] = mqdriver.BatchDeleteEntry{
					ID:            m.MessageID,
					ReceiptHandle: m.ReceiptHandle,
				}
			}

			delResult, err := p.d.DeleteMessageBatch(ctx, q.URL, entries)
			if err != nil {
				t.Fatalf("DeleteMessageBatch: %v", err)
			}
			if len(delResult.Successful) != len(msgs) {
				t.Errorf("expected %d successful deletes, got %d", len(msgs), len(delResult.Successful))
			}

			// Verify empty
			remaining, err := p.d.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{
				QueueURL:    q.URL,
				MaxMessages: 10,
			})
			if err != nil {
				t.Fatalf("ReceiveMessages after delete: %v", err)
			}
			if len(remaining) != 0 {
				t.Errorf("expected 0 messages after batch delete, got %d", len(remaining))
			}
		})
	}
}

func TestQueueAttributes(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    mqdriver.MessageQueue
	}{
		{"AWS", awsP.SQS},
		{"Azure", azureP.ServiceBus},
		{"GCP", gcpP.PubSub},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			q, err := p.d.CreateQueue(ctx, mqdriver.QueueConfig{
				Name:              "attr-q",
				VisibilityTimeout: 30,
			})
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}

			attrs, err := p.d.GetQueueAttributes(ctx, q.URL)
			if err != nil {
				t.Fatalf("GetQueueAttributes: %v", err)
			}
			if attrs.VisibilityTimeout != 30 {
				t.Errorf("expected visibility timeout 30, got %d", attrs.VisibilityTimeout)
			}

			// Set attributes
			err = p.d.SetQueueAttributes(ctx, q.URL, map[string]int{
				"VisibilityTimeout": 60,
			})
			if err != nil {
				t.Fatalf("SetQueueAttributes: %v", err)
			}

			attrs, err = p.d.GetQueueAttributes(ctx, q.URL)
			if err != nil {
				t.Fatalf("GetQueueAttributes after set: %v", err)
			}
			if attrs.VisibilityTimeout != 60 {
				t.Errorf("expected visibility timeout 60, got %d", attrs.VisibilityTimeout)
			}
		})
	}
}

func TestPurgeQueue(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    mqdriver.MessageQueue
	}{
		{"AWS", awsP.SQS},
		{"Azure", azureP.ServiceBus},
		{"GCP", gcpP.PubSub},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			q, err := p.d.CreateQueue(ctx, mqdriver.QueueConfig{Name: "purge-q"})
			if err != nil {
				t.Fatalf("CreateQueue: %v", err)
			}

			// Send messages
			for i := 0; i < 5; i++ {
				_, err = p.d.SendMessage(ctx, mqdriver.SendMessageInput{
					QueueURL: q.URL,
					Body:     "to-purge",
				})
				if err != nil {
					t.Fatalf("SendMessage: %v", err)
				}
			}

			// Purge
			err = p.d.PurgeQueue(ctx, q.URL)
			if err != nil {
				t.Fatalf("PurgeQueue: %v", err)
			}

			// Verify empty
			msgs, err := p.d.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{
				QueueURL:    q.URL,
				MaxMessages: 10,
			})
			if err != nil {
				t.Fatalf("ReceiveMessages after purge: %v", err)
			}
			if len(msgs) != 0 {
				t.Errorf("expected 0 messages after purge, got %d", len(msgs))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Networking Enhancement Tests
// ---------------------------------------------------------------------------

func TestVPCPeering(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    netdriver.Networking
	}{
		{"AWS", awsP.VPC},
		{"Azure", azureP.VNet},
		{"GCP", gcpP.VPC},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			vpc1, err := p.d.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
			if err != nil {
				t.Fatalf("CreateVPC 1: %v", err)
			}
			vpc2, err := p.d.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.1.0.0/16"})
			if err != nil {
				t.Fatalf("CreateVPC 2: %v", err)
			}

			// Create peering
			peering, err := p.d.CreatePeeringConnection(ctx, netdriver.PeeringConfig{
				RequesterVPC: vpc1.ID,
				AccepterVPC:  vpc2.ID,
				Tags:         map[string]string{"env": "test"},
			})
			if err != nil {
				t.Fatalf("CreatePeeringConnection: %v", err)
			}
			if peering.Status != "pending-acceptance" {
				t.Errorf("expected pending-acceptance, got %s", peering.Status)
			}

			// Accept
			err = p.d.AcceptPeeringConnection(ctx, peering.ID)
			if err != nil {
				t.Fatalf("AcceptPeeringConnection: %v", err)
			}

			// Describe
			peers, err := p.d.DescribePeeringConnections(ctx, []string{peering.ID})
			if err != nil {
				t.Fatalf("DescribePeeringConnections: %v", err)
			}
			if len(peers) != 1 {
				t.Fatalf("expected 1 peering, got %d", len(peers))
			}
			if peers[0].Status != "active" {
				t.Errorf("expected active, got %s", peers[0].Status)
			}

			// Delete
			err = p.d.DeletePeeringConnection(ctx, peering.ID)
			if err != nil {
				t.Fatalf("DeletePeeringConnection: %v", err)
			}
		})
	}
}

func TestNATGateway(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    netdriver.Networking
	}{
		{"AWS", awsP.VPC},
		{"Azure", azureP.VNet},
		{"GCP", gcpP.VPC},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			vpcInfo, err := p.d.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
			if err != nil {
				t.Fatalf("CreateVPC: %v", err)
			}

			subnet, err := p.d.CreateSubnet(ctx, netdriver.SubnetConfig{
				VPCID:     vpcInfo.ID,
				CIDRBlock: "10.0.1.0/24",
			})
			if err != nil {
				t.Fatalf("CreateSubnet: %v", err)
			}

			nat, err := p.d.CreateNATGateway(ctx, netdriver.NATGatewayConfig{
				SubnetID: subnet.ID,
				Tags:     map[string]string{"env": "test"},
			})
			if err != nil {
				t.Fatalf("CreateNATGateway: %v", err)
			}
			if nat.SubnetID != subnet.ID {
				t.Errorf("expected subnet %s, got %s", subnet.ID, nat.SubnetID)
			}

			// Describe
			nats, err := p.d.DescribeNATGateways(ctx, []string{nat.ID})
			if err != nil {
				t.Fatalf("DescribeNATGateways: %v", err)
			}
			if len(nats) != 1 {
				t.Fatalf("expected 1 NAT gateway, got %d", len(nats))
			}

			// Delete
			err = p.d.DeleteNATGateway(ctx, nat.ID)
			if err != nil {
				t.Fatalf("DeleteNATGateway: %v", err)
			}
		})
	}
}

func TestFlowLogs(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    netdriver.Networking
	}{
		{"AWS", awsP.VPC},
		{"Azure", azureP.VNet},
		{"GCP", gcpP.VPC},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			vpcInfo, err := p.d.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
			if err != nil {
				t.Fatalf("CreateVPC: %v", err)
			}

			fl, err := p.d.CreateFlowLog(ctx, netdriver.FlowLogConfig{
				ResourceID:   vpcInfo.ID,
				ResourceType: "VPC",
				TrafficType:  "ALL",
				Tags:         map[string]string{"env": "test"},
			})
			if err != nil {
				t.Fatalf("CreateFlowLog: %v", err)
			}
			if fl.Status != "ACTIVE" {
				t.Errorf("expected ACTIVE, got %s", fl.Status)
			}

			// Describe
			fls, err := p.d.DescribeFlowLogs(ctx, []string{fl.ID})
			if err != nil {
				t.Fatalf("DescribeFlowLogs: %v", err)
			}
			if len(fls) != 1 {
				t.Fatalf("expected 1 flow log, got %d", len(fls))
			}

			// GetFlowLogRecords
			records, err := p.d.GetFlowLogRecords(ctx, fl.ID, 10)
			if err != nil {
				t.Fatalf("GetFlowLogRecords: %v", err)
			}
			// May be empty initially, that's ok
			_ = records

			// Delete
			err = p.d.DeleteFlowLog(ctx, fl.ID)
			if err != nil {
				t.Fatalf("DeleteFlowLog: %v", err)
			}
		})
	}
}

func TestRouteTables(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    netdriver.Networking
	}{
		{"AWS", awsP.VPC},
		{"Azure", azureP.VNet},
		{"GCP", gcpP.VPC},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			vpcInfo, err := p.d.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
			if err != nil {
				t.Fatalf("CreateVPC: %v", err)
			}

			rt, err := p.d.CreateRouteTable(ctx, netdriver.RouteTableConfig{
				VPCID: vpcInfo.ID,
				Tags:  map[string]string{"env": "test"},
			})
			if err != nil {
				t.Fatalf("CreateRouteTable: %v", err)
			}
			if rt.VPCID != vpcInfo.ID {
				t.Errorf("expected VPC %s, got %s", vpcInfo.ID, rt.VPCID)
			}

			// Create route
			err = p.d.CreateRoute(ctx, rt.ID, "0.0.0.0/0", "igw-12345", "gateway")
			if err != nil {
				t.Fatalf("CreateRoute: %v", err)
			}

			// Describe
			rts, err := p.d.DescribeRouteTables(ctx, []string{rt.ID})
			if err != nil {
				t.Fatalf("DescribeRouteTables: %v", err)
			}
			if len(rts) != 1 {
				t.Fatalf("expected 1 route table, got %d", len(rts))
			}
			if len(rts[0].Routes) < 1 {
				t.Errorf("expected at least 1 route, got %d", len(rts[0].Routes))
			}

			// Delete route
			err = p.d.DeleteRoute(ctx, rt.ID, "0.0.0.0/0")
			if err != nil {
				t.Fatalf("DeleteRoute: %v", err)
			}

			// Delete route table
			err = p.d.DeleteRouteTable(ctx, rt.ID)
			if err != nil {
				t.Fatalf("DeleteRouteTable: %v", err)
			}
		})
	}
}

func TestNetworkACLs(t *testing.T) {
	ctx := context.Background()
	awsP := NewAWS()
	azureP := NewAzure()
	gcpP := NewGCP()

	providers := []struct {
		name string
		d    netdriver.Networking
	}{
		{"AWS", awsP.VPC},
		{"Azure", azureP.VNet},
		{"GCP", gcpP.VPC},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			vpcInfo, err := p.d.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
			if err != nil {
				t.Fatalf("CreateVPC: %v", err)
			}

			acl, err := p.d.CreateNetworkACL(ctx, vpcInfo.ID, map[string]string{"env": "test"})
			if err != nil {
				t.Fatalf("CreateNetworkACL: %v", err)
			}
			if acl.VPCID != vpcInfo.ID {
				t.Errorf("expected VPC %s, got %s", vpcInfo.ID, acl.VPCID)
			}

			// Add rule
			err = p.d.AddNetworkACLRule(ctx, acl.ID, &netdriver.NetworkACLRule{
				RuleNumber: 100,
				Protocol:   "tcp",
				Action:     "allow",
				CIDR:       "0.0.0.0/0",
				FromPort:   80,
				ToPort:     80,
				Egress:     false,
			})
			if err != nil {
				t.Fatalf("AddNetworkACLRule: %v", err)
			}

			// Describe
			acls, err := p.d.DescribeNetworkACLs(ctx, []string{acl.ID})
			if err != nil {
				t.Fatalf("DescribeNetworkACLs: %v", err)
			}
			if len(acls) != 1 {
				t.Fatalf("expected 1 ACL, got %d", len(acls))
			}
			if len(acls[0].Rules) < 1 {
				t.Errorf("expected at least 1 rule, got %d", len(acls[0].Rules))
			}

			// Remove rule
			err = p.d.RemoveNetworkACLRule(ctx, acl.ID, 100, false)
			if err != nil {
				t.Fatalf("RemoveNetworkACLRule: %v", err)
			}

			// Delete ACL
			err = p.d.DeleteNetworkACL(ctx, acl.ID)
			if err != nil {
				t.Fatalf("DeleteNetworkACL: %v", err)
			}
		})
	}
}

// helperGetMetric is a test helper that queries a monitoring service for a single metric.
func helperGetMetric(
	t *testing.T,
	ctx context.Context,
	mon mondriver.Monitoring,
	clk *config.FakeClock,
	namespace, metricName string,
	dims map[string]string,
) float64 {
	t.Helper()

	result, err := mon.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace:  namespace,
		MetricName: metricName,
		Dimensions: dims,
		StartTime:  clk.Now().Add(-time.Minute),
		EndTime:    clk.Now().Add(time.Minute),
		Period:     60,
		Stat:       "Sum",
	})
	if err != nil {
		t.Fatalf("GetMetricData(%s/%s): %v", namespace, metricName, err)
	}

	if len(result.Values) == 0 {
		t.Fatalf("expected metric %s/%s to have values, got none", namespace, metricName)
	}

	return result.Values[0]
}

func TestAWSMetricsEmission(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Now())
	p := NewAWS(config.WithClock(clk))
	mon := p.CloudWatch

	t.Run("S3", func(t *testing.T) {
		if err := p.S3.CreateBucket(ctx, "m-bucket"); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}

		if err := p.S3.PutObject(ctx, "m-bucket", "f.txt", []byte("hello"), "text/plain", nil); err != nil {
			t.Fatalf("PutObject: %v", err)
		}

		dims := map[string]string{"BucketName": "m-bucket"}
		ns := "AWS/S3"

		v := helperGetMetric(t, ctx, mon, clk, ns, "PutRequests", dims)
		if v != 1.0 {
			t.Errorf("PutRequests: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "BytesUploaded", dims)
		if v != 5.0 {
			t.Errorf("BytesUploaded: expected 5, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "AllRequests", dims)
		if v != 1.0 {
			t.Errorf("AllRequests: expected 1, got %v", v)
		}

		_, err := p.S3.GetObject(ctx, "m-bucket", "f.txt")
		if err != nil {
			t.Fatalf("GetObject: %v", err)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "GetRequests", dims)
		if v != 1.0 {
			t.Errorf("GetRequests: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "BytesDownloaded", dims)
		if v != 5.0 {
			t.Errorf("BytesDownloaded: expected 5, got %v", v)
		}
	})

	t.Run("DynamoDB", func(t *testing.T) {
		if err := p.DynamoDB.CreateTable(ctx, driver.TableConfig{
			Name: "m-tbl", PartitionKey: "pk",
		}); err != nil {
			t.Fatalf("CreateTable: %v", err)
		}

		if err := p.DynamoDB.PutItem(ctx, "m-tbl", map[string]any{"pk": "k1", "val": "v1"}); err != nil {
			t.Fatalf("PutItem: %v", err)
		}

		dims := map[string]string{"TableName": "m-tbl"}
		ns := "AWS/DynamoDB"

		v := helperGetMetric(t, ctx, mon, clk, ns, "ConsumedWriteCapacityUnits", dims)
		if v != 1.0 {
			t.Errorf("ConsumedWriteCapacityUnits: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "SuccessfulRequestCount", dims)
		if v < 1.0 {
			t.Errorf("SuccessfulRequestCount: expected >=1, got %v", v)
		}

		_, err := p.DynamoDB.GetItem(ctx, "m-tbl", map[string]any{"pk": "k1"})
		if err != nil {
			t.Fatalf("GetItem: %v", err)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "ConsumedReadCapacityUnits", dims)
		if v != 1.0 {
			t.Errorf("ConsumedReadCapacityUnits: expected 1, got %v", v)
		}
	})

	t.Run("Lambda", func(t *testing.T) {
		_, err := p.Lambda.CreateFunction(ctx, serverlessdriver.FunctionConfig{
			Name: "m-fn", Runtime: "go1.x", Handler: "main",
		})
		if err != nil {
			t.Fatalf("CreateFunction: %v", err)
		}

		p.Lambda.RegisterHandler("m-fn", func(_ context.Context, payload []byte) ([]byte, error) {
			return []byte("ok"), nil
		})

		_, err = p.Lambda.Invoke(ctx, serverlessdriver.InvokeInput{
			FunctionName: "m-fn", Payload: []byte("{}"),
		})
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}

		dims := map[string]string{"FunctionName": "m-fn"}
		ns := "AWS/Lambda"

		v := helperGetMetric(t, ctx, mon, clk, ns, "Invocations", dims)
		if v != 1.0 {
			t.Errorf("Invocations: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "Duration", dims)
		if v != 1.0 {
			t.Errorf("Duration: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "ConcurrentExecutions", dims)
		if v != 1.0 {
			t.Errorf("ConcurrentExecutions: expected 1, got %v", v)
		}
	})

	t.Run("SQS", func(t *testing.T) {
		qi, err := p.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "m-q"})
		if err != nil {
			t.Fatalf("CreateQueue: %v", err)
		}

		_, err = p.SQS.SendMessage(ctx, mqdriver.SendMessageInput{
			QueueURL: qi.URL, Body: "hello-sqs",
		})
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}

		dims := map[string]string{"QueueName": "m-q"}
		ns := "AWS/SQS"

		v := helperGetMetric(t, ctx, mon, clk, ns, "NumberOfMessagesSent", dims)
		if v != 1.0 {
			t.Errorf("NumberOfMessagesSent: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "SentMessageSize", dims)
		if v != 9.0 {
			t.Errorf("SentMessageSize: expected 9, got %v", v)
		}
	})

	t.Run("ElastiCache", func(t *testing.T) {
		_, err := p.ElastiCache.CreateCache(ctx, cachedriver.CacheConfig{
			Name: "m-cache", Engine: "redis", NodeType: "cache.t2.micro",
		})
		if err != nil {
			t.Fatalf("CreateCache: %v", err)
		}

		if err := p.ElastiCache.Set(ctx, "m-cache", "key1", []byte("val1"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}

		_, err = p.ElastiCache.Get(ctx, "m-cache", "key1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}

		dims := map[string]string{"CacheClusterId": "m-cache"}
		ns := "AWS/ElastiCache"

		v := helperGetMetric(t, ctx, mon, clk, ns, "SetCommands", dims)
		if v != 1.0 {
			t.Errorf("SetCommands: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "CacheHits", dims)
		if v != 1.0 {
			t.Errorf("CacheHits: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "GetCommands", dims)
		if v != 1.0 {
			t.Errorf("GetCommands: expected 1, got %v", v)
		}

		// Get a missing key to trigger CacheMisses
		_, _ = p.ElastiCache.Get(ctx, "m-cache", "no-key")

		v = helperGetMetric(t, ctx, mon, clk, ns, "CacheMisses", dims)
		if v < 1.0 {
			t.Errorf("CacheMisses: expected >=1, got %v", v)
		}
	})

	t.Run("CloudWatchLogs", func(t *testing.T) {
		_, err := p.CloudWatchLogs.CreateLogGroup(ctx, loggingdriver.LogGroupConfig{Name: "m-lg"})
		if err != nil {
			t.Fatalf("CreateLogGroup: %v", err)
		}

		_, err = p.CloudWatchLogs.CreateLogStream(ctx, "m-lg", "m-stream")
		if err != nil {
			t.Fatalf("CreateLogStream: %v", err)
		}

		err = p.CloudWatchLogs.PutLogEvents(ctx, "m-lg", "m-stream", []loggingdriver.LogEvent{
			{Timestamp: clk.Now(), Message: "test log message"},
		})
		if err != nil {
			t.Fatalf("PutLogEvents: %v", err)
		}

		dims := map[string]string{"LogGroupName": "m-lg"}
		ns := "AWS/Logs"

		v := helperGetMetric(t, ctx, mon, clk, ns, "IncomingLogEvents", dims)
		if v != 1.0 {
			t.Errorf("IncomingLogEvents: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "IncomingBytes", dims)
		if v != 16.0 {
			t.Errorf("IncomingBytes: expected 16, got %v", v)
		}
	})

	t.Run("SNS", func(t *testing.T) {
		_, err := p.SNS.CreateTopic(ctx, notifdriver.TopicConfig{Name: "m-topic"})
		if err != nil {
			t.Fatalf("CreateTopic: %v", err)
		}

		_, err = p.SNS.Publish(ctx, notifdriver.PublishInput{
			TopicID: "m-topic", Message: "hello-sns",
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}

		dims := map[string]string{"TopicName": "m-topic"}
		ns := "AWS/SNS"

		v := helperGetMetric(t, ctx, mon, clk, ns, "NumberOfMessagesPublished", dims)
		if v != 1.0 {
			t.Errorf("NumberOfMessagesPublished: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "PublishSize", dims)
		if v != 9.0 {
			t.Errorf("PublishSize: expected 9, got %v", v)
		}
	})

	t.Run("ECR", func(t *testing.T) {
		_, err := p.ECR.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "m-repo"})
		if err != nil {
			t.Fatalf("CreateRepository: %v", err)
		}

		_, err = p.ECR.PutImage(ctx, &crdriver.ImageManifest{
			Repository: "m-repo", Tag: "latest", Digest: "sha256:abc123",
			MediaType: "application/vnd.docker.distribution.manifest.v2+json",
			SizeBytes: 1024,
		})
		if err != nil {
			t.Fatalf("PutImage: %v", err)
		}

		_, err = p.ECR.GetImage(ctx, "m-repo", "latest")
		if err != nil {
			t.Fatalf("GetImage: %v", err)
		}

		dims := map[string]string{"RepositoryName": "m-repo"}
		ns := "AWS/ECR"

		v := helperGetMetric(t, ctx, mon, clk, ns, "ImagePushCount", dims)
		if v != 1.0 {
			t.Errorf("ImagePushCount: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "ImagePullCount", dims)
		if v != 1.0 {
			t.Errorf("ImagePullCount: expected 1, got %v", v)
		}
	})

	t.Run("EventBridge", func(t *testing.T) {
		_, err := p.EventBridge.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "m-bus"})
		if err != nil {
			t.Fatalf("CreateEventBus: %v", err)
		}

		_, err = p.EventBridge.PutRule(ctx, &ebdriver.RuleConfig{
			Name:         "m-rule",
			EventBus:     "m-bus",
			EventPattern: `{"source":["my.app"]}`,
			State:        "ENABLED",
		})
		if err != nil {
			t.Fatalf("PutRule: %v", err)
		}

		_, err = p.EventBridge.PutEvents(ctx, []ebdriver.Event{
			{Source: "my.app", DetailType: "test", Detail: `{"key":"val"}`, EventBus: "m-bus"},
		})
		if err != nil {
			t.Fatalf("PutEvents: %v", err)
		}

		dims := map[string]string{"EventBusName": "m-bus"}
		ns := "AWS/Events"

		v := helperGetMetric(t, ctx, mon, clk, ns, "PutEventsRequestCount", dims)
		if v != 1.0 {
			t.Errorf("PutEventsRequestCount: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "MatchedEvents", dims)
		if v != 1.0 {
			t.Errorf("MatchedEvents: expected 1, got %v", v)
		}
	})
}

func TestAzureMetricsEmission(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Now())
	p := NewAzure(config.WithClock(clk))
	mon := p.Monitor

	t.Run("BlobStorage", func(t *testing.T) {
		if err := p.BlobStorage.CreateBucket(ctx, "m-container"); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}

		if err := p.BlobStorage.PutObject(ctx, "m-container", "f.txt", []byte("hello"), "text/plain", nil); err != nil {
			t.Fatalf("PutObject: %v", err)
		}

		dims := map[string]string{"containerName": "m-container"}
		ns := "Microsoft.Storage/storageAccounts"

		v := helperGetMetric(t, ctx, mon, clk, ns, "Transactions", dims)
		if v != 1.0 {
			t.Errorf("Transactions: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "Ingress", dims)
		if v != 5.0 {
			t.Errorf("Ingress: expected 5, got %v", v)
		}

		_, err := p.BlobStorage.GetObject(ctx, "m-container", "f.txt")
		if err != nil {
			t.Fatalf("GetObject: %v", err)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "Egress", dims)
		if v != 5.0 {
			t.Errorf("Egress: expected 5, got %v", v)
		}
	})

	t.Run("CosmosDB", func(t *testing.T) {
		if err := p.CosmosDB.CreateTable(ctx, driver.TableConfig{
			Name: "m-coll", PartitionKey: "pk",
		}); err != nil {
			t.Fatalf("CreateTable: %v", err)
		}

		if err := p.CosmosDB.PutItem(ctx, "m-coll", map[string]any{"pk": "k1", "val": "v1"}); err != nil {
			t.Fatalf("PutItem: %v", err)
		}

		dims := map[string]string{"containerName": "m-coll"}
		ns := "Microsoft.DocumentDB/databaseAccounts"

		v := helperGetMetric(t, ctx, mon, clk, ns, "TotalRequests", dims)
		if v < 1.0 {
			t.Errorf("TotalRequests: expected >=1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "TotalRequestUnits", dims)
		if v < 1.0 {
			t.Errorf("TotalRequestUnits: expected >=1, got %v", v)
		}
	})

	t.Run("Functions", func(t *testing.T) {
		_, err := p.Functions.CreateFunction(ctx, serverlessdriver.FunctionConfig{
			Name: "m-fn", Runtime: "dotnet6", Handler: "main",
		})
		if err != nil {
			t.Fatalf("CreateFunction: %v", err)
		}

		p.Functions.RegisterHandler("m-fn", func(_ context.Context, payload []byte) ([]byte, error) {
			return []byte("ok"), nil
		})

		_, err = p.Functions.Invoke(ctx, serverlessdriver.InvokeInput{
			FunctionName: "m-fn", Payload: []byte("{}"),
		})
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}

		dims := map[string]string{"functionName": "m-fn"}
		ns := "Microsoft.Web/sites"

		v := helperGetMetric(t, ctx, mon, clk, ns, "FunctionExecutionCount", dims)
		if v != 1.0 {
			t.Errorf("FunctionExecutionCount: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "FunctionExecutionUnits", dims)
		if v != 1.0 {
			t.Errorf("FunctionExecutionUnits: expected 1, got %v", v)
		}
	})

	t.Run("ServiceBus", func(t *testing.T) {
		qi, err := p.ServiceBus.CreateQueue(ctx, mqdriver.QueueConfig{Name: "m-q"})
		if err != nil {
			t.Fatalf("CreateQueue: %v", err)
		}

		_, err = p.ServiceBus.SendMessage(ctx, mqdriver.SendMessageInput{
			QueueURL: qi.URL, Body: "hello-bus",
		})
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}

		dims := map[string]string{"queueName": "m-q"}
		ns := "Microsoft.ServiceBus/namespaces"

		v := helperGetMetric(t, ctx, mon, clk, ns, "IncomingMessages", dims)
		if v != 1.0 {
			t.Errorf("IncomingMessages: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "Size", dims)
		if v != 9.0 {
			t.Errorf("Size: expected 9, got %v", v)
		}
	})

	t.Run("AzureCache", func(t *testing.T) {
		_, err := p.Cache.CreateCache(ctx, cachedriver.CacheConfig{
			Name: "m-cache", Engine: "redis", NodeType: "Standard_C1",
		})
		if err != nil {
			t.Fatalf("CreateCache: %v", err)
		}

		if err := p.Cache.Set(ctx, "m-cache", "key1", []byte("val1"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}

		_, err = p.Cache.Get(ctx, "m-cache", "key1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}

		dims := map[string]string{"cacheName": "m-cache"}
		ns := "Microsoft.Cache/redis"

		v := helperGetMetric(t, ctx, mon, clk, ns, "SetCommands", dims)
		if v != 1.0 {
			t.Errorf("SetCommands: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "CacheHits", dims)
		if v != 1.0 {
			t.Errorf("CacheHits: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "TotalCommandsProcessed", dims)
		if v >= 2.0 {
			// Set + Get should yield at least 2
		} else {
			t.Errorf("TotalCommandsProcessed: expected >=2, got %v", v)
		}

		_, _ = p.Cache.Get(ctx, "m-cache", "no-key")

		v = helperGetMetric(t, ctx, mon, clk, ns, "CacheMisses", dims)
		if v < 1.0 {
			t.Errorf("CacheMisses: expected >=1, got %v", v)
		}
	})

	t.Run("LogAnalytics", func(t *testing.T) {
		_, err := p.LogAnalytics.CreateLogGroup(ctx, loggingdriver.LogGroupConfig{Name: "m-lg"})
		if err != nil {
			t.Fatalf("CreateLogGroup: %v", err)
		}

		_, err = p.LogAnalytics.CreateLogStream(ctx, "m-lg", "m-stream")
		if err != nil {
			t.Fatalf("CreateLogStream: %v", err)
		}

		err = p.LogAnalytics.PutLogEvents(ctx, "m-lg", "m-stream", []loggingdriver.LogEvent{
			{Timestamp: clk.Now(), Message: "azure log msg"},
		})
		if err != nil {
			t.Fatalf("PutLogEvents: %v", err)
		}

		dims := map[string]string{"logGroupName": "m-lg"}
		ns := "Microsoft.OperationalInsights/workspaces"

		v := helperGetMetric(t, ctx, mon, clk, ns, "IngestedEvents", dims)
		if v != 1.0 {
			t.Errorf("IngestedEvents: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "IngestedBytes", dims)
		if v != 13.0 {
			t.Errorf("IngestedBytes: expected 13, got %v", v)
		}
	})

	t.Run("NotificationHubs", func(t *testing.T) {
		_, err := p.NotificationHubs.CreateTopic(ctx, notifdriver.TopicConfig{Name: "m-topic"})
		if err != nil {
			t.Fatalf("CreateTopic: %v", err)
		}

		_, err = p.NotificationHubs.Publish(ctx, notifdriver.PublishInput{
			TopicID: "m-topic", Message: "hello-nh",
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}

		dims := map[string]string{"topicName": "m-topic"}
		ns := "Microsoft.NotificationHubs/namespaces"

		v := helperGetMetric(t, ctx, mon, clk, ns, "OutgoingNotifications", dims)
		if v != 1.0 {
			t.Errorf("OutgoingNotifications: expected 1, got %v", v)
		}
	})

	t.Run("ACR", func(t *testing.T) {
		_, err := p.ACR.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "m-repo"})
		if err != nil {
			t.Fatalf("CreateRepository: %v", err)
		}

		_, err = p.ACR.PutImage(ctx, &crdriver.ImageManifest{
			Repository: "m-repo", Tag: "latest", Digest: "sha256:def456",
			MediaType: "application/vnd.docker.distribution.manifest.v2+json",
			SizeBytes: 2048,
		})
		if err != nil {
			t.Fatalf("PutImage: %v", err)
		}

		_, err = p.ACR.GetImage(ctx, "m-repo", "latest")
		if err != nil {
			t.Fatalf("GetImage: %v", err)
		}

		dims := map[string]string{"repositoryName": "m-repo"}
		ns := "Microsoft.ContainerRegistry/registries"

		v := helperGetMetric(t, ctx, mon, clk, ns, "ImagePushCount", dims)
		if v != 1.0 {
			t.Errorf("ImagePushCount: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "ImagePullCount", dims)
		if v != 1.0 {
			t.Errorf("ImagePullCount: expected 1, got %v", v)
		}
	})

	t.Run("EventGrid", func(t *testing.T) {
		_, err := p.EventGrid.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "m-topic"})
		if err != nil {
			t.Fatalf("CreateEventBus: %v", err)
		}

		_, err = p.EventGrid.PutRule(ctx, &ebdriver.RuleConfig{
			Name:         "m-rule",
			EventBus:     "m-topic",
			EventPattern: `{"source":["my.app"]}`,
			State:        "ENABLED",
		})
		if err != nil {
			t.Fatalf("PutRule: %v", err)
		}

		_, err = p.EventGrid.PutEvents(ctx, []ebdriver.Event{
			{Source: "my.app", DetailType: "test", Detail: `{"k":"v"}`, EventBus: "m-topic"},
		})
		if err != nil {
			t.Fatalf("PutEvents: %v", err)
		}

		dims := map[string]string{"topicName": "m-topic"}
		ns := "Microsoft.EventGrid/topics"

		v := helperGetMetric(t, ctx, mon, clk, ns, "PublishedEvents", dims)
		if v != 1.0 {
			t.Errorf("PublishedEvents: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "MatchedEvents", dims)
		if v != 1.0 {
			t.Errorf("MatchedEvents: expected 1, got %v", v)
		}
	})
}

func TestGCPMetricsEmission(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Now())
	p := NewGCP(config.WithClock(clk))
	mon := p.CloudMonitoring

	t.Run("GCS", func(t *testing.T) {
		if err := p.GCS.CreateBucket(ctx, "m-bucket"); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}

		if err := p.GCS.PutObject(ctx, "m-bucket", "f.txt", []byte("hello"), "text/plain", nil); err != nil {
			t.Fatalf("PutObject: %v", err)
		}

		dims := map[string]string{"bucket_name": "m-bucket"}
		ns := "storage.googleapis.com"

		v := helperGetMetric(t, ctx, mon, clk, ns, "api/request_count", dims)
		if v != 1.0 {
			t.Errorf("api/request_count: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "network/received_bytes_count", dims)
		if v != 5.0 {
			t.Errorf("network/received_bytes_count: expected 5, got %v", v)
		}

		_, err := p.GCS.GetObject(ctx, "m-bucket", "f.txt")
		if err != nil {
			t.Fatalf("GetObject: %v", err)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "network/sent_bytes_count", dims)
		if v != 5.0 {
			t.Errorf("network/sent_bytes_count: expected 5, got %v", v)
		}
	})

	t.Run("Firestore", func(t *testing.T) {
		if err := p.Firestore.CreateTable(ctx, driver.TableConfig{
			Name: "m-coll", PartitionKey: "pk",
		}); err != nil {
			t.Fatalf("CreateTable: %v", err)
		}

		if err := p.Firestore.PutItem(ctx, "m-coll", map[string]any{"pk": "k1", "val": "v1"}); err != nil {
			t.Fatalf("PutItem: %v", err)
		}

		dims := map[string]string{"collection_id": "m-coll"}
		ns := "firestore.googleapis.com"

		v := helperGetMetric(t, ctx, mon, clk, ns, "document/write_count", dims)
		if v != 1.0 {
			t.Errorf("document/write_count: expected 1, got %v", v)
		}

		_, err := p.Firestore.GetItem(ctx, "m-coll", map[string]any{"pk": "k1"})
		if err != nil {
			t.Fatalf("GetItem: %v", err)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "document/read_count", dims)
		if v != 1.0 {
			t.Errorf("document/read_count: expected 1, got %v", v)
		}

		if err := p.Firestore.DeleteItem(ctx, "m-coll", map[string]any{"pk": "k1"}); err != nil {
			t.Fatalf("DeleteItem: %v", err)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "document/delete_count", dims)
		if v != 1.0 {
			t.Errorf("document/delete_count: expected 1, got %v", v)
		}
	})

	t.Run("CloudFunctions", func(t *testing.T) {
		_, err := p.CloudFunctions.CreateFunction(ctx, serverlessdriver.FunctionConfig{
			Name: "m-fn", Runtime: "go121", Handler: "main",
		})
		if err != nil {
			t.Fatalf("CreateFunction: %v", err)
		}

		p.CloudFunctions.RegisterHandler("m-fn", func(_ context.Context, payload []byte) ([]byte, error) {
			return []byte("ok"), nil
		})

		_, err = p.CloudFunctions.Invoke(ctx, serverlessdriver.InvokeInput{
			FunctionName: "m-fn", Payload: []byte("{}"),
		})
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}

		dims := map[string]string{"function_name": "m-fn"}
		ns := "cloudfunctions.googleapis.com"

		v := helperGetMetric(t, ctx, mon, clk, ns, "function/execution_count", dims)
		if v != 1.0 {
			t.Errorf("function/execution_count: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "function/execution_times", dims)
		if v != 1.0 {
			t.Errorf("function/execution_times: expected 1, got %v", v)
		}
	})

	t.Run("PubSub", func(t *testing.T) {
		qi, err := p.PubSub.CreateQueue(ctx, mqdriver.QueueConfig{Name: "m-q"})
		if err != nil {
			t.Fatalf("CreateQueue: %v", err)
		}

		_, err = p.PubSub.SendMessage(ctx, mqdriver.SendMessageInput{
			QueueURL: qi.URL, Body: "hello-pub",
		})
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}

		dims := map[string]string{"topic_id": "m-q"}
		ns := "pubsub.googleapis.com"

		v := helperGetMetric(t, ctx, mon, clk, ns, "topic/send_message_operation_count", dims)
		if v != 1.0 {
			t.Errorf("topic/send_message_operation_count: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "topic/byte_cost", dims)
		if v != 9.0 {
			t.Errorf("topic/byte_cost: expected 9, got %v", v)
		}
	})

	t.Run("Memorystore", func(t *testing.T) {
		_, err := p.Memorystore.CreateCache(ctx, cachedriver.CacheConfig{
			Name: "m-cache", Engine: "redis", NodeType: "BASIC",
		})
		if err != nil {
			t.Fatalf("CreateCache: %v", err)
		}

		if err := p.Memorystore.Set(ctx, "m-cache", "key1", []byte("val1"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}

		_, err = p.Memorystore.Get(ctx, "m-cache", "key1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}

		dims := map[string]string{"instance_id": "m-cache"}
		ns := "redis.googleapis.com"

		v := helperGetMetric(t, ctx, mon, clk, ns, "commands/set", dims)
		if v != 1.0 {
			t.Errorf("commands/set: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "stats/cache_hit_count", dims)
		if v != 1.0 {
			t.Errorf("stats/cache_hit_count: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "commands/get", dims)
		if v != 1.0 {
			t.Errorf("commands/get: expected 1, got %v", v)
		}

		_, _ = p.Memorystore.Get(ctx, "m-cache", "no-key")

		v = helperGetMetric(t, ctx, mon, clk, ns, "stats/cache_miss_count", dims)
		if v < 1.0 {
			t.Errorf("stats/cache_miss_count: expected >=1, got %v", v)
		}
	})

	t.Run("CloudLogging", func(t *testing.T) {
		_, err := p.CloudLogging.CreateLogGroup(ctx, loggingdriver.LogGroupConfig{Name: "m-lg"})
		if err != nil {
			t.Fatalf("CreateLogGroup: %v", err)
		}

		_, err = p.CloudLogging.CreateLogStream(ctx, "m-lg", "m-stream")
		if err != nil {
			t.Fatalf("CreateLogStream: %v", err)
		}

		err = p.CloudLogging.PutLogEvents(ctx, "m-lg", "m-stream", []loggingdriver.LogEvent{
			{Timestamp: clk.Now(), Message: "gcp log msg"},
		})
		if err != nil {
			t.Fatalf("PutLogEvents: %v", err)
		}

		dims := map[string]string{"log_group": "m-lg"}
		ns := "logging.googleapis.com"

		v := helperGetMetric(t, ctx, mon, clk, ns, "api/request_count", dims)
		if v != 1.0 {
			t.Errorf("api/request_count: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "byte_count", dims)
		if v != 11.0 {
			t.Errorf("byte_count: expected 11, got %v", v)
		}
	})

	t.Run("FCM", func(t *testing.T) {
		_, err := p.FCM.CreateTopic(ctx, notifdriver.TopicConfig{Name: "m-topic"})
		if err != nil {
			t.Fatalf("CreateTopic: %v", err)
		}

		_, err = p.FCM.Publish(ctx, notifdriver.PublishInput{
			TopicID: "m-topic", Message: "hello-fcm",
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}

		dims := map[string]string{"topic_name": "m-topic"}
		ns := "fcm.googleapis.com"

		v := helperGetMetric(t, ctx, mon, clk, ns, "message_count", dims)
		if v != 1.0 {
			t.Errorf("message_count: expected 1, got %v", v)
		}
	})

	t.Run("ArtifactRegistry", func(t *testing.T) {
		_, err := p.ArtifactRegistry.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "m-repo"})
		if err != nil {
			t.Fatalf("CreateRepository: %v", err)
		}

		_, err = p.ArtifactRegistry.PutImage(ctx, &crdriver.ImageManifest{
			Repository: "m-repo", Tag: "latest", Digest: "sha256:ghi789",
			MediaType: "application/vnd.docker.distribution.manifest.v2+json",
			SizeBytes: 4096,
		})
		if err != nil {
			t.Fatalf("PutImage: %v", err)
		}

		_, err = p.ArtifactRegistry.GetImage(ctx, "m-repo", "latest")
		if err != nil {
			t.Fatalf("GetImage: %v", err)
		}

		dims := map[string]string{"repository_name": "m-repo"}
		ns := "artifactregistry.googleapis.com"

		v := helperGetMetric(t, ctx, mon, clk, ns, "push_request_count", dims)
		if v != 1.0 {
			t.Errorf("push_request_count: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "pull_request_count", dims)
		if v != 1.0 {
			t.Errorf("pull_request_count: expected 1, got %v", v)
		}
	})

	t.Run("Eventarc", func(t *testing.T) {
		_, err := p.Eventarc.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: "m-channel"})
		if err != nil {
			t.Fatalf("CreateEventBus: %v", err)
		}

		_, err = p.Eventarc.PutRule(ctx, &ebdriver.RuleConfig{
			Name:         "m-rule",
			EventBus:     "m-channel",
			EventPattern: `{"source":["my.app"]}`,
			State:        "ENABLED",
		})
		if err != nil {
			t.Fatalf("PutRule: %v", err)
		}

		_, err = p.Eventarc.PutEvents(ctx, []ebdriver.Event{
			{Source: "my.app", DetailType: "test", Detail: `{"k":"v"}`, EventBus: "m-channel"},
		})
		if err != nil {
			t.Fatalf("PutEvents: %v", err)
		}

		dims := map[string]string{"channel_name": "m-channel"}
		ns := "eventarc.googleapis.com"

		v := helperGetMetric(t, ctx, mon, clk, ns, "event_count", dims)
		if v != 1.0 {
			t.Errorf("event_count: expected 1, got %v", v)
		}

		v = helperGetMetric(t, ctx, mon, clk, ns, "matched_event_count", dims)
		if v != 1.0 {
			t.Errorf("matched_event_count: expected 1, got %v", v)
		}
	})
}

func TestUpdateItemAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	if err := p.DynamoDB.CreateTable(ctx, driver.TableConfig{
		Name: "users", PartitionKey: "pk", SortKey: "sk",
	}); err != nil {
		t.Fatal(err)
	}

	// Put initial item
	if err := p.DynamoDB.PutItem(ctx, "users", map[string]any{
		"pk": "user1", "sk": "profile", "name": "Alice", "age": 30, "city": "NYC",
	}); err != nil {
		t.Fatal(err)
	}

	// SET: update name and add new field
	updated, err := p.DynamoDB.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "users",
		Key:   map[string]any{"pk": "user1", "sk": "profile"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "name", Value: "Alice Smith"},
			{Action: "SET", Field: "email", Value: "alice@example.com"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if updated["name"] != "Alice Smith" {
		t.Errorf("expected 'Alice Smith', got %v", updated["name"])
	}

	if updated["email"] != "alice@example.com" {
		t.Errorf("expected 'alice@example.com', got %v", updated["email"])
	}

	if updated["age"] != 30 {
		t.Errorf("expected age 30 preserved, got %v", updated["age"])
	}

	// REMOVE: remove city field
	updated, err = p.DynamoDB.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "users",
		Key:   map[string]any{"pk": "user1", "sk": "profile"},
		Actions: []driver.UpdateAction{
			{Action: "REMOVE", Field: "city"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, hasCityField := updated["city"]; hasCityField {
		t.Error("expected city field to be removed")
	}

	if updated["name"] != "Alice Smith" {
		t.Errorf("expected name preserved as 'Alice Smith', got %v", updated["name"])
	}

	// Verify via GetItem
	got, err := p.DynamoDB.GetItem(ctx, "users", map[string]any{"pk": "user1", "sk": "profile"})
	if err != nil {
		t.Fatal(err)
	}

	if got["name"] != "Alice Smith" {
		t.Errorf("GetItem: expected 'Alice Smith', got %v", got["name"])
	}

	if got["email"] != "alice@example.com" {
		t.Errorf("GetItem: expected 'alice@example.com', got %v", got["email"])
	}

	if _, hasCityField := got["city"]; hasCityField {
		t.Error("GetItem: expected city field to be removed")
	}
}

func TestUpdateItemAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()

	if err := p.CosmosDB.CreateTable(ctx, driver.TableConfig{
		Name: "users", PartitionKey: "pk", SortKey: "sk",
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.CosmosDB.PutItem(ctx, "users", map[string]any{
		"pk": "user1", "sk": "profile", "name": "Alice", "age": 30, "city": "NYC",
	}); err != nil {
		t.Fatal(err)
	}

	// SET fields
	updated, err := p.CosmosDB.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "users",
		Key:   map[string]any{"pk": "user1", "sk": "profile"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "name", Value: "Alice Smith"},
			{Action: "SET", Field: "email", Value: "alice@example.com"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if updated["name"] != "Alice Smith" {
		t.Errorf("expected 'Alice Smith', got %v", updated["name"])
	}

	if updated["email"] != "alice@example.com" {
		t.Errorf("expected 'alice@example.com', got %v", updated["email"])
	}

	if updated["age"] != 30 {
		t.Errorf("expected age 30 preserved, got %v", updated["age"])
	}

	// REMOVE field
	updated, err = p.CosmosDB.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "users",
		Key:   map[string]any{"pk": "user1", "sk": "profile"},
		Actions: []driver.UpdateAction{
			{Action: "REMOVE", Field: "city"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, hasCityField := updated["city"]; hasCityField {
		t.Error("expected city field to be removed")
	}

	// Verify via GetItem
	got, err := p.CosmosDB.GetItem(ctx, "users", map[string]any{"pk": "user1", "sk": "profile"})
	if err != nil {
		t.Fatal(err)
	}

	if got["name"] != "Alice Smith" {
		t.Errorf("GetItem: expected 'Alice Smith', got %v", got["name"])
	}
}

func TestUpdateItemGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()

	if err := p.Firestore.CreateTable(ctx, driver.TableConfig{
		Name: "users", PartitionKey: "pk", SortKey: "sk",
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.Firestore.PutItem(ctx, "users", map[string]any{
		"pk": "user1", "sk": "profile", "name": "Alice", "age": 30, "city": "NYC",
	}); err != nil {
		t.Fatal(err)
	}

	// SET fields
	updated, err := p.Firestore.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "users",
		Key:   map[string]any{"pk": "user1", "sk": "profile"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "name", Value: "Alice Smith"},
			{Action: "SET", Field: "email", Value: "alice@example.com"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if updated["name"] != "Alice Smith" {
		t.Errorf("expected 'Alice Smith', got %v", updated["name"])
	}

	if updated["email"] != "alice@example.com" {
		t.Errorf("expected 'alice@example.com', got %v", updated["email"])
	}

	if updated["age"] != 30 {
		t.Errorf("expected age 30 preserved, got %v", updated["age"])
	}

	// REMOVE field
	updated, err = p.Firestore.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "users",
		Key:   map[string]any{"pk": "user1", "sk": "profile"},
		Actions: []driver.UpdateAction{
			{Action: "REMOVE", Field: "city"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, hasCityField := updated["city"]; hasCityField {
		t.Error("expected city field to be removed")
	}

	// Verify via GetItem
	got, err := p.Firestore.GetItem(ctx, "users", map[string]any{"pk": "user1", "sk": "profile"})
	if err != nil {
		t.Fatal(err)
	}

	if got["name"] != "Alice Smith" {
		t.Errorf("GetItem: expected 'Alice Smith', got %v", got["name"])
	}
}

func TestUpdateItemNotFound(t *testing.T) {
	ctx := context.Background()

	t.Run("AWS", func(t *testing.T) {
		p := NewAWS()

		if err := p.DynamoDB.CreateTable(ctx, driver.TableConfig{
			Name: "t1", PartitionKey: "pk",
		}); err != nil {
			t.Fatal(err)
		}

		_, err := p.DynamoDB.UpdateItem(ctx, driver.UpdateItemInput{
			Table:   "t1",
			Key:     map[string]any{"pk": "missing"},
			Actions: []driver.UpdateAction{{Action: "SET", Field: "x", Value: 1}},
		})
		if !cerrors.IsNotFound(err) {
			t.Errorf("expected NotFound, got %v", err)
		}
	})

	t.Run("Azure", func(t *testing.T) {
		p := NewAzure()

		if err := p.CosmosDB.CreateTable(ctx, driver.TableConfig{
			Name: "t1", PartitionKey: "pk",
		}); err != nil {
			t.Fatal(err)
		}

		_, err := p.CosmosDB.UpdateItem(ctx, driver.UpdateItemInput{
			Table:   "t1",
			Key:     map[string]any{"pk": "missing"},
			Actions: []driver.UpdateAction{{Action: "SET", Field: "x", Value: 1}},
		})
		if !cerrors.IsNotFound(err) {
			t.Errorf("expected NotFound, got %v", err)
		}
	})

	t.Run("GCP", func(t *testing.T) {
		p := NewGCP()

		if err := p.Firestore.CreateTable(ctx, driver.TableConfig{
			Name: "t1", PartitionKey: "pk",
		}); err != nil {
			t.Fatal(err)
		}

		_, err := p.Firestore.UpdateItem(ctx, driver.UpdateItemInput{
			Table:   "t1",
			Key:     map[string]any{"pk": "missing"},
			Actions: []driver.UpdateAction{{Action: "SET", Field: "x", Value: 1}},
		})
		if !cerrors.IsNotFound(err) {
			t.Errorf("expected NotFound, got %v", err)
		}
	})
}

func TestUpdateItemTableNotFound(t *testing.T) {
	ctx := context.Background()

	t.Run("AWS", func(t *testing.T) {
		p := NewAWS()

		_, err := p.DynamoDB.UpdateItem(ctx, driver.UpdateItemInput{
			Table:   "nonexistent",
			Key:     map[string]any{"pk": "x"},
			Actions: []driver.UpdateAction{{Action: "SET", Field: "x", Value: 1}},
		})
		if !cerrors.IsNotFound(err) {
			t.Errorf("expected NotFound, got %v", err)
		}
	})

	t.Run("Azure", func(t *testing.T) {
		p := NewAzure()

		_, err := p.CosmosDB.UpdateItem(ctx, driver.UpdateItemInput{
			Table:   "nonexistent",
			Key:     map[string]any{"pk": "x"},
			Actions: []driver.UpdateAction{{Action: "SET", Field: "x", Value: 1}},
		})
		if !cerrors.IsNotFound(err) {
			t.Errorf("expected NotFound, got %v", err)
		}
	})

	t.Run("GCP", func(t *testing.T) {
		p := NewGCP()

		_, err := p.Firestore.UpdateItem(ctx, driver.UpdateItemInput{
			Table:   "nonexistent",
			Key:     map[string]any{"pk": "x"},
			Actions: []driver.UpdateAction{{Action: "SET", Field: "x", Value: 1}},
		})
		if !cerrors.IsNotFound(err) {
			t.Errorf("expected NotFound, got %v", err)
		}
	})
}

func TestUpdateItemInvalidAction(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	if err := p.DynamoDB.CreateTable(ctx, driver.TableConfig{
		Name: "t1", PartitionKey: "pk",
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.DynamoDB.PutItem(ctx, "t1", map[string]any{"pk": "k1", "v": 1}); err != nil {
		t.Fatal(err)
	}

	_, err := p.DynamoDB.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   "t1",
		Key:     map[string]any{"pk": "k1"},
		Actions: []driver.UpdateAction{{Action: "INVALID", Field: "v", Value: 2}},
	})
	if err == nil {
		t.Error("expected error for invalid action, got nil")
	}
}

func TestUpdateItemWithStreams(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	if err := p.DynamoDB.CreateTable(ctx, driver.TableConfig{
		Name: "t1", PartitionKey: "pk",
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.DynamoDB.UpdateStreamConfig(ctx, "t1", driver.StreamConfig{
		Enabled: true, ViewType: "NEW_AND_OLD_IMAGES",
	}); err != nil {
		t.Fatal(err)
	}

	if err := p.DynamoDB.PutItem(ctx, "t1", map[string]any{"pk": "k1", "val": "old"}); err != nil {
		t.Fatal(err)
	}

	_, err := p.DynamoDB.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   "t1",
		Key:     map[string]any{"pk": "k1"},
		Actions: []driver.UpdateAction{{Action: "SET", Field: "val", Value: "new"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	iter, err := p.DynamoDB.GetStreamRecords(ctx, "t1", 10, "")
	if err != nil {
		t.Fatal(err)
	}

	// Should have INSERT (from PutItem) + MODIFY (from UpdateItem)
	if len(iter.Records) != 2 {
		t.Fatalf("expected 2 stream records, got %d", len(iter.Records))
	}

	modifyRec := iter.Records[1]
	if modifyRec.EventType != "MODIFY" {
		t.Errorf("expected MODIFY event, got %s", modifyRec.EventType)
	}

	if modifyRec.OldImage["val"] != "old" {
		t.Errorf("expected old image val='old', got %v", modifyRec.OldImage["val"])
	}

	if modifyRec.NewImage["val"] != "new" {
		t.Errorf("expected new image val='new', got %v", modifyRec.NewImage["val"])
	}
}

func TestCacheExpireAndPersistAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	_, err := p.ElastiCache.CreateCache(ctx, cachedriver.CacheConfig{Name: "c1"})
	if err != nil {
		t.Fatal(err)
	}

	if err := p.ElastiCache.Set(ctx, "c1", "k1", []byte("val"), 0); err != nil {
		t.Fatal(err)
	}

	ttl, err := p.ElastiCache.GetTTL(ctx, "c1", "k1")
	if err != nil {
		t.Fatal(err)
	}

	if ttl != -1 {
		t.Errorf("expected TTL -1, got %v", ttl)
	}

	if err := p.ElastiCache.Expire(ctx, "c1", "k1", 1*time.Hour); err != nil {
		t.Fatal(err)
	}

	ttl, err = p.ElastiCache.GetTTL(ctx, "c1", "k1")
	if err != nil {
		t.Fatal(err)
	}

	if ttl <= 0 {
		t.Errorf("expected positive TTL, got %v", ttl)
	}

	if err := p.ElastiCache.Persist(ctx, "c1", "k1"); err != nil {
		t.Fatal(err)
	}

	ttl, err = p.ElastiCache.GetTTL(ctx, "c1", "k1")
	if err != nil {
		t.Fatal(err)
	}

	if ttl != -1 {
		t.Errorf("expected TTL -1 after Persist, got %v", ttl)
	}
}

func TestCacheExpireAndPersistAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()

	_, err := p.Cache.CreateCache(ctx, cachedriver.CacheConfig{Name: "c1"})
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Cache.Set(ctx, "c1", "k1", []byte("val"), 0); err != nil {
		t.Fatal(err)
	}

	if err := p.Cache.Expire(ctx, "c1", "k1", 1*time.Hour); err != nil {
		t.Fatal(err)
	}

	ttl, err := p.Cache.GetTTL(ctx, "c1", "k1")
	if err != nil {
		t.Fatal(err)
	}

	if ttl <= 0 {
		t.Errorf("expected positive TTL, got %v", ttl)
	}

	if err := p.Cache.Persist(ctx, "c1", "k1"); err != nil {
		t.Fatal(err)
	}

	ttl, err = p.Cache.GetTTL(ctx, "c1", "k1")
	if err != nil {
		t.Fatal(err)
	}

	if ttl != -1 {
		t.Errorf("expected TTL -1 after Persist, got %v", ttl)
	}
}

func TestCacheExpireAndPersistGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()

	_, err := p.Memorystore.CreateCache(ctx, cachedriver.CacheConfig{Name: "c1"})
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Memorystore.Set(ctx, "c1", "k1", []byte("val"), 0); err != nil {
		t.Fatal(err)
	}

	if err := p.Memorystore.Expire(ctx, "c1", "k1", 1*time.Hour); err != nil {
		t.Fatal(err)
	}

	ttl, err := p.Memorystore.GetTTL(ctx, "c1", "k1")
	if err != nil {
		t.Fatal(err)
	}

	if ttl <= 0 {
		t.Errorf("expected positive TTL, got %v", ttl)
	}

	if err := p.Memorystore.Persist(ctx, "c1", "k1"); err != nil {
		t.Fatal(err)
	}

	ttl, err = p.Memorystore.GetTTL(ctx, "c1", "k1")
	if err != nil {
		t.Fatal(err)
	}

	if ttl != -1 {
		t.Errorf("expected TTL -1 after Persist, got %v", ttl)
	}
}

func TestCacheIncrDecrAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	_, err := p.ElastiCache.CreateCache(ctx, cachedriver.CacheConfig{Name: "c1"})
	if err != nil {
		t.Fatal(err)
	}

	val, err := p.ElastiCache.Incr(ctx, "c1", "counter")
	if err != nil {
		t.Fatal(err)
	}

	if val != 1 {
		t.Errorf("expected 1, got %d", val)
	}

	val, err = p.ElastiCache.IncrBy(ctx, "c1", "counter", 9)
	if err != nil {
		t.Fatal(err)
	}

	if val != 10 {
		t.Errorf("expected 10, got %d", val)
	}

	val, err = p.ElastiCache.Decr(ctx, "c1", "counter")
	if err != nil {
		t.Fatal(err)
	}

	if val != 9 {
		t.Errorf("expected 9, got %d", val)
	}

	val, err = p.ElastiCache.DecrBy(ctx, "c1", "counter", 4)
	if err != nil {
		t.Fatal(err)
	}

	if val != 5 {
		t.Errorf("expected 5, got %d", val)
	}

	item, err := p.ElastiCache.Get(ctx, "c1", "counter")
	if err != nil {
		t.Fatal(err)
	}

	if string(item.Value) != "5" {
		t.Errorf("expected '5', got %q", string(item.Value))
	}
}

func TestCacheIncrDecrAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()

	_, err := p.Cache.CreateCache(ctx, cachedriver.CacheConfig{Name: "c1"})
	if err != nil {
		t.Fatal(err)
	}

	val, err := p.Cache.Incr(ctx, "c1", "counter")
	if err != nil {
		t.Fatal(err)
	}

	if val != 1 {
		t.Errorf("expected 1, got %d", val)
	}

	val, err = p.Cache.IncrBy(ctx, "c1", "counter", 9)
	if err != nil {
		t.Fatal(err)
	}

	if val != 10 {
		t.Errorf("expected 10, got %d", val)
	}

	val, err = p.Cache.Decr(ctx, "c1", "counter")
	if err != nil {
		t.Fatal(err)
	}

	if val != 9 {
		t.Errorf("expected 9, got %d", val)
	}

	val, err = p.Cache.DecrBy(ctx, "c1", "counter", 4)
	if err != nil {
		t.Fatal(err)
	}

	if val != 5 {
		t.Errorf("expected 5, got %d", val)
	}
}

func TestCacheIncrDecrGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()

	_, err := p.Memorystore.CreateCache(ctx, cachedriver.CacheConfig{Name: "c1"})
	if err != nil {
		t.Fatal(err)
	}

	val, err := p.Memorystore.Incr(ctx, "c1", "counter")
	if err != nil {
		t.Fatal(err)
	}

	if val != 1 {
		t.Errorf("expected 1, got %d", val)
	}

	val, err = p.Memorystore.IncrBy(ctx, "c1", "counter", 9)
	if err != nil {
		t.Fatal(err)
	}

	if val != 10 {
		t.Errorf("expected 10, got %d", val)
	}

	val, err = p.Memorystore.Decr(ctx, "c1", "counter")
	if err != nil {
		t.Fatal(err)
	}

	if val != 9 {
		t.Errorf("expected 9, got %d", val)
	}

	val, err = p.Memorystore.DecrBy(ctx, "c1", "counter", 4)
	if err != nil {
		t.Fatal(err)
	}

	if val != 5 {
		t.Errorf("expected 5, got %d", val)
	}
}

func TestBucketPolicyAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	if err := p.S3.CreateBucket(ctx, "b1"); err != nil {
		t.Fatal(err)
	}

	policy := storagedriver.BucketPolicy{
		Version: "2012-10-17",
		Statements: []storagedriver.PolicyStatement{
			{Effect: "Allow", Principal: "*", Actions: []string{"s3:GetObject"}, Resources: []string{"arn:aws:s3:::b1/*"}},
		},
	}

	if err := p.S3.PutBucketPolicy(ctx, "b1", policy); err != nil {
		t.Fatal(err)
	}

	got, err := p.S3.GetBucketPolicy(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}

	if got.Version != "2012-10-17" {
		t.Errorf("expected version '2012-10-17', got %q", got.Version)
	}

	if len(got.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(got.Statements))
	}

	if got.Statements[0].Effect != "Allow" {
		t.Errorf("expected effect 'Allow', got %q", got.Statements[0].Effect)
	}

	if err := p.S3.DeleteBucketPolicy(ctx, "b1"); err != nil {
		t.Fatal(err)
	}

	_, err = p.S3.GetBucketPolicy(ctx, "b1")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got %v", err)
	}
}

func TestBucketPolicyAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()

	if err := p.BlobStorage.CreateBucket(ctx, "c1"); err != nil {
		t.Fatal(err)
	}

	policy := storagedriver.BucketPolicy{
		Version: "1.0",
		Statements: []storagedriver.PolicyStatement{
			{Effect: "Allow", Principal: "*", Actions: []string{"read"}, Resources: []string{"c1/*"}},
		},
	}

	if err := p.BlobStorage.PutBucketPolicy(ctx, "c1", policy); err != nil {
		t.Fatal(err)
	}

	got, err := p.BlobStorage.GetBucketPolicy(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(got.Statements))
	}
}

func TestBucketPolicyGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()

	if err := p.GCS.CreateBucket(ctx, "b1"); err != nil {
		t.Fatal(err)
	}

	policy := storagedriver.BucketPolicy{
		Version: "1",
		Statements: []storagedriver.PolicyStatement{
			{Effect: "Allow", Principal: "allUsers", Actions: []string{"storage.objects.get"}, Resources: []string{"b1/*"}},
		},
	}

	if err := p.GCS.PutBucketPolicy(ctx, "b1", policy); err != nil {
		t.Fatal(err)
	}

	got, err := p.GCS.GetBucketPolicy(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(got.Statements))
	}
}

func TestCORSConfigAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	if err := p.S3.CreateBucket(ctx, "b1"); err != nil {
		t.Fatal(err)
	}

	cors := storagedriver.CORSConfig{
		Rules: []storagedriver.CORSRule{
			{
				AllowedOrigins: []string{"https://example.com"},
				AllowedMethods: []string{"GET", "PUT"},
				AllowedHeaders: []string{"*"},
				MaxAgeSeconds:  3600,
			},
		},
	}

	if err := p.S3.PutCORSConfig(ctx, "b1", cors); err != nil {
		t.Fatal(err)
	}

	got, err := p.S3.GetCORSConfig(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Rules) != 1 {
		t.Fatalf("expected 1 CORS rule, got %d", len(got.Rules))
	}

	if got.Rules[0].AllowedOrigins[0] != "https://example.com" {
		t.Errorf("expected origin 'https://example.com', got %q", got.Rules[0].AllowedOrigins[0])
	}

	if err := p.S3.DeleteCORSConfig(ctx, "b1"); err != nil {
		t.Fatal(err)
	}

	_, err = p.S3.GetCORSConfig(ctx, "b1")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got %v", err)
	}
}

func TestEncryptionConfigAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	if err := p.S3.CreateBucket(ctx, "b1"); err != nil {
		t.Fatal(err)
	}

	enc := storagedriver.EncryptionConfig{
		Enabled:   true,
		Algorithm: "AES256",
	}

	if err := p.S3.PutEncryptionConfig(ctx, "b1", enc); err != nil {
		t.Fatal(err)
	}

	got, err := p.S3.GetEncryptionConfig(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}

	if !got.Enabled {
		t.Error("expected encryption enabled")
	}

	if got.Algorithm != "AES256" {
		t.Errorf("expected algorithm 'AES256', got %q", got.Algorithm)
	}
}

func TestEncryptionConfigAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()

	if err := p.BlobStorage.CreateBucket(ctx, "c1"); err != nil {
		t.Fatal(err)
	}

	enc := storagedriver.EncryptionConfig{
		Enabled:   true,
		Algorithm: "AES256",
	}

	if err := p.BlobStorage.PutEncryptionConfig(ctx, "c1", enc); err != nil {
		t.Fatal(err)
	}

	got, err := p.BlobStorage.GetEncryptionConfig(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}

	if !got.Enabled {
		t.Error("expected encryption enabled")
	}
}

func TestEncryptionConfigGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()

	if err := p.GCS.CreateBucket(ctx, "b1"); err != nil {
		t.Fatal(err)
	}

	enc := storagedriver.EncryptionConfig{
		Enabled:   true,
		Algorithm: "AES256",
	}

	if err := p.GCS.PutEncryptionConfig(ctx, "b1", enc); err != nil {
		t.Fatal(err)
	}

	got, err := p.GCS.GetEncryptionConfig(ctx, "b1")
	if err != nil {
		t.Fatal(err)
	}

	if !got.Enabled {
		t.Error("expected encryption enabled")
	}
}

func TestListenerRulesAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	lb, err := p.ELB.CreateLoadBalancer(ctx, lbdriver.LBConfig{
		Name: "test-lb", Type: "application", Scheme: "internet-facing",
	})
	if err != nil {
		t.Fatal(err)
	}

	tg, err := p.ELB.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{
		Name: "test-tg", Protocol: "HTTP", Port: 80, VPCID: "vpc-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	li, err := p.ELB.CreateListener(ctx, lbdriver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tg.ARN,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create rules with path conditions
	rule1, err := p.ELB.CreateRule(ctx, lbdriver.RuleConfig{
		ListenerARN: li.ARN,
		Priority:    10,
		Conditions:  []lbdriver.RuleCondition{{Field: "path-pattern", Values: []string{"/api/*"}}},
		Actions:     []lbdriver.RuleAction{{Type: "forward", TargetGroupARN: tg.ARN}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if rule1.ARN == "" {
		t.Error("expected non-empty rule ARN")
	}

	if rule1.Priority != 10 {
		t.Errorf("expected priority 10, got %d", rule1.Priority)
	}

	_, err = p.ELB.CreateRule(ctx, lbdriver.RuleConfig{
		ListenerARN: li.ARN,
		Priority:    20,
		Conditions:  []lbdriver.RuleCondition{{Field: "host-header", Values: []string{"example.com"}}},
		Actions:     []lbdriver.RuleAction{{Type: "forward", TargetGroupARN: tg.ARN}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Describe rules
	rules, err := p.ELB.DescribeRules(ctx, li.ARN)
	if err != nil {
		t.Fatal(err)
	}

	if len(rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(rules))
	}

	// Delete a rule
	if err := p.ELB.DeleteRule(ctx, rule1.ARN); err != nil {
		t.Fatal(err)
	}

	rules, err = p.ELB.DescribeRules(ctx, li.ARN)
	if err != nil {
		t.Fatal(err)
	}

	if len(rules) != 1 {
		t.Errorf("expected 1 rule after deletion, got %d", len(rules))
	}
}

func TestModifyListenerAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	lb, err := p.ELB.CreateLoadBalancer(ctx, lbdriver.LBConfig{
		Name: "test-lb", Type: "application", Scheme: "internet-facing",
	})
	if err != nil {
		t.Fatal(err)
	}

	tg, err := p.ELB.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{
		Name: "test-tg", Protocol: "HTTP", Port: 80, VPCID: "vpc-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	li, err := p.ELB.CreateListener(ctx, lbdriver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tg.ARN,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Modify port
	if err := p.ELB.ModifyListener(ctx, lbdriver.ModifyListenerInput{
		ListenerARN: li.ARN, Port: 8080,
	}); err != nil {
		t.Fatal(err)
	}

	listeners, err := p.ELB.DescribeListeners(ctx, lb.ARN)
	if err != nil {
		t.Fatal(err)
	}

	if len(listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(listeners))
	}

	if listeners[0].Port != 8080 {
		t.Errorf("expected port 8080, got %d", listeners[0].Port)
	}
}

func TestLBAttributesAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	lb, err := p.ELB.CreateLoadBalancer(ctx, lbdriver.LBConfig{
		Name: "test-lb", Type: "application", Scheme: "internet-facing",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get default attributes
	attrs, err := p.ELB.GetLBAttributes(ctx, lb.ARN)
	if err != nil {
		t.Fatal(err)
	}

	if attrs.IdleTimeout != 60 {
		t.Errorf("expected default idle timeout 60, got %d", attrs.IdleTimeout)
	}

	// Put custom attributes
	if err := p.ELB.PutLBAttributes(ctx, lb.ARN, lbdriver.LBAttributes{
		IdleTimeout:        120,
		DeletionProtection: true,
		AccessLogsEnabled:  true,
		AccessLogsBucket:   "my-access-logs",
	}); err != nil {
		t.Fatal(err)
	}

	attrs, err = p.ELB.GetLBAttributes(ctx, lb.ARN)
	if err != nil {
		t.Fatal(err)
	}

	if attrs.IdleTimeout != 120 {
		t.Errorf("expected idle timeout 120, got %d", attrs.IdleTimeout)
	}

	if !attrs.DeletionProtection {
		t.Error("expected deletion protection enabled")
	}

	if !attrs.AccessLogsEnabled {
		t.Error("expected access logs enabled")
	}

	if attrs.AccessLogsBucket != "my-access-logs" {
		t.Errorf("expected bucket 'my-access-logs', got %q", attrs.AccessLogsBucket)
	}
}

func TestListenerRulesAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()

	lb, err := p.LB.CreateLoadBalancer(ctx, lbdriver.LBConfig{
		Name: "test-lb", Type: "application", Scheme: "internet-facing",
	})
	if err != nil {
		t.Fatal(err)
	}

	tg, err := p.LB.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{
		Name: "test-tg", Protocol: "HTTP", Port: 80, VPCID: "vnet-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	li, err := p.LB.CreateListener(ctx, lbdriver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tg.ARN,
	})
	if err != nil {
		t.Fatal(err)
	}

	rule, err := p.LB.CreateRule(ctx, lbdriver.RuleConfig{
		ListenerARN: li.ARN,
		Priority:    10,
		Conditions:  []lbdriver.RuleCondition{{Field: "path-pattern", Values: []string{"/api/*"}}},
		Actions:     []lbdriver.RuleAction{{Type: "forward", TargetGroupARN: tg.ARN}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if rule.ARN == "" {
		t.Error("expected non-empty rule ARN")
	}

	rules, err := p.LB.DescribeRules(ctx, li.ARN)
	if err != nil {
		t.Fatal(err)
	}

	if len(rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(rules))
	}

	if err := p.LB.DeleteRule(ctx, rule.ARN); err != nil {
		t.Fatal(err)
	}

	rules, err = p.LB.DescribeRules(ctx, li.ARN)
	if err != nil {
		t.Fatal(err)
	}

	if len(rules) != 0 {
		t.Errorf("expected 0 rules after deletion, got %d", len(rules))
	}
}

func TestListenerRulesGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()

	lb, err := p.LB.CreateLoadBalancer(ctx, lbdriver.LBConfig{
		Name: "test-lb", Type: "application", Scheme: "internet-facing",
	})
	if err != nil {
		t.Fatal(err)
	}

	tg, err := p.LB.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{
		Name: "test-tg", Protocol: "HTTP", Port: 80, VPCID: "vpc-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	li, err := p.LB.CreateListener(ctx, lbdriver.ListenerConfig{
		LBARN: lb.ARN, Protocol: "HTTP", Port: 80, TargetGroupARN: tg.ARN,
	})
	if err != nil {
		t.Fatal(err)
	}

	rule, err := p.LB.CreateRule(ctx, lbdriver.RuleConfig{
		ListenerARN: li.ARN,
		Priority:    10,
		Conditions:  []lbdriver.RuleCondition{{Field: "path-pattern", Values: []string{"/api/*"}}},
		Actions:     []lbdriver.RuleAction{{Type: "forward", TargetGroupARN: tg.ARN}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if rule.ARN == "" {
		t.Error("expected non-empty rule ARN")
	}

	rules, err := p.LB.DescribeRules(ctx, li.ARN)
	if err != nil {
		t.Fatal(err)
	}

	if len(rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(rules))
	}

	if err := p.LB.DeleteRule(ctx, rule.ARN); err != nil {
		t.Fatal(err)
	}

	rules, err = p.LB.DescribeRules(ctx, li.ARN)
	if err != nil {
		t.Fatal(err)
	}

	if len(rules) != 0 {
		t.Errorf("expected 0 rules after deletion, got %d", len(rules))
	}
}

func TestVolumeLifecycleAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	vol, err := p.EC2.CreateVolume(ctx, computedriver.VolumeConfig{Size: 100, VolumeType: "gp3"})
	if err != nil {
		t.Fatal(err)
	}

	if vol.State != "available" {
		t.Errorf("expected state 'available', got %q", vol.State)
	}

	instances, err := p.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t3.micro",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.EC2.AttachVolume(ctx, vol.ID, instances[0].ID, "/dev/sdf"); err != nil {
		t.Fatal(err)
	}

	vols, err := p.EC2.DescribeVolumes(ctx, []string{vol.ID})
	if err != nil {
		t.Fatal(err)
	}

	if vols[0].State != "in-use" {
		t.Errorf("expected state 'in-use', got %q", vols[0].State)
	}

	if err := p.EC2.DetachVolume(ctx, vol.ID); err != nil {
		t.Fatal(err)
	}

	if err := p.EC2.DeleteVolume(ctx, vol.ID); err != nil {
		t.Fatal(err)
	}

	vols, err = p.EC2.DescribeVolumes(ctx, []string{vol.ID})
	if err != nil {
		t.Fatal(err)
	}

	if len(vols) != 0 {
		t.Errorf("expected 0 volumes after delete, got %d", len(vols))
	}
}

func TestVolumeLifecycleAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()

	vol, err := p.VirtualMachines.CreateVolume(ctx, computedriver.VolumeConfig{Size: 50})
	if err != nil {
		t.Fatal(err)
	}

	if vol.State != "available" {
		t.Errorf("expected 'available', got %q", vol.State)
	}

	if err := p.VirtualMachines.DeleteVolume(ctx, vol.ID); err != nil {
		t.Fatal(err)
	}
}

func TestVolumeLifecycleGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()

	vol, err := p.GCE.CreateVolume(ctx, computedriver.VolumeConfig{Size: 50})
	if err != nil {
		t.Fatal(err)
	}

	if vol.State != "available" {
		t.Errorf("expected 'available', got %q", vol.State)
	}

	if err := p.GCE.DeleteVolume(ctx, vol.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	vol, err := p.EC2.CreateVolume(ctx, computedriver.VolumeConfig{Size: 100})
	if err != nil {
		t.Fatal(err)
	}

	snap, err := p.EC2.CreateSnapshot(ctx, computedriver.SnapshotConfig{
		VolumeID: vol.ID, Description: "test snapshot",
	})
	if err != nil {
		t.Fatal(err)
	}

	if snap.VolumeID != vol.ID {
		t.Errorf("expected VolumeID %q, got %q", vol.ID, snap.VolumeID)
	}

	if snap.Size != 100 {
		t.Errorf("expected size 100, got %d", snap.Size)
	}

	snaps, err := p.EC2.DescribeSnapshots(ctx, []string{snap.ID})
	if err != nil {
		t.Fatal(err)
	}

	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}

	if err := p.EC2.DeleteSnapshot(ctx, snap.ID); err != nil {
		t.Fatal(err)
	}
}

func TestImageAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	instances, err := p.EC2.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "ami-test", InstanceType: "t3.micro",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}

	img, err := p.EC2.CreateImage(ctx, computedriver.ImageConfig{
		InstanceID: instances[0].ID, Name: "my-image", Description: "test image",
	})
	if err != nil {
		t.Fatal(err)
	}

	if img.Name != "my-image" {
		t.Errorf("expected name 'my-image', got %q", img.Name)
	}

	if img.State != "available" {
		t.Errorf("expected state 'available', got %q", img.State)
	}

	imgs, err := p.EC2.DescribeImages(ctx, []string{img.ID})
	if err != nil {
		t.Fatal(err)
	}

	if len(imgs) != 1 {
		t.Fatalf("expected 1 image, got %d", len(imgs))
	}

	if err := p.EC2.DeregisterImage(ctx, img.ID); err != nil {
		t.Fatal(err)
	}
}

func TestIAMGroupsAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	// Create user and group
	_, err := p.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	grp, err := p.IAM.CreateGroup(ctx, iamdriver.GroupConfig{
		Name: "developers",
		Path: "/eng/",
	})
	if err != nil {
		t.Fatal(err)
	}

	if grp.Name != "developers" {
		t.Errorf("expected group name developers, got %s", grp.Name)
	}

	if grp.ARN == "" {
		t.Error("expected non-empty ARN")
	}

	// Duplicate group should fail
	_, err = p.IAM.CreateGroup(ctx, iamdriver.GroupConfig{
		Name: "developers",
	})
	if err == nil {
		t.Error("expected error for duplicate group")
	}

	// Get group
	got, err := p.IAM.GetGroup(ctx, "developers")
	if err != nil {
		t.Fatal(err)
	}

	if got.Path != "/eng/" {
		t.Errorf("expected path /eng/, got %s", got.Path)
	}

	// List groups
	groups, err := p.IAM.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(groups) != 1 {
		t.Errorf("expected 1 group, got %d", len(groups))
	}

	// Add user to group
	if err := p.IAM.AddUserToGroup(ctx, "alice", "developers"); err != nil {
		t.Fatal(err)
	}

	// List groups for user
	userGroups, err := p.IAM.ListGroupsForUser(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}

	if len(userGroups) != 1 {
		t.Errorf("expected 1 group for user, got %d", len(userGroups))
	}

	// Remove user from group
	if err := p.IAM.RemoveUserFromGroup(ctx, "alice", "developers"); err != nil {
		t.Fatal(err)
	}

	userGroups, err = p.IAM.ListGroupsForUser(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}

	if len(userGroups) != 0 {
		t.Errorf("expected 0 groups after removal, got %d", len(userGroups))
	}

	// Delete group
	if err := p.IAM.DeleteGroup(ctx, "developers"); err != nil {
		t.Fatal(err)
	}

	_, err = p.IAM.GetGroup(ctx, "developers")
	if err == nil {
		t.Error("expected error after deleting group")
	}
}

func TestIAMGroupsAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()

	_, err := p.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "bob"})
	if err != nil {
		t.Fatal(err)
	}

	grp, err := p.IAM.CreateGroup(ctx, iamdriver.GroupConfig{
		Name: "admins",
	})
	if err != nil {
		t.Fatal(err)
	}

	if grp.Name != "admins" {
		t.Errorf("expected group name admins, got %s", grp.Name)
	}

	if err := p.IAM.AddUserToGroup(ctx, "bob", "admins"); err != nil {
		t.Fatal(err)
	}

	userGroups, err := p.IAM.ListGroupsForUser(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}

	if len(userGroups) != 1 {
		t.Errorf("expected 1 group, got %d", len(userGroups))
	}

	if err := p.IAM.RemoveUserFromGroup(ctx, "bob", "admins"); err != nil {
		t.Fatal(err)
	}

	if err := p.IAM.DeleteGroup(ctx, "admins"); err != nil {
		t.Fatal(err)
	}
}

func TestIAMGroupsGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()

	_, err := p.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}

	grp, err := p.IAM.CreateGroup(ctx, iamdriver.GroupConfig{
		Name: "viewers",
	})
	if err != nil {
		t.Fatal(err)
	}

	if grp.Name != "viewers" {
		t.Errorf("expected group name viewers, got %s", grp.Name)
	}

	if err := p.IAM.AddUserToGroup(ctx, "carol", "viewers"); err != nil {
		t.Fatal(err)
	}

	userGroups, err := p.IAM.ListGroupsForUser(ctx, "carol")
	if err != nil {
		t.Fatal(err)
	}

	if len(userGroups) != 1 {
		t.Errorf("expected 1 group, got %d", len(userGroups))
	}

	if err := p.IAM.RemoveUserFromGroup(ctx, "carol", "viewers"); err != nil {
		t.Fatal(err)
	}

	if err := p.IAM.DeleteGroup(ctx, "viewers"); err != nil {
		t.Fatal(err)
	}
}

func TestAccessKeysAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	_, err := p.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "dave"})
	if err != nil {
		t.Fatal(err)
	}

	// Create access key
	ak, err := p.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{
		UserName: "dave",
	})
	if err != nil {
		t.Fatal(err)
	}

	if ak.AccessKeyID == "" {
		t.Error("expected non-empty access key ID")
	}

	if ak.SecretAccessKey == "" {
		t.Error("expected non-empty secret access key")
	}

	if ak.UserName != "dave" {
		t.Errorf("expected user dave, got %s", ak.UserName)
	}

	if ak.Status != "Active" {
		t.Errorf("expected Active status, got %s", ak.Status)
	}

	// List access keys
	keys, err := p.IAM.ListAccessKeys(ctx, "dave")
	if err != nil {
		t.Fatal(err)
	}

	if len(keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keys))
	}

	// Create another key
	ak2, err := p.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{
		UserName: "dave",
	})
	if err != nil {
		t.Fatal(err)
	}

	keys, err = p.IAM.ListAccessKeys(ctx, "dave")
	if err != nil {
		t.Fatal(err)
	}

	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}

	// Delete access key
	if err := p.IAM.DeleteAccessKey(ctx, "dave", ak.AccessKeyID); err != nil {
		t.Fatal(err)
	}

	keys, err = p.IAM.ListAccessKeys(ctx, "dave")
	if err != nil {
		t.Fatal(err)
	}

	if len(keys) != 1 {
		t.Errorf("expected 1 key after delete, got %d", len(keys))
	}

	// Delete wrong user should fail
	err = p.IAM.DeleteAccessKey(ctx, "nobody", ak2.AccessKeyID)
	if err == nil {
		t.Error("expected error deleting key with wrong user")
	}

	// Create key for nonexistent user should fail
	_, err = p.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{
		UserName: "nonexistent",
	})
	if err == nil {
		t.Error("expected error creating key for nonexistent user")
	}
}

func TestInternetGatewayAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	// Create VPC first.
	vpc, err := p.VPC.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create internet gateway.
	igw, err := p.VPC.CreateInternetGateway(ctx, netdriver.InternetGatewayConfig{
		Tags: map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if igw.State != "detached" {
		t.Errorf("expected detached, got %s", igw.State)
	}

	// Attach to VPC.
	if err := p.VPC.AttachInternetGateway(ctx, igw.ID, vpc.ID); err != nil {
		t.Fatal(err)
	}

	// Describe and verify attached state.
	igws, err := p.VPC.DescribeInternetGateways(ctx, []string{igw.ID})
	if err != nil {
		t.Fatal(err)
	}

	if len(igws) != 1 {
		t.Fatalf("expected 1 igw, got %d", len(igws))
	}

	if igws[0].State != "attached" {
		t.Errorf("expected attached, got %s", igws[0].State)
	}

	if igws[0].VpcID != vpc.ID {
		t.Errorf("expected vpc %s, got %s", vpc.ID, igws[0].VpcID)
	}

	// Cannot delete while attached.
	err = p.VPC.DeleteInternetGateway(ctx, igw.ID)
	if err == nil {
		t.Error("expected error deleting attached igw")
	}

	// Detach then delete.
	if err := p.VPC.DetachInternetGateway(ctx, igw.ID, vpc.ID); err != nil {
		t.Fatal(err)
	}

	if err := p.VPC.DeleteInternetGateway(ctx, igw.ID); err != nil {
		t.Fatal(err)
	}

	// Verify gone.
	igws, err = p.VPC.DescribeInternetGateways(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(igws) != 0 {
		t.Errorf("expected 0 igws, got %d", len(igws))
	}
}

func TestElasticIPAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	// Allocate EIP.
	eip, err := p.VPC.AllocateAddress(ctx, netdriver.ElasticIPConfig{
		Tags: map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if eip.PublicIP == "" {
		t.Error("expected public IP")
	}

	if eip.AllocationID == "" {
		t.Error("expected allocation ID")
	}

	// Associate with instance.
	assocID, err := p.VPC.AssociateAddress(
		ctx, eip.AllocationID, "i-12345",
	)
	if err != nil {
		t.Fatal(err)
	}

	if assocID == "" {
		t.Error("expected association ID")
	}

	// Describe and verify association.
	eips, err := p.VPC.DescribeAddresses(
		ctx, []string{eip.AllocationID},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(eips) != 1 {
		t.Fatalf("expected 1 eip, got %d", len(eips))
	}

	if eips[0].InstanceID != "i-12345" {
		t.Errorf("expected i-12345, got %s", eips[0].InstanceID)
	}

	// Cannot release while associated.
	err = p.VPC.ReleaseAddress(ctx, eip.AllocationID)
	if err == nil {
		t.Error("expected error releasing associated eip")
	}

	// Disassociate then release.
	if err := p.VPC.DisassociateAddress(ctx, assocID); err != nil {
		t.Fatal(err)
	}

	if err := p.VPC.ReleaseAddress(ctx, eip.AllocationID); err != nil {
		t.Fatal(err)
	}

	// Verify gone.
	eips, err = p.VPC.DescribeAddresses(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(eips) != 0 {
		t.Errorf("expected 0 eips, got %d", len(eips))
	}
}

func TestRouteTableAssociationAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()

	// Create VPC, subnet, route table.
	vpc, err := p.VPC.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16",
	})
	if err != nil {
		t.Fatal(err)
	}

	subnet, err := p.VPC.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID:     vpc.ID,
		CIDRBlock: "10.0.1.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}

	rt, err := p.VPC.CreateRouteTable(ctx, netdriver.RouteTableConfig{
		VPCID: vpc.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Associate route table with subnet.
	assoc, err := p.VPC.AssociateRouteTable(
		ctx, rt.ID, subnet.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	if assoc.RouteTableID != rt.ID {
		t.Errorf("expected rt %s, got %s", rt.ID, assoc.RouteTableID)
	}

	if assoc.SubnetID != subnet.ID {
		t.Errorf(
			"expected subnet %s, got %s",
			subnet.ID, assoc.SubnetID,
		)
	}

	// Disassociate.
	if err := p.VPC.DisassociateRouteTable(ctx, assoc.ID); err != nil {
		t.Fatal(err)
	}

	// Disassociate again should fail.
	err = p.VPC.DisassociateRouteTable(ctx, assoc.ID)
	if err == nil {
		t.Error("expected error on double disassociate")
	}
}

func TestInternetGatewayAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()

	vpc, err := p.VNet.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16",
	})
	if err != nil {
		t.Fatal(err)
	}

	igw, err := p.VNet.CreateInternetGateway(
		ctx, netdriver.InternetGatewayConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}

	if igw.State != "detached" {
		t.Errorf("expected detached, got %s", igw.State)
	}

	if err := p.VNet.AttachInternetGateway(ctx, igw.ID, vpc.ID); err != nil {
		t.Fatal(err)
	}

	if err := p.VNet.DetachInternetGateway(ctx, igw.ID, vpc.ID); err != nil {
		t.Fatal(err)
	}

	if err := p.VNet.DeleteInternetGateway(ctx, igw.ID); err != nil {
		t.Fatal(err)
	}
}

func TestInternetGatewayGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()

	vpc, err := p.VPC.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16",
	})
	if err != nil {
		t.Fatal(err)
	}

	igw, err := p.VPC.CreateInternetGateway(
		ctx, netdriver.InternetGatewayConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}

	if igw.State != "detached" {
		t.Errorf("expected detached, got %s", igw.State)
	}

	if err := p.VPC.AttachInternetGateway(ctx, igw.ID, vpc.ID); err != nil {
		t.Fatal(err)
	}

	if err := p.VPC.DetachInternetGateway(ctx, igw.ID, vpc.ID); err != nil {
		t.Fatal(err)
	}

	if err := p.VPC.DeleteInternetGateway(ctx, igw.ID); err != nil {
		t.Fatal(err)
	}
}

// ─── Object & Bucket Tagging ────────────────────────────────────────────

func TestObjectTaggingAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testObjectTagging(t, ctx, p.S3)
}

func TestObjectTaggingAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testObjectTagging(t, ctx, p.BlobStorage)
}

func TestObjectTaggingGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testObjectTagging(t, ctx, p.GCS)
}

func testObjectTagging(t *testing.T, ctx context.Context, d storagedriver.Bucket) {
	t.Helper()

	// Setup: create bucket and object.
	if err := d.CreateBucket(ctx, "tag-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := d.PutObject(ctx, "tag-bucket", "file.txt", []byte("data"), "text/plain", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// Initially no tags.
	tags, err := d.GetObjectTagging(ctx, "tag-bucket", "file.txt")
	if err != nil {
		t.Fatalf("GetObjectTagging (empty): %v", err)
	}

	if len(tags) != 0 {
		t.Errorf("expected 0 tags initially, got %d", len(tags))
	}

	// Put tags.
	err = d.PutObjectTagging(ctx, "tag-bucket", "file.txt", map[string]string{
		"env": "prod", "team": "platform",
	})
	if err != nil {
		t.Fatalf("PutObjectTagging: %v", err)
	}

	// Get tags.
	tags, err = d.GetObjectTagging(ctx, "tag-bucket", "file.txt")
	if err != nil {
		t.Fatalf("GetObjectTagging: %v", err)
	}

	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(tags))
	}

	if tags["env"] != "prod" {
		t.Errorf("expected tag env=prod, got %q", tags["env"])
	}

	if tags["team"] != "platform" {
		t.Errorf("expected tag team=platform, got %q", tags["team"])
	}

	// Replace tags (PutObjectTagging replaces all).
	err = d.PutObjectTagging(ctx, "tag-bucket", "file.txt", map[string]string{"version": "v2"})
	if err != nil {
		t.Fatalf("PutObjectTagging (replace): %v", err)
	}

	tags, err = d.GetObjectTagging(ctx, "tag-bucket", "file.txt")
	if err != nil {
		t.Fatalf("GetObjectTagging (after replace): %v", err)
	}

	if len(tags) != 1 || tags["version"] != "v2" {
		t.Errorf("expected {version: v2}, got %v", tags)
	}

	// Delete tags.
	err = d.DeleteObjectTagging(ctx, "tag-bucket", "file.txt")
	if err != nil {
		t.Fatalf("DeleteObjectTagging: %v", err)
	}

	tags, err = d.GetObjectTagging(ctx, "tag-bucket", "file.txt")
	if err != nil {
		t.Fatalf("GetObjectTagging (after delete): %v", err)
	}

	if len(tags) != 0 {
		t.Errorf("expected 0 tags after delete, got %d", len(tags))
	}

	// Error: tag nonexistent object.
	err = d.PutObjectTagging(ctx, "tag-bucket", "missing.txt", map[string]string{"a": "b"})
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for missing object, got %v", err)
	}

	// Error: tag in nonexistent bucket.
	err = d.PutObjectTagging(ctx, "no-bucket", "file.txt", map[string]string{"a": "b"})
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for missing bucket, got %v", err)
	}
}

func TestBucketTaggingAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testBucketTagging(t, ctx, p.S3)
}

func TestBucketTaggingAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testBucketTagging(t, ctx, p.BlobStorage)
}

func TestBucketTaggingGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testBucketTagging(t, ctx, p.GCS)
}

func testBucketTagging(t *testing.T, ctx context.Context, d storagedriver.Bucket) {
	t.Helper()

	if err := d.CreateBucket(ctx, "tagged-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	// Initially no tags.
	tags, err := d.GetBucketTagging(ctx, "tagged-bucket")
	if err != nil {
		t.Fatalf("GetBucketTagging (empty): %v", err)
	}

	if len(tags) != 0 {
		t.Errorf("expected 0 tags initially, got %d", len(tags))
	}

	// Put bucket tags.
	err = d.PutBucketTagging(ctx, "tagged-bucket", map[string]string{
		"project": "cloudemu", "cost-center": "engineering",
	})
	if err != nil {
		t.Fatalf("PutBucketTagging: %v", err)
	}

	// Get bucket tags.
	tags, err = d.GetBucketTagging(ctx, "tagged-bucket")
	if err != nil {
		t.Fatalf("GetBucketTagging: %v", err)
	}

	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(tags))
	}

	if tags["project"] != "cloudemu" {
		t.Errorf("expected tag project=cloudemu, got %q", tags["project"])
	}

	// Delete bucket tags.
	err = d.DeleteBucketTagging(ctx, "tagged-bucket")
	if err != nil {
		t.Fatalf("DeleteBucketTagging: %v", err)
	}

	tags, err = d.GetBucketTagging(ctx, "tagged-bucket")
	if err != nil {
		t.Fatalf("GetBucketTagging (after delete): %v", err)
	}

	if len(tags) != 0 {
		t.Errorf("expected 0 tags after delete, got %d", len(tags))
	}

	// Error: nonexistent bucket.
	err = d.PutBucketTagging(ctx, "no-bucket", map[string]string{"a": "b"})
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for missing bucket, got %v", err)
	}
}

// testKeyPairOperations is a shared helper that tests key pair CRUD operations.
func testKeyPairOperations(t *testing.T, ctx context.Context, c computedriver.Compute) {
	t.Helper()

	// Create a key pair
	kp, err := c.CreateKeyPair(ctx, computedriver.KeyPairConfig{
		Name:    "test-key",
		KeyType: "ed25519",
		Tags:    map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateKeyPair: %v", err)
	}

	if kp.Name != "test-key" {
		t.Errorf("expected name 'test-key', got %q", kp.Name)
	}

	if kp.KeyType != "ed25519" {
		t.Errorf("expected key type 'ed25519', got %q", kp.KeyType)
	}

	if kp.ID == "" {
		t.Error("expected non-empty ID")
	}

	if kp.Fingerprint != "fp-test-key" {
		t.Errorf("expected fingerprint 'fp-test-key', got %q", kp.Fingerprint)
	}

	if kp.PrivateKey == "" {
		t.Error("expected PrivateKey to be populated on create")
	}

	if kp.PublicKey == "" {
		t.Error("expected PublicKey to be populated")
	}

	if kp.CreatedAt == "" {
		t.Error("expected non-empty CreatedAt")
	}

	if kp.Tags["env"] != "test" {
		t.Errorf("expected tag env=test, got %q", kp.Tags["env"])
	}

	// Create another with default key type
	kp2, err := c.CreateKeyPair(ctx, computedriver.KeyPairConfig{Name: "default-key"})
	if err != nil {
		t.Fatalf("CreateKeyPair default: %v", err)
	}

	if kp2.KeyType != "rsa" {
		t.Errorf("expected default key type 'rsa', got %q", kp2.KeyType)
	}

	// Duplicate should fail
	_, err = c.CreateKeyPair(ctx, computedriver.KeyPairConfig{Name: "test-key"})
	if err == nil {
		t.Error("expected error for duplicate key pair")
	}

	// Empty name should fail
	_, err = c.CreateKeyPair(ctx, computedriver.KeyPairConfig{Name: ""})
	if err == nil {
		t.Error("expected error for empty key pair name")
	}

	// Describe all
	all, err := c.DescribeKeyPairs(ctx, nil)
	if err != nil {
		t.Fatalf("DescribeKeyPairs all: %v", err)
	}

	if len(all) != 2 {
		t.Fatalf("expected 2 key pairs, got %d", len(all))
	}

	for _, kpi := range all {
		if kpi.PrivateKey != "" {
			t.Errorf("DescribeKeyPairs should not return PrivateKey, got %q", kpi.PrivateKey)
		}
	}

	// Describe by name
	filtered, err := c.DescribeKeyPairs(ctx, []string{"test-key"})
	if err != nil {
		t.Fatalf("DescribeKeyPairs filtered: %v", err)
	}

	if len(filtered) != 1 {
		t.Fatalf("expected 1 key pair, got %d", len(filtered))
	}

	if filtered[0].Name != "test-key" {
		t.Errorf("expected name 'test-key', got %q", filtered[0].Name)
	}

	// Delete
	if err := c.DeleteKeyPair(ctx, "test-key"); err != nil {
		t.Fatalf("DeleteKeyPair: %v", err)
	}

	// Delete again should fail
	err = c.DeleteKeyPair(ctx, "test-key")
	if err == nil {
		t.Error("expected error deleting non-existent key pair")
	}

	// Verify only one remains
	remaining, err := c.DescribeKeyPairs(ctx, nil)
	if err != nil {
		t.Fatalf("DescribeKeyPairs after delete: %v", err)
	}

	if len(remaining) != 1 {
		t.Errorf("expected 1 key pair after delete, got %d", len(remaining))
	}
}

func TestKeyPairAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testKeyPairOperations(t, ctx, p.EC2)
}

func TestKeyPairAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testKeyPairOperations(t, ctx, p.VirtualMachines)
}

func TestKeyPairGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testKeyPairOperations(t, ctx, p.GCE)
}

// ─── Global Secondary Indexes ───────────────────────────────────────────

func TestGSIOperationsAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testGSIOperations(t, ctx, p.DynamoDB)
}

func TestGSIOperationsAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testGSIOperations(t, ctx, p.CosmosDB)
}

func TestGSIOperationsGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testGSIOperations(t, ctx, p.Firestore)
}

func testGSIOperations(t *testing.T, ctx context.Context, d driver.Database) {
	t.Helper()

	// Create table with no GSIs.
	err := d.CreateTable(ctx, driver.TableConfig{
		Name:         "users",
		PartitionKey: "id",
		SortKey:      "created",
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// Initially no indexes.
	indexes, err := d.ListIndexes(ctx, "users")
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}

	if len(indexes) != 0 {
		t.Errorf("expected 0 indexes, got %d", len(indexes))
	}

	// Create a GSI.
	info, err := d.CreateIndex(ctx, "users", driver.GSIConfig{
		Name: "email-index", PartitionKey: "email", SortKey: "created",
	})
	if err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}

	if info.Name != "email-index" {
		t.Errorf("expected index name email-index, got %s", info.Name)
	}

	if info.Status != "ACTIVE" {
		t.Errorf("expected status ACTIVE, got %s", info.Status)
	}

	// Describe the index.
	desc, err := d.DescribeIndex(ctx, "users", "email-index")
	if err != nil {
		t.Fatalf("DescribeIndex: %v", err)
	}

	if desc.PartitionKey != "email" {
		t.Errorf("expected partition key email, got %s", desc.PartitionKey)
	}

	// List should show 1 index.
	indexes, err = d.ListIndexes(ctx, "users")
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}

	if len(indexes) != 1 {
		t.Errorf("expected 1 index, got %d", len(indexes))
	}

	// Put some items and query via GSI.
	_ = d.PutItem(ctx, "users", map[string]any{"id": "1", "email": "alice@test.com", "created": "2025-01-01"})
	_ = d.PutItem(ctx, "users", map[string]any{"id": "2", "email": "alice@test.com", "created": "2025-06-01"})
	_ = d.PutItem(ctx, "users", map[string]any{"id": "3", "email": "bob@test.com", "created": "2025-03-01"})

	result, err := d.Query(ctx, driver.QueryInput{
		Table:     "users",
		IndexName: "email-index",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "email",
			PartitionVal: "alice@test.com",
		},
	})
	if err != nil {
		t.Fatalf("Query via GSI: %v", err)
	}

	if result.Count != 2 {
		t.Errorf("expected 2 items for alice@test.com via GSI, got %d", result.Count)
	}

	// Duplicate index name should fail.
	_, err = d.CreateIndex(ctx, "users", driver.GSIConfig{
		Name: "email-index", PartitionKey: "name",
	})
	if err == nil {
		t.Error("expected error for duplicate index, got nil")
	}

	// Delete the index.
	err = d.DeleteIndex(ctx, "users", "email-index")
	if err != nil {
		t.Fatalf("DeleteIndex: %v", err)
	}

	// Describe should fail after delete.
	_, err = d.DescribeIndex(ctx, "users", "email-index")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got %v", err)
	}

	// Query via deleted index should fail.
	_, err = d.Query(ctx, driver.QueryInput{
		Table:     "users",
		IndexName: "email-index",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "email",
			PartitionVal: "alice@test.com",
		},
	})
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for deleted index query, got %v", err)
	}

	// Error: table not found.
	_, err = d.CreateIndex(ctx, "no-table", driver.GSIConfig{Name: "idx"})
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for missing table, got %v", err)
	}
}
// ---------------------------------------------------------------------------
// Event Source Mapping Tests
// ---------------------------------------------------------------------------

func testEventSourceMapping(t *testing.T, ctx context.Context, d serverlessdriver.Serverless) {
	t.Helper()

	// Create a function first (needed for context but not enforced by the mock).
	_, err := d.CreateFunction(ctx, serverlessdriver.FunctionConfig{
		Name:    "esm-func",
		Runtime: "go1.x",
		Handler: "main",
		Memory:  128,
		Timeout: 30,
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	// Error: missing function name.
	_, err = d.CreateEventSourceMapping(ctx, serverlessdriver.EventSourceMappingConfig{
		EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:my-queue",
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("expected InvalidArgument for empty function name, got %v", err)
	}

	// Error: missing event source ARN.
	_, err = d.CreateEventSourceMapping(ctx, serverlessdriver.EventSourceMappingConfig{
		FunctionName: "esm-func",
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("expected InvalidArgument for empty event source ARN, got %v", err)
	}

	// Create an enabled mapping.
	mapping, err := d.CreateEventSourceMapping(ctx, serverlessdriver.EventSourceMappingConfig{
		FunctionName:     "esm-func",
		EventSourceArn:   "arn:aws:sqs:us-east-1:123456789012:my-queue",
		Enabled:          true,
		BatchSize:        5,
		StartingPosition: "LATEST",
	})
	if err != nil {
		t.Fatalf("CreateEventSourceMapping: %v", err)
	}
	if mapping.UUID == "" {
		t.Error("expected non-empty UUID")
	}
	if mapping.State != "Enabled" {
		t.Errorf("expected State=Enabled, got %s", mapping.State)
	}
	if mapping.BatchSize != 5 {
		t.Errorf("expected BatchSize=5, got %d", mapping.BatchSize)
	}
	if mapping.FunctionName != "esm-func" {
		t.Errorf("expected FunctionName=esm-func, got %s", mapping.FunctionName)
	}

	// Create a disabled mapping with default batch size.
	mapping2, err := d.CreateEventSourceMapping(ctx, serverlessdriver.EventSourceMappingConfig{
		FunctionName:   "esm-func",
		EventSourceArn: "arn:aws:sqs:us-east-1:123456789012:other-queue",
		Enabled:        false,
	})
	if err != nil {
		t.Fatalf("CreateEventSourceMapping (disabled): %v", err)
	}
	if mapping2.State != "Disabled" {
		t.Errorf("expected State=Disabled, got %s", mapping2.State)
	}
	if mapping2.BatchSize != 10 {
		t.Errorf("expected default BatchSize=10, got %d", mapping2.BatchSize)
	}

	// Get mapping.
	got, err := d.GetEventSourceMapping(ctx, mapping.UUID)
	if err != nil {
		t.Fatalf("GetEventSourceMapping: %v", err)
	}
	if got.UUID != mapping.UUID {
		t.Errorf("expected UUID=%s, got %s", mapping.UUID, got.UUID)
	}

	// Get nonexistent mapping.
	_, err = d.GetEventSourceMapping(ctx, "nonexistent-uuid")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for missing mapping, got %v", err)
	}

	// List all mappings.
	all, err := d.ListEventSourceMappings(ctx, "")
	if err != nil {
		t.Fatalf("ListEventSourceMappings (all): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 mappings, got %d", len(all))
	}

	// List mappings filtered by function name.
	filtered, err := d.ListEventSourceMappings(ctx, "esm-func")
	if err != nil {
		t.Fatalf("ListEventSourceMappings (filtered): %v", err)
	}
	if len(filtered) != 2 {
		t.Errorf("expected 2 mappings for esm-func, got %d", len(filtered))
	}

	// List mappings for nonexistent function returns empty.
	empty, err := d.ListEventSourceMappings(ctx, "nonexistent-func")
	if err != nil {
		t.Fatalf("ListEventSourceMappings (nonexistent): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 mappings for nonexistent function, got %d", len(empty))
	}

	// Update: disable the enabled mapping.
	updated, err := d.UpdateEventSourceMapping(ctx, mapping.UUID, serverlessdriver.EventSourceMappingConfig{
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("UpdateEventSourceMapping (disable): %v", err)
	}
	if updated.State != "Disabled" {
		t.Errorf("expected State=Disabled after update, got %s", updated.State)
	}
	if updated.Enabled {
		t.Errorf("expected Enabled=false after update, got true")
	}

	// Update: re-enable and change batch size.
	updated2, err := d.UpdateEventSourceMapping(ctx, mapping.UUID, serverlessdriver.EventSourceMappingConfig{
		Enabled:   true,
		BatchSize: 20,
	})
	if err != nil {
		t.Fatalf("UpdateEventSourceMapping (re-enable): %v", err)
	}
	if updated2.State != "Enabled" {
		t.Errorf("expected State=Enabled, got %s", updated2.State)
	}
	if updated2.BatchSize != 20 {
		t.Errorf("expected BatchSize=20, got %d", updated2.BatchSize)
	}

	// Update nonexistent mapping.
	_, err = d.UpdateEventSourceMapping(ctx, "nonexistent-uuid", serverlessdriver.EventSourceMappingConfig{})
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for update on missing mapping, got %v", err)
	}

	// Delete mapping.
	err = d.DeleteEventSourceMapping(ctx, mapping.UUID)
	if err != nil {
		t.Fatalf("DeleteEventSourceMapping: %v", err)
	}

	// Verify deletion.
	_, err = d.GetEventSourceMapping(ctx, mapping.UUID)
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got %v", err)
	}

	// Delete nonexistent mapping.
	err = d.DeleteEventSourceMapping(ctx, "nonexistent-uuid")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for deleting missing mapping, got %v", err)
	}

	// Verify only one mapping remains.
	remaining, err := d.ListEventSourceMappings(ctx, "")
	if err != nil {
		t.Fatalf("ListEventSourceMappings (remaining): %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining mapping, got %d", len(remaining))
	}
}

func TestEventSourceMappingAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testEventSourceMapping(t, ctx, p.Lambda)
}

func TestEventSourceMappingAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testEventSourceMapping(t, ctx, p.Functions)
}

func TestEventSourceMappingGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testEventSourceMapping(t, ctx, p.CloudFunctions)
}
// ─── VPC Endpoints ────────────────────────────────────────────

func TestVPCEndpointAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testVPCEndpointOperations(t, ctx, p.VPC)
}

func TestVPCEndpointAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testVPCEndpointOperations(t, ctx, p.VNet)
}

func TestVPCEndpointGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testVPCEndpointOperations(t, ctx, p.VPC)
}

func testVPCEndpointOperations(
	t *testing.T, ctx context.Context, d netdriver.Networking,
) {
	t.Helper()

	// Create VPC first.
	vpc, err := d.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create endpoint.
	ep, err := d.CreateVPCEndpoint(ctx, netdriver.VPCEndpointConfig{
		VPCID:        vpc.ID,
		ServiceName:  "com.amazonaws.us-east-1.s3",
		EndpointType: "Gateway",
		SubnetIDs:    []string{"subnet-1"},
		Tags:         map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if ep.State != "available" {
		t.Errorf("expected available, got %s", ep.State)
	}

	if ep.VPCID != vpc.ID {
		t.Errorf("expected vpc %s, got %s", vpc.ID, ep.VPCID)
	}

	if ep.ServiceName != "com.amazonaws.us-east-1.s3" {
		t.Errorf("expected s3 service, got %s", ep.ServiceName)
	}

	// Describe all.
	eps, err := d.DescribeVPCEndpoints(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}

	// Describe by ID.
	eps, err = d.DescribeVPCEndpoints(ctx, []string{ep.ID})
	if err != nil {
		t.Fatal(err)
	}

	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}

	if eps[0].ID != ep.ID {
		t.Errorf("expected %s, got %s", ep.ID, eps[0].ID)
	}

	// Modify endpoint.
	modified, err := d.ModifyVPCEndpoint(ctx, ep.ID, netdriver.VPCEndpointConfig{
		SubnetIDs: []string{"subnet-2", "subnet-3"},
		Tags:      map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(modified.SubnetIDs) != 2 {
		t.Errorf("expected 2 subnets, got %d", len(modified.SubnetIDs))
	}

	if modified.Tags["env"] != "prod" {
		t.Errorf("expected tag env=prod, got %q", modified.Tags["env"])
	}

	// Modify nonexistent.
	_, err = d.ModifyVPCEndpoint(ctx, "vpce-nonexistent", netdriver.VPCEndpointConfig{
		SubnetIDs: []string{"subnet-1"},
	})
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}

	// Delete endpoint.
	if err := d.DeleteVPCEndpoint(ctx, ep.ID); err != nil {
		t.Fatal(err)
	}

	// Verify gone.
	eps, err = d.DescribeVPCEndpoints(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(eps) != 0 {
		t.Errorf("expected 0 endpoints, got %d", len(eps))
	}

	// Delete nonexistent.
	err = d.DeleteVPCEndpoint(ctx, ep.ID)
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}

	// Create with missing VPC ID.
	_, err = d.CreateVPCEndpoint(ctx, netdriver.VPCEndpointConfig{
		ServiceName: "svc",
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("expected InvalidArgument, got %v", err)
	}

	// Create with missing service name.
	_, err = d.CreateVPCEndpoint(ctx, netdriver.VPCEndpointConfig{
		VPCID: vpc.ID,
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("expected InvalidArgument, got %v", err)
	}

	// Create with nonexistent VPC.
	_, err = d.CreateVPCEndpoint(ctx, netdriver.VPCEndpointConfig{
		VPCID:       "vpc-nonexistent",
		ServiceName: "svc",
	})
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}
}
func TestHealthCheckAWS(t *testing.T) {
	ctx := context.Background()
	p := NewAWS()
	testHealthCheck(t, ctx, p.Route53)
}

func TestHealthCheckAzure(t *testing.T) {
	ctx := context.Background()
	p := NewAzure()
	testHealthCheck(t, ctx, p.DNS)
}

func TestHealthCheckGCP(t *testing.T) {
	ctx := context.Background()
	p := NewGCP()
	testHealthCheck(t, ctx, p.CloudDNS)
}

func testHealthCheck(t *testing.T, ctx context.Context, d dnsdriver.DNS) {
	t.Helper()

	// Create health check with defaults.
	hc, err := d.CreateHealthCheck(ctx, dnsdriver.HealthCheckConfig{
		Endpoint: "10.0.0.1",
		Port:     80,
		Protocol: "HTTP",
		Path:     "/health",
		Tags:     map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateHealthCheck: %v", err)
	}

	if hc.ID == "" {
		t.Error("expected non-empty health check ID")
	}

	if hc.Endpoint != "10.0.0.1" {
		t.Errorf("expected endpoint 10.0.0.1, got %q", hc.Endpoint)
	}

	if hc.Status != "HEALTHY" {
		t.Errorf("expected HEALTHY status, got %q", hc.Status)
	}

	if hc.IntervalSeconds != 30 {
		t.Errorf("expected default interval 30, got %d", hc.IntervalSeconds)
	}

	if hc.FailureThreshold != 3 {
		t.Errorf("expected default threshold 3, got %d", hc.FailureThreshold)
	}

	if hc.Tags["env"] != "test" {
		t.Errorf("expected tag env=test, got %q", hc.Tags["env"])
	}

	// Get health check.
	got, err := d.GetHealthCheck(ctx, hc.ID)
	if err != nil {
		t.Fatalf("GetHealthCheck: %v", err)
	}

	if got.Endpoint != "10.0.0.1" {
		t.Errorf("expected endpoint 10.0.0.1, got %q", got.Endpoint)
	}

	// List health checks.
	checks, err := d.ListHealthChecks(ctx)
	if err != nil {
		t.Fatalf("ListHealthChecks: %v", err)
	}

	if len(checks) != 1 {
		t.Errorf("expected 1 health check, got %d", len(checks))
	}

	// Create second health check.
	hc2, err := d.CreateHealthCheck(ctx, dnsdriver.HealthCheckConfig{
		Endpoint:         "10.0.0.2",
		Port:             443,
		Protocol:         "HTTPS",
		Path:             "/status",
		IntervalSeconds:  10,
		FailureThreshold: 5,
	})
	if err != nil {
		t.Fatalf("CreateHealthCheck (second): %v", err)
	}

	if hc2.IntervalSeconds != 10 {
		t.Errorf("expected interval 10, got %d", hc2.IntervalSeconds)
	}

	if hc2.FailureThreshold != 5 {
		t.Errorf("expected threshold 5, got %d", hc2.FailureThreshold)
	}

	checks, err = d.ListHealthChecks(ctx)
	if err != nil {
		t.Fatalf("ListHealthChecks (after second create): %v", err)
	}

	if len(checks) != 2 {
		t.Errorf("expected 2 health checks, got %d", len(checks))
	}

	// Update health check.
	updated, err := d.UpdateHealthCheck(ctx, hc.ID, dnsdriver.HealthCheckConfig{
		Endpoint: "10.0.0.99",
		Port:     8080,
		Tags:     map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("UpdateHealthCheck: %v", err)
	}

	if updated.Endpoint != "10.0.0.99" {
		t.Errorf("expected updated endpoint 10.0.0.99, got %q", updated.Endpoint)
	}

	if updated.Port != 8080 {
		t.Errorf("expected updated port 8080, got %d", updated.Port)
	}

	if updated.Tags["env"] != "prod" {
		t.Errorf("expected updated tag env=prod, got %q", updated.Tags["env"])
	}

	// Set health check status.
	err = d.SetHealthCheckStatus(ctx, hc.ID, "UNHEALTHY")
	if err != nil {
		t.Fatalf("SetHealthCheckStatus: %v", err)
	}

	got, err = d.GetHealthCheck(ctx, hc.ID)
	if err != nil {
		t.Fatalf("GetHealthCheck (after status change): %v", err)
	}

	if got.Status != "UNHEALTHY" {
		t.Errorf("expected UNHEALTHY status, got %q", got.Status)
	}

	// Set back to healthy.
	err = d.SetHealthCheckStatus(ctx, hc.ID, "HEALTHY")
	if err != nil {
		t.Fatalf("SetHealthCheckStatus (back to healthy): %v", err)
	}

	got, err = d.GetHealthCheck(ctx, hc.ID)
	if err != nil {
		t.Fatalf("GetHealthCheck (after revert): %v", err)
	}

	if got.Status != "HEALTHY" {
		t.Errorf("expected HEALTHY status, got %q", got.Status)
	}

	// Delete health check.
	err = d.DeleteHealthCheck(ctx, hc.ID)
	if err != nil {
		t.Fatalf("DeleteHealthCheck: %v", err)
	}

	checks, err = d.ListHealthChecks(ctx)
	if err != nil {
		t.Fatalf("ListHealthChecks (after delete): %v", err)
	}

	if len(checks) != 1 {
		t.Errorf("expected 1 health check after delete, got %d", len(checks))
	}

	// Error: create with empty endpoint.
	_, err = d.CreateHealthCheck(ctx, dnsdriver.HealthCheckConfig{})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("expected InvalidArgument for empty endpoint, got %v", err)
	}

	// Error: get nonexistent.
	_, err = d.GetHealthCheck(ctx, "nonexistent")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for missing health check, got %v", err)
	}

	// Error: update nonexistent.
	_, err = d.UpdateHealthCheck(ctx, "nonexistent", dnsdriver.HealthCheckConfig{Endpoint: "10.0.0.1"})
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for update on missing health check, got %v", err)
	}

	// Error: delete nonexistent.
	err = d.DeleteHealthCheck(ctx, "nonexistent")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for delete on missing health check, got %v", err)
	}

	// Error: set status on nonexistent.
	err = d.SetHealthCheckStatus(ctx, "nonexistent", "HEALTHY")
	if !cerrors.IsNotFound(err) {
		t.Errorf("expected NotFound for set status on missing health check, got %v", err)
	}

	// Error: set invalid status.
	err = d.SetHealthCheckStatus(ctx, hc2.ID, "INVALID")
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("expected InvalidArgument for invalid status, got %v", err)
	}
}
func testAlarmActions(
	t *testing.T,
	label string,
	createChannel func(ctx context.Context, cfg mondriver.NotificationChannelConfig) (*mondriver.NotificationChannelInfo, error),
	deleteChannel func(ctx context.Context, id string) error,
	getChannel func(ctx context.Context, id string) (*mondriver.NotificationChannelInfo, error),
	listChannels func(ctx context.Context) ([]mondriver.NotificationChannelInfo, error),
	createAlarm func(ctx context.Context, cfg mondriver.AlarmConfig) error,
	describeAlarms func(ctx context.Context, names []string) ([]mondriver.AlarmInfo, error),
	putMetricData func(ctx context.Context, data []mondriver.MetricDatum) error,
	getHistory func(ctx context.Context, alarmName string, limit int) ([]mondriver.AlarmHistoryEntry, error),
	clock *config.FakeClock,
) {
	t.Helper()

	ctx := context.Background()

	// 1. Create notification channel.
	ch, err := createChannel(ctx, mondriver.NotificationChannelConfig{
		Name: "ops-email", Type: "email", Endpoint: "ops@example.com",
		Tags: map[string]string{"team": "ops"},
	})
	if err != nil {
		t.Fatalf("[%s] CreateNotificationChannel: %v", label, err)
	}

	if ch.Name != "ops-email" {
		t.Errorf("[%s] expected channel name 'ops-email', got %q", label, ch.Name)
	}

	if ch.Type != "email" {
		t.Errorf("[%s] expected channel type 'email', got %q", label, ch.Type)
	}

	if ch.Endpoint != "ops@example.com" {
		t.Errorf("[%s] expected endpoint 'ops@example.com', got %q", label, ch.Endpoint)
	}

	if ch.ID == "" {
		t.Fatalf("[%s] expected non-empty channel ID", label)
	}

	// 2. Get notification channel.
	got, err := getChannel(ctx, ch.ID)
	if err != nil {
		t.Fatalf("[%s] GetNotificationChannel: %v", label, err)
	}

	if got.Name != "ops-email" {
		t.Errorf("[%s] get channel name mismatch: got %q", label, got.Name)
	}

	// 3. List notification channels.
	channels, err := listChannels(ctx)
	if err != nil {
		t.Fatalf("[%s] ListNotificationChannels: %v", label, err)
	}

	if len(channels) != 1 {
		t.Errorf("[%s] expected 1 channel, got %d", label, len(channels))
	}

	// 4. Create alarm with actions referencing the channel.
	if err := createAlarm(ctx, mondriver.AlarmConfig{
		Name: "high-cpu-actions", Namespace: "TestNS", MetricName: "CPU",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 80,
		Period: 300, EvaluationPeriods: 1, Stat: "Average",
		AlarmActions: []string{ch.ID},
		OKActions:    []string{ch.ID},
	}); err != nil {
		t.Fatalf("[%s] CreateAlarm: %v", label, err)
	}

	// 5. Verify initial state.
	alarms, err := describeAlarms(ctx, []string{"high-cpu-actions"})
	if err != nil {
		t.Fatalf("[%s] DescribeAlarms: %v", label, err)
	}

	if len(alarms) != 1 {
		t.Fatalf("[%s] expected 1 alarm, got %d", label, len(alarms))
	}

	if alarms[0].State != "INSUFFICIENT_DATA" {
		t.Errorf("[%s] expected INSUFFICIENT_DATA, got %s", label, alarms[0].State)
	}

	// 6. Push metric below threshold -> state transitions to OK.
	now := clock.Now()
	if err := putMetricData(ctx, []mondriver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 50, Timestamp: now},
	}); err != nil {
		t.Fatalf("[%s] PutMetricData: %v", label, err)
	}

	alarms, _ = describeAlarms(ctx, []string{"high-cpu-actions"})
	if alarms[0].State != "OK" {
		t.Errorf("[%s] expected OK, got %s", label, alarms[0].State)
	}

	// 7. Advance clock and push metric above threshold -> state transitions to ALARM.
	clock.Advance(10 * time.Minute)
	now = clock.Now()

	if err := putMetricData(ctx, []mondriver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 95, Timestamp: now},
	}); err != nil {
		t.Fatalf("[%s] PutMetricData: %v", label, err)
	}

	alarms, _ = describeAlarms(ctx, []string{"high-cpu-actions"})
	if alarms[0].State != "ALARM" {
		t.Errorf("[%s] expected ALARM, got %s", label, alarms[0].State)
	}

	// 8. Verify alarm history has entries.
	history, err := getHistory(ctx, "high-cpu-actions", 10)
	if err != nil {
		t.Fatalf("[%s] GetAlarmHistory: %v", label, err)
	}

	// Two transitions: INSUFFICIENT_DATA->OK, OK->ALARM.
	if len(history) != 2 {
		t.Fatalf("[%s] expected 2 history entries, got %d", label, len(history))
	}

	if history[0].OldState != "INSUFFICIENT_DATA" || history[0].NewState != "OK" {
		t.Errorf("[%s] history[0]: expected INSUFFICIENT_DATA->OK, got %s->%s", label, history[0].OldState, history[0].NewState)
	}

	if history[1].OldState != "OK" || history[1].NewState != "ALARM" {
		t.Errorf("[%s] history[1]: expected OK->ALARM, got %s->%s", label, history[1].OldState, history[1].NewState)
	}

	// 9. Verify history limit works.
	limited, err := getHistory(ctx, "high-cpu-actions", 1)
	if err != nil {
		t.Fatalf("[%s] GetAlarmHistory (limited): %v", label, err)
	}

	if len(limited) != 1 {
		t.Errorf("[%s] expected 1 limited history entry, got %d", label, len(limited))
	}

	// 10. Delete notification channel.
	if err := deleteChannel(ctx, ch.ID); err != nil {
		t.Fatalf("[%s] DeleteNotificationChannel: %v", label, err)
	}

	// 11. Get deleted channel should fail.
	_, err = getChannel(ctx, ch.ID)
	if !cerrors.IsNotFound(err) {
		t.Errorf("[%s] expected NotFound after delete, got %v", label, err)
	}

	// 12. List should be empty.
	channels, err = listChannels(ctx)
	if err != nil {
		t.Fatalf("[%s] ListNotificationChannels after delete: %v", label, err)
	}

	if len(channels) != 0 {
		t.Errorf("[%s] expected 0 channels after delete, got %d", label, len(channels))
	}
}

func TestAlarmActionsAWS(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC))
	p := NewAWS(config.WithClock(clock))
	testAlarmActions(t, "AWS",
		p.CloudWatch.CreateNotificationChannel,
		p.CloudWatch.DeleteNotificationChannel,
		p.CloudWatch.GetNotificationChannel,
		p.CloudWatch.ListNotificationChannels,
		p.CloudWatch.CreateAlarm,
		p.CloudWatch.DescribeAlarms,
		p.CloudWatch.PutMetricData,
		p.CloudWatch.GetAlarmHistory,
		clock,
	)
}

func TestAlarmActionsAzure(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC))
	p := NewAzure(config.WithClock(clock))
	testAlarmActions(t, "Azure",
		p.Monitor.CreateNotificationChannel,
		p.Monitor.DeleteNotificationChannel,
		p.Monitor.GetNotificationChannel,
		p.Monitor.ListNotificationChannels,
		p.Monitor.CreateAlarm,
		p.Monitor.DescribeAlarms,
		p.Monitor.PutMetricData,
		p.Monitor.GetAlarmHistory,
		clock,
	)
}

func TestAlarmActionsGCP(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC))
	p := NewGCP(config.WithClock(clock))
	testAlarmActions(t, "GCP",
		p.CloudMonitoring.CreateNotificationChannel,
		p.CloudMonitoring.DeleteNotificationChannel,
		p.CloudMonitoring.GetNotificationChannel,
		p.CloudMonitoring.ListNotificationChannels,
		p.CloudMonitoring.CreateAlarm,
		p.CloudMonitoring.DescribeAlarms,
		p.CloudMonitoring.PutMetricData,
		p.CloudMonitoring.GetAlarmHistory,
		clock,
	)
}
func testInstanceProfileOps(
	t *testing.T,
	iamSvc iamdriver.IAM,
	providerName string,
) {
	t.Helper()

	ctx := context.Background()

	// Create instance profile
	profile, err := iamSvc.CreateInstanceProfile(ctx, iamdriver.InstanceProfileConfig{
		Name: "web-server-profile",
		Tags: map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("[%s] CreateInstanceProfile: %v", providerName, err)
	}

	if profile.Name != "web-server-profile" {
		t.Errorf("[%s] expected name web-server-profile, got %s", providerName, profile.Name)
	}

	if profile.ARN == "" {
		t.Errorf("[%s] expected non-empty ARN", providerName)
	}

	if profile.ID == "" {
		t.Errorf("[%s] expected non-empty ID", providerName)
	}

	if profile.CreatedAt == "" {
		t.Errorf("[%s] expected non-empty CreatedAt", providerName)
	}

	// Duplicate should fail
	_, err = iamSvc.CreateInstanceProfile(ctx, iamdriver.InstanceProfileConfig{
		Name: "web-server-profile",
	})
	if err == nil {
		t.Errorf("[%s] expected error for duplicate instance profile", providerName)
	}

	// Empty name should fail
	_, err = iamSvc.CreateInstanceProfile(ctx, iamdriver.InstanceProfileConfig{})
	if err == nil {
		t.Errorf("[%s] expected error for empty name", providerName)
	}

	// Get instance profile
	got, err := iamSvc.GetInstanceProfile(ctx, "web-server-profile")
	if err != nil {
		t.Fatalf("[%s] GetInstanceProfile: %v", providerName, err)
	}

	if got.Name != "web-server-profile" {
		t.Errorf("[%s] expected name web-server-profile, got %s", providerName, got.Name)
	}

	// Get non-existent should fail
	_, err = iamSvc.GetInstanceProfile(ctx, "nonexistent")
	if err == nil {
		t.Errorf("[%s] expected error for nonexistent profile", providerName)
	}

	// Create a role to associate
	_, err = iamSvc.CreateRole(ctx, iamdriver.RoleConfig{Name: "ec2-role"})
	if err != nil {
		t.Fatalf("[%s] CreateRole: %v", providerName, err)
	}

	// Add role to instance profile
	if err := iamSvc.AddRoleToInstanceProfile(ctx, "web-server-profile", "ec2-role"); err != nil {
		t.Fatalf("[%s] AddRoleToInstanceProfile: %v", providerName, err)
	}

	// Verify role is set
	got, err = iamSvc.GetInstanceProfile(ctx, "web-server-profile")
	if err != nil {
		t.Fatalf("[%s] GetInstanceProfile after add role: %v", providerName, err)
	}

	if got.RoleName != "ec2-role" {
		t.Errorf("[%s] expected role ec2-role, got %s", providerName, got.RoleName)
	}

	// Add role to nonexistent profile should fail
	if err := iamSvc.AddRoleToInstanceProfile(ctx, "nonexistent", "ec2-role"); err == nil {
		t.Errorf("[%s] expected error adding role to nonexistent profile", providerName)
	}

	// Add nonexistent role should fail
	if err := iamSvc.AddRoleToInstanceProfile(ctx, "web-server-profile", "nonexistent"); err == nil {
		t.Errorf("[%s] expected error adding nonexistent role", providerName)
	}

	// List instance profiles
	profiles, err := iamSvc.ListInstanceProfiles(ctx)
	if err != nil {
		t.Fatalf("[%s] ListInstanceProfiles: %v", providerName, err)
	}

	if len(profiles) != 1 {
		t.Errorf("[%s] expected 1 profile, got %d", providerName, len(profiles))
	}

	// Remove role from instance profile
	if err := iamSvc.RemoveRoleFromInstanceProfile(ctx, "web-server-profile", "ec2-role"); err != nil {
		t.Fatalf("[%s] RemoveRoleFromInstanceProfile: %v", providerName, err)
	}

	// Verify role is cleared
	got, err = iamSvc.GetInstanceProfile(ctx, "web-server-profile")
	if err != nil {
		t.Fatalf("[%s] GetInstanceProfile after remove role: %v", providerName, err)
	}

	if got.RoleName != "" {
		t.Errorf("[%s] expected empty role, got %s", providerName, got.RoleName)
	}

	// Remove role from nonexistent profile should fail
	if err := iamSvc.RemoveRoleFromInstanceProfile(ctx, "nonexistent", "ec2-role"); err == nil {
		t.Errorf("[%s] expected error removing role from nonexistent profile", providerName)
	}

	// Remove wrong role should fail
	if err := iamSvc.RemoveRoleFromInstanceProfile(ctx, "web-server-profile", "wrong-role"); err == nil {
		t.Errorf("[%s] expected error removing wrong role", providerName)
	}

	// Delete instance profile
	if err := iamSvc.DeleteInstanceProfile(ctx, "web-server-profile"); err != nil {
		t.Fatalf("[%s] DeleteInstanceProfile: %v", providerName, err)
	}

	// Delete nonexistent should fail
	if err := iamSvc.DeleteInstanceProfile(ctx, "web-server-profile"); err == nil {
		t.Errorf("[%s] expected error deleting nonexistent profile", providerName)
	}

	// Verify deleted
	_, err = iamSvc.GetInstanceProfile(ctx, "web-server-profile")
	if err == nil {
		t.Errorf("[%s] expected error after deleting profile", providerName)
	}
}

func TestInstanceProfileAWS(t *testing.T) {
	p := NewAWS()
	testInstanceProfileOps(t, p.IAM, "AWS")
}

func TestInstanceProfileAzure(t *testing.T) {
	p := NewAzure()
	testInstanceProfileOps(t, p.IAM, "Azure")
}

func TestInstanceProfileGCP(t *testing.T) {
	p := NewGCP()
	testInstanceProfileOps(t, p.IAM, "GCP")
}
