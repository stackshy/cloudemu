<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/stackshy/cloudemu/development/.github/logo-dark.png" />
    <img src="https://raw.githubusercontent.com/stackshy/cloudemu/development/.github/logo-light.png" alt="cloudemu logo" width="440" />
  </picture>
</p>

<p align="center">In-memory emulator for the AWS, Azure and GCP APIs. Run it as a local server or embed it in Go tests.</p>

<p align="center">
  <a href="https://github.com/stackshy/cloudemu/pkgs/container/cloudemu"><img src="https://img.shields.io/badge/docker-ghcr.io%2Fstackshy%2Fcloudemu-2496ED?logo=docker&logoColor=white" alt="Docker Image"></a>
  <a href="https://pkg.go.dev/github.com/stackshy/cloudemu/v2"><img src="https://pkg.go.dev/badge/github.com/stackshy/cloudemu/v2.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/stackshy/cloudemu/v2"><img src="https://goreportcard.com/badge/github.com/stackshy/cloudemu/v2" alt="Go Report Card"></a>
  <a href="https://github.com/stackshy/cloudemu/blob/development/LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
  <img src="https://img.shields.io/badge/providers-AWS_|_Azure_|_GCP-orange" alt="Providers">
</p>

<p align="center">
  <a href="https://zop.dev/zopday/app/deploy?image=ghcr.io/stackshy/cloudemu:2.5&amp;port=8000"><img src="https://zop.dev/deploytozopday-inkhard.svg" alt="Deploy to Zopday"></a>
</p>

---

cloudemu serves the AWS, Azure and GCP APIs from memory. You point the normal SDK or CLI (any language) at a local endpoint and your code runs unchanged. You don't need a cloud account or network access, and nothing is billed. State is deterministic and can be reset between tests.

It emulates the API surface your code calls. It does not create real infrastructure.

## Ways to run it

1. Standalone server or Docker: `cloudemu serve` or `docker run … ghcr.io/stackshy/cloudemu`. A long-running local cloud that any app, CLI or SDK can use, similar to LocalStack.
2. In-process SDK server (Go): an `httptest.NewServer` that your tests point the real SDKs at. No container needed.
3. Typed Go API: call the in-memory backends directly, e.g. `cloud.EC2.RunInstances(ctx, …)`.

## Install

```sh
# Homebrew (macOS / Linux)
brew install stackshy/tap/cloudemu

# Install script (macOS / Linux). Downloads the release binary and checks its SHA-256.
curl -fsSL https://raw.githubusercontent.com/stackshy/cloudemu/HEAD/install.sh | sh

# Go toolchain
go install github.com/stackshy/cloudemu/v2/cmd/cloudemu@latest

# Docker (nothing to install, runs the server)
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 -p 4570:4570 \
  ghcr.io/stackshy/cloudemu:latest
```

