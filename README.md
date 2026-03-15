# cloudemu

**Zero-cost, in-memory cloud emulation of AWS, Azure, and GCP services for Go .**

Stop paying for cloud infrastructure in your test suite. `cloudemu` gives you lightweight, thread-safe, fully in-memory emulations of 10 cloud services across all three major providers — tests run in ~10ms at zero cost.

```go
import "github.com/NitinKumar004/cloudemu"

func TestMyApp(t *testing.T) {
    aws := cloudemu.NewAWS()

    aws.S3.CreateBucket(ctx, "my-bucket")
    aws.S3.PutObject(ctx, "my-bucket", "key", []byte("hello"), "text/plain", nil)

    obj, _ := aws.S3.GetObject(ctx, "my-bucket", "key")
    // obj.Data == []byte("hello")
}
```

---

## Table of Contents

- [Why cloudemu?](#why-cloudemu)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Architecture](#architecture)
- [Supported Services](#supported-services)
- [Usage Guide](#usage-guide)
  - [Creating Providers](#creating-providers)
  - [Storage (S3 / Blob Storage / GCS)](#storage)
  - [Compute (EC2 / VMs / GCE)](#compute)
  - [Database (DynamoDB / CosmosDB / Firestore)](#database)
  - [Serverless (Lambda / Functions / Cloud Functions)](#serverless)
  - [Networking (VPC / VNet / GCP VPC)](#networking)
  - [Monitoring (CloudWatch / Azure Monitor / Cloud Monitoring)](#monitoring)
  - [IAM](#iam)
  - [DNS (Route53 / Azure DNS / Cloud DNS)](#dns)
  - [Load Balancer (ELB / Azure LB / GCP LB)](#load-balancer)
  - [Message Queue (SQS / Service Bus / Pub/Sub)](#message-queue)
- [Realistic Cloud Behaviors](#realistic-cloud-behaviors)
  - [Auto-Metric Generation](#auto-metric-generation)
  - [Alarm Auto-Evaluation](#alarm-auto-evaluation)
  - [IAM Policy Evaluation](#iam-policy-evaluation)
  - [FIFO Deduplication](#fifo-deduplication)
  - [Numeric-Aware Comparisons](#numeric-aware-comparisons)
  - [Dead-Letter Queues](#dead-letter-queues)
  - [Serverless Triggers (SQS → Lambda)](#serverless-triggers)
- [Cost Simulation](#cost-simulation)
- [Cross-Cutting Features](#cross-cutting-features)
  - [Call Recording (VCR Pattern)](#call-recording)
  - [Error Injection](#error-injection)
  - [Rate Limiting](#rate-limiting)
  - [Metrics Collection](#metrics-collection)
  - [Fake Clock (Deterministic Time)](#fake-clock)
  - [Latency Simulation](#latency-simulation)
- [Portable API (Cloud-Agnostic)](#portable-api)
- [Test Helper Suite](#test-helper-suite)
- [Configuration Options](#configuration-options)
- [Error Handling](#error-handling)
- [Package Reference](#package-reference)

---

## Why cloudemu?

| Approach | Cost | Speed | Fidelity | Offline |
|----------|------|-------|----------|---------|
| Real cloud (AWS/Azure/GCP) | $$$ | Slow (seconds) | Perfect | No |
| LocalStack / Emulators | $ | Medium (100ms+) | Good | Yes |
| **cloudemu** | **Free** | **Fast (~10ms)** | **Good** | **Yes** |

- **Zero cost** — no cloud accounts, no Docker containers, no network calls
- **Fast** — pure in-memory Go, no I/O
- **Thread-safe** — all stores use `sync.RWMutex`, safe for parallel tests
- **Deterministic** — fake clock, error injection, rate limiting — full control over test conditions
- **Realistic** — VMs auto-generate metrics, alarms auto-evaluate, IAM checks real policy documents, FIFO queues deduplicate, dead-letter queues, serverless triggers
- **Cost tracking** — simulate cloud billing with per-operation cost estimation
- **Three providers** — same interface, same patterns, write once test everywhere
- **10 services each** — Storage, Compute, Database, Serverless, Networking, Monitoring, IAM, DNS, Load Balancer, Message Queue

---

## Installation

```bash
go get github.com/NitinKumar004/cloudemu
```

Requires Go 1.25.0+.

---

## Quick Start

### Test AWS S3

```go
package myapp_test

import (
    "context"
    "testing"

    "github.com/NitinKumar004/cloudemu"
    "github.com/NitinKumar004/cloudemu/storage/driver"
)

func TestUploadFile(t *testing.T) {
    ctx := context.Background()
    aws := cloudemu.NewAWS()

    // Create bucket and upload
    aws.S3.CreateBucket(ctx, "uploads")
    aws.S3.PutObject(ctx, "uploads", "photo.jpg", []byte("image-data"), "image/jpeg", nil)

    // Retrieve and verify
    obj, err := aws.S3.GetObject(ctx, "uploads", "photo.jpg")
    if err != nil {
        t.Fatal(err)
    }
    if string(obj.Data) != "image-data" {
        t.Errorf("unexpected data: %s", obj.Data)
    }

    // List with prefix
    result, _ := aws.S3.ListObjects(ctx, "uploads", driver.ListOptions{Prefix: "photo"})
    if len(result.Objects) != 1 {
        t.Errorf("expected 1 object, got %d", len(result.Objects))
    }
}
```

### Test EC2 Lifecycle

```go
func TestVMLifecycle(t *testing.T) {
    ctx := context.Background()
    aws := cloudemu.NewAWS()

    // Launch instance — auto-generates CloudWatch metrics (CPU, Network, Disk)
    instances, _ := aws.EC2.RunInstances(ctx, computedriver.InstanceConfig{
        ImageID: "ami-123", InstanceType: "t2.micro",
        Tags: map[string]string{"env": "test"},
    }, 1)
    id := instances[0].ID
    // State is "running"

    // Stop it
    aws.EC2.StopInstances(ctx, []string{id})

    // Verify state
    descs, _ := aws.EC2.DescribeInstances(ctx, []string{id}, nil)
    if descs[0].State != "stopped" {
        t.Error("expected stopped")
    }

    // Terminate
    aws.EC2.TerminateInstances(ctx, []string{id})

    // Can't stop a terminated instance
    err := aws.EC2.StopInstances(ctx, []string{id})
    // err != nil — FailedPrecondition
}
```

### Test Across All Three Providers

```go
func TestCrossCloud(t *testing.T) {
    ctx := context.Background()

    providers := []struct {
        name    string
        storage storagedriver.Bucket
    }{
        {"aws", cloudemu.NewAWS().S3},
        {"azure", cloudemu.NewAzure().BlobStorage},
        {"gcp", cloudemu.NewGCP().GCS},
    }

    for _, p := range providers {
        t.Run(p.name, func(t *testing.T) {
            p.storage.CreateBucket(ctx, "test-bucket")
            p.storage.PutObject(ctx, "test-bucket", "key", []byte("data"), "", nil)
            obj, _ := p.storage.GetObject(ctx, "test-bucket", "key")
            if string(obj.Data) != "data" {
                t.Error("mismatch")
            }
        })
    }
}
```

---

## Architecture

cloudemu uses a **three-layer design** inspired by [Go CDK](https://gocloud.dev):

```
┌─────────────────────────────────────────────────────────┐
│              Your Application / Test Code                │
├─────────────────────────────────────────────────────────┤
│                                                          │
│   Layer 1: Portable API                                  │
│   ┌──────────────────────────────────────────────────┐  │
│   │  storage.Bucket  │  compute.Compute  │  ...      │  │
│   │                                                   │  │
│   │  Cross-cutting pipeline per call:                 │  │
│   │  error-inject → rate-limit → latency →            │  │
│   │  call-driver → collect-metrics → record-call      │  │
│   └──────────────────────────────────────────────────┘  │
│                          │                               │
│   Layer 2: Driver Interfaces                             │
│   ┌──────────────────────────────────────────────────┐  │
│   │  storage/driver.Bucket                            │  │
│   │  compute/driver.Compute                           │  │
│   │  database/driver.Database                         │  │
│   │  ... (10 service contracts)                       │  │
│   └──────────────────────────────────────────────────┘  │
│            │              │               │              │
│   Layer 3: Provider Implementations                      │
│   ┌──────────┐  ┌────────────┐  ┌───────────┐          │
│   │   AWS    │  │   Azure    │  │    GCP    │          │
│   │  s3      │  │ blobstorage│  │   gcs     │          │
│   │  ec2     │  │ vms        │  │   gce     │          │
│   │  dynamodb│  │ cosmosdb   │  │ firestore │          │
│   │  ...     │  │ ...        │  │   ...     │          │
│   └──────────┘  └────────────┘  └───────────┘          │
│         All backed by generic memstore.Store[V]          │
└─────────────────────────────────────────────────────────┘
```

### Layer 1: Portable API

High-level types (`storage.Bucket`, `compute.Compute`, etc.) that wrap any driver with cross-cutting concerns. Every method call runs through this pipeline:

1. **Error Injection** — check if a test-configured error should be returned
2. **Rate Limiting** — check token bucket, return `Throttled` if exhausted
3. **Latency Simulation** — sleep for configured duration
4. **Driver Call** — execute the actual in-memory operation
5. **Metrics Collection** — record call count, duration, error count
6. **Call Recording** — save full call details for later assertion

### Layer 2: Driver Interfaces

Minimal Go interfaces that define the contract each provider must implement. These live in `*/driver/driver.go` files. Example:

```go
// storage/driver/driver.go
type Bucket interface {
    CreateBucket(ctx context.Context, name string) error
    PutObject(ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error
    GetObject(ctx context.Context, bucket, key string) (*Object, error)
    // ... more methods
}
```

### Layer 3: Provider Implementations

In-memory backends for each cloud service. All use the generic `memstore.Store[V]` for thread-safe storage and implement compile-time interface checks:

```go
var _ driver.Bucket = (*Mock)(nil)  // compile-time guarantee
```

### Key Internal Components

| Package | Purpose |
|---------|---------|
| `internal/memstore` | Generic thread-safe `Store[V]` — the backing store for everything |
| `internal/idgen` | ID generators for AWS ARNs, Azure resource IDs, GCP self-links |
| `statemachine` | Generic FSM for compute lifecycle (pending→running→stopped→terminated) |
| `pagination` | Generic `Paginate[T]` with base64 page tokens |

---

## Supported Services

| # | Service | AWS | Azure | GCP |
|---|---------|-----|-------|-----|
| 1 | **Storage** | S3 | Blob Storage | Cloud Storage (GCS) |
| 2 | **Compute** | EC2 | Virtual Machines | Compute Engine (GCE) |
| 3 | **Database** | DynamoDB | CosmosDB | Firestore |
| 4 | **Serverless** | Lambda | Functions | Cloud Functions |
| 5 | **Networking** | VPC | VNet | GCP VPC |
| 6 | **Monitoring** | CloudWatch | Azure Monitor | Cloud Monitoring |
| 7 | **IAM** | IAM | Azure IAM | GCP IAM |
| 8 | **DNS** | Route53 | Azure DNS | Cloud DNS |
| 9 | **Load Balancer** | ELB | Azure LB | GCP LB |
| 10 | **Message Queue** | SQS | Service Bus | Pub/Sub |

---

## Usage Guide

### Creating Providers

```go
// Quick — default configuration
aws := cloudemu.NewAWS()
azure := cloudemu.NewAzure()
gcp := cloudemu.NewGCP()

// With options
aws := cloudemu.NewAWS(
    config.WithRegion("eu-west-1"),
    config.WithAccountID("999888777666"),
)

gcp := cloudemu.NewGCP(
    config.WithProjectID("my-gcp-project"),
    config.WithClock(config.NewFakeClock(time.Now())),
)
```

### Storage

All three providers implement the same `storage/driver.Bucket` interface.

```go
ctx := context.Background()
s3 := cloudemu.NewAWS().S3

// Bucket operations
s3.CreateBucket(ctx, "my-bucket")
s3.DeleteBucket(ctx, "my-bucket")  // must be empty
buckets, _ := s3.ListBuckets(ctx)

// Object operations
s3.PutObject(ctx, "my-bucket", "path/to/file.txt", []byte("content"), "text/plain",
    map[string]string{"author": "alice"})

obj, _ := s3.GetObject(ctx, "my-bucket", "path/to/file.txt")
// obj.Data, obj.Info.ContentType, obj.Info.ETag, obj.Info.Metadata

info, _ := s3.HeadObject(ctx, "my-bucket", "path/to/file.txt")
// info.Size, info.ContentType — without downloading data

s3.DeleteObject(ctx, "my-bucket", "path/to/file.txt")

// List with prefix and delimiter (directory-like listing)
result, _ := s3.ListObjects(ctx, "my-bucket", driver.ListOptions{
    Prefix:    "path/to/",
    Delimiter: "/",
    MaxKeys:   10,
})
// result.Objects       — files directly under path/to/
// result.CommonPrefixes — "subdirectories" under path/to/
// result.NextPageToken — for pagination
// result.IsTruncated   — whether more results exist

// Copy between buckets
s3.CopyObject(ctx, "dst-bucket", "copy.txt", driver.CopySource{
    Bucket: "my-bucket", Key: "path/to/file.txt",
})

// Pagination
page1, _ := s3.ListObjects(ctx, "bucket", driver.ListOptions{MaxKeys: 10})
page2, _ := s3.ListObjects(ctx, "bucket", driver.ListOptions{
    MaxKeys: 10, PageToken: page1.NextPageToken,
})
```

### Compute

VM lifecycle with enforced state machine transitions. Creating a VM automatically generates monitoring metrics (CPU, Network, Disk).

```go
ec2 := cloudemu.NewAWS().EC2

// Launch instances — auto-generates CloudWatch metrics
instances, _ := ec2.RunInstances(ctx, computedriver.InstanceConfig{
    ImageID:      "ami-12345",
    InstanceType: "t2.micro",
    Tags:         map[string]string{"env": "staging"},
    SubnetID:     "subnet-abc",
}, 2) // launch 2 instances
// instances[0].State == "running"
// instances[0].PrivateIP == "10.0.0.1" (auto-assigned)

id := instances[0].ID

// Stop → Start → Terminate lifecycle
ec2.StopInstances(ctx, []string{id})    // running → stopping → stopped
ec2.StartInstances(ctx, []string{id})   // stopped → pending → running
ec2.RebootInstances(ctx, []string{id})  // running → restarting → running
ec2.TerminateInstances(ctx, []string{id}) // → shutting-down → terminated

// Describe with filters
running, _ := ec2.DescribeInstances(ctx, nil, []computedriver.DescribeFilter{
    {Name: "instance-state-name", Values: []string{"running"}},
    {Name: "tag:env", Values: []string{"staging"}},
})

// Modify (only when stopped)
ec2.StopInstances(ctx, []string{id})
ec2.ModifyInstance(ctx, id, computedriver.ModifyInstanceInput{
    InstanceType: "t2.large",
})
```

**State machine enforces legal transitions:**
```
pending → running → stopping → stopped → pending (restart cycle)
                  → shutting-down → terminated (terminal state)
                  → restarting → running (reboot)
stopped → shutting-down → terminated
```

Illegal transitions return a `FailedPrecondition` error — you can't stop a terminated instance.

**Auto-generated metrics per instance (AWS example):**

| Metric | Namespace | Value |
|--------|-----------|-------|
| CPUUtilization | AWS/EC2 | 25.0 |
| NetworkIn | AWS/EC2 | 1024.0 |
| NetworkOut | AWS/EC2 | 512.0 |
| DiskReadOps | AWS/EC2 | 100.0 |
| DiskWriteOps | AWS/EC2 | 50.0 |

Each metric gets 5 backfill datapoints at 1-minute intervals from launch time.

### Database

DynamoDB-style key-value with queries, scans, and batch operations. Supports numeric-aware comparisons and full operator set.

```go
db := cloudemu.NewAWS().DynamoDB

// Create table with sort key and GSI
db.CreateTable(ctx, dbdriver.TableConfig{
    Name:         "orders",
    PartitionKey: "customer_id",
    SortKey:      "order_date",
    GSIs: []dbdriver.GSIConfig{
        {Name: "status-index", PartitionKey: "status", SortKey: "order_date"},
    },
})

// Put items
db.PutItem(ctx, "orders", map[string]interface{}{
    "customer_id": "cust-1",
    "order_date":  "2024-01-15",
    "status":      "shipped",
    "total":       "99.99",
})

// Get by key
item, _ := db.GetItem(ctx, "orders", map[string]interface{}{
    "customer_id": "cust-1",
    "order_date":  "2024-01-15",
})

// Query with key conditions
result, _ := db.Query(ctx, dbdriver.QueryInput{
    Table: "orders",
    KeyCondition: dbdriver.KeyCondition{
        PartitionKey: "customer_id",
        PartitionVal: "cust-1",
        SortOp:       ">=",
        SortVal:      "2024-01-01",
    },
})

// Query using GSI
result, _ = db.Query(ctx, dbdriver.QueryInput{
    Table:     "orders",
    IndexName: "status-index",
    KeyCondition: dbdriver.KeyCondition{
        PartitionKey: "status",
        PartitionVal: "shipped",
    },
})

// Scan with filter — supports full operator set
result, _ = db.Scan(ctx, dbdriver.ScanInput{
    Table: "orders",
    Filters: []dbdriver.ScanFilter{
        {Field: "total", Op: ">=", Value: "50"},  // numeric-aware: 99.99 >= 50
    },
    Limit: 10,
})

// Batch operations
db.BatchPutItems(ctx, "orders", items)
db.BatchGetItems(ctx, "orders", keys)
```

**Supported sort key operators:** `=`, `<`, `>`, `<=`, `>=`, `BEGINS_WITH`, `BETWEEN`

**Supported scan filter operators:** `=`, `!=`, `<`, `>`, `<=`, `>=`, `CONTAINS`, `BEGINS_WITH`

**Numeric-aware comparisons:** When both values can be parsed as numbers, comparison uses numeric ordering. `"10" > "9"` correctly returns `true` (not string-compared as `"10" < "9"`).

### Serverless

```go
lambda := cloudemu.NewAWS().Lambda

// Register a handler BEFORE or AFTER creating the function
lambda.RegisterHandler("my-func", func(ctx context.Context, payload []byte) ([]byte, error) {
    return []byte(`{"result": "ok"}`), nil
})

// Create function
lambda.CreateFunction(ctx, sdriver.FunctionConfig{
    Name: "my-func", Runtime: "go1.x", Handler: "main",
    Memory: 128, Timeout: 30,
})

// Invoke
output, _ := lambda.Invoke(ctx, sdriver.InvokeInput{
    FunctionName: "my-func",
    Payload:      []byte(`{"key": "value"}`),
})
// output.StatusCode == 200
// output.Payload == []byte(`{"result": "ok"}`)
```

### Networking

```go
vpc := cloudemu.NewAWS().VPC

// Create VPC
vpcInfo, _ := vpc.CreateVPC(ctx, netdriver.VPCConfig{
    CIDRBlock: "10.0.0.0/16",
    Tags:      map[string]string{"env": "test"},
})

// Create subnet
subnet, _ := vpc.CreateSubnet(ctx, netdriver.SubnetConfig{
    VPCID:            vpcInfo.ID,
    CIDRBlock:        "10.0.1.0/24",
    AvailabilityZone: "us-east-1a",
})

// Create security group with rules
sg, _ := vpc.CreateSecurityGroup(ctx, netdriver.SecurityGroupConfig{
    Name: "web-sg", Description: "Web traffic", VPCID: vpcInfo.ID,
})
vpc.AddIngressRule(ctx, sg.ID, netdriver.SecurityRule{
    Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "0.0.0.0/0",
})
```

### Monitoring

Alarms auto-evaluate when metric data is pushed — they transition to `"ALARM"` or `"OK"` automatically.

```go
cw := cloudemu.NewAWS().CloudWatch

// Put metric data
cw.PutMetricData(ctx, []mondriver.MetricDatum{
    {Namespace: "MyApp", MetricName: "RequestCount", Value: 42,
     Dimensions: map[string]string{"API": "/users"}, Timestamp: time.Now()},
})

// Query metrics
result, _ := cw.GetMetricData(ctx, mondriver.GetMetricInput{
    Namespace: "MyApp", MetricName: "RequestCount",
    StartTime: time.Now().Add(-1*time.Hour), EndTime: time.Now(),
    Period: 300, Stat: "Sum",
})

// List available metric names
names, _ := cw.ListMetrics(ctx, "MyApp")

// Alarms — auto-evaluate when metric data arrives
cw.CreateAlarm(ctx, mondriver.AlarmConfig{
    Name: "HighErrors", Namespace: "MyApp", MetricName: "ErrorCount",
    ComparisonOperator: "GreaterThanThreshold", Threshold: 100,
    Period: 300, EvaluationPeriods: 1, Stat: "Average",
})

// Push data above threshold — alarm auto-transitions to "ALARM"
cw.PutMetricData(ctx, []mondriver.MetricDatum{
    {Namespace: "MyApp", MetricName: "ErrorCount", Value: 150, Timestamp: time.Now()},
})

alarms, _ := cw.DescribeAlarms(ctx, []string{"HighErrors"})
// alarms[0].State == "ALARM"

// Push data below threshold — alarm auto-transitions to "OK"
cw.PutMetricData(ctx, []mondriver.MetricDatum{
    {Namespace: "MyApp", MetricName: "ErrorCount", Value: 50, Timestamp: time.Now()},
})

alarms, _ = cw.DescribeAlarms(ctx, []string{"HighErrors"})
// alarms[0].State == "OK"

// You can also set alarm state manually
cw.SetAlarmState(ctx, "HighErrors", "ALARM", "Manual override")
```

**Supported comparison operators:** `GreaterThanThreshold`, `LessThanThreshold`, `GreaterThanOrEqualToThreshold`, `LessThanOrEqualToThreshold`

**Supported statistics:** `Average`, `Sum`, `Minimum`, `Maximum`, `SampleCount`

### IAM

IAM evaluates real JSON policy documents with wildcard matching. Explicit `Deny` always overrides `Allow`.

```go
iam := cloudemu.NewAWS().IAM

// Create user, role, policy
user, _ := iam.CreateUser(ctx, iamdriver.UserConfig{Name: "alice"})
role, _ := iam.CreateRole(ctx, iamdriver.RoleConfig{Name: "admin-role"})

// Create policy with JSON document
policy, _ := iam.CreatePolicy(ctx, iamdriver.PolicyConfig{
    Name: "s3-read-policy",
    PolicyDocument: `{
        "Version": "2012-10-17",
        "Statement": [
            {
                "Effect": "Allow",
                "Action": ["s3:GetObject", "s3:ListBucket"],
                "Resource": "*"
            }
        ]
    }`,
})

// Attach policies
iam.AttachUserPolicy(ctx, "alice", policy.ARN)
iam.AttachRolePolicy(ctx, "admin-role", policy.ARN)

// Check permissions — evaluates JSON policy with wildcard matching
allowed, _ := iam.CheckPermission(ctx, "alice", "s3:GetObject", "arn:aws:s3:::my-bucket/file.txt")
// allowed == true (matches "s3:GetObject" with Resource "*")

allowed, _ = iam.CheckPermission(ctx, "alice", "ec2:RunInstances", "*")
// allowed == false (not in policy)

// Wildcard actions
adminPolicy, _ := iam.CreatePolicy(ctx, iamdriver.PolicyConfig{
    Name: "admin-policy",
    PolicyDocument: `{
        "Version": "2012-10-17",
        "Statement": [{"Effect": "Allow", "Action": "s3:*", "Resource": "*"}]
    }`,
})
iam.AttachUserPolicy(ctx, "alice", adminPolicy.ARN)
allowed, _ = iam.CheckPermission(ctx, "alice", "s3:PutObject", "*")
// allowed == true (wildcard "s3:*" matches "s3:PutObject")

// Explicit Deny overrides Allow
denyPolicy, _ := iam.CreatePolicy(ctx, iamdriver.PolicyConfig{
    Name: "deny-delete",
    PolicyDocument: `{
        "Version": "2012-10-17",
        "Statement": [{"Effect": "Deny", "Action": "s3:DeleteObject", "Resource": "*"}]
    }`,
})
iam.AttachUserPolicy(ctx, "alice", denyPolicy.ARN)
allowed, _ = iam.CheckPermission(ctx, "alice", "s3:DeleteObject", "*")
// allowed == false (explicit Deny wins over any Allow)
```

### DNS

```go
dns := cloudemu.NewAWS().Route53

zone, _ := dns.CreateZone(ctx, dnsdriver.ZoneConfig{Name: "example.com"})
dns.CreateRecord(ctx, dnsdriver.RecordConfig{
    ZoneID: zone.ID, Name: "www.example.com", Type: "A",
    TTL: 300, Values: []string{"1.2.3.4"},
})

// Weighted routing
weight80 := 80
weight20 := 20
dns.CreateRecord(ctx, dnsdriver.RecordConfig{
    ZoneID: zone.ID, Name: "api.example.com", Type: "A",
    Values: []string{"1.1.1.1"}, Weight: &weight80, SetID: "primary",
})
dns.CreateRecord(ctx, dnsdriver.RecordConfig{
    ZoneID: zone.ID, Name: "api.example.com", Type: "A",
    Values: []string{"2.2.2.2"}, Weight: &weight20, SetID: "secondary",
})
```

### Load Balancer

```go
elb := cloudemu.NewAWS().ELB

lb, _ := elb.CreateLoadBalancer(ctx, lbdriver.LBConfig{
    Name: "web-lb", Type: "application", Scheme: "internet-facing",
})
tg, _ := elb.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{
    Name: "web-targets", Protocol: "HTTP", Port: 80, HealthPath: "/health",
})
elb.CreateListener(ctx, lbdriver.ListenerConfig{
    LBARN: lb.ARN, Protocol: "HTTPS", Port: 443, TargetGroupARN: tg.ARN,
})

// Register targets and manage health
elb.RegisterTargets(ctx, tg.ARN, []lbdriver.Target{
    {ID: "i-001", Port: 80}, {ID: "i-002", Port: 80},
})
elb.SetTargetHealth(ctx, tg.ARN, "i-001", "healthy")

health, _ := elb.DescribeTargetHealth(ctx, tg.ARN)
```

### Message Queue

FIFO queues automatically deduplicate messages with the same `DeduplicationID` within a 5-minute window.

```go
sqs := cloudemu.NewAWS().SQS

queue, _ := sqs.CreateQueue(ctx, mqdriver.QueueConfig{
    Name: "orders", VisibilityTimeout: 30,
})

// Send
sqs.SendMessage(ctx, mqdriver.SendMessageInput{
    QueueURL: queue.URL, Body: `{"orderId": "123"}`,
    Attributes: map[string]string{"type": "order"},
})

// Receive
msgs, _ := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{
    QueueURL: queue.URL, MaxMessages: 5,
})
// msgs[0].Body, msgs[0].MessageID, msgs[0].ReceiptHandle

// Delete after processing
sqs.DeleteMessage(ctx, queue.URL, msgs[0].ReceiptHandle)

// FIFO queues with deduplication
fifo, _ := sqs.CreateQueue(ctx, mqdriver.QueueConfig{
    Name: "orders.fifo", FIFO: true,
})

out1, _ := sqs.SendMessage(ctx, mqdriver.SendMessageInput{
    QueueURL: fifo.URL, Body: "order-123",
    GroupID: "group-1", DeduplicationID: "dedup-abc",
})

// Same DeduplicationID within 5 minutes — returns same MessageID, no duplicate
out2, _ := sqs.SendMessage(ctx, mqdriver.SendMessageInput{
    QueueURL: fifo.URL, Body: "order-123",
    GroupID: "group-1", DeduplicationID: "dedup-abc",
})
// out1.MessageID == out2.MessageID — only 1 message in queue

// After 5 minutes, same DeduplicationID is accepted as a new message

// Dead-letter queue — messages that fail processing are moved automatically
dlq, _ := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "orders-dlq"})
mainQ, _ := sqs.CreateQueue(ctx, mqdriver.QueueConfig{
    Name: "orders-main",
    DeadLetterQueue: &mqdriver.DeadLetterConfig{
        TargetQueueURL:  dlq.URL,
        MaxReceiveCount: 3, // move to DLQ after 3 failed receives
    },
})

// Serverless trigger — automatically invoke Lambda when message arrives
aws.SQS.SetTrigger(mainQ.URL, func(queueURL string, msg mqdriver.Message) {
    aws.Lambda.Invoke(ctx, sdriver.InvokeInput{
        FunctionName: "order-processor",
        Payload:      []byte(msg.Body),
    })
})
// Now every SendMessage automatically invokes "order-processor"
```

---

## Realistic Cloud Behaviors

cloudemu goes beyond basic CRUD mocks. These behaviors make it behave like real cloud services.

### Auto-Metric Generation

When you launch a VM, monitoring metrics are automatically generated — just like real AWS/Azure/GCP.

```go
aws := cloudemu.NewAWS()
aws.EC2.RunInstances(ctx, computedriver.InstanceConfig{
    ImageID: "ami-123", InstanceType: "t2.micro",
}, 1)

// Metrics are immediately available — no manual PutMetricData needed
names, _ := aws.CloudWatch.ListMetrics(ctx, "AWS/EC2")
// names == ["CPUUtilization", "DiskReadOps", "DiskWriteOps", "NetworkIn", "NetworkOut"]
```

**Metrics generated per provider:**

| Provider | Namespace | Metrics |
|----------|-----------|---------|
| AWS | `AWS/EC2` | CPUUtilization, NetworkIn, NetworkOut, DiskReadOps, DiskWriteOps |
| Azure | `Microsoft.Compute/virtualMachines` | Percentage CPU, Network In Total, Network Out Total, Disk Read Operations/Sec, Disk Write Operations/Sec |
| GCP | `compute.googleapis.com` | instance/cpu/utilization, instance/network/received_bytes_count, instance/network/sent_bytes_count, instance/disk/read_ops_count, instance/disk/write_ops_count |

### Alarm Auto-Evaluation

Alarms automatically evaluate against incoming metric data. No need to manually call `SetAlarmState`.

```go
cw := cloudemu.NewAWS().CloudWatch

// Create alarm
cw.CreateAlarm(ctx, mondriver.AlarmConfig{
    Name: "high-cpu", Namespace: "AWS/EC2", MetricName: "CPUUtilization",
    ComparisonOperator: "GreaterThanThreshold", Threshold: 80,
    Period: 300, EvaluationPeriods: 1, Stat: "Average",
})

// Push metric above threshold
cw.PutMetricData(ctx, []mondriver.MetricDatum{
    {Namespace: "AWS/EC2", MetricName: "CPUUtilization", Value: 95, Timestamp: time.Now()},
})

// Alarm automatically transitions to "ALARM"
alarms, _ := cw.DescribeAlarms(ctx, []string{"high-cpu"})
// alarms[0].State == "ALARM"
```

This also works with auto-generated VM metrics — create an alarm on CPU, launch a VM, and the alarm evaluates automatically.

### IAM Policy Evaluation

`CheckPermission` parses real JSON policy documents and evaluates them with wildcard matching.

- **Allow/Deny evaluation:** Each attached policy's `Statement` array is checked
- **Wildcard matching:** `"s3:*"` matches `"s3:GetObject"`, `"s3:PutObject"`, etc.
- **Explicit Deny wins:** If any policy has `"Effect": "Deny"` matching the action/resource, the result is always `false`
- **Default deny:** If no Allow statement matches, permission is denied

### FIFO Deduplication

FIFO queues (SQS, Service Bus, Pub/Sub) enforce a 5-minute deduplication window based on `DeduplicationID`:

- Same `DeduplicationID` within 5 minutes → returns existing `MessageID`, no duplicate created
- After 5 minutes → accepted as a new message

### Numeric-Aware Comparisons

Database scan filters and query conditions compare values numerically when both sides are valid numbers:

```go
// "10" > "9" → true (numeric comparison)
// Without this, string comparison would give "10" < "9" (wrong)
```

This applies to all comparison operators (`<`, `>`, `<=`, `>=`, `BETWEEN`) in DynamoDB, CosmosDB, and Firestore mocks.

### Dead-Letter Queues

Messages that fail processing too many times are automatically moved to a dead-letter queue — just like real AWS SQS, Azure Service Bus, and GCP Pub/Sub.

```go
sqs := cloudemu.NewAWS().SQS

// Create DLQ
dlq, _ := sqs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "my-dlq"})

// Create main queue with DLQ configured
mainQ, _ := sqs.CreateQueue(ctx, mqdriver.QueueConfig{
    Name: "my-queue",
    DeadLetterQueue: &mqdriver.DeadLetterConfig{
        TargetQueueURL:  dlq.URL,
        MaxReceiveCount: 3, // move to DLQ after 3 receives without deletion
    },
})

// Send a message
sqs.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: mainQ.URL, Body: "process me"})

// Simulate failed processing — receive 3 times without deleting
for i := 0; i < 3; i++ {
    msgs, _ := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: mainQ.URL})
    // Don't delete — simulating failure
    // Wait for visibility timeout to expire...
}

// On next receive, message is gone from main queue — moved to DLQ
msgs, _ := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: mainQ.URL})
// len(msgs) == 0

// Message is now in the DLQ
dlqMsgs, _ := sqs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: dlq.URL})
// dlqMsgs[0].Body == "process me"
```

Works identically across all three providers (SQS, Service Bus, Pub/Sub).

### Serverless Triggers

Register a trigger on a message queue — when a message is sent, a serverless function is automatically invoked. This emulates real event source mappings (AWS SQS → Lambda, Azure Service Bus → Functions, GCP Pub/Sub → Cloud Functions).

```go
aws := cloudemu.NewAWS()

// Register Lambda handler
aws.Lambda.RegisterHandler("processor", func(ctx context.Context, payload []byte) ([]byte, error) {
    // Process the message
    return []byte("done"), nil
})
aws.Lambda.CreateFunction(ctx, sdriver.FunctionConfig{Name: "processor", Runtime: "go1.x", Handler: "main"})

// Create queue
q, _ := aws.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "events"})

// Wire trigger: SQS → Lambda
aws.SQS.SetTrigger(q.URL, func(queueURL string, msg mqdriver.Message) {
    aws.Lambda.Invoke(ctx, sdriver.InvokeInput{
        FunctionName: "processor",
        Payload:      []byte(msg.Body),
    })
})

// Now every SendMessage automatically invokes the Lambda function
aws.SQS.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: q.URL, Body: "event-data"})
// Lambda "processor" is invoked with payload "event-data"

// Remove trigger when no longer needed
aws.SQS.RemoveTrigger(q.URL)
```

Works across all three providers:
- **AWS:** `SQS.SetTrigger()` → invokes Lambda
- **Azure:** `ServiceBus.SetTrigger()` → invokes Azure Functions
- **GCP:** `PubSub.SetTrigger()` → invokes Cloud Functions

---

## Cost Simulation

Track simulated cloud costs across all operations with the `cost.Tracker`. No other mock library offers this.

```go
import "github.com/NitinKumar004/cloudemu/cost"

tracker := cost.New()

// Record costs as you use services
tracker.Record("compute", "RunInstances", 3)      // 3 instances
tracker.Record("storage", "PutObject", 1000)       // 1000 uploads
tracker.Record("database", "PutItem", 5000)        // 5000 writes
tracker.Record("serverless", "Invoke", 100000)     // 100K invocations
tracker.Record("messagequeue", "SendMessage", 10000) // 10K messages

// Get total estimated cost
total := tracker.TotalCost()

// Breakdown by service
byService := tracker.CostByService()
// byService["compute"] == 0.0348
// byService["storage"] == 0.005
// byService["serverless"] == 0.02

// Breakdown by operation
byOp := tracker.CostByOperation()
// byOp["compute:RunInstances"] == 0.0348

// Custom pricing — override any rate
tracker.SetRate("compute", "RunInstances", 0.50) // custom instance price

// Reset between tests
tracker.Reset()
```

**Default rates (per unit):**

| Service | Operation | Rate |
|---------|-----------|------|
| Compute | RunInstances | $0.0116/instance |
| Storage | PutObject | $0.000005/op |
| Storage | GetObject | $0.0000004/op |
| Database | PutItem | $0.00000125/op |
| Database | GetItem | $0.00000025/op |
| Serverless | Invoke | $0.0000002/invocation |
| Message Queue | SendMessage | $0.0000004/op |
| Monitoring | PutMetricData | $0.00001/metric |
| Load Balancer | CreateLoadBalancer | $0.0225/hour |

All rates are customizable via `SetRate()`.

---

## Cross-Cutting Features

These features work with any service through the **portable API layer**. Wrap any provider's mock with a portable type to get recording, injection, limiting, metrics, and latency simulation.

### Call Recording

Record every API call for later assertion — the VCR pattern for cloud APIs.

```go
rec := recorder.New()
bucket := storage.NewBucket(aws.S3, storage.WithRecorder(rec))

bucket.CreateBucket(ctx, "test")
bucket.PutObject(ctx, "test", "key", []byte("data"), "", nil)
bucket.GetObject(ctx, "test", "key")

// Assert call counts
if rec.CallCount() != 3 {
    t.Error("expected 3 calls")
}
if rec.CallCountFor("storage", "PutObject") != 1 {
    t.Error("expected 1 PutObject")
}

// Fluent assertions
recorder.NewMatcher(t, rec).
    ForService("storage").
    ForOperation("GetObject").
    Count(1).
    NoErrors()

// Inspect individual calls
last := rec.LastCall()
// last.Service, last.Operation, last.Input, last.Output, last.Error, last.Duration
```

### Error Injection

Simulate cloud failures with configurable policies.

```go
inj := inject.NewInjector()
bucket := storage.NewBucket(aws.S3, storage.WithErrorInjection(inj))

// Policy: Always fail
inj.Set("storage", "GetObject", cerrors.New(cerrors.NotFound, "injected"), inject.Always{})

// Policy: Fail on every 3rd call
inj.Set("storage", "PutObject", cerrors.New(cerrors.Internal, "server error"), inject.NewNthCall(3))

// Policy: Fail with 50% probability
inj.Set("storage", "ListObjects", cerrors.New(cerrors.Unavailable, "flaky"), inject.NewProbabilistic(0.5))

// Policy: Fail first 5 calls then succeed (simulates eventual consistency)
inj.Set("storage", "GetObject", cerrors.New(cerrors.NotFound, "not yet"), inject.NewCountdown(5))

// Remove injection
inj.Remove("storage", "GetObject")

// Reset all rules
inj.Reset()
```

**Available policies:**

| Policy | Behavior | Use Case |
|--------|----------|----------|
| `Always{}` | Every call fails | Test error handling paths |
| `NewNthCall(n)` | Every Nth call fails | Test retry logic |
| `NewProbabilistic(p)` | Fails with probability p | Chaos testing |
| `NewCountdown(n)` | First N calls fail, rest succeed | Test eventual consistency |

### Rate Limiting

Simulate real API throttling with token bucket rate limiting.

```go
clock := config.NewFakeClock(time.Now())
limiter := ratelimit.New(
    2,     // 2 requests per second
    2,     // burst size of 2
    clock, // use fake clock for deterministic testing
)

bucket := storage.NewBucket(aws.S3, storage.WithRateLimiter(limiter))

bucket.PutObject(ctx, "b", "k1", data, "", nil) // OK — token 1
bucket.PutObject(ctx, "b", "k2", data, "", nil) // OK — token 2
err := bucket.PutObject(ctx, "b", "k3", data, "", nil)
// err is Throttled — burst exhausted

clock.Advance(time.Second) // refill tokens
bucket.PutObject(ctx, "b", "k4", data, "", nil) // OK again
```

### Metrics Collection

Track call counts, durations, and error rates.

```go
mc := metrics.NewCollector()
bucket := storage.NewBucket(aws.S3, storage.WithMetrics(mc))

bucket.CreateBucket(ctx, "b")
bucket.PutObject(ctx, "b", "k", data, "", nil)

// Query metrics
q := metrics.NewQuery(mc)

// Total calls
q.ByName("calls_total").Count()  // 2
q.ByName("calls_total").Sum()    // 2.0

// Filter by operation
q.ByName("calls_total").ByLabel("operation", "PutObject").Count()  // 1

// Duration histograms
q.ByName("call_duration").ByLabel("service", "storage").Results()

// Error counts
q.ByName("errors_total").Count()  // 0

mc.Reset() // clear between tests
```

**Metrics recorded per call:**

| Metric | Type | Labels |
|--------|------|--------|
| `calls_total` | Counter | service, operation |
| `call_duration` | Histogram | service, operation |
| `errors_total` | Counter | service, operation |

### Fake Clock (Deterministic Time)

Control time in tests for TTL, expiry, and ordering scenarios.

```go
clock := config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))

aws := cloudemu.NewAWS(config.WithClock(clock))

// All timestamps use fake clock
aws.S3.CreateBucket(ctx, "b")
aws.S3.PutObject(ctx, "b", "k", data, "", nil)
obj, _ := aws.S3.GetObject(ctx, "b", "k")
// obj.Info.LastModified == "2024-01-01T00:00:00Z"

// Advance time
clock.Advance(24 * time.Hour)

// SQS visibility timeouts, CloudWatch time ranges, alarm evaluation windows,
// FIFO deduplication windows — all respect the clock
```

### Latency Simulation

Add realistic delays to test timeout handling and async patterns.

```go
bucket := storage.NewBucket(aws.S3, storage.WithLatency(100*time.Millisecond))

// Each operation takes at least 100ms
bucket.PutObject(ctx, "b", "k", data, "", nil) // sleeps 100ms
```

### Combining Multiple Features

All cross-cutting features compose naturally:

```go
rec := recorder.New()
mc := metrics.NewCollector()
inj := inject.NewInjector()
clock := config.NewFakeClock(time.Now())
limiter := ratelimit.New(10, 10, clock)

bucket := storage.NewBucket(aws.S3,
    storage.WithRecorder(rec),
    storage.WithMetrics(mc),
    storage.WithErrorInjection(inj),
    storage.WithRateLimiter(limiter),
    storage.WithLatency(5*time.Millisecond),
)

// Now every call goes through the full pipeline:
// inject → rate-limit → latency → driver → metrics → record
```

---

## Portable API

The portable API lets you write **cloud-agnostic code** that works with any provider. Your application code depends on the portable types, and tests can swap providers freely.

```go
// Application code — depends on portable storage.Bucket
type FileService struct {
    storage *storage.Bucket
}

func (s *FileService) Upload(ctx context.Context, bucket, key string, data []byte) error {
    return s.storage.PutObject(ctx, bucket, key, data, "application/octet-stream", nil)
}

// Test with AWS
func TestWithAWS(t *testing.T) {
    aws := cloudemu.NewAWS()
    svc := &FileService{storage: storage.NewBucket(aws.S3)}
    // ...
}

// Test with GCP
func TestWithGCP(t *testing.T) {
    gcp := cloudemu.NewGCP()
    svc := &FileService{storage: storage.NewBucket(gcp.GCS)}
    // ...
}

// Or use the factory
func TestWithFactory(t *testing.T) {
    bucket := cloudemu.NewStorage("aws") // or "azure" or "gcp"
    svc := &FileService{storage: bucket}
    // ...
}
```

**Available portable types:**

| Package | Type | Wraps |
|---------|------|-------|
| `storage` | `Bucket` | `storage/driver.Bucket` |
| `compute` | `Compute` | `compute/driver.Compute` |
| `database` | `Database` | `database/driver.Database` |
| `serverless` | `Serverless` | `serverless/driver.Serverless` |
| `networking` | `Networking` | `networking/driver.Networking` |
| `monitoring` | `Monitoring` | `monitoring/driver.Monitoring` |
| `iam` | `IAM` | `iam/driver.IAM` |
| `dns` | `DNS` | `dns/driver.DNS` |
| `loadbalancer` | `LB` | `loadbalancer/driver.LoadBalancer` |
| `messagequeue` | `MQ` | `messagequeue/driver.MessageQueue` |

Each portable type accepts the same cross-cutting options: `WithRecorder`, `WithMetrics`, `WithRateLimiter`, `WithErrorInjection`, `WithLatency`.

---

## Test Helper Suite

The `testhelper` package provides a pre-configured test suite with shared infrastructure.

```go
import "github.com/NitinKumar004/cloudemu/testhelper"

func TestWithSuite(t *testing.T) {
    suite := testhelper.NewSuite()
    // suite.Recorder  — shared call recorder
    // suite.Metrics   — shared metrics collector
    // suite.Injector  — shared error injector
    // suite.Clock     — shared fake clock

    // Create providers with suite's clock
    aws := suite.AWSProvider()
    azure := suite.AzureProvider()
    gcp := suite.GCPProvider()

    // Run test...

    // Reset between test cases
    suite.Reset()
}
```

---

## Configuration Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithClock(clock)` | `RealClock{}` | Clock implementation (use `FakeClock` for deterministic tests) |
| `WithRegion(region)` | `"us-east-1"` | Cloud region for resource IDs and ARNs |
| `WithAccountID(id)` | `"123456789012"` | AWS account ID / Azure subscription ID |
| `WithProjectID(id)` | `"mock-project"` | GCP project ID |
| `WithLatency(d)` | `0` | Global simulated latency |

---

## Error Handling

All operations return errors using canonical error codes:

```go
import cerrors "github.com/NitinKumar004/cloudemu/errors"

_, err := s3.GetObject(ctx, "bucket", "missing-key")

// Check by code
if cerrors.IsNotFound(err) {
    // handle not found
}

// Extract code
code := cerrors.GetCode(err) // cerrors.NotFound

// Available codes
cerrors.NotFound           // resource doesn't exist
cerrors.AlreadyExists      // resource already exists (duplicate create)
cerrors.InvalidArgument    // bad input (empty name, invalid config)
cerrors.FailedPrecondition // illegal state transition, non-empty bucket
cerrors.PermissionDenied   // access denied
cerrors.Throttled          // rate limit exceeded
cerrors.Internal           // unexpected internal error
```

---

## Package Reference

```
github.com/NitinKumar004/cloudemu
├── cloudemu.go            # NewAWS(), NewAzure(), NewGCP()
├── errors/                # Canonical error codes and helpers
├── config/                # Options, Clock, FakeClock
├── cost/                  # Simulated cloud cost tracking
├── recorder/              # Call recording + fluent assertions
├── inject/                # Error injection + policies
├── ratelimit/             # Token bucket rate limiter
├── metrics/               # In-memory metrics + query API
├── statemachine/          # Generic FSM for compute lifecycle
├── pagination/            # Generic paginator with page tokens
├── internal/
│   ├── memstore/          # Generic Store[V] — backing store for all mocks
│   └── idgen/             # AWS ARN, Azure ID, GCP ID generators
├── storage/               # Portable storage API
│   └── driver/            # Storage driver interface
├── compute/               # Portable compute API + VM states
│   └── driver/            # Compute driver interface
├── database/              # Portable database API
│   └── driver/            # Database driver interface
├── serverless/            # Portable serverless API
│   └── driver/            # Serverless driver interface
├── networking/            # Portable networking API
│   └── driver/            # Networking driver interface
├── monitoring/            # Portable monitoring API
│   └── driver/            # Monitoring driver interface
├── iam/                   # Portable IAM API
│   └── driver/            # IAM driver interface
├── dns/                   # Portable DNS API
│   └── driver/            # DNS driver interface
├── loadbalancer/          # Portable load balancer API
│   └── driver/            # Load balancer driver interface
├── messagequeue/          # Portable message queue API
│   └── driver/            # Message queue driver interface
├── providers/
│   ├── aws/               # AWS factory + 10 service mocks
│   ├── azure/             # Azure factory + 10 service mocks
│   └── gcp/               # GCP factory + 10 service mocks
└── testhelper/            # Test suite with shared infrastructure
```
