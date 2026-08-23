# Repository Structure & Naming Convention

This is the **source of truth** for how code is named and laid out in cloudemu. It
exists so every service is named the same way in every layer, every file lands in a
predictable place, and a new service or feature has an obvious home. Read this before
adding a service, a resource, or a sub-surface.

For the *why* of the three-layer design, see [architecture.md](architecture.md). This
document is only about **naming and placement**.

---

## 1. The three layers

Every service spans up to three layers, in three parallel trees:

```
services/<capability>/           # portable API + driver interface (cross-cloud abstraction)
providers/<cloud>/<service>/     # in-memory mock for one cloud
server/<cloud>/<service>/        # SDK-compat wire handler for one cloud
```

- `<cloud>` is one of `aws`, `azure`, `gcp`.
- Not every service has all three layers — a product-specific mock (e.g. `bedrock`)
  may have no cross-cloud `services/` abstraction, and some services have no wire
  handler yet. That is fine; the naming rules below still apply to whichever layers exist.

### Sibling modules under `contrib/`

Heavyweight, opt-in add-ons live in their own Go modules under `contrib/` so their
dependencies stay out of the core `cloudemu` module:

```
contrib/realengine/       # opt-in real engines, no Docker (embedded-postgres, miniredis, subprocess)
contrib/dockerengine/     # opt-in real engines, Docker required (mysql, compute, containers, azure functions)
contrib/server/           # the batteries-included `cloudemu-server` binary (engines wired onto the wire servers)
contrib/testcontainers/   # a Testcontainers Go module
```

A real-engine implementation goes in the matching `contrib/<realengine|dockerengine>/<capability>/`
subpackage and satisfies the `config.<X>Engine` interface from `config/engine.go`.

---

## 2. Naming (the rule)

cloudemu is an **SDK-compat emulator**: pointing a real SDK/CLI at it should feel
natural. So provider and wire layers keep the name that cloud's own SDK uses, while
the shared abstraction uses a generic capability name.

### 2.1 `services/<capability>` — generic capability name
The shared abstraction is named for the **capability**, never a product:
`storage`, `compute`, `networking`, `loadbalancer`, `iam`, `database`,
`relationaldb`, `dns`, `monitoring`, `logging`, `secrets`, `messagequeue`,
`eventbus`, `serverless`, `cache`, `containerregistry`, `notification`.

Product-specific services with no cross-cloud abstraction keep their **product name**
consistently (`bedrock`, `vertexai`, `databricks`, `bigtable`, `ecs`, `sagemaker`, …).

### 2.2 `providers/<cloud>/<service>` and `server/<cloud>/<service>` — short service name, no cloud prefix
The mock and wire layers use the **service's own short name, without a cloud prefix** —
the real SDK/CLI name where a service clearly has one (`s3`, `ec2`, `elbv2`), and a short
capability name otherwise (`cache`, `dns`, `monitor`, `sql` — Azure's SDK spells these
`armredis`/`armdns`/`armmonitor`, but the short form reads better and stays prefix-free):

| Capability | AWS | Azure | GCP |
|---|---|---|---|
| Storage | `s3` | `blobstorage` | `gcs` |
| Compute | `ec2` | `virtualmachines` | `compute` |
| Networking | `vpc` | `vnet` | `vpc` |
| IAM | `iam` | `iam` | `iam` |
| Load balancer | `elbv2` | `loadbalancer` | `loadbalancer` |
| Database (NoSQL) | `dynamodb` | `cosmosdb` | `firestore` |
| DNS | `route53` | `dns` | `clouddns` |
| Cache | `elasticache` | `cache` | `memorystore` |
| Monitoring | `cloudwatch` | `monitor` | `monitoring` |

Two hard rules:

- **No redundant cloud prefix.** The parent directory already encodes the cloud, so an
  `aws`/`azure`/`gcp` prefix on the leaf is noise. Drop it:
  `awsiam` → `iam`, `azurecache` → `cache`, `azuredns` → `dns`, `azureiam` → `iam`,
  `azurelb` → `loadbalancer`, `azuremonitor` → `monitor`, `azuresql` → `sql`,
  `azureai` → `ai`, `azuresearch` → `search`, `gcpiam` → `iam`, `gcplb` → `loadbalancer`,
  `gcpvpc` → `vpc`.
- **Provider ↔ wire match within a cloud.** For a given cloud, the `providers/<cloud>/X`
  and `server/<cloud>/X` directories share the **same** name `X`, so the two layers of a
  service map by name (e.g. `providers/azure/blobstorage` ↔ `server/azure/blobstorage`).

> Names are **not** forced to be identical *across* clouds at the provider/wire layer —
> `s3` / `blobstorage` / `gcs` each read naturally for their own cloud, which is the point.
> Cross-cloud unification lives in the `services/` name only.

**Documented exceptions to the "provider ↔ wire same name" rule:**
- **AWS `providers/aws/vpc` ↔ `server/aws/ec2`.** AWS's own SDK folds VPC operations under
  the EC2 service, so the wire handler lives in `ec2`; the mock keeps the clearer `vpc` name.
  Pre-existing and intentional.