Prebuilt binaries are on the [releases page](https://github.com/stackshy/cloudemu/releases). The install script takes a version argument (`... | sh -s -- v2.5.0`) and an `INSTALL_DIR` override (`... | INSTALL_DIR="$HOME/bin" sh`). To use cloudemu as a Go library, see [Library mode](#library-mode-go-unit-tests) below.

## Quickstart

To use cloudemu with an existing application, run it as a server and change your SDK's endpoint (`AWS_ENDPOINT_URL` / `BaseEndpoint`, `option.WithEndpoint`, or the Azure ARM endpoint override). You don't need a `_test.go` file for this. The in-process library mode shown further down is for Go unit tests.

```sh
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 -p 4570:4570 \
  ghcr.io/stackshy/cloudemu:latest
#   AWS 4566 · Azure 4568 (TLS) · GCP 4569 · Kubernetes 4570 (TLS)
# Apple Silicon: add --platform linux/amd64 if the amd64 image won't start natively.
```

Any SDK or CLI works against it:

```sh
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_DEFAULT_REGION=us-east-1
aws --endpoint-url http://127.0.0.1:4566 s3 mb s3://demo
aws --endpoint-url http://127.0.0.1:4566 s3 ls
```

In code, you build the client the same way as in production and only change the endpoint:

```go
// AWS (aws-sdk-go-v2), or export AWS_ENDPOINT_URL=http://127.0.0.1:4566
client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String("http://127.0.0.1:4566")
    o.UsePathStyle = true
})

// GCP (cloud.google.com/go)
gcs, _ := storage.NewClient(ctx,
    option.WithEndpoint("http://127.0.0.1:4569"),
    option.WithoutAuthentication())

// Azure (azure-sdk-for-go): ARM endpoint override (HTTPS, self-signed cert)
cloudCfg := cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
    cloud.ResourceManager: {Endpoint: "https://127.0.0.1:4568", Audience: "https://management.azure.com"},
}}
```

The app then writes an object and reads it back from the in-memory backend:

```go
_, _ = client.PutObject(ctx, &s3.PutObjectInput{
    Bucket: aws.String("demo"), Key: aws.String("hello.txt"),
    Body: strings.NewReader("hi from my app")})

out, _ := client.GetObject(ctx, &s3.GetObjectInput{
    Bucket: aws.String("demo"), Key: aws.String("hello.txt")})
// out.Body streams "hi from my app"
```

`kubectl apply -f deployment.yaml` works against the in-memory cluster. `curl -X POST http://127.0.0.1:4566/_cloudemu/reset` clears all state between tests. Flags and per-SDK setup are in [docs/standalone-server.md](docs/standalone-server.md).

### Library mode (Go unit tests)

For Go unit tests, you can skip the server and run cloudemu in-process:

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

`go get github.com/stackshy/cloudemu/v2` (Go 1.25+). See [docs/getting-started.md](docs/getting-started.md).

## Coverage

76 AWS, 75 Azure and 55 GCP services (173 service interfaces, 3,700+ operations), plus an in-memory Kubernetes data plane. The full list is generated from the code and lives in [docs/coverage](docs/coverage/README.md). Some of what's there:

- Storage, compute, databases: S3/Blob/GCS, EC2/VMs/GCE, DynamoDB/Cosmos/Firestore, RDS/Aurora, Cloud SQL, Spanner, Bigtable
- Serverless and containers: Lambda/Functions, App Runner, Cloud Run, ECS, Container Apps, and EKS/AKS/GKE with a Kubernetes API
- Messaging and events: SQS/SNS/EventBridge, Service Bus/Event Grid/Event Hubs, Pub/Sub/Eventarc
- Networking, DNS, load balancing: VPC, security groups, route tables, Global Accelerator, Azure Firewall, Route 53/Cloud DNS, ELB
- Data and analytics: Athena, Glue, EMR, Kinesis, Synapse, Kusto, BigQuery, Dataproc
- Secrets, IAM, KMS, monitoring, logging: Secrets Manager/Key Vault, KMS, managed identities, CloudWatch/Azure Monitor, structured logs
- AI/ML: Bedrock, SageMaker, Vertex AI, Azure OpenAI
- Governance and FinOps: Backup, Config, Chaos Studio, management locks, Cost Explorer / Cost Management / Cloud Billing

The Kubernetes data plane supports CRUD, server-side apply and watch streams, so `client-go` informers work. Controllers converge synchronously: writing a Deployment brings its Pods to Running. It has a scheduler (node and inter-pod affinity, topology spread, scoring), an optional multi-node cluster (`--k8s-nodes N`, taints/tolerations, adding and removing nodes at runtime), and `exec`/`attach` over WebSocket. See [docs/services.md](docs/services.md).

## Tooling

- Terraform / OpenTofu: `apply`, `plan` and `destroy` run against cloudemu, and CI checks that a second apply is a no-op. The [`cloudemu-tf`](contrib/terraform) wrapper handles the provider setup. See [docs/terraform.md](docs/terraform.md).
- Testcontainers (Go): starts and stops cloudemu from your test suite. See [contrib/testcontainers](contrib/testcontainers).
- Any SDK or CLI in any language, since cloudemu speaks the real wire protocols.

## Real engines (opt-in)

By default nothing real runs: no database, cache or user code. If you want a resource to run real SQL, real Redis, or your uploaded function or container, opt in with `config.With<X>Engine(...)`. The heavy dependencies live in two separate modules so the core stays small:

- [contrib/realengine](contrib/realengine): no Docker. Embedded Postgres, miniredis, and `python3`/`node` for Lambda/Functions code.
- [contrib/dockerengine](contrib/dockerengine): real containers. MySQL, VM boot scripts, ECS/ACI/Cloud Run, the Azure Functions host.

The in-memory default doesn't change. `Provider.Close()` shuts down whatever engines you wired in.

## Persistence (opt-in)

State lives in memory, so by default it is gone when the process exits. To keep it across restarts, you can snapshot the whole emulator to one JSON file and restore it into a fresh instance. This covers every stateful service in all four providers and keeps resource IDs intact. The options are:

- run the background server with `--persist`
- save and load named states with `cloudemu snapshot save` / `load`
- call `GET` / `POST /_cloudemu/snapshot`
- use the `persist` package from Go

Named states can also be rewound and forked: `POST /_cloudemu/snapshot/{name}/rewind` restores a checkpoint, and `…/{from}/fork/{to}` copies one to a new name. See [docs/persistence.md](docs/persistence.md).

## Other features

- VCR record/replay: `cloudemu serve --vcr record --vcr-cassette tape.json` records the wire traffic. `--vcr replay` plays it back with no backend, so a recorded session reruns the same way each time.
- `cloudemu doctor` checks that the default ports are free, prints the build version, and reports whether Docker is available.

## Docs

- [Getting Started](docs/getting-started.md): a working test in a few minutes
- [Standalone Server](docs/standalone-server.md): Docker, flags, ports
- [Terraform / OpenTofu](docs/terraform.md)
- [Persistence](docs/persistence.md): snapshot and restore emulator state
- [Architecture](docs/architecture.md) · [Features](docs/features.md) · [Chaos](docs/chaos.md) · [Topology](docs/topology.md)
- [Capability coverage](docs/coverage/README.md): every service and operation, generated from the code

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the dev setup and the branch-from-`development` flow, and the [Code of Conduct](CODE_OF_CONDUCT.md). Report bugs or ask questions in [GitHub issues](https://github.com/stackshy/cloudemu/issues). For security issues, follow [SECURITY.md](SECURITY.md).

## License

MIT
