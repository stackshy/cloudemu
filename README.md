# cloudemu

**Zero-cost, in-memory cloud emulation of AWS, Azure, and GCP for Go.**

cloudemu is a lightweight Go library that provides mock implementations of 30 cloud services (10 each for AWS, Azure, and GCP). It runs entirely in memory — no real cloud accounts, no Docker containers, no network calls needed. Just import the package and start testing your cloud-dependent code instantly.

```go
aws := cloudemu.NewAWS()
azure := cloudemu.NewAzure()
gcp := cloudemu.NewGCP()
```

**Note:** This project is actively under development. We are expanding support for more cloud services and resources across all three providers. Contributions and feedback are welcome!

## Installation

```bash
go get github.com/NitinKumar004/cloudemu
```

Requires Go 1.25.0+.

## Why cloudemu?

Testing cloud-dependent code is painful. You either pay for real cloud accounts, wrestle with heavy emulators like LocalStack that need Docker, or write incomplete mocks from scratch. cloudemu solves all of this — it gives you realistic, thread-safe cloud mocks that run in milliseconds with zero setup.

| Approach | Cost | Speed | Offline |
|----------|------|-------|---------|
| Real cloud (AWS/Azure/GCP) | $$$ | Slow (seconds) | No |
| LocalStack / Emulators | $ | Medium (100ms+) | Yes |
| **cloudemu** | **Free** | **Fast (~10ms)** | **Yes** |

## Supported Services

cloudemu covers 10 cloud services across all three major providers, giving you 30 mock implementations in total.

| Service | AWS | Azure | GCP |
|---------|-----|-------|-----|
| Storage | S3 | Blob Storage | GCS |
| Compute | EC2 | Virtual Machines | GCE |
| Database | DynamoDB | CosmosDB | Firestore |
| Serverless | Lambda | Functions | Cloud Functions |
| Networking | VPC | VNet | GCP VPC |
| Monitoring | CloudWatch | Azure Monitor | Cloud Monitoring |
| IAM | IAM | Azure IAM | GCP IAM |
| DNS | Route53 | Azure DNS | Cloud DNS |
| Load Balancer | ELB | Azure LB | GCP LB |
| Message Queue | SQS | Service Bus | Pub/Sub |

## Quick Start

### Storage

Create buckets, upload objects, list with prefix filtering, and paginate results — all in memory. Works the same way across S3, Azure Blob Storage, and GCS.

```go
aws := cloudemu.NewAWS()
aws.S3.CreateBucket(ctx, "my-bucket")
aws.S3.PutObject(ctx, "my-bucket", "key", []byte("hello"), "text/plain", nil)

obj, _ := aws.S3.GetObject(ctx, "my-bucket", "key")
// obj.Data == []byte("hello")
```

### Compute

Launch virtual machines with a real lifecycle state machine. Instances transition through `pending -> running -> stopping -> stopped -> terminated`, and illegal transitions (like stopping a terminated instance) return errors — just like real cloud. Launching a VM also auto-generates monitoring metrics (CPU, Network, Disk).

```go
instances, _ := aws.EC2.RunInstances(ctx, computedriver.InstanceConfig{
    ImageID: "ami-123", InstanceType: "t2.micro",
}, 2)
// Instances are "running", CloudWatch metrics auto-generated

aws.EC2.StopInstances(ctx, []string{instances[0].ID})
aws.EC2.TerminateInstances(ctx, []string{instances[0].ID})
// State machine enforced: can't stop a terminated instance
```

### Database

Create tables with partition and sort keys, put and get items, run queries with key conditions, and scan with filters. Supports all comparison operators (`=`, `!=`, `<`, `>`, `<=`, `>=`, `CONTAINS`, `BEGINS_WITH`) with numeric-aware comparisons — so `"10" > "9"` works correctly.

```go
aws.DynamoDB.CreateTable(ctx, dbdriver.TableConfig{
    Name: "users", PartitionKey: "pk", SortKey: "sk",
})
aws.DynamoDB.PutItem(ctx, "users", map[string]interface{}{
    "pk": "user1", "sk": "profile", "name": "Alice",
})
item, _ := aws.DynamoDB.GetItem(ctx, "users", map[string]interface{}{
    "pk": "user1", "sk": "profile",
})
```

### Message Queue with Dead-Letter Queue

Send and receive messages with visibility timeouts, FIFO ordering, and 5-minute deduplication windows. Configure a dead-letter queue so that messages which fail processing too many times are automatically moved out of the main queue — exactly how real SQS, Service Bus, and Pub/Sub work.

```go
dlq, _ := aws.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "my-dlq"})
mainQ, _ := aws.SQS.CreateQueue(ctx, mqdriver.QueueConfig{
    Name: "my-queue",
    DeadLetterQueue: &mqdriver.DeadLetterConfig{
        TargetQueueURL:  dlq.URL,
        MaxReceiveCount: 3, // move to DLQ after 3 failed receives
    },
})
aws.SQS.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: mainQ.URL, Body: "hello"})
```

### Serverless Triggers

Wire message queues to serverless functions so that every incoming message automatically triggers a function invocation. This emulates real event source mappings like AWS SQS -> Lambda, Azure Service Bus -> Functions, and GCP Pub/Sub -> Cloud Functions.

