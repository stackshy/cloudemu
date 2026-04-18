# Cross-Cutting Features

CloudEmu goes beyond simple CRUD mocking. These features emulate real cloud behaviors so that integration tests can validate end-to-end logic without deploying to a real cloud.

---

## 1. Auto-Metric Generation

When a compute instance is launched with `RunInstances`, the compute mock automatically pushes 5 metrics to the provider's monitoring service. This happens because the provider factory wires compute to monitoring via `SetMonitoring()`.

### Metrics Pushed on RunInstances

Each instance gets 5 metrics with 5 backfill datapoints at 1-minute intervals from launch time:

| Provider | Namespace | Metrics | Dimension Key |
|----------|-----------|---------|---------------|
| AWS | `AWS/EC2` | CPUUtilization, NetworkIn, NetworkOut, DiskReadOps, DiskWriteOps | `InstanceId` |
| Azure | `Microsoft.Compute/virtualMachines` | Percentage CPU, Network In Total, Network Out Total, Disk Read Operations/Sec, Disk Write Operations/Sec | `resourceId` |
| GCP | `compute.googleapis.com` | instance/cpu/utilization, instance/network/received_bytes_count, instance/network/sent_bytes_count, instance/disk/read_ops_count, instance/disk/write_ops_count | `instance_id` |

### Lifecycle Metric Emission

All VM lifecycle operations also emit metrics via `emitLifecycleMetrics()`:

| Operation | Values |
|-----------|--------|
| `StartInstances` | Running values (CPU=25, Network=1024/512, Disk=100/50; GCP CPU=0.25) |
| `StopInstances` | Zero values (all 0.0) |
| `RebootInstances` | Running values |
| `TerminateInstances` | Zero values |

Each lifecycle call emits 1 datapoint per metric at `Clock.Now()`. This allows alarms to detect state changes -- for example, a "low CPU" alarm fires when a VM is stopped.

### Auto-Metrics for Other Services

In addition to compute, 9 other services per provider are wired to push metrics to monitoring: Storage, Database, Serverless, Message Queue, Cache, Logging, Notification, Container Registry, and Event Bus.

---

## 2. Alarm Auto-Evaluation

When `PutMetricData` is called, the monitoring mock automatically evaluates all alarms that match the affected namespace and metric name. This is implemented in `evaluateAlarms()` within each monitoring mock.

### Evaluation Process

1. For each metric datum pushed, find alarms matching the namespace + metric name + dimensions.
2. Collect datapoints within the evaluation window: `Period * EvaluationPeriods` seconds.
3. Compute the statistic over those datapoints:
   - `Average` -- mean of all values
   - `Sum` -- sum of all values
   - `Minimum` -- smallest value
   - `Maximum` -- largest value
   - `SampleCount` -- number of datapoints
4. Compare against the alarm's threshold using the configured operator.
5. Update alarm state to `"ALARM"` or `"OK"`.

### Supported Comparison Operators

- `GreaterThanThreshold`
- `LessThanThreshold`
- `GreaterThanOrEqualToThreshold`
- `LessThanOrEqualToThreshold`

### Alarm Actions and History

Alarms support three types of action channels:

- `AlarmActions` -- notification channel IDs to notify when state transitions to `ALARM`
- `OKActions` -- channel IDs to notify when state transitions to `OK`
- `InsufficientDataActions` -- channel IDs to notify on `INSUFFICIENT_DATA`

Every state transition is recorded in alarm history, queryable via `GetAlarmHistory()`. Each entry includes the alarm name, timestamp, old state, new state, and a reason string.

---

## 3. IAM Policy Evaluation

`CheckPermission(principal, action, resource)` evaluates real JSON policy documents against a request.

### Evaluation Process

1. Look up the principal (user or role) and collect all attached policy ARNs.
2. For users, also collect policies attached to the user's groups.
3. Parse each policy's JSON document into structured statements.
4. For each statement, check if the action and resource match using `wildcardMatch()`.
5. Apply standard IAM evaluation logic:
   - Explicit `Deny` always overrides `Allow`.
   - If no statement explicitly allows the action, the default is deny.
   - `wildcardMatch()` supports `*` (match any sequence) and `?` (match single character).

