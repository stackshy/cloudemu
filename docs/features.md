# Cross-Cutting Features

Besides plain CRUD, CloudEmu reproduces a number of cloud behaviors so integration tests can check end-to-end logic without deploying anything. This page describes them.

---

## 1. Auto-Metric Generation

When you launch an instance with `RunInstances`, the compute mock pushes 5 metrics to the provider's monitoring service. The provider factory sets this up by connecting compute to monitoring with `SetMonitoring()`.

### Metrics Pushed on RunInstances

Each instance gets 5 metrics, each with 5 backfill datapoints at 1-minute intervals from launch time:

| Provider | Namespace | Metrics | Dimension Key |
|----------|-----------|---------|---------------|
| AWS | `AWS/EC2` | CPUUtilization, NetworkIn, NetworkOut, DiskReadOps, DiskWriteOps | `InstanceId` |
| Azure | `Microsoft.Compute/virtualMachines` | Percentage CPU, Network In Total, Network Out Total, Disk Read Operations/Sec, Disk Write Operations/Sec | `resourceId` |
| GCP | `compute.googleapis.com` | instance/cpu/utilization, instance/network/received_bytes_count, instance/network/sent_bytes_count, instance/disk/read_ops_count, instance/disk/write_ops_count | `instance_id` |

### Lifecycle Metric Emission

VM lifecycle operations also emit metrics, via `emitLifecycleMetrics()`:

| Operation | Values |
|-----------|--------|
| `StartInstances` | Running values (CPU=25, Network=1024/512, Disk=100/50; GCP CPU=0.25) |
| `StopInstances` | Zero values (all 0.0) |
| `RebootInstances` | Running values |
| `TerminateInstances` | Zero values |

Each lifecycle call emits 1 datapoint per metric at `Clock.Now()`. Alarms can then react to state changes. For example, a "low CPU" alarm fires when a VM is stopped.

AWS/EC2 datapoints carry the units real EC2 publishes: `CPUUtilization` is `Percent`, `NetworkIn`/`NetworkOut` are `Bytes`, and `DiskReadOps`/`DiskWriteOps` are `Count` (visible as `Unit` on `get-metric-statistics`).

### Auto-Metrics for Other Services

Besides compute, 9 other services per provider push metrics to monitoring: Storage, Database, Serverless, Message Queue, Cache, Logging, Notification, Container Registry, and Event Bus.

On AWS, these follow the real CloudWatch taxonomy (namespace, metric name, dimensions, unit). Notable examples:

| Service | Namespace | Metrics | Dimensions |
|---------|-----------|---------|------------|
| DynamoDB | `AWS/DynamoDB` | ConsumedRead/WriteCapacityUnits (Count); SuccessfulRequestLatency (Milliseconds); ReturnedItemCount (Count, Query/Scan) | `TableName`; latency and item count on `TableName`+`Operation` |
| Lambda | `AWS/Lambda` | Invocations, Errors, Throttles, ConcurrentExecutions (Count); Duration (Milliseconds) | `FunctionName` |
| ECR | `AWS/ECR` | RepositoryPullCount (Count) — the only metric real ECR publishes; pushes emit nothing | `RepositoryName` |
| Kinesis | `AWS/Kinesis` | IncomingBytes/Records, PutRecord.\*, PutRecords.\*, GetRecords.\* (Bytes/Count/Milliseconds) | `StreamName` |
| Step Functions | `AWS/States` | ExecutionsStarted/Succeeded/Failed/Aborted/TimedOut (Count); ExecutionTime (Milliseconds) | `StateMachineArn` |
| API Gateway | `AWS/ApiGateway` | Count, 4XXError, 5XXError (Count); Latency, IntegrationLatency (Milliseconds) | `ApiName`, and `ApiName`+`Stage` |
| Athena | `AWS/Athena` | TotalExecutionTime, EngineExecutionTime (Milliseconds); ProcessedBytes (Bytes, DML) — only for workgroups with `PublishCloudWatchMetricsEnabled` | `QueryState`+`QueryType`+`WorkGroup` |

---

## 2. Alarm Auto-Evaluation

Each call to `PutMetricData` makes the monitoring mock evaluate every alarm that matches the affected namespace and metric name. The logic is in `evaluateAlarms()` in each monitoring mock.

### Evaluation Process

