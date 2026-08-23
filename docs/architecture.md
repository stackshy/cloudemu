# Architecture

## Overview

CloudEmu separates **what** a cloud service does from **which** cloud does it, in three layers: a provider-agnostic **portable API**, a minimal **driver interface** per service, and an in-memory **provider mock** per cloud. Every provider implements the same driver interface, so one set of cross-cutting behaviors (recording, metrics, rate limiting, error injection, latency) and one set of wire servers work uniformly across clouds.

AWS, Azure, and GCP are fully implemented; **OCI** is an in-progress fourth provider (see [below](#fourth-provider-oci-in-progress)). The generated [capability coverage](coverage/README.md) is the source of truth for which services exist.

## The three layers

```mermaid
flowchart TB
    L1["<b>Layer 1 · Portable API</b> — services/&lt;svc&gt;<br/>optional wrapper: recording · metrics · rate limiting · error injection · latency<br/>provider-agnostic (the same wrapper works over S3, Blob, or GCS)"]
    L2{{"<b>Layer 2 · Driver interface</b> — services/&lt;svc&gt;/driver<br/>a minimal Go contract per service · no cloud SDK types"}}
    L3["<b>Layer 3 · Provider mock</b> — providers/&lt;cloud&gt;/&lt;service&gt;<br/>an in-memory backend, one per cloud (aws · azure · gcp · oci)"]
    S[("State — internal/memstore Store&lt;V&gt;<br/>or an opt-in real engine")]

    L1 -->|delegates to| L2
    L3 -. implements .-> L2
    L3 -->|reads / writes| S
```

- **Layer 1 — Portable API** (`services/storage`, `services/compute`, `services/database`, …). Each portable type wraps a driver and adds the cross-cutting concerns above to every call. It is provider-agnostic and **optional**: you wrap a driver with it when you want those behaviors. For example `storage.Bucket` wraps any `driver.Bucket` — S3, Blob, or GCS.
- **Layer 2 — Driver interface** (`services/<svc>/driver/driver.go`). The hinge of the whole design: a minimal Go interface listing the operations every provider must implement (`CreateBucket`, `PutObject`, …), using plain Go types. Everything that reaches state goes through a driver.
- **Layer 3 — Provider mock** (`providers/{aws,azure,gcp,oci}/<service>`). The concrete implementation for one cloud. It implements the driver interface and stores state in `internal/memstore.Store[V]` — a generic, thread-safe in-memory store — or, when opted in, a [real engine](#real-data-plane-engines).

## How a call reaches state

There are **two ways in**, and both converge on the driver interface:

```mermaid
flowchart LR
    SDK["Real SDK / CLI / IaC<br/>(any language)"] -->|"HTTP · native protocol"| WH["Wire handler<br/>server/&lt;cloud&gt;/&lt;service&gt;"]
    Go["Go code / tests"] -->|typed call| PW["Portable wrapper<br/>services/&lt;svc&gt; · optional"]
    Go -.->|or straight to the mock| DRV
    WH --> DRV{{"Driver<br/>interface"}}
    PW --> DRV
    DRV -. implemented by .-> MOCK["Provider mock<br/>providers/&lt;cloud&gt;/…"]
    MOCK -->|default| MEM[("memstore")]
    MOCK -.->|"opt-in With&lt;X&gt;Engine"| ENG[("real engine")]
```

- **Standalone / SDK path** — a real `aws-sdk-go-v2`, `azure-sdk-for-go`, or `cloud.google.com/go` client (or a CLI, or IaC) sends an HTTP request in the cloud's native wire format. The matching **wire handler** decodes it and calls the driver. Point the client at the endpoint; nothing else changes. See [sdk-server.md](sdk-server.md) and [standalone-server.md](standalone-server.md).
- **Typed Go path** — `cloudemu.NewAWS().S3.CreateBucket(...)` calls a provider mock (a driver implementation) directly. Wrap it in the portable API first (`storage.New(mock, …)`) when you want recording/metrics/rate-limiting/injection/latency on those calls.

Either way the request lands on the **driver interface**, the provider mock services it, and state lives in `memstore` — unless a real engine is wired in.

## Real data-plane engines

The provider mock's data path is normally `memstore`. Passing a `config.With<X>Engine` option routes that path through an **opt-in real engine** instead — real Postgres/MySQL, Redis, function runtimes, Docker compute/containers, or filesystem-backed object bytes — so clients run real workloads against the emulator. Unset (the default) keeps everything in-memory. Engine code lives in sibling modules `contrib/realengine` (no Docker) and `contrib/dockerengine` (Docker); `Provider.Close()` tears every wired engine down via `Options.EngineClosers()`. Full catalog: [features.md — Real Data-Plane Engines](features.md#11-real-data-plane-engines-opt-in).

## Cross-service engines

Some features need to read **several** drivers at once, so they sit beside the portable API and consume Layer 2 interfaces directly (never concrete provider types), which is why they work uniformly across clouds:

```mermaid
flowchart LR
    subgraph drivers["Driver interfaces (Layer 2)"]
        C["compute"]
        N["networking"]
        D["dns"]
        R["all service drivers"]
    end
    T["features/topology<br/>CanConnect · TraceRoute · Resolve"]
    RD["services/resourcediscovery<br/>one unified inventory view"]
    SV["server/<br/>SDK-compat wire handlers"]

    C --> T
    N --> T
    D --> T
    R --> RD
    R --> SV
```

- **`features/topology`** — reads compute, networking, and DNS drivers to evaluate real network reachability. See [topology.md](topology.md).
- **`services/resourcediscovery`** — walks every driver a provider holds and returns one normalized inventory (backs AWS Resource Explorer, Azure Resource Graph, GCP Cloud Asset). See [features.md](features.md#10-cross-service-resource-discovery).
- **`server/`** — exposes drivers over HTTP in each cloud's native SDK wire format, via a pluggable `Handler` registry so new services drop in as self-contained packages. See [sdk-server.md](sdk-server.md).

## Provider factory & cross-service wiring

Each provider has a factory (`New()` in `providers/aws/aws.go`, etc.) that reads `config.Option` values, instantiates every service mock with the shared options, **wires cross-service dependencies**, and returns a `Provider` struct whose services are public fields.

```go
aws := cloudemu.NewAWS(config.WithRegion("us-west-2"))
defer aws.Close()                       // tears down any wired real engines

aws.S3.CreateBucket(ctx, "my-bucket")
aws.EC2.RunInstances(ctx, instanceConfig, 1)
```

The key wiring is auto-metrics: `SetMonitoring()` connects a service to its monitoring backend at construction, so a launched VM pushes metrics straight into CloudWatch / Azure Monitor / Cloud Monitoring.

```go
p.EC2.SetMonitoring(p.CloudWatch)          // AWS
p.VirtualMachines.SetMonitoring(p.Monitor) // Azure
p.GCE.SetMonitoring(p.CloudMonitoring)     // GCP
```

10 services per provider push auto-metrics this way: Compute, Storage, Database, Serverless, Message Queue, Cache, Logging, Notification, Container Registry, and Event Bus.

## Package map

| Package | Purpose |
|---------|---------|
| `config` | Functional options (`WithClock`/`WithRegion`/`WithAccountID`/`WithProjectID`/`WithLatency`, the `With<X>Engine` engine options), the `Clock` interface, and `FakeClock` for deterministic time |
| `errors` | Canonical error codes: `NotFound`, `AlreadyExists`, `InvalidArgument`, `FailedPrecondition`, `PermissionDenied`, `Throttled`, `Internal`, … |
| `internal/memstore` | Generic thread-safe `Store[V]` — the backing store for every mock |
| `internal/idgen` | ID generators: AWS ARNs, Azure resource IDs, GCP self-links, OCIDs |
| `statemachine` | Generic FSM for VM lifecycle transitions (pending → running → stopping → …) |
| `pagination` | Generic `Paginate[T]` with base64 page tokens |
| `features/{recorder,metrics,ratelimit,inject,chaos,topology}` | The cross-cutting behaviors and cross-service engines |
| `cost` | Simulated per-operation cost tracking |

The source tree mirrors the layers — `services/<svc>/` (portable API + `driver/`), `providers/<cloud>/<service>/` (mocks), `server/<cloud>/<service>/` (wire handlers) — plus the `contrib/*` sibling modules. See [STRUCTURE.md](STRUCTURE.md) for the full layout, naming rules, and where new code goes.

## Fourth provider: OCI (in progress)

OCI (`providers/oci/`) follows the same three-layer shape: a `memstore`-backed mock per service implementing the shared driver, plus a `server/oci/<service>/` handler. Its foundation is in place (identity options, OCID generation in `internal/idgen/ocid.go`, `services/scope` compartment scoping, the `server/wire/ocirest` format, and the `server/oci/workrequest` async envelope); services land one PR at a time — see [oci-conventions.md](oci-conventions.md).

Until every service has landed, OCI diverges in two documented ways:

- **Partially populated provider** — `providers/oci.Provider` declares each service as a bare driver interface, so a service that hasn't landed reads as `nil` rather than failing to compile. The generated [capability coverage](coverage/README.md) is the source of truth for what exists.
- **No OCI branch in resource discovery** — the ARN/ID formatters in `services/resourcediscovery` fall through to a default for OCI, so its resources don't yet get native OCIDs from discovery. This fills in alongside the services that produce discoverable resources.