### Example Policy Document

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject"],
      "Resource": ["arn:aws:s3:::my-bucket/*"]
    },
    {
      "Effect": "Deny",
      "Action": ["s3:DeleteObject"],
      "Resource": ["*"]
    }
  ]
}
```

With this policy attached, `CheckPermission("user1", "s3:GetObject", "arn:aws:s3:::my-bucket/file.txt")` returns `true`, while `CheckPermission("user1", "s3:DeleteObject", "arn:aws:s3:::my-bucket/file.txt")` returns `false`.

---

## 4. FIFO Message Deduplication

FIFO queues enforce a 5-minute deduplication window to prevent duplicate message processing.

### How It Works

1. Each FIFO queue maintains a `deduplicationIndex map[string]time.Time` tracking when each `DeduplicationID` was last seen.
2. When `SendMessage` is called with a `DeduplicationID`:
   - If the same ID was seen within the last 5 minutes, the call returns the existing `MessageID` without creating a new message.
   - If the ID has not been seen, or was last seen more than 5 minutes ago, a new message is created and the dedup index is updated.
3. `SentAt time.Time` on message structs tracks when each message was sent.

This behavior matches the real AWS SQS, Azure Service Bus, and GCP Pub/Sub FIFO semantics.

### Deterministic Testing

Use `config.FakeClock` to control time in dedup tests:

```go
clock := config.NewFakeClock(time.Now())
aws := cloudemu.NewAWS(config.WithClock(clock))

// First send -- creates message
aws.SQS.SendMessage(ctx, input)

// Second send within 5 minutes -- returns same MessageID
aws.SQS.SendMessage(ctx, input)

// Advance past dedup window
clock.Advance(6 * time.Minute)

// Third send -- creates new message
aws.SQS.SendMessage(ctx, input)
```

---

## 5. Database Features

### Global Secondary Indexes (GSI)

Tables support creating GSIs with a different partition key and optional sort key. Query operations can target a specific index by name via `QueryInput.IndexName`.

| Operation | Description |
|-----------|-------------|
| `CreateIndex` | Add a GSI to an existing table |
| `DeleteIndex` | Remove a GSI |
| `DescribeIndex` | Get GSI status and key schema |
| `ListIndexes` | List all GSIs on a table |

### Numeric-Aware Comparisons

The `compareValues(a, b string)` helper in each database mock tries `strconv.ParseFloat` on both values. If both parse as numbers, it performs numeric comparison. Otherwise it falls back to string comparison. This is used by all comparison operators in scan filters and query sort conditions.

### Full Scan Operators

Scan filters support: `=`, `!=`, `<`, `>`, `<=`, `>=`, `CONTAINS`, `BEGINS_WITH`

Query sort key conditions support: `=`, `<`, `>`, `<=`, `>=`, `BEGINS_WITH`, `BETWEEN`

### TTL (Time To Live)

Tables can be configured with TTL on a specific attribute. The TTL configuration specifies an `AttributeName` that holds a Unix timestamp. Items past their TTL can be identified and cleaned up.

### Streams / Change Feed

Tables can enable streams that capture change events (`INSERT`, `MODIFY`, `REMOVE`). Each `StreamRecord` includes the event type, keys, old image, new image, and a sequence number. The stream view type controls what data is captured: `NEW_IMAGE`, `OLD_IMAGE`, `NEW_AND_OLD_IMAGES`, or `KEYS_ONLY`.

### Transactional Writes

`TransactWriteItems` provides atomic batch writes -- a set of puts and deletes that either all succeed or all fail. This matches DynamoDB's `TransactWriteItems`, Cosmos DB's transactional batch, and Firestore's transactions.

---

## 6. Dead-Letter Queues

Message queues support dead-letter queue (DLQ) configuration. When creating a queue, you can specify a `DeadLetterConfig` with:

- `TargetQueueURL` -- the URL of the DLQ
- `MaxReceiveCount` -- after this many receives without deletion, the message is moved to the DLQ

This enables testing of poison message handling and retry exhaustion scenarios.

```go
// Create the DLQ first
dlq, _ := aws.SQS.CreateQueue(ctx, driver.QueueConfig{Name: "my-dlq"})

// Create the main queue with DLQ config
aws.SQS.CreateQueue(ctx, driver.QueueConfig{
    Name: "my-queue",
    DeadLetterQueue: &driver.DeadLetterConfig{
        TargetQueueURL:  dlq.URL,
        MaxReceiveCount: 3,
    },
})
```

---

## 7. Cost Tracking

The `cost.Tracker` provides simulated cost estimation for cloud operations. It ships with default per-operation rates based on approximate real cloud pricing.

### Default Rates (Subset)

| Operation | Rate |
|-----------|------|
| `compute:RunInstances` | $0.0116/instance-hour |
| `storage:PutObject` | $0.000005 |
| `storage:GetObject` | $0.0000004 |
| `database:PutItem` | $0.00000125 |
| `database:GetItem` | $0.00000025 |
| `serverless:Invoke` | $0.0000002 |
| `messagequeue:SendMessage` | $0.0000004 |
| `monitoring:PutMetricData` | $0.00001 |
| `loadbalancer:CreateLoadBalancer` | $0.0225/hour |

### API

```go
tracker := cost.New()