1. For each metric datum pushed, find alarms matching the namespace + metric name + dimensions.
2. Collect datapoints within the evaluation window: `Period * EvaluationPeriods` seconds.
3. Compute the statistic over those datapoints:
   - `Average`: mean of all values
   - `Sum`: sum of all values
   - `Minimum`: smallest value
   - `Maximum`: largest value
   - `SampleCount`: number of datapoints
4. Compare against the alarm's threshold using the configured operator.
5. Set the alarm state to `"ALARM"` or `"OK"`.

### Supported Comparison Operators

- `GreaterThanThreshold`
- `LessThanThreshold`
- `GreaterThanOrEqualToThreshold`
- `LessThanOrEqualToThreshold`

### Alarm Actions and History

Alarms have three kinds of action channels:

- `AlarmActions`: notification channel IDs to notify when the state changes to `ALARM`
- `OKActions`: channel IDs to notify when the state changes to `OK`
- `InsufficientDataActions`: channel IDs to notify on `INSUFFICIENT_DATA`

Every state change is recorded in the alarm history, which you can read with `GetAlarmHistory()`. Each entry has the alarm name, timestamp, old state, new state, and a reason string.

---

## 3. IAM Policy Evaluation

`CheckPermission(principal, action, resource)` evaluates real JSON policy documents against a request.

### Evaluation Process

1. Look up the principal (user or role) and collect all attached policy ARNs.
2. For users, also collect policies attached to the user's groups.
3. Parse each policy's JSON document into statements.
4. For each statement, check whether the action and resource match using `wildcardMatch()`.
5. Apply standard IAM evaluation logic:
   - An explicit `Deny` always overrides `Allow`.
   - If no statement explicitly allows the action, the result is deny.
   - `wildcardMatch()` supports `*` (any sequence) and `?` (a single character).

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

With this policy attached, `CheckPermission("user1", "s3:GetObject", "arn:aws:s3:::my-bucket/file.txt")` returns `true` and `CheckPermission("user1", "s3:DeleteObject", "arn:aws:s3:::my-bucket/file.txt")` returns `false`.

---

## 4. FIFO Message Deduplication

FIFO queues drop duplicate messages sent within a 5-minute deduplication window.

### How It Works

1. Each FIFO queue keeps a `deduplicationIndex map[string]time.Time` recording when each `DeduplicationID` was last seen.
2. When `SendMessage` is called with a `DeduplicationID`:
   - If the same ID was seen in the last 5 minutes, the call returns the existing `MessageID` and doesn't create a new message.
   - If the ID is new, or was last seen more than 5 minutes ago, a new message is created and the index is updated.
3. `SentAt time.Time` on message structs records when each message was sent.

This behavior matches the real AWS SQS, Azure Service Bus, and GCP Pub/Sub FIFO semantics.

### Deterministic Testing

Use `config.FakeClock` to control time in dedup tests:

```go
clock := config.NewFakeClock(time.Now())
aws := cloudemu.NewAWS(config.WithClock(clock))

// First send: creates message
aws.SQS.SendMessage(ctx, input)

// Second send within 5 minutes: returns same MessageID
aws.SQS.SendMessage(ctx, input)

// Advance past dedup window
clock.Advance(6 * time.Minute)

// Third send: creates new message
aws.SQS.SendMessage(ctx, input)
```

---

## 5. Database Features

### Global Secondary Indexes (GSI)

You can add GSIs to a table with a different partition key and an optional sort key. A query targets an index by name through `QueryInput.IndexName`.

| Operation | Description |
|-----------|-------------|
| `CreateIndex` | Add a GSI to an existing table |
| `DeleteIndex` | Remove a GSI |
| `DescribeIndex` | Get GSI status and key schema |
| `ListIndexes` | List all GSIs on a table |

### Numeric-Aware Comparisons

The `compareValues(a, b string)` helper in each database mock tries `strconv.ParseFloat` on both values. If both parse as numbers, it compares them numerically; otherwise it compares them as strings. All comparison operators in scan filters and query sort conditions use it.

### Query & Expression Grammar

The database drivers evaluate the real expression grammars, not a reduced
subset. The expression strings a client sends are tokenized, parsed and
evaluated with type-aware semantics:

