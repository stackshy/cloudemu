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

<p align="center">
  <a href="https://zop.dev/zopday/app/deploy?image=ghcr.io/stackshy/cloudemu:2.5&amp;port=8000"><img src="https://zop.dev/deploytozopday-inkhard.svg" alt="Deploy to Zopday"></a>
</p>

---

cloudemu emulates the **cloud APIs** of AWS, Azure, and GCP entirely in memory. Point the real SDKs or CLIs — in **any language** — at a local endpoint, and your unmodified code runs against an in-memory backend. No accounts, no network, no bill; instant, deterministic, and resettable.

It emulates the API **control surface** your code actually calls, not real infrastructure — which is exactly what removes cost, latency, and flakiness from the loop.

## Three ways to run it

1. **Standalone server / Docker** — `cloudemu serve` (or `docker run … ghcr.io/stackshy/cloudemu`). A long-lived local cloud you point any app, CLI, or SDK at, LocalStack-style.
2. **In-process SDK server** (Go) — a `httptest.NewServer` your tests point the real SDKs at. No container.
3. **Typed Go API** — call the in-memory mocks directly: `cloud.EC2.RunInstances(ctx, …)`.

## Quickstart

```sh
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 -p 4570:4570 \
  ghcr.io/stackshy/cloudemu:latest
#   AWS 4566 · Azure 4568 (TLS) · GCP 4569 · Kubernetes 4570 (TLS)
```

Point any existing SDK or CLI at it — nothing cloudemu-specific:

```sh
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=us-east-1
aws --endpoint-url http://127.0.0.1:4566 s3 mb s3://demo
aws --endpoint-url http://127.0.0.1:4566 s3 ls
```

`kubectl apply -f deployment.yaml` round-trips against the in-memory cluster, and `curl -X POST http://127.0.0.1:4566/_cloudemu/reset` clears all state between tests. Full flags and per-SDK wiring: [docs/standalone-server.md](docs/standalone-server.md).

### In-process (Go)

```go
cloud := cloudemu.NewAWS()
ts := httptest.NewServer(awsserver.NewFromProvider(cloud))
defer ts.Close()

cfg, _ := config.LoadDefaultConfig(ctx) // credentials/region are ignored
client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String(ts.URL)
    o.UsePathStyle = true
})
client.PutObject(ctx, &s3.PutObjectInput{ /* … */ }) // hits the in-memory backend
```

`go get github.com/stackshy/cloudemu/v2` (Go 1.25+) · [docs/getting-started.md](docs/getting-started.md)

## What you get

**~45 AWS · 36 Azure · 24 GCP services**, plus a real in-memory **Kubernetes data plane**. The always-current, generated list is [docs/coverage](docs/coverage/README.md); the highlights:

- **Storage · Compute · Databases** — S3/Blob/GCS, EC2/VMs/GCE, DynamoDB & Cosmos & Firestore, RDS/Aurora, Cloud SQL, AlloyDB
- **Serverless & Containers** — Lambda/Functions, ECS, and EKS/AKS/GKE with a full Kubernetes API
- **Messaging & Events** — SQS/SNS/EventBridge, Service Bus/Event Grid, Pub/Sub/Eventarc
- **Networking · DNS · Load Balancing** — VPC, subnets, security groups, route tables, Route 53/Cloud DNS, ELB
- **Secrets · IAM · Monitoring · Logging** — Secrets Manager/Key Vault, CloudWatch/Azure Monitor, structured logs
- **AI/ML** — Bedrock, SageMaker, Vertex AI

The **Kubernetes data plane** does real CRUD, server-side apply, and watch streaming — so `client-go` informers work — and converges controllers synchronously (a Deployment materializes Pods to Running on write). See [docs/services.md](docs/services.md).

## Works with your tools

- **Terraform / OpenTofu** — real `apply` / `plan` / `destroy` against cloudemu, proven idempotent in CI. Zero boilerplate with the [`cloudemu-tf`](contrib/terraform) wrapper. → [docs/terraform.md](docs/terraform.md)
- **Testcontainers** (Go) — auto start/stop in your test suite. → [contrib/testcontainers](contrib/testcontainers)
- **Any SDK or CLI**, any language — it speaks the real wire protocols.

## Real engines (opt-in)

By default everything is in memory — no real database, cache, or code runs. When you want a resource to do the **real thing** — actual SQL, real Redis, your uploaded function or container — opt in with `config.With<X>Engine(...)`. Two sibling modules keep the heavy dependencies out of the core:

- **[contrib/realengine](contrib/realengine)** — no Docker: embedded Postgres, miniredis, and `python3`/`node` for Lambda/Functions code.
- **[contrib/dockerengine](contrib/dockerengine)** — real containers: MySQL, VM boot scripts, ECS/ACI/Cloud Run, the Azure Functions host.

The in-memory default is unchanged; `Provider.Close()` tears down whatever you wired.

## Persistence (opt-in)

State is in memory and resettable, so it's ephemeral by default. When you want it to survive a restart, snapshot the **whole emulator** — every stateful service across all four providers, identity-preserving — to a single JSON file and restore it into a fresh instance. Run the background server with `--persist`, capture named states with `cloudemu snapshot save`/`load`, hit `GET`/`POST /_cloudemu/snapshot`, or drive it from Go with the `persist` package. → [docs/persistence.md](docs/persistence.md)

## Docs

- [Getting Started](docs/getting-started.md) — a working test in 5 minutes
- [Standalone Server](docs/standalone-server.md) — the local dev cloud (Docker, flags, ports)
- [Terraform / OpenTofu](docs/terraform.md) — run real IaC against cloudemu
- [Persistence](docs/persistence.md) — snapshot & restore the whole emulator's state
- [Architecture](docs/architecture.md) · [Features](docs/features.md) · [Chaos](docs/chaos.md) · [Topology](docs/topology.md)
- [Capability coverage](docs/coverage/README.md) — every service and operation, generated

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the dev setup and the branch-from-`development` flow, and the [Code of Conduct](CODE_OF_CONDUCT.md). Questions or bugs? Open a [GitHub issue](https://github.com/stackshy/cloudemu/issues); for security, follow [SECURITY.md](SECURITY.md).

## License

MIT
