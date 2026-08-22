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

# Everything real (Docker required for compute/containers):
go run . --all-real
```

Then point your tools at the printed endpoints:

```bash
eval "$(cloudemu env)"
```

### Docker

Build from the **repository root** (the module `replace`s the core and engine
modules by relative path):

```bash
docker build -f contrib/server/Dockerfile -t cloudemu-server .

# In-memory:
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 cloudemu-server

# Real Postgres + Redis via env:
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 \
  -e CLOUDEMU_DB=postgres -e CLOUDEMU_CACHE=redis cloudemu-server

# Docker-backed engines need the host docker socket:
docker run --rm -p 4566:4566 -p 4568:4568 -p 4569:4569 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e CLOUDEMU_DB=mysql -e CLOUDEMU_CONTAINERS=docker cloudemu-server
```

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
| `--all-real`   | —                      | —                          | postgres + redis + subprocess + docker compute + docker containers      |

Docker-backed selections fail fast with a clear error when the `docker` CLI is
not on `PATH`.

## Other flags

| Flag                  | Default                                  | Meaning                                    |
|-----------------------|------------------------------------------|--------------------------------------------|
| `--host`              | `127.0.0.1`                              | bind host (`0.0.0.0` exposes on network)   |
| `--aws-port`          | `4566`                                   | AWS endpoint (HTTP)                        |
| `--azure-port`        | `4568`                                   | Azure endpoint (HTTPS, self-signed)        |
| `--gcp-port`          | `4569`                                   | GCP endpoint (HTTP)                        |
| `--account-id`        | `000000000000`                           | AWS/GCP account id                         |
| `--azure-subscription`| `00000000-0000-0000-0000-000000000000`   | Azure subscription id (GUID)               |
| `--region`            | `us-east-1`                              | default region                             |
| `--project-id`        | `cloudemu-local`                         | GCP project id                             |
| `--shutdown-timeout`  | `10s`                                    | grace period for in-flight requests        |

The Azure endpoint serves HTTPS with an in-memory self-signed certificate
(covering `localhost` and the loopback IPs); clients must trust it or skip
verification.

On `SIGINT`/`SIGTERM` the server drains in-flight requests, then closes each
provider — cascading teardown to the wired engines so embedded Postgres,
miniredis, and any Docker containers are freed.