// Record operations
tracker.Record("storage", "PutObject", 100)
tracker.Record("compute", "RunInstances", 2)

// Query costs
total := tracker.TotalCost()                    // total across all operations
byService := tracker.CostByService()            // map[string]float64
byOp := tracker.CostByOperation()               // map[string]float64
all := tracker.AllCosts()                        // []ServiceCost with full detail

// Override a rate
tracker.SetRate("compute", "RunInstances", 0.0464)  // m5.xlarge pricing

// Reset
tracker.Reset()
```

---

## 8. Portable API Cross-Cutting Concerns

The portable API layer wraps every driver operation with five optional cross-cutting concerns. These are configured per service instance using functional options.

### 1. Recording

Captures every API call with service name, operation, input, output, error, and duration. Useful for test assertions like "verify that PutObject was called exactly twice."

### 2. Metrics Collection

Automatically records `calls_total` (counter), `call_duration` (histogram), and `errors_total` (counter) for every operation, labeled by service and operation name.

### 3. Rate Limiting

Token bucket rate limiter. When the bucket is exhausted, operations return a `Throttled` error without calling the underlying driver.

### 4. Error Injection

Inject errors into specific service/operation pairs with configurable policies:

- `Always` -- fail every call
- `NthCall(n)` -- fail every Nth call
- `Probabilistic(p)` -- fail with probability p (0.0-1.0)
- `Countdown(n)` -- fail the first n calls, then succeed

### 5. Latency Simulation

Add a fixed delay to every operation to simulate network latency.

### Example

```go
import (
    "time"
    "errors"

    "github.com/stackshy/cloudemu/storage"
    "github.com/stackshy/cloudemu/recorder"
    "github.com/stackshy/cloudemu/metrics"
    "github.com/stackshy/cloudemu/ratelimit"
    "github.com/stackshy/cloudemu/inject"
    cerrors "github.com/stackshy/cloudemu/errors"
)

rec := recorder.New()
col := metrics.NewCollector()
lim := ratelimit.New(100, 10, nil) // 100 req/s, burst 10
inj := inject.NewInjector()

// Fail every 5th GetObject call with a Throttled error
inj.Set("storage", "GetObject",
    cerrors.New(cerrors.Throttled, "simulated throttle"),
    inject.NewNthCall(5),
)

bucket := storage.NewBucket(awsProvider.S3,
    storage.WithRecorder(rec),
    storage.WithMetrics(col),
    storage.WithRateLimiter(lim),
    storage.WithErrorInjection(inj),
    storage.WithLatency(5 * time.Millisecond),
)

// Use bucket normally -- all cross-cutting concerns are applied
bucket.PutObject(ctx, "my-bucket", "key", data, "text/plain", nil)

// Assert calls were recorded
calls := rec.CallsFor("storage", "PutObject")
count := rec.CallCountFor("storage", "PutObject")

// Check metrics
allMetrics := col.All()
```

---

## 9. Deterministic Time

All time-dependent features in CloudEmu use the `config.Clock` interface rather than calling `time.Now()` directly. This allows tests to use `config.FakeClock` for fully deterministic behavior.

### Clock Interface

```go
type Clock interface {
    Now() time.Time
    Since(t time.Time) time.Duration
    After(d time.Duration) <-chan time.Time
}
```

### FakeClock

```go
// Create a fake clock set to a specific time
clock := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

// Create providers with the fake clock
aws := cloudemu.NewAWS(config.WithClock(clock))

// Operations use clock.Now() for timestamps
aws.EC2.RunInstances(ctx, config, 1)

// Advance time to test time-dependent behavior
clock.Advance(5 * time.Minute)

// Set to a specific time
clock.Set(time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC))
```

### Where FakeClock Matters

- **FIFO deduplication** -- The 5-minute dedup window is evaluated against `clock.Now()`. Advance the clock past 5 minutes to test dedup expiry.
- **Alarm evaluation** -- Metric timestamps and evaluation windows use the clock. Control when alarms transition between states.
- **Auto-metrics** -- Backfill datapoints are generated at 1-minute intervals from `clock.Now()`. FakeClock ensures predictable timestamps.
- **TTL evaluation** -- Database TTL checks compare item timestamps against the clock.
- **Resource timestamps** -- All `CreatedAt`, `LastModified`, and similar fields use the clock.