- DynamoDB: `KeyConditionExpression` (`=`/`<`/`<=`/`>`/`>=`, `BETWEEN`,
  `begins_with`), `FilterExpression`/`ConditionExpression` (boolean operators,
  `IN`, `BETWEEN`, `attribute_exists`/`attribute_type`/`begins_with`/`contains`/
  `size`), `ProjectionExpression`, and `UpdateExpression` (`SET` with arithmetic,
  `if_not_exists`, `list_append`; `REMOVE`; `ADD`; `DELETE`), including the
  `SS`/`NS`/`BS` set types.
- Firestore: structured queries with all field operators (`IN`/`NOT_IN`/
  `ARRAY_CONTAINS`/`ARRAY_CONTAINS_ANY`), `AND`/`OR` composite filters, unary
  `IS_NULL`/`IS_NOT_NULL`, `orderBy`, `offset`, `startAt`/`endAt` cursors, and
  field projection.
- Cosmos DB: Cosmos SQL (`SELECT`/`WHERE`/`ORDER BY`/`OFFSET`-`LIMIT`,
  `DISTINCT`, `TOP`, projections including `SELECT VALUE`, and `COUNT`/`SUM`/`AVG`/
  `MIN`/`MAX` aggregates).

The older driver-level `ScanFilter`/`SortOp` operators (`=`, `!=`, `<`, `>`,
`<=`, `>=`, `CONTAINS`, `BEGINS_WITH`, `BETWEEN`) are still there for callers
of the Go API.

### TTL (Time To Live)

A table can have TTL on one attribute. The TTL configuration names an `AttributeName` that holds a Unix timestamp. Items past their TTL can be found and cleaned up.

### Streams / Change Feed

Tables can enable streams that capture change events (`INSERT`, `MODIFY`, `REMOVE`). Each `StreamRecord` has the event type, keys, old image, new image, and a sequence number. The stream view type controls what is captured: `NEW_IMAGE`, `OLD_IMAGE`, `NEW_AND_OLD_IMAGES`, or `KEYS_ONLY`.

### Transactional Writes

`TransactWriteItems` applies a set of puts and deletes atomically: either all succeed or all fail. This corresponds to DynamoDB's `TransactWriteItems`, Cosmos DB's transactional batch, and Firestore's transactions.

---

## 6. Dead-Letter Queues

Message queues support dead-letter queue (DLQ) configuration. When you create a queue, you can pass a `DeadLetterConfig` with:

- `TargetQueueURL`: the URL of the DLQ
- `MaxReceiveCount`: after this many receives without a delete, the message moves to the DLQ

Use this to test poison-message handling and retry exhaustion.

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

CloudEmu models cost in two ways: a per-operation tracker (metered API usage) and a resource-inventory estimate (what the resources that currently exist would cost per month). Both live in `services/cost` and use the rate tables in `services/pricing`.

### Per-operation tracker (`cost.Tracker`)

`cost.Tracker` estimates the cost of metered cloud operations. Its default per-operation rates are based on approximate real cloud prices.

#### Default Rates (Subset)

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

#### API

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

### Inventory estimate (line-item + commitment model)

`services/cost` can also build a bill from the resources that exist, instead of from metered calls. `cost.Estimate(ctx, inv)` walks a provider's resource inventory and emits a `cost.Line` per resource (service, resource type, SKU/size, region, monthly rate). `cost.ServiceMonthly(...)` sums the lines by service. The per-resource rates come from `services/pricing.Monthly(provider, service, resourceType, sku, region, props)`. `cost.Commitment` (with the `Commitments` registry) models reservations and savings plans and computes `Coverage` and `Utilization` over a time window, in the same shape real cost tools report.

### Billing / FinOps SDK-compat handlers

The same cost model is served through each provider's native billing APIs, so real FinOps SDKs and CLIs work against the emulator:

| Provider | Handlers | Native surface |
|----------|----------|----------------|
| AWS | `server/aws/costexplorer`, `server/aws/savingsplans`, `server/aws/servicequotas` | Cost Explorer (GetCostAndUsage, …), Savings Plans, Service Quotas |
| Azure | `server/azure/costmanagement` | Cost Management (query/usage) |
| GCP | `server/gcp/cloudbilling` | Cloud Billing (billing accounts, project billing info) |

---

## 8. Portable API Cross-Cutting Concerns

The portable API layer can wrap every driver operation with five optional behaviors. You turn them on per service instance with functional options.

### 1. Recording

Records every API call with the service name, operation, input, output, error, and duration. Use it for assertions like "PutObject was called exactly twice."

### 2. Metrics Collection

