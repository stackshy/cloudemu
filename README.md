<p align="center">
  <h1 align="center">cloudemu</h1>
  <p align="center"><b>Zero-Cost In-Memory Cloud Emulation for Go</b></p>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/stackshy/cloudemu"><img src="https://pkg.go.dev/badge/github.com/stackshy/cloudemu.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/stackshy/cloudemu"><img src="https://goreportcard.com/badge/github.com/stackshy/cloudemu" alt="Go Report Card"></a>
  <a href="https://github.com/stackshy/cloudemu/blob/development/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
  <a href="https://github.com/stackshy/cloudemu/actions"><img src="https://img.shields.io/github/actions/workflow/status/stackshy/cloudemu/go.yml?branch=development&label=tests" alt="Tests"></a>
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white" alt="Go Version">
  <img src="https://img.shields.io/badge/providers-AWS_|_Azure_|_GCP-orange" alt="Providers">
  <img src="https://img.shields.io/badge/cost-$0-brightgreen" alt="Zero Cost">
  <a href="https://cloudemu.vercel.app"><img src="https://img.shields.io/badge/docs-cloudemu.vercel.app-blueviolet" alt="Documentation"></a>
</p>

---

cloudemu is a Go library that emulates cloud services entirely in memory. No real cloud accounts, no Docker, no network calls. Import the package, create a provider, and test your cloud code instantly.

```go
aws := cloudemu.NewAWS()
azure := cloudemu.NewAzure()
gcp := cloudemu.NewGCP()
```

## Installation

```bash
go get github.com/stackshy/cloudemu
```

Requires Go 1.25.0+.

## The Problem

Testing cloud-dependent code is painful. You either pay for real accounts, wrestle with heavy emulators that need Docker, or write incomplete mocks from scratch.

| Approach | Cost | Speed | Offline |
|----------|------|-------|---------|
| Real cloud (AWS/Azure/GCP) | $$$ | Slow (seconds) | No |
| LocalStack / Emulators | $ | Medium (100ms+) | Yes |
| **cloudemu** | **Free** | **Fast (~10ms)** | **Yes** |

## How It Works

cloudemu provides mock implementations of cloud services that behave like the real thing. Each provider (AWS, Azure, GCP) gives you typed access to all supported services. You call the same operations you'd call in production — create resources, read data, manage lifecycle — and cloudemu handles it in memory with realistic behavior.

### Example: EC2 Compute

```go
package main

import (
    "context"
    "fmt"

    "github.com/stackshy/cloudemu"
    "github.com/stackshy/cloudemu/compute/driver"
)

func main() {
    ctx := context.Background()
    aws := cloudemu.NewAWS()

    // Launch 2 instances — they start in "pending" and transition to "running"
    instances, _ := aws.EC2.RunInstances(ctx, driver.InstanceConfig{
        ImageID:      "ami-0abcdef1234567890",
        InstanceType: "t2.micro",
        Tags:         map[string]string{"env": "test"},
    }, 2)

    fmt.Println(instances[0].State) // "running"
    fmt.Println(instances[0].ID)    // "i-00000001"

    // Stop an instance — state machine enforces valid transitions
    _ = aws.EC2.StopInstances(ctx, []string{instances[0].ID})

    // Describe to check state
    desc, _ := aws.EC2.DescribeInstances(ctx, []string{instances[0].ID}, nil)
    fmt.Println(desc[0].State) // "stopped"

    // Terminate
    _ = aws.EC2.TerminateInstances(ctx, []string{instances[0].ID})

    // Trying to stop a terminated instance returns an error — just like real EC2
    err := aws.EC2.StopInstances(ctx, []string{instances[0].ID})
    fmt.Println(err) // "cannot stop instance: invalid transition"
}
```

This same pattern works across all services and all three providers. Replace `aws.EC2` with `azure.VirtualMachines` or `gcp.GCE` and the code behaves identically.

## What Makes It Realistic

cloudemu goes beyond basic CRUD mocks. It reproduces real cloud behaviors so your tests catch real issues:

- **State Machines** — VMs enforce valid lifecycle transitions (`pending → running → stopped → terminated`). Illegal transitions return errors.
- **Auto-Metrics** — Launching a VM automatically pushes CPU, Network, and Disk metrics to the monitoring service. Stop/start/terminate emit corresponding metric values.
- **Alarm Evaluation** — Push metric data and alarms automatically transition between `OK` and `ALARM` based on threshold comparison.
- **IAM Policy Evaluation** — Parses JSON policy documents with wildcard matching. Explicit `Deny` overrides `Allow`.
- **FIFO Deduplication** — FIFO queues enforce 5-minute deduplication windows.
- **Dead-Letter Queues** — Messages exceeding max receive count move to the DLQ automatically.
- **TTL Expiry** — Cached items and database records expire after their TTL. Lazy cleanup on read.
- **Stream/Change Feed** — Database mutations produce stream records (INSERT/MODIFY/REMOVE).
- **Numeric-Aware Comparisons** — Database filters compare `"10" > "9"` correctly using numeric ordering.

## Use the Real AWS SDK (no code changes)

If your app already uses `aws-sdk-go-v2`, you don't have to rewrite anything. cloudemu ships an HTTP server that speaks the real AWS wire protocols. Point the SDK's `BaseEndpoint` at localhost and your production code just works.

