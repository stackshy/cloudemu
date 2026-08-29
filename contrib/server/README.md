# cloudemu-server — batteries-included wire server

`cloudemu-server` is the CloudEmu standalone wire-protocol server **with real
data-plane engines wired in** — the thing `cloudemu serve` cannot do on its own.

The core `cloudemu` module is deliberately dependency-free, so the real engines
(embedded Postgres, miniredis, a subprocess function runner, and the Docker-backed
MySQL/compute/container engines) live in `contrib/realengine` and
`contrib/dockerengine`. This binary is a **separate module** that composes them
onto the AWS/GCP/Azure wire servers, so a client points real SDKs/CLIs at the
emulator and runs **real** workloads (real SQL, real Redis commands, real
containers) against it.

With no engine flags it is identical to `cloudemu serve`: pure in-memory, no
external processes.

## Run

```bash
# In-memory (same as `cloudemu serve`):
go run .

# Real Postgres + real Redis (no Docker needed — embedded Postgres + miniredis):
go run . --db=postgres --cache=redis

# Persist object-storage bytes to a real local filesystem (no Docker):
go run . --storage=localfs --storage-dir ./objects

# Add the OCI endpoint and the shared Kubernetes data-plane:
go run . --providers aws,azure,gcp,oci --k8s-port 4570

# Everything real (Docker required for compute/containers):
go run . --all-real
```

Then point your tools at the printed endpoints:

```bash
eval "$(cloudemu env)"
```

### Docker

The batteries-included image is published to GHCR as
**`ghcr.io/stackshy/cloudemu:engines`** (the `:engines` variant of the root
`ghcr.io/stackshy/cloudemu` in-memory image; versioned tags are
`:vX.Y.Z-engines` / `:X.Y-engines`). It bundles `python3` and `nodejs` so the
subprocess functions engine (`--functions=subprocess`) works out of the box, and
exposes AWS (4566), Azure (4568), GCP (4569), Kubernetes (4570) and OCI (4571).

```bash
docker pull ghcr.io/stackshy/cloudemu:engines

# No-arg run is pure in-memory (identical to `cloudemu serve`); every engine
# defaults to off until you opt in:
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 \
  ghcr.io/stackshy/cloudemu:engines

# Real Postgres + Redis via env (no Docker socket needed):
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 \
  -e CLOUDEMU_DB=postgres -e CLOUDEMU_CACHE=redis \
  ghcr.io/stackshy/cloudemu:engines

# Docker-backed engines need the host docker socket:
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e CLOUDEMU_DB=mysql -e CLOUDEMU_CONTAINERS=docker \
  ghcr.io/stackshy/cloudemu:engines
```

Or build it yourself from the **repository root** (the module `replace`s the core
and engine modules by relative path, so the build context must be the repo root,
not `contrib/server`):

```bash
docker build -f contrib/server/Dockerfile -t cloudemu-server .
```

Without the socket mount, Docker-backed engines **degrade to in-memory** (see the
MODE banner section) rather than failing — so the same `docker run` works with or
without the mount.

> **Security note:** mounting `/var/run/docker.sock` grants the container
> root-equivalent control of the host Docker daemon. Do it only for local dev on
> a machine you trust; never mount the raw socket in shared or multi-tenant CI —
> use a socket-proxy that restricts the API surface instead. If you cannot mount
> a socket, just leave the Docker-backed engines off (or let them degrade) and use
> the no-Docker engines (`postgres`, `redis`, `subprocess`, `localfs`).

## Engine flags / env

Each capability is independently selectable; all default to `off` (in-memory).
Flags override the environment variable.

| Flag           | Env                    | Values                     | Backing                                                                 |
|----------------|------------------------|----------------------------|-------------------------------------------------------------------------|
| `--db`         | `CLOUDEMU_DB`          | `off\|postgres\|mysql\|both` | postgres = embedded Postgres (no Docker); mysql/both = Docker MySQL      |
| `--cache`      | `CLOUDEMU_CACHE`       | `off\|redis`               | in-process Redis (miniredis, no Docker)                                 |
| `--functions`  | `CLOUDEMU_FUNCTIONS`   | `off\|subprocess`          | subprocess function runner (no Docker)                                  |
| `--compute`    | `CLOUDEMU_COMPUTE`     | `off\|docker`              | Docker-backed VM boot (Docker required)                                 |
| `--containers` | `CLOUDEMU_CONTAINERS`  | `off\|docker`              | Docker-backed container workloads (Docker required)                     |
| `--storage`    | `CLOUDEMU_STORAGE`     | `off\|localfs`             | persist object-storage bytes (S3/Blob/GCS) to a real local filesystem (no Docker) |
| `--all-real`   | —                      | —                          | postgres + redis + subprocess + docker compute + docker containers + localfs storage |

`--storage-dir` sets the root directory for `--storage=localfs` (default: a
temporary directory).

### Startup MODE banner & docker-socket degrade

At startup the server prints a per-capability **MODE banner** showing what each
engine resolved to:

