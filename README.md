<p align="center">
  <img src="https://raw.githubusercontent.com/stackshy/cloudemu/development/.github/logo.svg" alt="cloudemu" width="96" height="96" />
</p>

<p align="center">
  <h1 align="center">cloudemu</h1>
  <p align="center"><b>Zero-Cost In-Memory Cloud Emulation for Go</b></p>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/stackshy/cloudemu/v2"><img src="https://pkg.go.dev/badge/github.com/stackshy/cloudemu/v2.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/stackshy/cloudemu/v2"><img src="https://goreportcard.com/badge/github.com/stackshy/cloudemu/v2" alt="Go Report Card"></a>
  <a href="https://github.com/stackshy/cloudemu/blob/development/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white" alt="Go Version">
  <img src="https://img.shields.io/badge/providers-AWS_|_Azure_|_GCP-orange" alt="Providers">
  <img src="https://img.shields.io/badge/cost-$0-brightgreen" alt="Zero Cost">
</p>

---

## What it does

cloudemu emulates AWS, Azure, and GCP cloud services entirely in memory, so you can test cloud-dependent code without real accounts, Docker, or network calls.

It ships two surfaces you can mix and match:

- **SDK-compat HTTP server** — point the real `aws-sdk-go-v2`, `azure-sdk-for-go`, `cloud.google.com/go`, or `databricks-sdk-go` clients at a local endpoint and they just work. No code changes in your app.
- **Go API** — typed in-memory mocks (`aws.S3`, `azure.VirtualMachines`, `gcp.GCE`, …) for tests written against cloudemu directly.

## What it is / isn't

cloudemu emulates cloud **control surfaces** — the APIs — so your code exercises real request/response behavior without a real account. It is **not** a cloud.

**It does:**
- ✅ Emulate cloud **APIs** for fast, deterministic, Docker-free testing — via the Go API, the SDK-compat HTTP server, the standalone `serve` process, and an in-memory Kubernetes API with built-in controllers.
- ✅ Keep all state in process memory, with a fake clock and deterministic IDs for reproducible tests.

**It does not:**
- ❌ **Run real workloads or containers.** Kubernetes controllers converge synchronously (no scheduler/kubelet); there is no real `kubectl logs`/`exec`, no container runtime, no VM.
- ❌ **Serve real traffic.** A created load balancer, DNS record, or queue models the API object — it does not route packets or deliver over the network.
- ❌ **Authenticate or authorize requests.** Any credentials are accepted and signatures are not verified. The IAM service evaluates policies only when you explicitly call it; it does not gate other services' operations.
- ❌ **Enforce quotas, limits, or billing.**
- ❌ **Persist state across restarts.** Everything is in memory and gone when the process exits.

Per-service "Not in scope" notes live alongside each service in the generated [capability coverage](docs/coverage/README.md).

## Install

```bash
go get github.com/stackshy/cloudemu/v2
```

Requires Go 1.25+.

## How it works (SDK-compat)

Most apps already use the official cloud SDKs. cloudemu speaks the same wire protocols (AWS Query/JSON/Smithy, Azure ARM, GCP REST) over a local `httptest.NewServer`. Change the SDK endpoint, and the same production code runs against an in-memory backend.

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
    RDS:      cloud.RDS,
    EKS:      cloud.EKS,
    // …leave fields nil to omit a service
}))
defer ts.Close()

client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String(ts.URL)
    o.UsePathStyle = true
})

client.PutObject(ctx, &s3.PutObjectInput{ /* … */ }) // hits the in-memory backend
```

Equivalent setups for Azure (`azureserver.New`) and GCP (`gcpserver.New`) are in [docs/sdk-server.md](docs/sdk-server.md).

### Standalone server

Prefer a long-lived process you point out-of-process apps at? Run the emulator
as a server and point any SDK — any language — at the printed endpoints:

```sh
go run ./cmd/cloudemu serve
#   AWS         http://127.0.0.1:4566
#   Azure       https://127.0.0.1:4568   (self-signed TLS)
#   GCP         http://127.0.0.1:4569
```

Full flags, port scheme, and per-SDK wiring: [docs/standalone-server.md](docs/standalone-server.md).

The snippet above is a quick taste. To adopt cloudemu in a real app, don't write a demo — wire it into your existing client and tests so your real code runs against it. See [docs/integration.md](docs/integration.md).

## Or use the Go API directly

```go
aws := cloudemu.NewAWS()

instances, _ := aws.EC2.RunInstances(ctx, driver.InstanceConfig{
    ImageID:      "ami-0abcdef1234567890",
    InstanceType: "t2.micro",
}, 2)

_ = aws.EC2.StopInstances(ctx, []string{instances[0].ID})

desc, _ := aws.EC2.DescribeInstances(ctx, []string{instances[0].ID}, nil)
// desc[0].State == "stopped"
```

The same pattern works across all services and all three providers — swap `aws.EC2` for `azure.VirtualMachines` or `gcp.GCE`.

## What's supported

The authoritative, always-current list is the generated [capability coverage](docs/coverage/README.md) — one page listing every service and every operation, produced from the driver interfaces by `go generate` so it cannot drift. The table below is a curated overview.

SDK-compat coverage across AWS, Azure, and GCP:

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

The Kubernetes story is two layers, both shipped:

- **Control plane** (EKS / AKS / GKE) — cluster, node-pool, addon / Fargate / maintenance-config lifecycle via the real cloud SDKs.
- **Data plane** (in-memory Kubernetes API) — core, apps, batch, networking, rbac, storage, autoscaling, policy, discovery, **apiextensions** (CRDs) and **admissionregistration** kinds. Supports CRUD, all patch types + **server-side apply** (field ownership + conflicts), `?dryRun=All`, finalizers, `limit`/`continue` pagination, watch streaming with `resourceVersion` resume + BOOKMARK — so real `client-go` `Informer`/`Reflector` machinery works against a cloudemu-emulated cluster. Kubeconfigs returned by the control plane point at the in-memory data plane — `kubectl apply -f deployment.yaml` followed by `kubectl get pods` round-trips end-to-end.

Emulation model: there is no scheduler or kubelet, so controllers converge **synchronously** — a Deployment interposes a ReplicaSet and materializes Pods straight to Running (a Job's straight to Succeeded), and Services get Endpoints, on every write. On top of the raw object store it also serves **CRDs** (dynamic servable kinds), **`metrics.k8s.io`** + **HPA** actuation, object-count **ResourceQuota** / **LimitRange** / **PDB-gated eviction** enforcement, **RBAC** SubjectAccessReview + **NetworkPolicy** evaluation, and **opt-in admission webhooks**. See [docs/services.md](docs/services.md) §18 for the authoritative capability list.

Full per-service operation list: [docs/services.md](docs/services.md).
Per-handler protocol details and limitations: [docs/sdk-server.md](docs/sdk-server.md).

## More

- [docs/getting-started.md](docs/getting-started.md) — set up a test in 5 minutes
- [docs/architecture.md](docs/architecture.md) — three-layer design, factory wiring
- [docs/features.md](docs/features.md) — auto-metrics, alarm evaluation, IAM policy evaluation, FIFO dedup, error injection, fake clock
- [docs/chaos.md](docs/chaos.md) — deliberately fail or slow down services to test retry/timeout paths
- [docs/topology.md](docs/topology.md) — network connectivity simulation across VPC, peering, SGs, ACLs

## Tests

```bash
go build ./...
go test ./...
```

## License

MIT
