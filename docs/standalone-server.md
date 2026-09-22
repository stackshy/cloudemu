# Standalone server (`cloudemu serve`)

`cloudemu serve` runs the emulator as a long-running HTTP server in its own
process. Point AWS, Azure and GCP SDK clients (in any language) at the endpoints
it prints, and they talk to cloudemu the same way they would talk to the real
cloud. You don't need an account or network access, and your code doesn't change.

Use this mode as a local dev cloud. For unit tests, the in-process API
(`cloudemu.NewAWS()`, `awsserver.New(Drivers{…})`) is still the better fit.

## Install & run

Install the `cloudemu` CLI, then run `cloudemu serve`:

```sh
# Homebrew (macOS / Linux)
brew install stackshy/tap/cloudemu

# Install script (downloads the release binary and checks its SHA-256)
curl -fsSL https://raw.githubusercontent.com/stackshy/cloudemu/HEAD/install.sh | sh

# Go toolchain
go install github.com/stackshy/cloudemu/v2/cmd/cloudemu@latest
```

```sh
cloudemu serve
```

The install script takes an optional version argument (`... | sh -s -- v2.5.0`)
and an `INSTALL_DIR` override (`... | INSTALL_DIR="$HOME/bin" sh`). Prebuilt
binaries are on the [releases page](https://github.com/stackshy/cloudemu/releases).

Or from a checkout:

```sh
go run ./cmd/cloudemu serve
```

### Docker

You don't need Go for this. The published image runs `serve --host 0.0.0.0`:

```sh
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 -p 4570:4570 ghcr.io/stackshy/cloudemu:latest
```

The container binds `0.0.0.0`, but the Kubernetes data plane advertises
`127.0.0.1` by default rather than the bind address, so an EKS/AKS/GKE kubeconfig
from the container works with `kubectl` on the host. To reach it from another
machine, pass `--advertise-host <name-or-ip>` (it is also added to the serving
cert's SANs).

You can also start everything with the example compose file:

```sh
docker compose up
```

### Testcontainers (Go)

Go test suites can start and stop the container with the
[Testcontainers module](https://github.com/stackshy/cloudemu/tree/development/contrib/testcontainers).
It is a separate module, so it doesn't add Docker dependencies to your app:

```go
ctr, _ := cloudemu.Run(ctx)
defer ctr.Terminate(ctx)

endpoint, _ := ctr.AWSEndpoint(ctx) // point aws-sdk-go-v2 here
ctr.Reset(ctx)                      // clean slate between tests
```

On start the server prints its endpoints:

```
cloudemu — standalone server
────────────────────────────
  AWS         http://127.0.0.1:4566
  Azure       https://127.0.0.1:4568   (self-signed TLS)
  GCP         http://127.0.0.1:4569
  Kubernetes  https://127.0.0.1:4570
```

## Background mode (`start` / `stop` / `status`)

`cloudemu serve` runs in the foreground. If you want to leave it running in the
background (like minikube), use the lifecycle commands:

```sh
cloudemu start                 # launch in the background; prints the endpoints
cloudemu status                # is it running? show pid + endpoints
cloudemu logs -f               # follow the server log
cloudemu stop                  # graceful shutdown
cloudemu delete                # stop and remove the run directory
```

`start` accepts every `serve` flag and passes it through, e.g.
`cloudemu start --providers aws --aws-port 4599`. It returns once every listener
accepts connections (it probes with a TCP connect, so this also works with
`--admin=false`). Running `start` a second time just reports the instance that is
already running. `start` sets `--endpoints-file` and `--quiet` itself, so your own
values for those are ignored.

Run state (pid, log, resolved endpoints) lives under `~/.cloudemu/` by default.
Use `--home <dir>` to put it somewhere else, and pass the same `--home` to the
other lifecycle commands.

### Persistence across restarts

By default the emulator starts empty every time. With `--persist`, `start` keeps
your resources across `stop`→`start` and across a crash:

```sh
cloudemu start --persist          # save periodically + on stop, restore on start
# create buckets/tables/objects…
cloudemu stop                     # writes snapshot.json in the run directory (~/.cloudemu by default)
cloudemu start --persist          # your resources are back
cloudemu delete                   # also removes the snapshot + assets
```

`start` manages the snapshot path for you (in the run directory). While the
server runs, `--persist` saves in the background, so a `kill -9`, panic or power
loss loses at most the changes since the last save. `--persist-strategy`
(`scheduled`, the default, every `--persist-interval` of 15s / `on-request` /
`on-shutdown` / `manual`) and `--persist-interval` control when it saves; they
also read `CLOUDEMU_PERSIST_STRATEGY` and `CLOUDEMU_PERSIST_INTERVAL`.

Every save includes object bodies by default, so an S3 object comes back with its
contents. The shared Kubernetes data plane is saved too. If you only need the
resource structure and want a smaller snapshot, add `--persist-metadata-only`;
restored objects then come back as zero-byte keys until you re-upload them:

```sh
cloudemu start --persist                       # full: structure + object bodies
cloudemu start --persist --persist-metadata-only   # smaller: structure only
```

The snapshot covers every stateful service in all four providers (AWS, Azure,
GCP, OCI) and keeps resource IDs, so a restored EC2 instance keeps its original
`i-…` ID and IP. Object bodies, secret values and table items (with any secondary
indexes) are all saved. The file is plain JSON, so you can read or diff it. See
[persistence.md](persistence.md#save-strategy---persist-strategy---persist-interval)
for the strategy table, crash windows, the darwin `fsync` caveat and the
comparison with LocalStack, and [persistence.md](persistence.md) for the HTTP
endpoint and Go API.

### Named snapshots (`snapshot save` / `load` / `list` / `delete`)

`--persist` keeps a single state. Named snapshots let you save several states on
a running server and switch between them, similar to LocalStack's Cloud Pods.

```sh
cloudemu start
# … create buckets / tables / secrets / instances …
cloudemu snapshot save baseline     # capture current state as "baseline"
# … run a destructive test …
cloudemu snapshot load baseline     # restore it, no restart needed
cloudemu snapshot list              # NAME  CREATED  PROVIDERS  SIZE
cloudemu snapshot delete baseline
```

Each snapshot is one JSON file under `~/.cloudemu/snapshots/<name>.json` (change
the directory with `--home`). You can read it, diff it and share it: copy the file
to a teammate and `snapshot load` gives them the same state.

`save` and `load` talk to the running server's control plane, so they need the
`--admin` plane (on by default) and the `aws` or `gcp` provider running. `list`
and `delete` are file operations and work without a server. Snapshots cover the
same services as persistence (every stateful service in the running providers).
Names must match `[A-Za-z0-9._-]` (1 to 64 chars).

`load` is destructive. It wipes the running state (like reset) and then
repopulates it from the snapshot, so anything created since the snapshot is lost.
If a restore fails partway, the running state has already been cleared. Save the
current state first if you might need it.

#### Time travel over HTTP (`/_cloudemu/snapshot/{name}`)

The same named states are available on the admin control plane, so any client
(not only the CLI) can save, rewind and fork state:

```sh
# save current state under a name
curl -X POST http://127.0.0.1:4566/_cloudemu/snapshot/baseline
# rewind the running estate to a saved state (destructive, like `load`)
curl -X POST http://127.0.0.1:4566/_cloudemu/snapshot/baseline/rewind
# fork a saved state to a new name (branch off a checkpoint)
curl -X POST http://127.0.0.1:4566/_cloudemu/snapshot/baseline/fork/experiment
# delete a saved state
curl -X DELETE http://127.0.0.1:4566/_cloudemu/snapshot/experiment
```

These act on the whole emulator at once and need the `--admin` plane. See
[persistence.md](persistence.md#admin-endpoint-_cloudemusnapshot).

### Init hooks (auto-seed on boot)

Put `*.json` seed fixtures in an init directory and they are applied on every
startup, so the emulator starts in a known state:

```sh
mkdir -p ~/.cloudemu/init.d
echo '{"buckets":[{"name":"app-data"}],"tables":[{"name":"users","partitionKey":"id"}]}' \
  > ~/.cloudemu/init.d/01-baseline.json
cloudemu start          # applies init.d automatically
```

`start` loads `<run-dir>/init.d` if it exists (use `--home` to change the run
directory). For the foreground server, pass the directory yourself:
`cloudemu serve --init-dir ./fixtures`.

Files are applied in lexical order (`01-…`, `02-…`) to every running provider.
The fixtures don't depend on a provider, so one file seeds S3, Blob and GCS
alike. A malformed fixture stops startup. An apply error (e.g. a resource that
already exists because it was restored from persistence) logs a warning and boot
continues. Fixtures use the same schema as
[`/_cloudemu/seed`](#resetting-state-between-tests-_cloudemu) (buckets, tables,
secrets, instances). Running setup scripts on boot is planned.

## Network reachability (`net can-connect` / `net trace`)

cloudemu can check whether your security groups, route tables, NACLs and VPC
peering would let traffic through. The `net` commands expose that check, so you
can find out locally whether your app will reach its database:

```sh
# after creating VPC/subnets/security-groups/instances (e.g. via Terraform or
# the aws CLI pointed at cloudemu):
cloudemu net can-connect i-app i-db --port 5432        # YES / NO + why
cloudemu net trace       i-app 10.0.2.15               # hop-by-hop path
cloudemu net can-connect i-app i-db --port 5432 --json # machine-readable, for CI
```

`can-connect` reports whether two instances can talk on a port/protocol (default
`tcp`) and, if not, which rule blocks it. `trace` shows the route a packet takes
from an instance to a destination IP (route table → gateway / NAT / peering /
local), or where it is dropped.

This only works for AWS (VPC, security group and route concepts) and needs the
`aws` provider and the `--admin` control plane (both on by default).

Note: unlike real AWS, a security group created in cloudemu has no implicit
allow-all egress rule. `can-connect` needs an explicit egress rule on the source
group (`authorize-security-group-egress`) as well as the ingress rule on the
destination. Launch instances with `--subnet-id` so they belong to the subnet's
VPC, since reachability and `trace` are resolved against it.

## Cost estimate (`cloudemu cost`)

Get a rough monthly cost estimate for what you've created:

```sh
cloudemu cost          # PROVIDER/SERVICE  EST. MONTHLY, plus a total
cloudemu cost --json   # machine-readable, for CI budgets
```

It walks the current inventory (via the cross-cloud resource-discovery engine)
and prices the always-on resources: compute instances (by instance type/SKU),
relational-DB instances (by class), Kubernetes control planes, idle public IPs,
and block volumes on AWS/Azure/GCP (by disk type × size). The price is per-SKU
hourly rate × a per-region multiplier × 730h, grouped by provider and service.
SKU and region come from each resource's own attributes, so an `m5.4xlarge` in
`sa-east-1` costs more than a `t3.micro` in `us-east-1`. Load balancers and NAT
gateways have flat rates and are priced once they appear in the inventory.

The rate tables are representative on-demand prices (AWS/Azure/GCP compute, DB,
disk and networking), not a live pricing API, and are accurate to about two
significant figures. Usage-based charges are not estimated: object storage (billed
per GB stored, which the inventory can't see), NoSQL throughput, data transfer and
per-request charges. Use it to spot unexpectedly expensive setups early, not to
reconcile invoices. A live pricing-API integration is planned.

## Preflight check (`cloudemu doctor`)

A quick check to run before `cloudemu serve`, with no server needed. It confirms
the default ports are free, prints the build version, and reports whether
`docker` is available (only needed for the `:engines` image):

```sh
cloudemu doctor                 # check 127.0.0.1
cloudemu doctor --host 0.0.0.0  # check the interface you'll bind serve to
```

```text
cloudemu doctor — preflight check

[ ok ] version 2.x.y (commit abc1234, built 2026-08-30 by goreleaser)

Ports on 127.0.0.1 (free = free at check time; a port can still be taken before serve binds it):
  [ ok ] AWS            127.0.0.1:4566  free (at check time)
  [ ok ] Azure          127.0.0.1:4568  free (at check time)
  [ ok ] GCP            127.0.0.1:4569  free (at check time)
  [ ok ] Kubernetes     127.0.0.1:4570  free (at check time)
  [ ok ] OCI (opt-in)   127.0.0.1:4571  free (at check time)

[ ok ] docker found at /usr/bin/docker (only needed for the :engines image)
...
[ ok ] all preflight checks passed — ready to `cloudemu serve`.
```

An in-use required port (AWS/Azure/GCP/Kubernetes) is a blocker: the line is
marked `[fail]`, the summary says how many failed, and the command exits non-zero,
so you can use it as a CI gate. The opt-in OCI port and a missing `docker` only
produce `[warn]`, never a failure. The default ports are read from `serve` itself,
so they always match what `cloudemu serve` binds.

## Ports

| Provider   | Default | Protocol | Notes                                    |
|------------|---------|----------|------------------------------------------|
| AWS        | `4566`  | HTTP     | same port LocalStack uses                |
| Azure      | `4568`  | HTTPS    | the ARM SDK requires TLS                 |
| GCP        | `4569`  | HTTP     |                                          |
| Kubernetes | `4570`  | HTTPS    | shared data-plane for EKS/AKS/GKE        |

Override them with `--aws-port`, `--azure-port`, `--gcp-port` and `--k8s-port`.
Start only some providers with `--providers=aws,gcp`. Bind another interface with
`--host 0.0.0.0` (the default, `127.0.0.1`, keeps it local). Run
[`cloudemu doctor`](#preflight-check-cloudemu-doctor) first to check that these
ports are free.

## Pointing SDKs at it

For CLIs and SDKs that read endpoint and credential environment variables,
`cloudemu env` prints the `export` lines for a running server:

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
`--endpoint-url`). Any credentials are accepted, because cloudemu doesn't
validate signatures unless you start it with `--enforce-auth`.

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

Any `azcore.TokenCredential` works, since tokens are not validated by default.

To use your own cert instead of the generated one:

```sh
cloudemu serve --tls-cert cert.pem --tls-key key.pem
# add SANs to the generated cert instead:
cloudemu serve --tls-host myhost.local --tls-host 192.168.1.10
```

### Trusting the Azure self-signed cert (any language)

AWS (`:4566`) and GCP (`:4569`) are plain HTTP, so there is nothing to do for
them. Only Azure (`:4568`) uses HTTPS with a self-signed cert, so a non-Go client
has to either skip verification or trust the cert. The cert already covers
`localhost`, `127.0.0.1` and `::1`, so connect to `https://localhost:4568` or
`https://127.0.0.1:4568`.

| Client | How to accept the cert (local dev) |
|--------|-------------------------------------|
| `curl` | `curl -k https://localhost:4568/...` |
| Node.js | `NODE_TLS_REJECT_UNAUTHORIZED=0` (env var) |
| Python (`requests` / azure-sdk) | `verify=False` / `connection_verify=False` |
| Go | `http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}` |
| .NET | `HttpClientHandler.ServerCertificateCustomValidationCallback = (_,_,_,_) => true` |

To verify the cert instead of skipping verification, pass a cert your OS or
language already trusts with `--tls-cert/--tls-key`, or add your hostname to the
generated cert's SANs with `--tls-host <name>` and trust that cert in your client.

## Flags

| Flag | Default | Purpose |
|------|---------|---------|
| `--providers` | `aws,azure,gcp` | which providers to start |
| `--host` | `127.0.0.1` | bind interface (`0.0.0.0` to expose on the network) |
| `--advertise-host` | (derived) | host/IP the Kubernetes endpoint is advertised at + its cert SAN; defaults to `--host`, or `127.0.0.1` when binding all interfaces |
| `--aws-port` / `--azure-port` / `--gcp-port` / `--k8s-port` / `--oci-port` | `4566`/`4568`/`4569`/`4570`/`4571` | listen ports (empty `--k8s-port` disables Kubernetes; OCI only served when `oci` is in `--providers`) |
| `--gcp-grpc-port` | (empty) | port for the GCP gRPC transport (health + reflection); empty disables it. Point `*_EMULATOR_HOST` clients here |
| `--account-id` | `000000000000` | AWS account ID (also used for GCP/OCI) |
| `--azure-subscription` | `00000000-0000-0000-0000-000000000000` | Azure subscription id (a GUID). Resource ids and Resource Graph scoping use it; discovery is subscription-transparent, so a query scoped to any subscription returns the estate rendered under it |
| `--region` | `us-east-1` | default region |
| `--project-id` | `cloudemu-local` | GCP project ID |
| `--latency` | `0` | artificial per-call latency (e.g. `20ms`) |
| `--tls-cert` / `--tls-key` | (none) | supply your own Azure cert (else self-signed) |
| `--tls-host` | (none) | extra SAN for the generated cert (repeatable) |
| `--endpoints-file` | (none) | write resolved endpoints as JSON |
| `--admin` | `true` | mount the `/_cloudemu` control plane (reset, seed, snapshot, health) |
| `--init-dir` | (none) | apply every `*.json` seed fixture in this directory on startup |
| `--persist` | `false` | save state in the background + on shutdown, restore on startup, including object bodies (requires `--state-file`) |
| `--state-file` | (none) | path to the JSON state snapshot (`start` manages this for you) |
| `--persist-metadata-only` | `false` | persist resource structure but omit object bodies (smaller snapshot) |
| `--persist-strategy` | `scheduled` | when to save with `--persist`: `scheduled` / `on-request` / `on-shutdown` / `manual` (env `CLOUDEMU_PERSIST_STRATEGY`) |
| `--persist-interval` | `15s` | save cadence for `--persist-strategy=scheduled` (env `CLOUDEMU_PERSIST_INTERVAL`) |
| `--async-settle` | `false` | resources report a transient state (pending/creating/…) for a short window before their final state (env `CLOUDEMU_ASYNC_SETTLE`) |
| `--k8s-nodes` | `1` | synthetic nodes per Kubernetes cluster; >1 adds a tainted control-plane node plus workers (env `CLOUDEMU_K8S_NODES`) |
| `--k8s-progression` | `false` | client-created Pods start Pending and move to Running on a ticker (env `CLOUDEMU_K8S_PROGRESSION`) |
| `--k8s-progression-interval` | (built-in) | tick interval for `--k8s-progression` (env `CLOUDEMU_K8S_PROGRESSION_INTERVAL`) |
| `--enforce-auth` | `false` | require authentication: SigV4 verification for AWS, Bearer-token claim checks for Azure (see `cloudemu serve -h` for the exact scope) |
| `--vcr` | (off) | record or replay the wire protocol: `record` \| `replay` (requires `--vcr-cassette`) |
| `--vcr-cassette` | (none) | path to the cassette file to record into / replay from |
| `--vcr-strict` | `true` | in `replay`, return `501` for a request with no recorded match (rather than passing through) |
| `--log-requests` | `false` | log every request |
| `--quiet` | `false` | suppress the startup banner |
| `--shutdown-timeout` | `10s` | grace period for in-flight requests on Ctrl-C |

`--endpoints-file cloudemu.json` writes the endpoints as JSON, which is handy for
configuring an app against all providers at once:

```json
{
  "aws": "http://127.0.0.1:4566",
  "azure": "https://127.0.0.1:4568",
  "gcp": "http://127.0.0.1:4569",
  "kubernetes": "https://127.0.0.1:4570"
}
```

## Resetting state between tests (`/_cloudemu`)

A long-running server keeps state between requests, so a shared or parallel test
suite needs a way to start clean. The control plane at `/_cloudemu` handles this
(on by default; disable it with `--admin=false`):

```sh
# wipe all emulator state (every provider back to empty)
curl -X POST http://127.0.0.1:4566/_cloudemu/reset

# load a fixture of resources into the provider on this port
curl -X POST http://127.0.0.1:4566/_cloudemu/seed --data @fixtures.json

# liveness check
curl http://127.0.0.1:4566/_cloudemu/health
```

`reset` rebuilds every provider (and the shared Kubernetes data plane) with empty
state and swaps it in atomically. Requests already in flight finish against the
old state; new requests see the new one. Call it from your suite's setup or
teardown so each test starts clean without restarting the process. A `POST` to
any provider's port resets the whole emulator.

`seed` loads a declarative fixture into the provider on that port. The fixture
doesn't depend on a provider, so the same file seeds S3, Azure Blob or GCS
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

In-process tests can load the same fixtures with the
[`seed`](https://pkg.go.dev/github.com/stackshy/cloudemu/v2/seed) package and
`go:embed`:

```go
//go:embed testdata/fixtures.json
var fixtures embed.FS

f, _ := seed.LoadFS(fixtures, "testdata/fixtures.json")
seed.Apply(ctx, f, seed.Target{Storage: aws.S3, Database: aws.DynamoDB})
```

## Real engines

`cloudemu serve` is purely in-memory. To give clients real SQL, Redis, function
runtimes or containers, use the `cloudemu-server` binary (the `contrib/server`
module). It wires the opt-in
[real engines](features.md#11-real-data-plane-engines-opt-in) into the same wire
servers. You turn engines on with flags or the matching `CLOUDEMU_*` env vars:

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
| `--storage` | `CLOUDEMU_STORAGE` | `off` \| `localfs` (object bytes on disk; see `--storage-dir`) |
| `--all-real` | (none) | shorthand: postgres + redis + subprocess + docker compute + docker containers + localfs storage |

`--db`, `--cache`, `--functions` and `--storage` don't need Docker
(`contrib/realengine`). `--compute` and `--containers` need a Docker daemon
(`contrib/dockerengine`). On shutdown the server calls `Provider.Close()`, which
stops every engine.
