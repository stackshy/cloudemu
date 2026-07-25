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

On start it prints the live endpoints:

```
cloudemu — standalone server
────────────────────────────
  AWS         http://127.0.0.1:4566
  Azure       https://127.0.0.1:4568   (self-signed TLS)
  GCP         http://127.0.0.1:4569
  Kubernetes  http://127.0.0.1:4570
```

## Ports

| Provider   | Default | Protocol | Notes                                    |
|------------|---------|----------|------------------------------------------|
| AWS        | `4566`  | HTTP     | same port LocalStack uses                |
| Azure      | `4568`  | HTTPS    | the ARM SDK requires TLS                 |
| GCP        | `4569`  | HTTP     |                                          |
| Kubernetes | `4570`  | HTTP     | shared data-plane for EKS/AKS/GKE        |

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
  "kubernetes": "http://127.0.0.1:4570"
}
```

## Resetting state between tests (`/_cloudemu`)

A long-lived server keeps state across requests, so a shared or parallel test
suite needs a way to get a clean slate. The control plane at `/_cloudemu` does
this (on by default; disable with `--admin=false`):

```sh
# wipe all emulator state — every provider back to empty
curl -X POST http://127.0.0.1:4566/_cloudemu/reset

# liveness check
curl http://127.0.0.1:4566/_cloudemu/health
```

`reset` rebuilds every provider (and the shared Kubernetes data-plane) to empty
state and swaps it in atomically — in-flight requests finish against the old
state, new requests see the fresh one. Call it from your suite's setup/teardown
so each test starts clean without restarting the process. A `POST` to any
provider's port resets the whole emulator.

## Not yet included

`seed` is reserved on the control plane but not implemented yet — for now,
create the resources a test needs with your SDK client after `reset`. Bulk
**seeding** and **persistence** across restarts need a cross-service
state-import loader (tracked in #250). Docker packaging (#247) and a
Testcontainers module (#248) build directly on this binary.
