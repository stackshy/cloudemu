# Standalone server (`cloudemu serve`)

`cloudemu serve` runs the emulator as a long-lived, out-of-process HTTP server.
Point real AWS, Azure, and GCP SDK clients — in any language — at the printed
endpoints and they talk to cloudemu over the network exactly as they would to
the real cloud. No accounts, no network, no code changes.

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
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 -p 4570:4570 ghcr.io/stackshy/cloudemu:latest
```

The container binds `0.0.0.0`, but the Kubernetes data plane advertises a
routable endpoint (defaults to `127.0.0.1`, not the bind address), so an
EKS/AKS/GKE kubeconfig from the container works with `kubectl` on the host. To
reach it from another machine, pass `--advertise-host <name-or-ip>` (also added
to the serving cert's SANs).

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

Coverage is **full-surface**: every stateful service across all four providers
(AWS, Azure, GCP, OCI) is captured, not a hand-picked subset — object storage,
NoSQL tables, secrets, compute, networking, queues, DNS, and the rest — and a
build-time completeness guard (`persist/completeness_test.go`) fails if a new
stateful service is added without persistence, so coverage can't silently drift.
The snapshot is a single human-readable JSON file spanning every provider, so you
can inspect or `git diff` it. See [persistence.md](persistence.md) for the full
picture (endpoint, Go API, guarantees).

Fidelity notes: the snapshot is **identity-preserving** — resource IDs and the
cross-references between resources are serialized as-is, so a restored EC2
instance keeps its original `i-…` ID and IP, not a freshly minted one. Object
bodies, secret values, and table items (with any secondary indexes) are all saved
by default. Pass `--persist-metadata-only` to drop object *bodies* for a smaller
snapshot — restored objects then come back as zero-byte keys until you re-upload
them.

### Named snapshots (`snapshot save` / `load` / `list` / `delete`)

Persistence auto-saves a single state on stop. **Snapshots** let you capture,
name, and switch between *multiple* states on a running server — a local, free
equivalent of LocalStack's Cloud Pods.

```sh
cloudemu start
# … create buckets / tables / secrets / instances …
cloudemu snapshot save baseline     # capture current state as "baseline"
# … run a destructive test …
cloudemu snapshot load baseline     # restore it instantly — no restart
cloudemu snapshot list              # NAME  CREATED  PROVIDERS  SIZE
cloudemu snapshot delete baseline
```

Each snapshot is a single JSON file under `~/.cloudemu/snapshots/<name>.json`
(override the dir with `--home`) — inspectable, `git`-diffable, and shareable:
copy the file to a teammate and they `snapshot load` the identical state.

`save` and `load` talk to the running server's control plane, so they need the
`--admin` plane (on by default) and the `aws` or `gcp` provider running; `list`
and `delete` are file operations that work without a running server. Snapshots
cover the same full surface as persistence (every stateful service across all
running providers). Names must match `[A-Za-z0-9._-]` (1–64 chars).

`load` is destructive: it wipes the running state (reset semantics) and then
repopulates from the snapshot, so anything created since the snapshot is
discarded. If a restore fails partway the running state is already cleared —
fine for a local emulator, but don't point `load` at a server whose current
state you haven't snapshotted.

### Init hooks (auto-seed on boot)

Drop `*.json` seed fixtures in an init directory and they're applied on every
startup, so the emulator comes up in a known state without manual seeding:

```sh
mkdir -p ~/.cloudemu/init.d
echo '{"buckets":[{"name":"app-data"}],"tables":[{"name":"users","partitionKey":"id"}]}' \
  > ~/.cloudemu/init.d/01-baseline.json
