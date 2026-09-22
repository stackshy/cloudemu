# Chaos Engineering

CloudEmu can make services fail or slow down for a set period of time. This lets your tests exercise the code that handles cloud failures.

## How it works

Wrap a driver with the chaos engine before you pass it to the portable API or the SDK-compat HTTP server. Then apply scenarios at runtime. Each scenario affects every call that reaches the wrapped driver, whether it comes from the Go API or an SDK.

Chaos is set up in-process (library mode), because you wrap the driver in Go. That is different from integrating cloudemu with a running app, which uses [server mode and an SDK endpoint override](integration.md) and does not expose chaos. The `httptest.NewServer` below is the in-process wiring. It is not a suggestion to start cloudemu from a `_test.go` file for integration tests.

```go
import (
    "github.com/stackshy/cloudemu/v2"
    "github.com/stackshy/cloudemu/v2/features/chaos"
    "github.com/stackshy/cloudemu/v2/config"
    awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

cloud  := cloudemu.NewAWS()
engine := chaos.New(config.RealClock{})
defer engine.Stop()

// Wrap the S3 driver. Same wrapper works for Go API or SDK-compat path.
chaosS3 := chaos.WrapBucket(cloud.S3, engine)

srv := awsserver.New(awsserver.Drivers{S3: chaosS3})
ts  := httptest.NewServer(srv)

// Apply a scenario; SDK calls during the window will fail or slow down.
engine.Apply(chaos.ServiceOutage("storage", 5*time.Second))
```

## Scenarios

| Scenario | What it does |
|---|---|
| `ServiceOutage(svc, duration)` | Every call to `svc` returns `Unavailable` until the window expires |
| `LatencySpike(svc, extra, duration)` | Adds `extra` latency on every call to `svc` |
| `ProbabilisticFailure(svc, op, err, p, duration)` | Returns `err` on a fraction `p` of calls to `svc.op` |
| `Throttle(svc, op, qps, duration)` | Returns `Throttled` once `qps` calls/sec is exceeded |
| `Composite(scenarios...)` | Combines several scenarios; latencies sum, first error wins |

`engine.Apply` returns an `*Active` handle. Call `.Stop()` on it to end the scenario before it expires.

## Supported drivers

There are 20 `Wrap*` helpers in the portable service layer. They cover storage, compute, database, cache, DNS, IAM, container registry, logging, event bus, load balancer, message queue, monitoring, networking, notification, secrets, serverless, and the ML/GenAI services (SageMaker, Vertex AI, Azure AI, Azure Search):

`WrapBucket`, `WrapCompute`, `WrapDatabase`, `WrapCache`, `WrapDNS`, `WrapIAM`,
`WrapContainerRegistry`, `WrapLogging`, `WrapEventBus`, `WrapLoadBalancer`,
`WrapMessageQueue`, `WrapMonitoring`, `WrapNetworking`, `WrapNotification`,
`WrapSecrets`, `WrapServerless`, `WrapSageMaker`, `WrapVertexAI`, `WrapAzureAI`,
`WrapAzureSearch`.

## Inspecting what happened

```go
events := engine.Recorded()  // every Effect that was applied
engine.Reset()               // clear the buffer between test phases
```

## Planned

- `SlowDegradation` (latency ramps up over a window)
- `BurstFailure` (N consecutive failures)
- `NetworkPartition` (cross-service: A → B fails, B → A is fine)
- Pre-built scenarios based on real cloud incidents (e.g. the 2017 AWS us-east-1 S3 outage)
- Cascade failures via the dependency graph