- **`services/azureai` / `services/azuresearch` keep the `azure` in their name**, while their
  providers dropped it (`providers/azure/ai`, `providers/azure/search`). These are
  **product-specific** services (Azure AI, Azure AI Search) with no cross-cloud abstraction,
  so per §2.1 the product name — including the `azure` that is part of the product — is kept
  at the `services/` layer.

---

## 3. File naming (the rule)

- **`snake_case`, full words, no abbreviations** — in *every* layer.
  `natgw.go` → `nat_gateway.go`, `eip.go` → `elastic_ip.go`, `eni.go` → `network_interface.go`,
  `igw.go` → `internet_gateway.go`, `trafficmirror.go` → `traffic_mirror.go`.
- **A feature uses the same filename across all three layers**, so one `grep` (or one
  filename) finds the interface, the mock, and the wire handler for a feature.

| Feature | ✅ filename (all layers) |
|---|---|
| NAT gateway | `nat_gateway.go` |
| Internet gateway | `internet_gateway.go` |
| Elastic IP / public IP | `elastic_ip.go` |
| Network interface | `network_interface.go` |
| Traffic mirroring | `traffic_mirror.go` |

Test files sit next to what they test: `<feature>_test.go`.

---

## 4. Per-directory layout (the template)

### `services/<capability>/`
```
driver/driver.go        # the driver interface(s) — always in a driver/ subpackage
<capability>.go         # portable API + do()/pipeline wiring
<capability>_test.go
<feature>.go            # optional: one file per portable-API feature area
```

### `providers/<cloud>/<service>/`
```
<service>.go            # store + core CRUD
<service>_test.go
<feature>.go            # one file per feature (same filename as the wire layer's)
clone.go                # optional: copy-on-write helpers
tags.go                 # optional: tagging, if the service is tagged
```

### `server/<cloud>/<service>/`
```
handler.go              # routing / dispatch (Matches + ServeHTTP)
types.go                # wire DTOs + mapping to/from the driver
<feature>.go            # one wire file per feature (same filename as the provider's)
```

### The `driver/` subpackage rule
Every **portable-API service** in `services/` puts its interface in a `driver/`
subpackage. Documented exceptions — these are *not* portable-API services and correctly
have no `driver/`:

- `services/kubernetes` — a self-contained data-plane engine (its own HTTP surface).
- `services/resourcediscovery` — a cross-service engine that *consumes* other drivers.
- `services/cost`, `services/scope` — cross-cutting utilities, not a cloud capability.

If you add a genuine portable-API service, it **must** have `driver/`.

---

## 5. Sub-surfaces → same-named subdirectories

When a service grows a large or self-contained sub-surface, promote it to a
**same-named subdirectory in each layer** instead of a pile of flat files.
*Illustrative only* (no service is laid out this way today — see the flat-exception note
below; in the current tree traffic mirroring is the flat `providers/aws/vpc/traffic_mirror.go`
/ `server/aws/ec2/traffic_mirror.go`):

```
services/networking/traffic_mirror/        # interface fragment
providers/aws/networking/traffic_mirror/   # mock
server/aws/networking/traffic_mirror/      # wire
```

**Rule of thumb:** a sub-surface with **more than ~5 files** or a self-contained
sub-API earns its own same-named subdirectory across the layers it touches. Smaller
sub-surfaces stay as flat `<feature>.go` files. Either way the filename/dirname is the
same in every layer, so a feature always has one home.

### This rule is for NEW services — existing coupled mocks stay flat
Subdirectory promotion applies to **new services designed that way from the start**,
where each sub-surface owns its state. It is **not** retrofitted onto existing large
single-`Mock` providers such as `providers/aws/vpc` and `providers/azure/databricks`,
which are an **accepted flat exception**. In Go a directory is a package, so promoting
a sub-surface there would mean splitting one `Mock` (vpc: 256 methods, 57 fields under a
single mutex) into sub-packages — forcing a shared internal helper package, cross-surface
callback injection, and a change to the single-lock concurrency model. That trades real
behavior risk for a navigation win the per-feature `snake_case` files (§3) already deliver.
For these services, keep one cohesive package with one `<feature>.go` per sub-surface.

---

## 6. Adding to the codebase — where things go

**A new resource on an existing service:** add `<feature>.go` (same filename) to the
provider mock, the wire handler, and — if it crosses the portable API — the `driver/`
interface + `services/<capability>` wrapper. Add `<feature>_test.go` beside each.

**A new service:** create `services/<capability>/driver/driver.go` +
`services/<capability>/<capability>.go`, then `providers/<cloud>/<service>/` and
`server/<cloud>/<service>/` for each cloud you implement, all following §2–§4. Wire it
into the provider factory (`providers/<cloud>/<cloud>.go`) and `SetMonitoring()` if it
emits metrics.

---

## 7. Migration policy (for existing drift)

The rules above are the target. Existing code that predates them is aligned
**incrementally, never in a big bang**:

- **One service per PR.** A rename is a mechanical `git mv` + import fixups + factory
  rename, with **no behavior change** in that PR.
- Start with the clearest wins (drop the cloud prefixes in §2.2; the `snake_case`
  filename fixes in §3).
- Keep each PR small so it rebases cleanly against in-flight feature work — the factory
  and wire files are edited by almost every branch, so a repo-wide rename would conflict
  with everything.

A CI check that fails on a new cloud-prefixed directory or a non-`snake_case` filename
keeps drift from creeping back once a service is migrated.