```go
aws.Lambda.RegisterHandler("processor", func(ctx context.Context, payload []byte) ([]byte, error) {
    return []byte("done"), nil
})
aws.Lambda.CreateFunction(ctx, sdriver.FunctionConfig{
    Name: "processor", Runtime: "go1.x", Handler: "main",
})

// Wire: every message sent to queue auto-invokes Lambda
aws.SQS.SetTrigger(queue.URL, func(queueURL string, msg mqdriver.Message) {
    aws.Lambda.Invoke(ctx, sdriver.InvokeInput{
        FunctionName: "processor", Payload: []byte(msg.Body),
    })
})
```

### Cost Simulation

Track estimated cloud costs across all your operations. cloudemu ships with default pricing rates for every service, and you can override them with custom rates. This is useful for budget testing, cost-aware CI pipelines, or just understanding what your test workload would cost on real cloud.

```go
tracker := cost.New()
tracker.Record("compute", "RunInstances", 3)
tracker.Record("storage", "PutObject", 1000)
tracker.Record("serverless", "Invoke", 100000)

tracker.TotalCost()       // total estimated cost
tracker.CostByService()   // breakdown by service
tracker.SetRate("compute", "RunInstances", 0.50) // custom pricing
```

## Realistic Cloud Behaviors

cloudemu goes beyond basic CRUD mocks. These behaviors make it behave like real cloud services, so your tests catch real issues:

- **VM State Machine** — `pending -> running -> stopped -> terminated` with enforced transitions. Illegal moves return errors.
- **Auto-Metric Generation** — Launching a VM automatically pushes 5 metrics (CPU, Network In/Out, Disk Read/Write) to the monitoring service with backfilled datapoints.
- **Lifecycle Metrics** — Start, stop, reboot, and terminate operations emit appropriate metric values so alarms can detect state changes.
- **Alarm Auto-Evaluation** — Push metric data and alarms automatically transition between `INSUFFICIENT_DATA`, `OK`, and `ALARM` based on threshold comparison.
- **IAM Policy Evaluation** — Parses real JSON policy documents with wildcard matching (`s3:*` matches `s3:GetObject`). Explicit `Deny` always overrides `Allow`.
- **FIFO Deduplication** — FIFO queues enforce a 5-minute deduplication window. Same `DeduplicationID` within the window returns the existing message ID.
- **Dead-Letter Queues** — Messages that exceed the max receive count are automatically moved to the configured DLQ. Works across SQS, Service Bus, and Pub/Sub.
- **Serverless Triggers** — Register event source mappings so messages automatically invoke Lambda, Azure Functions, or Cloud Functions.
- **Numeric-Aware DB Comparisons** — Database filters compare values numerically when both sides are valid numbers, avoiding string-sorting bugs.
- **Cost Simulation** — Track estimated cloud costs per operation with default or custom pricing rates.

## Cross-Cutting Features

The portable API layer wraps any provider mock with cross-cutting concerns. Every API call passes through a pipeline of recording, error injection, rate limiting, latency simulation, and metrics collection — giving you full control over test conditions.

```go
bucket := storage.NewBucket(aws.S3,
    storage.WithRecorder(rec),       // record every API call
    storage.WithMetrics(mc),         // track call counts & durations
    storage.WithErrorInjection(inj), // simulate failures
    storage.WithRateLimiter(limiter),// simulate API throttling
    storage.WithLatency(5*time.Millisecond), // simulate network delay
)
```

| Feature | Description |
|---------|-------------|
| **Call Recording** | Capture every API call with inputs, outputs, errors, and timing for later assertions |
| **Error Injection** | Simulate cloud failures with policies: Always, every Nth call, probabilistic, or first N calls |
| **Rate Limiting** | Token bucket rate limiter that returns `Throttled` errors when the burst is exhausted |
| **Metrics Collection** | Track `calls_total`, `call_duration`, and `errors_total` per service and operation |
| **Fake Clock** | Control time deterministically for testing dedup windows, alarm evaluation, TTL, and timeouts |
| **Latency Simulation** | Add realistic delays to test timeout handling and async patterns |

## Configuration

All providers accept functional options to customize region, account ID, clock, and latency.

```go
aws := cloudemu.NewAWS(
    config.WithRegion("eu-west-1"),
    config.WithAccountID("999888777666"),
    config.WithClock(config.NewFakeClock(time.Now())),
    config.WithLatency(10 * time.Millisecond),
)
```

## Error Handling

All operations return errors using canonical error codes. Use helper functions to check the error type without string matching.

```go
import cerrors "github.com/NitinKumar004/cloudemu/errors"

_, err := s3.GetObject(ctx, "bucket", "missing-key")
if cerrors.IsNotFound(err) { /* handle */ }

// Available codes: NotFound, AlreadyExists, InvalidArgument,
//                  FailedPrecondition, PermissionDenied, Throttled
```

## Architecture

cloudemu follows a three-layer design inspired by Go CDK. The portable API layer adds cross-cutting concerns on top of minimal driver interfaces, which are implemented by in-memory provider backends. All 30 mocks are backed by a single generic, thread-safe `memstore.Store[V]`.

```
Portable API     →  recording, metrics, rate limiting, error injection
Driver Interface →  minimal Go interfaces per service
Provider Mocks   →  in-memory backends (AWS/Azure/GCP) using generic memstore
```

## Running Tests

```bash
go build ./...   # compile all packages
go vet ./...     # static analysis
go test -v ./... # run all 32 tests
```

## License

MIT