cloudemu start          # applies init.d automatically
```

`start` auto-loads `<run-dir>/init.d` when it exists (point `--home` elsewhere to
change the run dir). For the foreground server, pass the directory explicitly:
`cloudemu serve --init-dir ./fixtures`.

Files are applied in lexical order (`01-…`, `02-…`) to **every** running provider
— the fixtures are provider-agnostic, so one file seeds S3, Blob, and GCS alike.
A malformed fixture fails startup; an apply error (e.g. a resource that already
exists from restored persistence) logs a warning and boot continues. Fixtures use
the same schema as [`/_cloudemu/seed`](#resetting-state-between-tests-_cloudemu)
(buckets, tables, secrets, instances). Running setup **scripts** on boot is a
planned follow-up.

## Network reachability (`net can-connect` / `net trace`)

cloudemu doesn't just emulate EC2/VPC APIs — it evaluates whether your security
groups, route tables, NACLs, and VPC peering would *actually* let traffic flow.
The `net` commands surface that engine, so you can answer "will my app reach the
database?" locally, before deploying to real AWS:

```sh
# after creating VPC/subnets/security-groups/instances (e.g. via Terraform or
# the aws CLI pointed at cloudemu):
cloudemu net can-connect i-app i-db --port 5432        # YES / NO + why
cloudemu net trace       i-app 10.0.2.15               # hop-by-hop path
cloudemu net can-connect i-app i-db --port 5432 --json # machine-readable, for CI
```

`can-connect` reports whether the two instances can talk on a port/protocol
(default `tcp`), and if not, which rule blocks it. `trace` shows the route a
packet from an instance to a destination IP takes (route table → gateway / NAT /
peering / local), or where it's dropped.

This is AWS-only (VPC/security-group/route concepts) and needs the `aws` provider
running with the `--admin` control plane (both on by default). No other local
emulator evaluates network reachability — it's cloudemu's standout capability for
catching connectivity misconfigurations before they reach production.

Note: unlike real AWS, a security group created in cloudemu has **no implicit
allow-all egress** rule, so `can-connect` requires an explicit egress rule on the
source group (`authorize-security-group-egress`) in addition to the ingress rule
on the destination. Launch instances with `--subnet-id` so they inherit the
subnet's VPC (that's what reachability and `trace` resolve against).

## Cost estimate (`cloudemu cost`)

Get a rough monthly-cost estimate of what you've provisioned, before it hits a
real cloud bill:

```sh
cloudemu cost          # PROVIDER/SERVICE  EST. MONTHLY, plus a total
cloudemu cost --json   # machine-readable, for CI budgets
```

It walks the current inventory (the cross-cloud resource-discovery engine) and
prices the **always-on** resources — compute instances (per instance type/SKU),
relational-DB instances (per class), Kubernetes control planes, idle public IPs,
and block volumes across AWS/Azure/GCP (per disk type × size) — using **per-SKU
hourly rates × a per-region multiplier × 730h**, grouped by provider and service.
SKU and region come from each resource's own attributes, so an `m5.4xlarge` in
`sa-east-1` prices higher than a `t3.micro` in `us-east-1`. Load balancers and
NAT gateways have flat rates wired and price as soon as they become discoverable
in the inventory.

The rate tables are **representative on-demand prices** (AWS/Azure/GCP compute,
DB, disk, and networking), not a live pricing-API feed — accurate to roughly
two significant figures. Usage-based dimensions are **not** estimated — including
**object storage** (billed per-GB stored, which the inventory can't observe), as
well as NoSQL throughput, data transfer, and per-request charges. It's meant to
catch "why is this so expensive?" surprises early, not to reconcile invoices; a
live pricing-API integration is a planned follow-up.

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

For CLIs and any-language SDKs that read endpoint/credential env vars, the
`cloudemu env` subcommand prints the `export` lines to point them at a running
server:

```bash
eval "$(cloudemu env)"   # exports AWS_ENDPOINT_URL, AWS_ACCESS_KEY_ID=test, ... (ports default 4566/4568/4569)
```

The Go examples below set the endpoint on the client directly.

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

### Trusting the Azure self-signed cert (any language)

AWS (`:4566`) and GCP (`:4569`) are plain HTTP — no TLS step. Only **Azure**
(`:4568`) is HTTPS with a self-signed cert, so a non-Go client must either skip
verification or trust the cert (the same one-liner you'd use against any local
HTTPS emulator). The cert already covers `localhost`, `127.0.0.1`, and `::1`, so
dial it at `https://localhost:4568` / `https://127.0.0.1:4568`.

| Client | How to accept the cert (local dev) |
|--------|-------------------------------------|
| `curl` | `curl -k https://localhost:4568/...` |
| Node.js | `NODE_TLS_REJECT_UNAUTHORIZED=0` (env var) |
| Python (`requests` / azure-sdk) | `verify=False` / `connection_verify=False` |
| Go | `http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}` |
| .NET | `HttpClientHandler.ServerCertificateCustomValidationCallback = (_,_,_,_) => true` |

To verify **properly** instead of skipping, supply a cert your OS/language
already trusts with `--tls-cert/--tls-key`, or add your hostname to the
generated cert's SANs with `--tls-host <name>` and import/trust that cert in
your client.

## Flags

| Flag | Default | Purpose |
|------|---------|---------|
| `--providers` | `aws,azure,gcp` | which providers to start |
| `--host` | `127.0.0.1` | bind interface (`0.0.0.0` to expose on the network) |
| `--advertise-host` | (derived) | host/IP the Kubernetes endpoint is advertised at + its cert SAN; defaults to `--host`, or `127.0.0.1` when binding all interfaces |
| `--aws-port` / `--azure-port` / `--gcp-port` / `--k8s-port` / `--oci-port` | `4566`/`4568`/`4569`/`4570`/`4571` | listen ports (empty `--k8s-port` disables Kubernetes; OCI only served when `oci` is in `--providers`) |
| `--account-id` | `000000000000` | AWS account ID (also used for GCP/OCI) |
| `--azure-subscription` | `00000000-0000-0000-0000-000000000000` | Azure subscription id (a GUID). Resource ids and Resource Graph scoping use it; discovery is subscription-transparent, so a query scoped to any subscription returns the estate rendered under it |
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

## Real engines

`cloudemu serve` is pure in-memory. To point clients at **real** SQL, Redis,
function runtimes or containers, use the batteries-included **`cloudemu-server`**
binary (the `contrib/server` module), which wires the opt-in
[real engines](features.md#11-real-data-plane-engines-opt-in) onto the same wire
servers. Engines are turned on with flags (or the matching `CLOUDEMU_*` env
vars):

```bash
cloudemu-server --db=postgres --cache=redis --functions=subprocess
# or turn everything on:
cloudemu-server --all-real
```

| Flag | Env | Values |
|------|-----|--------|
| `--db` | `CLOUDEMU_DB` | `off` \| `postgres` \| `mysql` \| `both` |
| `--cache` | `CLOUDEMU_CACHE` | `off` \| `redis` |
| `--functions` | `CLOUDEMU_FUNCTIONS` | `off` \| `subprocess` |
| `--compute` | `CLOUDEMU_COMPUTE` | `off` \| `docker` |
| `--containers` | `CLOUDEMU_CONTAINERS` | `off` \| `docker` |
| `--all-real` | — | shorthand: postgres + redis + subprocess + docker compute + docker containers |

`--db`/`--cache`/`--functions` need no Docker (`contrib/realengine`);
`--compute`/`--containers` require a Docker daemon (`contrib/dockerengine`). The
server calls `Provider.Close()` on shutdown, tearing every engine down.