```go
import (
    "github.com/stackshy/cloudemu"
    awsserver "github.com/stackshy/cloudemu/server/aws"
)

aws := cloudemu.NewAWS()
srv := awsserver.New(awsserver.Drivers{
    S3: aws.S3, DynamoDB: aws.DynamoDB, EC2: aws.EC2,
})
ts := httptest.NewServer(srv)

// Use the REAL aws-sdk-go-v2 client — only the endpoint changes.
s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String(ts.URL)
    o.UsePathStyle = true
})
s3Client.PutObject(ctx, &s3.PutObjectInput{...}) // works
```

Currently covered:

| Service | Operations |
|---------|-----------|
| **S3** | CreateBucket, DeleteBucket, ListBuckets, PutObject, GetObject, HeadObject, DeleteObject, ListObjectsV2, CopyObject |
| **DynamoDB** | CreateTable, DeleteTable, DescribeTable, ListTables, PutItem, GetItem, DeleteItem, Query |
| **EC2** | RunInstances, DescribeInstances (with filters), StartInstances, StopInstances, RebootInstances, TerminateInstances, ModifyInstanceAttribute |
| **EC2 — VPC & Networking** | CreateVpc, DeleteVpc, DescribeVpcs, CreateSubnet, DeleteSubnet, DescribeSubnets, CreateSecurityGroup, DeleteSecurityGroup, DescribeSecurityGroups, AuthorizeSecurityGroupIngress/Egress, RevokeSecurityGroupIngress/Egress, CreateInternetGateway, AttachInternetGateway, DetachInternetGateway, DescribeInternetGateways, CreateRouteTable, DescribeRouteTables, CreateRoute |
| **EC2 — EBS & Key Pairs** | CreateVolume, DeleteVolume, DescribeVolumes, AttachVolume, DetachVolume, CreateKeyPair, DeleteKeyPair, DescribeKeyPairs |
| **Auto Scaling** | CreateAutoScalingGroup, Update/Delete/DescribeAutoScalingGroups, SetDesiredCapacity, Put/Delete/ExecutePolicy |
| **EC2 — Snapshots/AMIs** | Create/Delete/DescribeSnapshots, Create/Deregister/DescribeImages |
| **EC2 — Spot/Launch Templates** | Request/Cancel/DescribeSpotInstanceRequests, Create/Delete/DescribeLaunchTemplates |
| **EC2 — NAT/Peering/Flow Logs** | NAT gateways, VPC peering connections, VPC flow logs |
| **EC2 — Network ACLs** | Create/Delete/DescribeNetworkAcls, Create/DeleteNetworkAclEntry |

More services land progressively — see [docs/sdk-server.md](docs/sdk-server.md).

## Cross-Cutting Features

Every service can be wrapped with a portable API layer that adds test-oriented features:

```go
bucket := storage.NewBucket(aws.S3,
    storage.WithRecorder(rec),              // record every API call
    storage.WithMetrics(mc),                // track call counts and durations
    storage.WithErrorInjection(inj),        // simulate cloud failures
    storage.WithRateLimiter(limiter),       // simulate API throttling
    storage.WithLatency(5*time.Millisecond),// simulate network delay
)
```

| Feature | What It Does |
|---------|-------------|
| **Call Recording** | Capture every API call with inputs, outputs, errors, and timing |
| **Error Injection** | Simulate failures: always, every Nth call, probabilistic, or first N calls |
| **Rate Limiting** | Token bucket limiter that returns `Throttled` errors when exhausted |
| **Metrics Collection** | Track `calls_total`, `call_duration`, `errors_total` per operation |
| **Fake Clock** | Control time for deterministic testing of TTL, dedup, alarms |
| **Latency Simulation** | Add delays to test timeout handling |

## Configuration

```go
aws := cloudemu.NewAWS(
    config.WithRegion("eu-west-1"),
    config.WithAccountID("999888777666"),
    config.WithClock(config.NewFakeClock(time.Now())),
)
```

## Error Handling

All operations return errors with canonical codes. Use helper functions to check types:

```go
import cerrors "github.com/stackshy/cloudemu/errors"

_, err := aws.S3.GetObject(ctx, "bucket", "missing-key")
if cerrors.IsNotFound(err) {
    // handle missing resource
}

// Codes: NotFound, AlreadyExists, InvalidArgument,
//        FailedPrecondition, PermissionDenied, Throttled
```

## Architecture

Three-layer design inspired by Go CDK, plus pluggable cross-service engines:

```
SDK-Compat Server  →  point real aws-sdk-go-v2 at localhost (server/)
Topology Engine    →  simulate network connectivity (topology/)
        ↓ both consume the same contract ↓
Portable API       →  recording, metrics, rate limiting, error injection
Driver Interface   →  minimal Go interfaces per service
Provider Mocks     →  in-memory backends (AWS/Azure/GCP) using generic memstore
```

All mocks are backed by a generic, thread-safe `memstore.Store[V]`. Services emit cloud-native metrics to the monitoring service, so you can query metrics via `GetMetricData` the same way you would with real CloudWatch, Azure Monitor, or Cloud Monitoring. The SDK-compat server and topology engine sit above the driver layer as separate consumers — new services plug in without touching the core.

## Running Tests

```bash
go build ./...   # compile
go vet ./...     # static analysis
go test -v ./... # run all tests
```

## License

MIT