Records `calls_total` (counter), `call_duration` (histogram), and `errors_total` (counter) for every operation, labeled by service and operation name.

### 3. Rate Limiting

A token bucket rate limiter. When the bucket is empty, operations return a `Throttled` error without calling the underlying driver.

### 4. Error Injection

Inject errors into specific service/operation pairs using one of these policies:

- `Always`: fail every call
- `NthCall(n)`: fail every Nth call
- `Probabilistic(p)`: fail with probability p (0.0-1.0)
- `Countdown(n)`: fail the first n calls, then succeed

### 5. Latency Simulation

Adds a fixed delay to every operation to simulate network latency.

### Example

```go
import (
    "time"
    "errors"

    "github.com/stackshy/cloudemu/v2/services/storage"
    "github.com/stackshy/cloudemu/v2/features/recorder"
    "github.com/stackshy/cloudemu/v2/features/metrics"
    "github.com/stackshy/cloudemu/v2/features/ratelimit"
    "github.com/stackshy/cloudemu/v2/features/inject"
    cerrors "github.com/stackshy/cloudemu/v2/errors"
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

// Use bucket normally; all cross-cutting concerns are applied
bucket.PutObject(ctx, "my-bucket", "key", data, "text/plain", nil)

// Assert calls were recorded
calls := rec.CallsFor("storage", "PutObject")
count := rec.CallCountFor("storage", "PutObject")

// Check metrics
allMetrics := col.All()
```

---

## 9. Deterministic Time

Every time-dependent feature in CloudEmu uses the `config.Clock` interface instead of calling `time.Now()` directly. Tests can pass a `config.FakeClock` to make timing fully deterministic.

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

- FIFO deduplication: the 5-minute window is checked against `clock.Now()`. Advance the clock past 5 minutes to test expiry.
- Alarm evaluation: metric timestamps and evaluation windows use the clock, so you control when alarms change state.
- Auto-metrics: backfill datapoints are generated at 1-minute intervals from `clock.Now()`, so their timestamps are predictable.
- TTL evaluation: database TTL checks compare item timestamps against the clock.
- Resource timestamps: `CreatedAt`, `LastModified`, and similar fields all use the clock.

---

## 10. Cross-Service Resource Discovery

`services/resourcediscovery/` is a cross-service inventory engine. It walks every service driver a provider holds and returns one normalized view of what exists. Like `features/topology/`, it sits beside the portable API: it holds no state, is built from driver interfaces, and only answers queries.

Three SDK-compat handlers that speak the real cloud inventory APIs are built on it: AWS Resource Explorer 2 + Resource Groups Tagging API, Azure Resource Graph, and GCP Cloud Asset Inventory. A tag set through any one of them is visible right away through the others, and through the engine's own `SearchByTag`.

### Provider wiring

Every provider factory wires the engine for you:

```go
aws := cloudemu.NewAWS(config.WithAccountID("123456789012"), config.WithRegion("us-west-2"))

all, _ := aws.ResourceDiscovery.ListAll(ctx)
// returns Resource{Provider, Service, Type, ID, ARN, Region, Tags, CreatedAt}
// for every bucket, instance, VPC, subnet, security group, table, and function
```

Azure and GCP providers have the same field (`azure.ResourceDiscovery`, `gcp.ResourceDiscovery`). The engine reads from the Compute, Networking, Storage, Database, Serverless, Databricks, Kubernetes, Relational Database, Secrets, Container Registry, Message Queue, Notification, DNS, Logging, Cache, Load Balancer, Monitoring, and IAM drivers. There is also a generic `Extra` hook for services with no shared driver (ML/GenAI). Nil fields are skipped, so partial test setups work.

These resources appear under their cloud's inventory type strings: managed relational servers (AWS RDS including DB proxies, Redshift, Azure SQL, the MySQL/PostgreSQL Flexible Servers, and Cloud SQL), compute snapshots, networking sub-types (NAT gateways, internet gateways, VPC peering connections, route tables), secrets, container repositories, queues, topics, DNS zones, log groups, cache clusters, load balancers, metric alarms, IAM users/roles/policies/groups, and ML resources (SageMaker models/endpoints/notebooks, Vertex AI endpoints/datasets).

### Engine API

