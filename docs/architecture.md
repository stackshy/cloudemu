# Architecture

## Overview

CloudEmu follows a three-layer architecture that separates portable API concerns, driver interfaces, and provider-specific implementations. This design allows each cloud provider (AWS, Azure, GCP) to implement the same driver interface while the portable API layer adds cross-cutting concerns such as recording, metrics collection, rate limiting, error injection, and latency simulation.

## Three-Layer Architecture

```
┌─────────────────────────────────────────────────────┐
│         Portable API Layer                          │
│   (storage/, compute/, database/, etc.)             │
│   Recording, Metrics, Rate Limiting, Error Inject   │
├─────────────────────────────────────────────────────┤
│                      ↓                              │
├─────────────────────────────────────────────────────┤
│         Driver Interfaces                           │
│   (*/driver/driver.go)                              │
│   Minimal Go interfaces per service                 │
├─────────────────────────────────────────────────────┤
│                      ↓                              │
├─────────────────────────────────────────────────────┤
│         Provider Implementations                    │
│   (providers/aws/, providers/azure/, providers/gcp/)│
│   In-memory backends using memstore.Store[V]        │
├─────────────────────────────────────────────────────┤
│                      ↓                              │
├─────────────────────────────────────────────────────┤
│         In-Memory State                             │
│   (internal/memstore) Generic Store[V]              │
└─────────────────────────────────────────────────────┘
```

### Layer 1: Portable API

The top layer lives in service-specific packages (`storage/`, `compute/`, `database/`, etc.). Each portable API type wraps a driver with cross-cutting concerns. For example, `storage.Bucket` wraps `driver.Bucket` and adds call recording, metrics collection, rate limiting, error injection, and simulated latency to every operation. This layer is provider-agnostic -- the same `storage.Bucket` works with S3, Blob Storage, or GCS.

### Layer 2: Driver Interfaces

Each service defines a minimal Go interface in `<service>/driver/driver.go`. These interfaces specify the operations that every provider must implement. For example, `storage/driver/driver.go` defines the `Bucket` interface with methods like `CreateBucket`, `PutObject`, `GetObject`, etc. Driver interfaces use plain Go types (no cloud SDK dependencies).

### Layer 3: Provider Implementations

The bottom layer contains the actual mock implementations for each cloud provider. These live in `providers/aws/`, `providers/azure/`, and `providers/gcp/`. Each implementation uses `internal/memstore.Store[V]` as its backing data structure -- a generic, thread-safe in-memory store. All state lives in process memory with no external dependencies.

## Cross-Service Wiring

Provider factories automatically wire cross-service dependencies using `SetMonitoring()`. When a compute instance is launched, the compute mock pushes metrics directly into the monitoring service. This wiring is established at provider creation time.

```go
// In providers/aws/aws.go
p.EC2.SetMonitoring(p.CloudWatch)        // EC2 pushes metrics to CloudWatch
p.S3.SetMonitoring(p.CloudWatch)         // S3 pushes metrics to CloudWatch
p.DynamoDB.SetMonitoring(p.CloudWatch)   // DynamoDB pushes metrics to CloudWatch

// In providers/azure/azure.go
p.VirtualMachines.SetMonitoring(p.Monitor) // VMs push metrics to Azure Monitor
p.BlobStorage.SetMonitoring(p.Monitor)     // Blob Storage pushes metrics

// In providers/gcp/gcp.go
p.GCE.SetMonitoring(p.CloudMonitoring)    // GCE pushes metrics to Cloud Monitoring
p.GCS.SetMonitoring(p.CloudMonitoring)    // GCS pushes metrics
```

Currently, 10 services per provider are wired to push auto-metrics to their respective monitoring service: Compute, Storage, Database, Serverless, Message Queue, Cache, Logging, Notification, Container Registry, and Event Bus.

## Provider Factory Pattern

Each provider has a factory function (`New()`) in its top-level package (`providers/aws/aws.go`, `providers/azure/azure.go`, `providers/gcp/gcp.go`). The factory:

1. Accepts functional `config.Option` values for configuration (clock, region, account ID, etc.)
2. Creates `config.Options` from the functional options
3. Instantiates all 16 service mocks, passing the shared options to each
4. Wires cross-service dependencies (e.g., compute to monitoring)
5. Returns the `Provider` struct with all services accessible as public fields

```go
aws := cloudemu.NewAWS(
    config.WithRegion("us-west-2"),
    config.WithAccountID("123456789012"),
)

// All 16 services are ready to use
aws.S3.CreateBucket(ctx, "my-bucket")
aws.EC2.RunInstances(ctx, instanceConfig, 1)
aws.DynamoDB.CreateTable(ctx, tableConfig)
```

## Package Structure Overview

