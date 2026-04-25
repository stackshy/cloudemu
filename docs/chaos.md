# Chaos Engineering

CloudEmu can deliberately fail or slow down services in controlled, time-bounded ways — so the parts of your app that handle cloud failure can actually be exercised in tests.

This is something real cloud can't do (you can't ask AWS to fail S3 for 5 seconds) and existing emulators don't do well.

## How it works

Wrap any driver with the chaos engine before handing it to the portable API or the SDK-compat HTTP server. Then declare scenarios at runtime and the chaos applies to every call that hits the wrapped driver — Go API or SDK.

```go
import (
    "github.com/stackshy/cloudemu"
    "github.com/stackshy/cloudemu/chaos"
    "github.com/stackshy/cloudemu/config"
    awsserver "github.com/stackshy/cloudemu/server/aws"
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

## Scenarios shipped today

| Scenario | What it does |
|---|---|
| `ServiceOutage(svc, duration)` | Every call to `svc` returns `Unavailable` until the window expires |
| `LatencySpike(svc, extra, duration)` | Adds `extra` latency on every call to `svc` |
| `ProbabilisticFailure(svc, op, err, p, duration)` | Returns `err` on a fraction `p` of calls to `svc.op` |
| `Throttle(svc, op, qps, duration)` | Returns `Throttled` once `qps` calls/sec is exceeded |
| `Composite(scenarios...)` | Combines several scenarios; latencies sum, first error wins |

Each call to `engine.Apply` returns an `*Active` handle with `.Stop()` to cancel before the natural expiry.

## What's wrapped today

`WrapBucket` (S3), `WrapCompute` (EC2), `WrapDatabase` (DynamoDB). Phase 2 will wire the remaining 13 services.

## Inspecting what happened

```go
events := engine.Recorded()  // every Effect that was applied
engine.Reset()               // clear the buffer between test phases
```

## Coming next (Phase 2 / 3)

- Wire chaos into the remaining 13 portable services
- `SlowDegradation` (latency ramps up over a window)
- `BurstFailure` (N consecutive failures)
- `NetworkPartition` (cross-service: A → B fails, B → A is fine)
- Pre-built scenarios based on real cloud incidents (e.g. AWS US-East-1 2017 S3 outage)
- Cascade failures via the dependency graph
