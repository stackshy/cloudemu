# cloudemu

**Zero-cost, in-memory cloud emulation of AWS, Azure, and GCP for Go.**

```go
aws := cloudemu.NewAWS()
azure := cloudemu.NewAzure()
gcp := cloudemu.NewGCP()
```

No cloud accounts. No Docker. No network calls. Just import and test.

## Installation

```bash
go get github.com/NitinKumar004/cloudemu
```

Requires Go 1.25.0+.

## Why cloudemu?

| Approach | Cost | Speed | Offline |
|----------|------|-------|---------|
| Real cloud | $$$ | Slow | No |
| LocalStack / Emulators | $ | Medium | Yes |
| **cloudemu** | **Free** | **~10ms** | **Yes** |

## Supported Services (10 per provider)

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

```go
aws := cloudemu.NewAWS()
aws.S3.CreateBucket(ctx, "my-bucket")
aws.S3.PutObject(ctx, "my-bucket", "key", []byte("hello"), "text/plain", nil)
obj, _ := aws.S3.GetObject(ctx, "my-bucket", "key")
// obj.Data == []byte("hello")
```

### Compute (with auto-generated metrics)

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

### Serverless Triggers (SQS -> Lambda)

```go
aws.Lambda.RegisterHandler("processor", func(ctx context.Context, payload []byte) ([]byte, error) {
    return []byte("done"), nil
})
aws.Lambda.CreateFunction(ctx, sdriver.FunctionConfig{Name: "processor", Runtime: "go1.x", Handler: "main"})

// Wire: every message sent to queue auto-invokes Lambda
aws.SQS.SetTrigger(queue.URL, func(queueURL string, msg mqdriver.Message) {
    aws.Lambda.Invoke(ctx, sdriver.InvokeInput{FunctionName: "processor", Payload: []byte(msg.Body)})
})
```

### Cost Simulation

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

These aren't just dummy mocks — they behave like real cloud:

- **VM State Machine** — `pending -> running -> stopped -> terminated` with enforced transitions
- **Auto-Metric Generation** — launching a VM auto-generates CPU, Network, Disk metrics in monitoring
- **Lifecycle Metrics** — start/stop/reboot/terminate emit metric values (running or zero)
- **Alarm Auto-Evaluation** — push metric data and alarms transition between `INSUFFICIENT_DATA`, `OK`, `ALARM` automatically
- **IAM Policy Evaluation** — parses real JSON policy documents with wildcard matching, explicit Deny overrides Allow
- **FIFO Deduplication** — 5-minute dedup window on FIFO queues (SQS, Service Bus, Pub/Sub)
- **Dead-Letter Queues** — messages exceeding max receive count auto-move to DLQ
- **Serverless Triggers** — SQS->Lambda, ServiceBus->Functions, PubSub->CloudFunctions event source mappings
- **Numeric-Aware DB Comparisons** — `"10" > "9"` compares numerically, not as strings
- **Cost Simulation** — track estimated cloud costs per operation with customizable rates

## Cross-Cutting Features

Wrap any provider mock with the portable API to get:

```go
rec := recorder.New()
mc := metrics.NewCollector()
inj := inject.NewInjector()
clock := config.NewFakeClock(time.Now())
limiter := ratelimit.New(10, 10, clock)

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
| **Call Recording** | Record every call for assertions — VCR pattern |
| **Error Injection** | Always, NthCall, Probabilistic, Countdown policies |
| **Rate Limiting** | Token bucket with burst — returns `Throttled` error |
| **Metrics Collection** | calls_total, call_duration, errors_total per operation |
| **Fake Clock** | Deterministic time for TTL, dedup windows, alarm evaluation |
| **Latency Simulation** | Add realistic delays to test timeout handling |

## Configuration

```go
aws := cloudemu.NewAWS(
    config.WithRegion("eu-west-1"),
    config.WithAccountID("999888777666"),
    config.WithClock(config.NewFakeClock(time.Now())),
    config.WithLatency(10 * time.Millisecond),
)
```

## Error Handling

```go
import cerrors "github.com/NitinKumar004/cloudemu/errors"

_, err := s3.GetObject(ctx, "bucket", "missing-key")
if cerrors.IsNotFound(err) { /* handle */ }

// Codes: NotFound, AlreadyExists, InvalidArgument,
//        FailedPrecondition, PermissionDenied, Throttled
```

## Running Tests

```bash
go build ./...   # compile
go vet ./...     # lint
go test -v ./... # 32 tests, all passing
```

## Architecture

Three-layer design:

```
Portable API     →  recording, metrics, rate limiting, error injection
Driver Interface →  minimal Go interfaces per service
Provider Mocks   →  in-memory backends (AWS/Azure/GCP) using generic memstore
```

All 30 mocks backed by a single generic thread-safe `memstore.Store[V]`.

## License

MIT
