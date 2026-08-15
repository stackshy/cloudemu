<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/stackshy/cloudemu/development/.github/logo-dark.png" />
    <img src="https://raw.githubusercontent.com/stackshy/cloudemu/development/.github/logo-light.png" alt="cloudemu — the cloud, in memory" width="440" />
  </picture>
</p>

<p align="center"><b>In-memory AWS, Azure &amp; GCP — run it as a local cloud, or mock in-process.</b><br/>Any language. $0. Deterministic. Resettable.</p>

<p align="center">
  <a href="https://github.com/stackshy/cloudemu/pkgs/container/cloudemu"><img src="https://img.shields.io/badge/docker-ghcr.io%2Fstackshy%2Fcloudemu-2496ED?logo=docker&logoColor=white" alt="Docker Image"></a>
  <a href="https://pkg.go.dev/github.com/stackshy/cloudemu/v2"><img src="https://pkg.go.dev/badge/github.com/stackshy/cloudemu/v2.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/stackshy/cloudemu/v2"><img src="https://goreportcard.com/badge/github.com/stackshy/cloudemu/v2" alt="Go Report Card"></a>
  <a href="https://github.com/stackshy/cloudemu/blob/development/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
  <img src="https://img.shields.io/badge/providers-AWS_|_Azure_|_GCP-orange" alt="Providers">
  <img src="https://img.shields.io/badge/cost-$0-brightgreen" alt="Zero Cost">
</p>

---

## What it is

cloudemu emulates the **cloud APIs** of AWS, Azure, and GCP entirely in memory. Point the real cloud SDKs — in any language — at a local endpoint and your unmodified app code runs against an in-memory backend. No accounts, no network, no bill.

It emulates control surfaces, **not** a real cloud: it doesn't run workloads, serve traffic, authenticate requests, enforce quotas, or persist across restarts. That's the point — it's fast, deterministic, and resettable.

## Three ways to run it

1. **Standalone server / Docker** — `docker run … ghcr.io/stackshy/cloudemu` (or `cloudemu serve`). A long-lived local cloud you point **any app, any language** at. LocalStack-style.
2. **SDK-compat HTTP server** (in-process, Go) — a `httptest.NewServer` your Go tests point the real SDKs at.
3. **Go API** — typed in-memory mocks (`aws.S3`, `azure.VirtualMachines`, `gcp.GCE`, …) driven directly.

## Quickstart (Docker)

```sh
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 -p 4570:4570 \
  ghcr.io/stackshy/cloudemu:latest
#   AWS         http://127.0.0.1:4566
#   Azure       https://127.0.0.1:4568   (self-signed TLS)
#   GCP         http://127.0.0.1:4569
#   Kubernetes  https://127.0.0.1:4570
```

Then point your existing SDK or CLI at it — nothing cloudemu-specific:

```sh
aws --endpoint-url http://127.0.0.1:4566 s3 mb s3://demo
aws --endpoint-url http://127.0.0.1:4566 s3 ls
```

The Kubernetes control plane hands back a real kubeconfig, so `kubectl apply -f deployment.yaml` then `kubectl get pods` round-trips end-to-end against the in-memory cluster. Reset all state between tests with `curl -X POST http://127.0.0.1:4566/_cloudemu/reset`.