| Operation | Purpose |
|-----------|---------|
| `ListAll(ctx)` | Every resource the provider currently holds |
| `List(ctx, Query)` | Filter by `Services`, `Type`, `Region`, and `Tags` (any-of for `Services`; AND across all non-empty fields) |
| `SearchByTag(ctx, key, value)` | Every resource whose `Tags[key] == value` |
| `GetTagKeys(ctx)` | Distinct tag keys across the inventory |
| `GetTagValues(ctx, key)` | Distinct values for a key |
| `TagResourceByARN(ctx, arn, tags)` | Apply tags to a resource addressed by canonical ARN/URN |
| `UntagResourceByARN(ctx, arn, keys)` | Remove tag keys from a resource addressed by canonical ARN/URN |

The `Resource` struct is the same for every cloud:

```go
type Resource struct {
    Provider  string            // "aws" | "azure" | "gcp"
    Service   string            // "compute" | "storage" | "networking" | "database" | "serverless"
    Type      string            // e.g. "instance", "bucket", "vpc", "table", "function"
    ID        string
    ARN       string            // AWS ARN, Azure resource ID, or GCP //-prefixed URN
    Region    string
    Tags      map[string]string
    CreatedAt time.Time
}
```

### SDK-compat surfaces

The engine backs three handlers, each registered on its provider's SDK-compat server. They all read from (and write tags through) the same engine, so which one you use depends only on the SDK your code already uses.

| Cloud | Handler | What real SDK clients see |
|-------|---------|--------------------------|
| AWS | `server/aws/resourceexplorer2` + `server/aws/resourcegroupstaggingapi` | `resourceexplorer2.Search`, `resourcegroupstaggingapi.GetResources/TagResources/UntagResources/GetTagKeys/GetTagValues` |
| Azure | `server/azure/resourcegraph` | `armresourcegraph.Resources`: KQL-shaped query over the unified inventory |
| GCP | `server/gcp/cloudasset` | `cloudasset.SearchAllResources`, `assets.List`, `ExportAssets`, Feeds CRUD, `Operations.Get` |

