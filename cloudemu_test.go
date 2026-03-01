package cloudemu

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/NitinKumar004/cloudemu/compute"
	computedriver "github.com/NitinKumar004/cloudemu/compute/driver"
	"github.com/NitinKumar004/cloudemu/config"
	"github.com/NitinKumar004/cloudemu/database/driver"
	dnsdriver "github.com/NitinKumar004/cloudemu/dns/driver"
	cerrors "github.com/NitinKumar004/cloudemu/errors"
	iamdriver "github.com/NitinKumar004/cloudemu/iam/driver"
	"github.com/NitinKumar004/cloudemu/inject"
	mqdriver "github.com/NitinKumar004/cloudemu/messagequeue/driver"
	"github.com/NitinKumar004/cloudemu/metrics"
	mondriver "github.com/NitinKumar004/cloudemu/monitoring/driver"
	netdriver "github.com/NitinKumar004/cloudemu/networking/driver"
	"github.com/NitinKumar004/cloudemu/ratelimit"
	"github.com/NitinKumar004/cloudemu/recorder"
	"github.com/NitinKumar004/cloudemu/storage"
	storagedriver "github.com/NitinKumar004/cloudemu/storage/driver"
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