Full flags, ports, per-SDK wiring, and the [Testcontainers module](https://github.com/stackshy/cloudemu/tree/development/contrib/testcontainers) (auto start/stop in Go tests): [docs/standalone-server.md](docs/standalone-server.md).

## In-process (Go)

Prefer running the emulator inside your Go test process instead of over the network? cloudemu speaks the same wire protocols (AWS Query/JSON/Smithy, Azure ARM, GCP REST) over a local `httptest.NewServer` — change the SDK endpoint and your production code runs against an in-memory backend.

```go
import (
    "net/http/httptest"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/stackshy/cloudemu/v2"
    awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

cloud := cloudemu.NewAWS()
ts := httptest.NewServer(awsserver.New(awsserver.Drivers{
    S3:       cloud.S3,
    DynamoDB: cloud.DynamoDB,
    EC2:      cloud.EC2,
    // …leave fields nil to omit a service
}))
defer ts.Close()

client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String(ts.URL)
    o.UsePathStyle = true
})
client.PutObject(ctx, &s3.PutObjectInput{ /* … */ }) // hits the in-memory backend
```

Or skip the SDK and drive the typed Go API directly:

```go
aws := cloudemu.NewAWS()

instances, _ := aws.EC2.RunInstances(ctx, driver.InstanceConfig{
    ImageID: "ami-0abcdef1234567890", InstanceType: "t2.micro",
}, 2)
_ = aws.EC2.StopInstances(ctx, []string{instances[0].ID})
```

Install: `go get github.com/stackshy/cloudemu/v2` (Go 1.25+). Equivalent Azure/GCP wiring is in [docs/sdk-server.md](docs/sdk-server.md); wiring cloudemu into a real app and test suite is in [docs/integration.md](docs/integration.md).

## What it is / isn't

cloudemu emulates cloud **control surfaces** — the APIs — so your code exercises real request/response behavior without a real account.

**It does:**
- ✅ Emulate cloud **APIs** for fast, deterministic testing — via Docker/standalone `serve`, the SDK-compat HTTP server, the typed Go API, and an in-memory Kubernetes API with built-in controllers.
- ✅ Keep all state in process memory, with a fake clock and deterministic IDs for reproducible tests.

**It does not:**
- ❌ **Run real workloads or containers.** Kubernetes controllers converge synchronously (no scheduler/kubelet); there is no real `kubectl logs`/`exec`, no container runtime, no VM.
- ❌ **Serve real traffic.** A created load balancer, DNS record, or queue models the API object — it does not route packets or deliver over the network.
- ❌ **Authenticate or authorize requests.** Any credentials are accepted and signatures are not verified. The IAM service evaluates policies only when you explicitly call it; it does not gate other services' operations.
- ❌ **Enforce quotas, limits, or billing.**
- ❌ **Persist state across restarts.** Everything is in memory and gone when the process exits.

Per-service "Not in scope" notes live alongside each service in the generated [capability coverage](docs/coverage/README.md).

## What's supported

The authoritative, always-current list is the generated [capability coverage](docs/coverage/README.md) — one page listing every service and every operation, produced from the driver interfaces by `go generate` so it cannot drift. The table below is a curated overview.

| Domain | AWS | Azure | GCP |
|---|---|---|---|
| Storage | S3 | Blob Storage | GCS |
| Compute | EC2 (+ VPC, EBS, Snapshots, AMIs, Spot, Launch Templates, Auto Scaling) | Virtual Machines (+ Disks, Snapshots, Images, SSH keys) | Compute Engine (+ Disks, Snapshots, Images) |
| NoSQL DB | DynamoDB | Cosmos DB | Firestore |
| Relational DB | RDS + Aurora (incl. Neptune & DocumentDB engines), Redshift | SQL Database, PostgreSQL Flexible Server, MySQL Flexible Server, Cosmos DB for PostgreSQL (Citus) | Cloud SQL, AlloyDB |
| Wide-column NoSQL | Keyspaces (Cassandra) | Managed Instance for Apache Cassandra | Bigtable |
| In-memory / Redis | ElastiCache, MemoryDB | Cache for Redis | Memorystore |
| Kubernetes | EKS (control plane + data plane) | AKS (control plane + data plane) | GKE (control plane + data plane) |
| Serverless | Lambda | Functions | Cloud Functions v1 |
| Container Orchestration | ECS | — | — |
| Container Registry | ECR | ACR | Artifact Registry |
| Message Queue | SQS | Service Bus | Pub/Sub |
| Event Bus | EventBridge | Event Grid | Eventarc |
| Notification | SNS | Notification Hubs | FCM |
| Networking | VPC (under EC2) | Virtual Network | VPC + Subnets + Firewalls + Routes |
| Load Balancer | ELB (ALB/NLB) | Load Balancer | Cloud Load Balancing |
| DNS | Route 53 | Azure DNS | Cloud DNS |
| Monitoring | CloudWatch | Azure Monitor | Cloud Monitoring |
| Logging | CloudWatch Logs | Log Analytics | Cloud Logging |
| Secrets | Secrets Manager | Key Vault | Secret Manager |
| IAM | IAM | Azure RBAC (armauthorization) | Cloud IAM |
| Resource Discovery | Resource Explorer + Resource Groups Tagging API | Resource Graph | Cloud Asset Inventory |
| Generative AI | Bedrock (+ runtime), Bedrock Agent (+ runtime) | — | — |
| Machine Learning | SageMaker (+ runtime) | Azure AI (Foundry / ML) | Vertex AI |
| AI Search | — | Azure AI Search | — |
| Databricks | — | Databricks (ARM workspace + workspace data plane) | — |

**Kubernetes is two layers, both shipped:**

- **Control plane** (EKS / AKS / GKE) — cluster, node-pool, addon / Fargate / maintenance-config lifecycle via the real cloud SDKs.
- **Data plane** (in-memory Kubernetes API) — core, apps, batch, networking, rbac, storage, autoscaling, policy, discovery, **apiextensions** (CRDs) and **admissionregistration** kinds. CRUD, all patch types + **server-side apply** (field ownership + conflicts), `?dryRun=All`, finalizers, `limit`/`continue` pagination, watch streaming with `resourceVersion` resume + BOOKMARK — so real `client-go` `Informer`/`Reflector` machinery works against a cloudemu cluster. There's no scheduler or kubelet, so controllers converge **synchronously** — a Deployment materializes Pods straight to Running, a Job to Succeeded, Services get Endpoints, on every write. It also serves **`metrics.k8s.io`** + **HPA** actuation, **ResourceQuota** / **LimitRange** / **PDB-gated eviction**, **RBAC** SubjectAccessReview + **NetworkPolicy** evaluation, and **opt-in admission webhooks**. See [docs/services.md](docs/services.md) §18.

Full per-service operation list: [docs/services.md](docs/services.md). Per-handler protocol details: [docs/sdk-server.md](docs/sdk-server.md).

## More

- [docs/getting-started.md](docs/getting-started.md) — set up a test in 5 minutes
- [docs/standalone-server.md](docs/standalone-server.md) — run the local dev cloud (Docker, flags, ports)
- [docs/architecture.md](docs/architecture.md) — three-layer design, factory wiring
- [docs/features.md](docs/features.md) — auto-metrics, alarm evaluation, IAM policy evaluation, FIFO dedup, error injection, fake clock
- [docs/chaos.md](docs/chaos.md) — deliberately fail or slow down services to test retry/timeout paths
- [docs/topology.md](docs/topology.md) — network connectivity simulation across VPC, peering, SGs, ACLs

## License

MIT