See [services.md: Resource Discovery](services.md#19-resource-discovery) for the per-handler operation list and [sdk-server.md](sdk-server.md) for the wire protocols.

## 11. Real Data-Plane Engines (opt-in)

By default CloudEmu is purely in-memory: every driver keeps state in
`memstore` and returns synthetic responses, and no external processes run. If
you want clients to run real workloads (real SQL, real Redis commands, real
function code) against the emulator, you can back drivers with an opt-in real
engine. The in-memory default doesn't change; a nil engine means "stay
in-memory".

### Capability → engine

`config/engine.go` defines six engine seams. Each is set with a
`config.With<X>Engine(...)` option:

| Capability | Option | Backed by |
|-----------|--------|-----------|
| Relational database | `WithDatabaseEngine` | real Postgres / MySQL |
| Cache | `WithCacheEngine` | real Redis |
| Functions | `WithFunctionEngine` | real code execution (subprocess / Docker) |
| Compute | `WithComputeEngine` | Docker containers as VMs |
| Containers | `WithContainerEngine` | Docker containers |
| Object storage | `WithStorageEngine` | filesystem-backed object bytes |

### Two backing modules

The engine implementations are in separate Go modules, so their large
dependencies stay out of the core `cloudemu` module:

- `contrib/realengine` (no Docker): real Postgres via `embedded-postgres`,
  real Redis via `miniredis`, real function execution via the host's
  `python3`/`node`, and filesystem-backed object storage.
- `contrib/dockerengine` (Docker required): MySQL, Docker-backed compute
  and containers, and Azure Functions. Tests are skipped when Docker isn't available.

### Wiring engines (Go)

```go
import (
    cloudemu "github.com/stackshy/cloudemu/v2"
    "github.com/stackshy/cloudemu/v2/config"
    "github.com/stackshy/cloudemu/v2/contrib/realengine/postgres"
)

pg, _ := postgres.New()                 // starts a real Postgres
aws := cloudemu.NewAWS(config.WithDatabaseEngine(pg))
defer aws.Close()                        // Provider.Close() tears down every wired engine
```

`Provider.Close()` calls `Options.EngineClosers()`, so every engine that
implements `io.Closer` is shut down when the provider is closed.

For the standalone server, the `cloudemu-server` binary in `contrib/server`
bundles the engines and turns them on with flags (`--db`, `--cache`, `--functions`,
`--compute`, `--containers`, `--all-real`). See
[standalone-server.md: Real engines](standalone-server.md#real-engines).

## 12. Persistence (snapshot & restore, opt-in)

State lives in `memstore`, so by default nothing survives: it is lost when the
process exits, and `/_cloudemu/reset` empties it. If you want state to survive
a restart, the `persist` package saves the whole emulator as one JSON document
and restores it into a fresh instance. It is opt-in, and CloudEmu doesn't write
to disk unless you ask it to.

Two properties matter:

- It covers everything. Every stateful service (one holding an in-memory
  `memstore.Store`) in AWS, Azure, GCP and OCI is captured. A completeness
  test (`persist/completeness_test.go`) fails the build if someone adds a
  stateful service without persistence support.
- It keeps identities. Resource IDs and the ID references between resources
  are written as-is, so clients can't tell a restored instance from the
  original: a restored EC2 instance keeps its `i-…` ID.

Services are found by reflection (`internal/snapshot.Discover`, exposed per
provider as `SnapshotServices()`), and each mock saves and restores itself via
the `internal/snapshot.Snapshottable` interface. The file on disk is a single
readable JSON document covering every provider, which diffs cleanly in version
control (schema version 5).

```go
import (
    cloudemu "github.com/stackshy/cloudemu/v2"
    "github.com/stackshy/cloudemu/v2/persist"
)

aws := cloudemu.NewAWS()
targets := map[string]persist.Services{"aws": aws.SnapshotServices()}

snap, _ := persist.ExportAll(ctx, targets, persist.Options{IncludeAssets: true})
_ = snap.WriteFile("state.json")

fresh := cloudemu.NewAWS()
loaded, _ := persist.ReadFile("state.json")
_ = persist.RestoreAll(ctx, &loaded, map[string]persist.Services{"aws": fresh.SnapshotServices()})
```

`Options{IncludeAssets: false}` (the default) gives a metadata-only snapshot
without large object bodies. On the standalone server the same feature is
available as `cloudemu serve --persist`, the `cloudemu snapshot save`/`load` commands, and the
`GET`/`POST /_cloudemu/snapshot` endpoint. See [persistence.md](persistence.md).

---

## 13. VCR record / replay (`features/vcr`)

`features/vcr` records the wire traffic through the standalone server into a
cassette and replays it later, so a recorded session can be served again with no
backend. It wraps each provider handler as HTTP middleware:

- Record (`vcr.ModeRecord`) passes requests to the in-memory handler and appends
  each request/response pair to the cassette, which is written to disk on shutdown.
- Replay (`vcr.ModeReplay`) serves the matching recorded responses. In strict
  mode (the default), a request with no recorded match returns `501` instead of
  falling through, so a replay reproduces exactly what was recorded.

On the server, use `--vcr record|replay`, `--vcr-cassette <path>`, and
`--vcr-strict` (see [standalone-server.md](standalone-server.md#flags)). In Go,
`vcr.New(vcr.Options{Mode, CassettePath, Strict, Clock})` returns a `*VCR`. Its
`Wrap(next, provider)` method returns the middleware and `Flush()` writes the cassette.

---

## 14. State fork and rewind (`features/timetravel`)

`features/timetravel` is a registry of named snapshots built on the persistence
capture/restore functions. It lets you save, restore and branch the state of a
running emulator:

| Operation | Effect |
|-----------|--------|
| `Save(name)` | capture current whole-emulator state under a name |
| `Rewind(name)` | restore a saved state, discarding everything since |
| `Fork(from, to)` | copy a saved state to a new name (branch off a checkpoint) |
| `Delete(name)` / `List()` | drop a saved state / enumerate them |

The standalone server exposes these on the admin control plane as
`POST /_cloudemu/snapshot/{name}` (save), `DELETE …/{name}`,
`POST …/{name}/rewind`, and `POST …/{from}/fork/{to}`. See
[persistence.md](persistence.md#admin-endpoint-_cloudemusnapshot) and
[standalone-server.md](standalone-server.md#named-snapshots-snapshot-save--load--list--delete).

---

## 15. Service quotas (`features/quota`)

`features/quota` is a per-service quota registry. It holds default and overridden
limits per `(serviceCode, quotaCode)`, records quota-increase requests
(`RequestIncrease` returns a tracked `ChangeRequest`), and keeps their history.
The AWS Service Quotas SDK-compat handler (`server/aws/servicequotas`) is built on it.