| Package | Purpose |
|---------|---------|
| `config` | Functional options (`WithClock`, `WithRegion`, `WithAccountID`, `WithProjectID`, `WithLatency`), `Clock` interface, `RealClock`, `FakeClock` for deterministic time |
| `errors` | Canonical error codes: `NotFound`, `AlreadyExists`, `InvalidArgument`, `FailedPrecondition`, `PermissionDenied`, `Throttled`, `Internal`, `Unimplemented`, `ResourceExhausted`, `Unavailable` |
| `internal/memstore` | Generic thread-safe `Store[V]` -- the backing data structure for all mock implementations |
| `internal/idgen` | ID generators: AWS ARNs, Azure resource IDs, GCP self-links |
| `statemachine` | Generic finite state machine for VM lifecycle transitions (pending, running, stopping, stopped, terminated) with callback support |
| `pagination` | Generic `Paginate[T]` with base64 page tokens for list operations |
| `recorder` | Call recording for test assertions -- captures service, operation, input, output, error, and duration |
| `metrics` | In-memory metrics collector with Counter, Gauge, and Histogram types |
| `ratelimit` | Token bucket rate limiter that returns `Throttled` errors |
| `inject` | Error injection with policies: `Always`, `NthCall`, `Probabilistic`, `Countdown` |
| `cost` | Simulated cost tracking with per-operation pricing rates |

## File Structure

```
cloudemu.go                           # Entry point: NewAWS(), NewAzure(), NewGCP()
cloudemu_test.go                      # All tests
doc.go                                # Package documentation
go.mod                                # Module: github.com/stackshy/cloudemu
config/
    options.go                        # Options, WithClock, WithRegion, etc.
    clock.go                          # Clock, RealClock, FakeClock
errors/
    errors.go                         # Canonical error codes and helpers
internal/
    memstore/                         # Generic Store[V]
    idgen/                            # ID generators (ARNs, Azure IDs, GCP IDs)
statemachine/
    machine.go                        # Generic FSM
    transitions.go                    # Transition map
pagination/                           # Generic Paginate[T]
recorder/                             # Call recording for assertions
metrics/                              # In-memory metrics collection
ratelimit/                            # Token bucket rate limiter
inject/                               # Error injection (policies + injector)
cost/
    cost.go                           # Cost tracker with default rates
storage/
    storage.go                        # Portable storage API
    driver/driver.go                  # Bucket interface
compute/
    driver/driver.go                  # Compute interface
database/
    driver/driver.go                  # Database interface
serverless/
    driver/driver.go                  # Serverless interface
networking/
    driver/driver.go                  # Networking interface
monitoring/
    driver/driver.go                  # Monitoring interface
iam/
    driver/driver.go                  # IAM interface
dns/
    driver/driver.go                  # DNS interface
loadbalancer/
    driver/driver.go                  # LoadBalancer interface
messagequeue/
    driver/driver.go                  # MessageQueue interface
cache/
    driver/driver.go                  # Cache interface
secrets/
    driver/driver.go                  # Secrets interface
logging/
    driver/driver.go                  # Logging interface
notification/
    driver/driver.go                  # Notification interface
containerregistry/
    driver/driver.go                  # ContainerRegistry interface
eventbus/
    driver/driver.go                  # EventBus interface
providers/
    aws/
        aws.go                        # AWS factory (wires all 16 services)
        s3/                           # S3 mock
        ec2/                          # EC2 mock
        dynamodb/                     # DynamoDB mock
        lambda/                       # Lambda mock
        vpc/                          # VPC mock
        cloudwatch/                   # CloudWatch mock
        awsiam/                       # IAM mock
        route53/                      # Route 53 mock
        elb/                          # ELB mock
        sqs/                          # SQS mock
        elasticache/                  # ElastiCache mock
        secretsmanager/               # Secrets Manager mock
        cloudwatchlogs/               # CloudWatch Logs mock
        sns/                          # SNS mock
        ecr/                          # ECR mock
        eventbridge/                  # EventBridge mock
    azure/
        azure.go                      # Azure factory (wires all 16 services)
        blobstorage/                  # Blob Storage mock
        virtualmachines/              # Virtual Machines mock
        cosmosdb/                     # Cosmos DB mock
        functions/                    # Azure Functions mock
        vnet/                         # VNet mock
        azuremonitor/                 # Azure Monitor mock
        azureiam/                     # Azure IAM mock
        azuredns/                     # Azure DNS mock
        azurelb/                      # Azure LB mock
        servicebus/                   # Service Bus mock
        azurecache/                   # Azure Cache mock
        keyvault/                     # Key Vault mock
        loganalytics/                 # Log Analytics mock
        notificationhubs/             # Notification Hubs mock
        acr/                          # ACR mock
        eventgrid/                    # Event Grid mock
    gcp/
        gcp.go                        # GCP factory (wires all 16 services)
        gcs/                          # GCS mock
        gce/                          # GCE mock
        firestore/                    # Firestore mock
        cloudfunctions/               # Cloud Functions mock
        gcpvpc/                       # GCP VPC mock
        cloudmonitoring/              # Cloud Monitoring mock
        gcpiam/                       # GCP IAM mock
        clouddns/                     # Cloud DNS mock
        gcplb/                        # GCP LB mock
        pubsub/                       # Pub/Sub mock
        memorystore/                  # Memorystore mock
        secretmanager/                # Secret Manager mock
        cloudlogging/                 # Cloud Logging mock
        fcm/                          # FCM mock
        artifactregistry/             # Artifact Registry mock
        eventarc/                     # Eventarc mock
```
