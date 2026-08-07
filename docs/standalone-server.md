# Standalone server (`cloudemu serve`)

`cloudemu serve` runs the emulator as a long-lived, out-of-process HTTP server.
Point real AWS, Azure, and GCP SDK clients — in any language — at the printed
endpoints and they talk to cloudemu over the network exactly as they would to
the real cloud. No accounts, no Docker, no code changes.

This is the "local dev cloud" mode. The in-process test-double API
(`cloudemu.NewAWS()`, `awsserver.New(Drivers{…})`) is unchanged and still the
right tool for unit tests.

## Install & run

```sh
go install github.com/stackshy/cloudemu/v2/cmd/cloudemu@latest
cloudemu serve
```

Or from a checkout:

```sh
go run ./cmd/cloudemu serve
```

### Docker

No Go toolchain needed — pull the published image (it runs `serve --host 0.0.0.0`):

```sh
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 ghcr.io/stackshy/cloudemu:latest
```

Or bring up the whole emulated cloud with the example compose file:

```sh
docker compose up
```

### Testcontainers (Go)

Go test suites can start and stop the container automatically with the
[Testcontainers module](https://github.com/stackshy/cloudemu/tree/development/contrib/testcontainers)
(a separate module, so it doesn't add Docker deps to your app):

```go
ctr, _ := cloudemu.Run(ctx)
defer ctr.Terminate(ctx)

endpoint, _ := ctr.AWSEndpoint(ctx) // point aws-sdk-go-v2 here
ctr.Reset(ctx)                      // clean slate between tests
```

On start it prints the live endpoints:

```
cloudemu — standalone server
────────────────────────────
  AWS         http://127.0.0.1:4566
  Azure       https://127.0.0.1:4568   (self-signed TLS)
  GCP         http://127.0.0.1:4569
  Kubernetes  https://127.0.0.1:4570
```

## Background mode (`start` / `stop` / `status`)

`cloudemu serve` runs in the foreground. For a minikube-style "leave it running"
workflow, the lifecycle commands manage a detached background server:

```sh
cloudemu start                 # launch in the background; prints the endpoints
cloudemu status                # is it running? show pid + endpoints
cloudemu logs -f               # follow the server log
cloudemu stop                  # graceful shutdown
cloudemu delete                # stop and remove the run directory
```

`start` accepts every `serve` flag and passes it through, e.g.
`cloudemu start --providers aws --aws-port 4599`. It waits for every listener to
start accepting connections before returning (a TCP-accept probe, so it also
works with `--admin=false`), and is idempotent (a second `start` reports the
already-running instance). `--endpoints-file` and `--quiet` are managed by
`start` itself, so passing your own copies has no effect.

Run state (pid, log, resolved endpoints) lives under `~/.cloudemu/` by default;
point it elsewhere with `--home <dir>` (pass the same `--home` to the other
lifecycle commands).

### Persistence across restarts

By default the emulator starts empty every time. Pass `--persist` to `start` and
your resources survive `stop`→`start`:

```sh
cloudemu start --persist          # save on stop, restore on start
# create buckets/tables/objects…
cloudemu stop                     # writes ~/.cloudemu/<home>/snapshot.json
cloudemu start --persist          # your resources are back
cloudemu delete                   # also removes the snapshot + assets
```

`start` manages the snapshot path for you (in the run dir). Persistence is
**opt-in**; when on, it saves your resources *including* object bodies, so an S3
object comes back with its contents intact. If you only care about the resource
structure and want a smaller snapshot, add `--persist-metadata-only` to skip
object bytes:

```sh
cloudemu start --persist                       # full: structure + object bodies
cloudemu start --persist --persist-metadata-only   # smaller: structure only
```

Coverage is currently the data-bearing services that share a cross-provider
driver interface — object storage (S3/Blob/GCS), NoSQL tables
(DynamoDB/Firestore/Cosmos), secrets (Secrets Manager/Key Vault/Secret Manager),
and compute instances (EC2/VMs/GCE); other services still start empty. The
snapshot is a single human-readable JSON file spanning all three providers, so
you can inspect or `git diff` it.

Fidelity notes: object bodies, secret values, and table items are all saved by
default, along with any secondary indexes present in a table's configuration.
Pass `--persist-metadata-only` to drop object *bodies* for a smaller snapshot —
restored objects then come back as zero-byte keys until you re-upload them.
Compute instances are recreated via `RunInstances`, so image/type/tags are
preserved but the emulator assigns fresh instance IDs and IPs on restore.

## Ports

| Provider   | Default | Protocol | Notes                                    |
|------------|---------|----------|------------------------------------------|
| AWS        | `4566`  | HTTP     | same port LocalStack uses                |
| Azure      | `4568`  | HTTPS    | the ARM SDK requires TLS                 |
| GCP        | `4569`  | HTTP     |                                          |
| Kubernetes | `4570`  | HTTPS    | shared data-plane for EKS/AKS/GKE        |

Override with `--aws-port`, `--azure-port`, `--gcp-port`, `--k8s-port`. Start a
subset with `--providers=aws,gcp`. Bind an interface with `--host 0.0.0.0` (the
default `127.0.0.1` keeps it local-only).

## Pointing SDKs at it

### AWS (`aws-sdk-go-v2`)

```go
cfg, _ := config.LoadDefaultConfig(ctx,
    config.WithRegion("us-east-1"),
    config.WithCredentialsProvider(
        credentials.NewStaticCredentialsProvider("test", "test", "")))

client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String("http://127.0.0.1:4566")
    o.UsePathStyle = true
})
```

Other languages: set `AWS_ENDPOINT_URL=http://127.0.0.1:4566` (SDK v3 / CLI
`--endpoint-url`). Any credentials are accepted — cloudemu does not validate
signatures.

### GCP (`cloud.google.com/go`)

```go
client, _ := storage.NewClient(ctx,
    option.WithEndpoint("http://127.0.0.1:4569"),
    option.WithoutAuthentication())
```

### Azure (`azure-sdk-for-go`)

Azure is served over HTTPS with a self-signed cert. Point the SDK at it through
a `cloud.Configuration`, and either trust the cert or use a transport that skips
verification for local dev:

```go
cloudCfg := cloud.Configuration{
    Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
        cloud.ResourceManager: {
            Endpoint: "https://127.0.0.1:4568",
            Audience: "https://management.azure.com",
        },
    },
}
opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudCfg}}
```

Any `azcore.TokenCredential` works — tokens are not validated.

To supply your own cert instead of the generated one:

```sh
cloudemu serve --tls-cert cert.pem --tls-key key.pem
# add SANs to the generated cert instead:
cloudemu serve --tls-host myhost.local --tls-host 192.168.1.10
```

## Flags

| Flag | Default | Purpose |
|------|---------|---------|
| `--providers` | `aws,azure,gcp` | which providers to start |
| `--host` | `127.0.0.1` | bind interface |
| `--aws-port` / `--azure-port` / `--gcp-port` / `--k8s-port` | `4566`/`4568`/`4569`/`4570` | listen ports (empty `--k8s-port` disables Kubernetes) |
| `--account-id` | `000000000000` | AWS account ID / Azure subscription ID |
| `--region` | `us-east-1` | default region |
| `--project-id` | `cloudemu-local` | GCP project ID |
| `--latency` | `0` | artificial per-call latency (e.g. `20ms`) |
| `--tls-cert` / `--tls-key` | — | supply your own Azure cert (else self-signed) |
| `--tls-host` | — | extra SAN for the generated cert (repeatable) |
| `--endpoints-file` | — | write resolved endpoints as JSON |
| `--persist` | `false` | save state on shutdown and restore it on startup, including object bodies (requires `--state-file`) |
| `--state-file` | — | path to the JSON state snapshot (`start` manages this for you) |
| `--persist-metadata-only` | `false` | persist resource structure but omit object bodies (smaller snapshot) |
| `--log-requests` | `false` | log every request |
| `--quiet` | `false` | suppress the startup banner |
| `--shutdown-timeout` | `10s` | grace period for in-flight requests on Ctrl-C |

`--endpoints-file cloudemu.json` emits a machine-readable bundle for wiring an
app at the whole emulated cloud at once:

```json
{
  "aws": "http://127.0.0.1:4566",
  "azure": "https://127.0.0.1:4568",
  "gcp": "http://127.0.0.1:4569",
  "kubernetes": "https://127.0.0.1:4570"
}
```

## Resetting state between tests (`/_cloudemu`)

A long-lived server keeps state across requests, so a shared or parallel test
suite needs a way to get a clean slate. The control plane at `/_cloudemu` does
this (on by default; disable with `--admin=false`):

```sh
# wipe all emulator state — every provider back to empty
curl -X POST http://127.0.0.1:4566/_cloudemu/reset

# load a fixture of resources into the provider on this port
curl -X POST http://127.0.0.1:4566/_cloudemu/seed --data @fixtures.json

# liveness check
curl http://127.0.0.1:4566/_cloudemu/health
```

`reset` rebuilds every provider (and the shared Kubernetes data-plane) to empty
state and swaps it in atomically — in-flight requests finish against the old
state, new requests see the fresh one. Call it from your suite's setup/teardown
so each test starts clean without restarting the process. A `POST` to any
provider's port resets the whole emulator.

`seed` bulk-loads a declarative fixture into the provider on that port. The
fixture is provider-agnostic — the same file seeds S3, Azure Blob, or GCS
depending on which port you POST it to:

```json
{
  "buckets": [
    { "name": "app-data", "objects": [{ "key": "config.yaml", "body": "port: 8080" }] }
  ],
  "tables": [
    { "name": "users", "partitionKey": "id", "items": [{ "id": "u1", "name": "Ada" }] }
  ],
  "secrets": [{ "name": "db-password", "value": "s3cr3t" }],
  "instances": [{ "imageId": "ami-123", "instanceType": "t3.micro", "count": 2 }]
}
```

In-process (or embedded) tests can load the same fixtures directly with the
[`seed`](https://pkg.go.dev/github.com/stackshy/cloudemu/v2/seed) package and
`go:embed`:

```go
//go:embed testdata/fixtures.json
var fixtures embed.FS

f, _ := seed.LoadFS(fixtures, "testdata/fixtures.json")
seed.Apply(ctx, f, seed.Target{Storage: aws.S3, Database: aws.DynamoDB})
```

## Not yet included

**Persistence** covers object storage and NoSQL tables today (see "Persistence
across restarts" above); the remaining services and full **snapshot/restore**
fidelity are tracked in #107. Docker packaging (#247) and a Testcontainers
module (#248) build directly on this binary.
