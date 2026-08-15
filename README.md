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

It models the cloud **control surfaces** — the APIs your code actually calls — with real request/response behavior, so the same production code path runs against an in-memory backend. That's what makes it instant, deterministic, and resettable, at $0.

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

For Go tests, run the emulator inside your process — no container. cloudemu speaks the same wire protocols over a local `httptest.NewServer`, so you just change the SDK endpoint:

```go
cloud := cloudemu.NewAWS()
ts := httptest.NewServer(awsserver.New(awsserver.Drivers{
    S3: cloud.S3, DynamoDB: cloud.DynamoDB, EC2: cloud.EC2, // nil fields omit a service
}))
defer ts.Close()

cfg, _ := config.LoadDefaultConfig(ctx) // credentials/region are ignored
client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String(ts.URL)
    o.UsePathStyle = true
})
client.PutObject(ctx, &s3.PutObjectInput{ /* … */ }) // hits the in-memory backend
```

Or skip the SDK and call the typed Go API directly — `cloud.EC2.RunInstances(ctx, …)`, `cloud.S3.PutObject(ctx, …)`.

Install: `go get github.com/stackshy/cloudemu/v2` (Go 1.25+). Azure/GCP wiring: [docs/sdk-server.md](docs/sdk-server.md) · adopting it in a real test suite: [docs/integration.md](docs/integration.md).

## Why it's fast

cloudemu keeps all state in process memory, with a fake clock and deterministic IDs, so tests are reproducible and reset in microseconds. It emulates the API layer — the control surface — rather than provisioning real infrastructure, which is exactly what removes accounts, network, cost, and flakiness from the loop. The precise per-service scope (and the handful of things that are intentionally out of scope, like running real containers or serving live traffic) is documented alongside each service in the generated [capability coverage](docs/coverage/README.md).

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
