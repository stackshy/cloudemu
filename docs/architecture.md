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

The top layer lives in service-specific packages (`services/storage/`, `services/compute/`, `services/database/`, etc.). Each portable API type wraps a driver with cross-cutting concerns. For example, `storage.Bucket` wraps `driver.Bucket` and adds call recording, metrics collection, rate limiting, error injection, and simulated latency to every operation. This layer is provider-agnostic -- the same `storage.Bucket` works with S3, Blob Storage, or GCS.

### Layer 2: Driver Interfaces

Each service defines a minimal Go interface in `<service>/driver/driver.go`. These interfaces specify the operations that every provider must implement. For example, `services/storage/driver/driver.go` defines the `Bucket` interface with methods like `CreateBucket`, `PutObject`, `GetObject`, etc. Driver interfaces use plain Go types (no cloud SDK dependencies).

### Layer 3: Provider Implementations

The bottom layer contains the actual mock implementations for each cloud provider. These live in `providers/aws/`, `providers/azure/`, and `providers/gcp/`. Each implementation uses `internal/memstore.Store[V]` as its backing data structure -- a generic, thread-safe in-memory store. All state lives in process memory with no external dependencies.

## Cross-Service Engines

Sitting above Layer 2 (driver interfaces) are **cross-service engines** that consume driver interfaces directly without going through the portable API. They're peers of each other, not layers in the three-layer stack. Two exist today:

- `features/topology/` -- reads from compute, networking, and DNS drivers to simulate real network connectivity (`CanConnect`, `TraceRoute`, `Resolve`, security-group and NACL evaluation). See [topology.md](topology.md).
- `server/` -- exposes driver interfaces over HTTP in each cloud's native SDK wire format, so real `aws-sdk-go-v2`, `azure-sdk-for-go`, and `cloud.google.com/go` clients work against CloudEmu by only changing the endpoint. Covers Storage, Compute, Database, Networking, Monitoring, Serverless, and Message Queue across all 3 providers (AWS S3/EC2/DynamoDB/Lambda/SQS/CloudWatch + sibling Azure ARM and GCP REST handlers). Uses a pluggable `Handler` registry so new services drop in as self-contained packages without touching the core. See [sdk-server.md](sdk-server.md).

Both engines depend only on Layer 2 interfaces -- never on concrete provider types -- so they work uniformly across AWS, Azure, and GCP backends.

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
3. Instantiates every service mock, passing the shared options to each
4. Wires cross-service dependencies (e.g., compute to monitoring)
5. Returns the `Provider` struct with all services accessible as public fields

```go
aws := cloudemu.NewAWS(
    config.WithRegion("us-west-2"),
    config.WithAccountID("123456789012"),
)

// All services are ready to use
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
go.mod                                # Module: github.com/stackshy/cloudemu/v2
config/
    options.go                        # Options, WithClock, WithRegion, etc.
    clock.go                          # Clock, RealClock, FakeClock
errors/
    errors.go                         # Canonical error codes and helpers
internal/
    memstore/                         # Generic Store[V]
    idgen/                            # ID generators (ARNs, Azure IDs, GCP IDs)
    statemachine/                     # Generic FSM
    pagination/                       # Generic Paginate[T]
services/                             # emulated cloud services (portable API + driver interface)
    storage/
        storage.go                    # Portable storage API
        driver/driver.go              # Bucket interface
    compute/  database/  relationaldb/  serverless/  networking/  monitoring/
    iam/  dns/  loadbalancer/  messagequeue/  cache/  secrets/  logging/
    notification/  eventbus/  containerregistry/  kubernetes/  resourcediscovery/
    bedrock/  sagemaker/  vertexai/  databricks/  azureai/  azuresearch/
    parameterstore/  tablestorage/  cost/
                                      # each: <name>.go (portable API) + driver/ (interface)
features/                             # cross-cutting capabilities you wrap drivers with
    chaos/                            # fault / latency / throttle injection
    recorder/                         # call recording for assertions
    metrics/                          # in-memory metrics collection
    ratelimit/                        # token-bucket rate limiter
    inject/                           # error injection (policies + injector)
    topology/                         # network reachability (CanConnect / TraceRoute / Resolve)
providers/
    aws/
        aws.go                        # AWS factory (wires all services)
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
        rds/                          # RDS mock (Aurora + Neptune + DocumentDB engines)
        redshift/                     # Redshift mock
        eks/                          # EKS control-plane mock (clusters, node groups,
                                      # Fargate profiles, addons)
    azure/
        azure.go                      # Azure factory (wires all services)
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
        azuresql/                     # Azure SQL Database mock
        postgresflex/                 # Azure PostgreSQL Flexible Server mock
        mysqlflex/                    # Azure MySQL Flexible Server mock
        aks/                          # AKS control-plane mock (managed clusters,
                                      # agent pools, maintenance configs)
    gcp/
        gcp.go                        # GCP factory (wires all services)
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
        cloudsql/                     # Cloud SQL mock
        gke/                          # GKE control-plane mock (clusters, node pools,
                                      # operations)
server/                               # SDK-compat HTTP servers (real cloud SDKs work against this)
    server.go                         # core: Handler interface + dispatcher
    wire/
        wire.go                       # shared XML/JSON helpers
        awsquery/                     # AWS query-protocol helpers
        azurearm/                     # ARM URL parser + JSON envelope
        gcprest/                      # GCP REST URL parser + LRO Operation helpers
    aws/
        aws.go                        # awsserver.New(Drivers{...})
        s3/  ec2/  dynamodb/          # S3 REST+XML, EC2 query, DynamoDB JSON-RPC
        cloudwatch/                   # Smithy rpc-v2-cbor
        lambda/  sqs/                 # REST + JSON-RPC handlers
        rds/  redshift/               # query-protocol relational DB handlers
        eks/                          # REST EKS control-plane handler
    azure/
        azure.go                      # azureserver.New(Drivers{...})
        virtualmachines/  disks/  snapshots/  images/  sshpublickeys/
        blob/  cosmos/  network/  monitor/  functions/  servicebus/
        azuresql/  postgresflex/  mysqlflex/   # ARM relational DB handlers
        aks/                          # ARM AKS control-plane handler
    gcp/
        gcp.go                        # gcpserver.New(Drivers{...})
        compute/  networks/  gcs/  firestore/  monitoring/
        cloudfunctions/  pubsub/
        cloudsql/                     # REST Cloud SQL handler
        gke/                          # REST GKE control-plane handler
```