```
engines:
  db          real (embedded postgres)
  cache       real (miniredis)
  functions   off
  compute     fell-back-to-memory (no docker socket)
  containers  off
  storage     real (localfs)
```

A Docker-backed selection (`--db=mysql`, `--db=both`, `--compute=docker`,
`--containers=docker`) **degrades to in-memory** — rather than failing — when the
`docker` CLI/socket is absent: that capability drops its real engine, the banner
row reads `fell-back-to-memory (no docker socket)`, and one warning line is
written to stderr. This keeps a socket-less `docker run` booting instead of
crashing. The MODE table is suppressed under `--quiet`; the degrade warnings are
not. Non-Docker engines (`postgres`, `redis`, `subprocess`, `localfs`) never
degrade.

## Other flags

Flag names and defaults mirror `cloudemu serve`.

| Flag                  | Default                                  | Meaning                                    |
|-----------------------|------------------------------------------|--------------------------------------------|
| `--providers`         | `aws,azure,gcp`                          | providers to start (`aws,azure,gcp,oci`; OCI opt-in) |
| `--host`              | `127.0.0.1`                              | bind host (`0.0.0.0` exposes on network)   |
| `--advertise-host`    | *(derives from `--host`)*                | host the Kubernetes endpoint is advertised at (its kubeconfig/TLS cert) |
| `--aws-port`          | `4566`                                   | AWS endpoint (HTTP)                        |
| `--azure-port`        | `4568`                                   | Azure endpoint (HTTPS, self-signed)        |
| `--gcp-port`          | `4569`                                   | GCP endpoint (HTTP)                        |
| `--oci-port`          | `4571`                                   | OCI endpoint (HTTP; only bound when `oci` is in `--providers`) |
| `--k8s-port`          | `4570`                                   | shared Kubernetes data-plane (HTTPS); empty disables it |
| `--account-id`        | `000000000000`                           | AWS/GCP/OCI account id                      |
| `--azure-subscription`| `00000000-0000-0000-0000-000000000000`   | Azure subscription id (GUID)               |
| `--region`            | `us-east-1`                              | default region                             |
| `--project-id`        | `cloudemu-local`                         | GCP project id                             |
| `--latency`           | `0`                                      | artificial latency added to every emulated call (e.g. `20ms`) |
| `--tls-cert`          | *(self-signed)*                          | PEM cert file for the Azure HTTPS endpoint |
| `--tls-key`           | *(self-signed)*                          | PEM key file matching `--tls-cert`         |
| `--tls-host`          | —                                        | extra SAN host/IP for the self-signed cert (repeatable) |
| `--log-requests`      | `false`                                  | log every HTTP request (method, path, status, duration) |
| `--quiet`             | `false`                                  | suppress the startup banner                |
| `--enforce-auth`      | `false`                                  | require authentication on each request (AWS SigV4 → 403 on an unregistered key; Azure Bearer-claims) |
| `--endpoints-file`    | *(none)*                                 | write the resolved endpoints as JSON to this path |
| `--shutdown-timeout`  | `10s`                                    | grace period for in-flight requests        |

The OCI endpoint (`--providers` includes `oci`) and the shared Kubernetes
data-plane (`--k8s-port`) are wired through the same `server/serverkit` assembly
as `cloudemu serve`. The Kubernetes port serves HTTPS with its own self-signed
serving certificate (`--tls-cert`/`--tls-key` apply only to the Azure endpoint).

## Admin, persistence & seeding

At parity with `cloudemu serve`, these flags are threaded through to the shared
`server/serverkit` assembly:

| Flag                     | Default | Meaning                                                            |
|--------------------------|---------|--------------------------------------------------------------------|
| `--admin`                | `true`  | mount the `/_cloudemu` control plane (`reset`, `health`, `seed`, `snapshot`) for test isolation |
| `--persist`              | `false` | restore state from `--state-file` on startup and save it on shutdown (includes object bodies) |
| `--state-file`           | *(none)*| path to the JSON state snapshot (**required** with `--persist`)     |
| `--persist-metadata-only`| `false` | persist resource structure but omit object bodies (smaller snapshot)|
| `--init-dir`             | *(none)*| apply every `*.json` seed fixture in this directory on startup      |

```bash
# Isolated test runs: POST /_cloudemu/reset between suites to wipe state.
go run . --admin

# Persist state across restarts:
go run . --persist --state-file ./state.json

# Seed fixtures on boot:
go run . --init-dir ./fixtures
```

`--admin` is on by default; on a non-loopback `--host` it prints a warning, since
`POST /_cloudemu/reset` wipes all state and `GET /_cloudemu/snapshot` dumps it
(secrets included) to any caller. Pass `--admin=false` to disable it.

The Azure endpoint serves HTTPS with an in-memory self-signed certificate
(covering `localhost` and the loopback IPs); clients must trust it or skip
verification.

On `SIGINT`/`SIGTERM` the server drains in-flight requests, then closes each
provider — cascading teardown to the wired engines so embedded Postgres,
miniredis, and any Docker containers are freed.
