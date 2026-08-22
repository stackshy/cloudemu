# cloudemu real engines (opt-in)

Back CloudEmu's data-plane resources with **real engines** so clients can run
**real SQL / real Redis commands** — and **real function code** — against the
emulator, not a synthetic endpoint or a stubbed response.

This is a **separate Go module** on purpose: its heavyweight dependencies (a real
Postgres binary via [`embedded-postgres`](https://github.com/fergusstrange/embedded-postgres),
a real Redis via [`miniredis`](https://github.com/alicebob/miniredis)) stay out of
the core `cloudemu` module — both run **without Docker**. Real function execution
uses the `python3` / `node` already on the host — also no Docker. The in-memory,
no-Docker default is unchanged; real engines are strictly opt-in.

## Use it

```go
import (
    cloudemu "github.com/stackshy/cloudemu/v2"
    "github.com/stackshy/cloudemu/v2/config"
    "github.com/stackshy/cloudemu/v2/contrib/realengine"
)

db := realengine.NewPostgres(0)  // real Postgres for RDS/Aurora
rd := realengine.NewRedis()      // real Redis for ElastiCache
fn := realengine.NewSubprocess() // real Python/Node for Lambda
defer db.Close()
defer rd.Close()
defer fn.Close()

cloud := cloudemu.NewAWS(
    config.WithDatabaseEngine(db),
    config.WithCacheEngine(rd),
    config.WithFunctionEngine(fn),
)
// serve `cloud` as usual; RDS Postgres instances and ElastiCache Redis clusters
// are now backed by real engines, and Lambda functions run their uploaded code.
// Clients connect to the endpoint the SDK returns / get the real invoke result.
```

Creating a Lambda function with a `.zip` deployment package and invoking it runs
the uploaded handler in a real runtime and returns its actual output; a handler
that raises surfaces as `X-Amz-Function-Error`, just like AWS. The handler format
is `file.function` (e.g. `lambda_function.lambda_handler`, `index.handler`) with
the file at the archive root; `python*` and `nodejs*` runtimes are supported.

Creating a Postgres database (RDS/Aurora Postgres, Azure PostgreSQL Flexible
Server, GCP Cloud SQL Postgres) provisions a real database + login role using
your master credentials; creating a Redis cache (ElastiCache, Azure Cache, GCP
Memorystore) starts a real Redis. Connect any Postgres/Redis client to the
endpoint the SDK returns and run real SQL / commands. Deleting the resource
tears the backing engine down. Resources with no engine configured (or
unsupported families) keep the default synthetic endpoint — unchanged unless you
opt in.

## Scope & caveats

- **Supported now:** Postgres for AWS RDS/Aurora, Azure PostgreSQL Flexible
  Server, GCP Cloud SQL, and GCP AlloyDB (no-Docker via embedded-postgres); Redis
  for AWS ElastiCache, Azure Cache for Redis, and GCP Memorystore (no-Docker via
  miniredis); real code execution for AWS Lambda (`python*` / `nodejs*`) and GCP
  Cloud Functions gen1 (`python*`, functions-framework HTTP), no-Docker subprocess.
- **Function packaging:** only `.zip` deployment packages are executed (the
  common `ZipFile` upload); container-image functions keep the stub. Nested
  package handlers (dotted Python module paths) aren't resolved — the handler
  file must sit at the archive root.
- **Postgres port:** Azure and GCP never surface a port in their SDK responses —
  clients always use 5432 — so `NewPostgres(0)` binds **5432** by default and a
  real client connects using only the SDK endpoint. Only one Postgres server can
  bind 5432 per host; pass an explicit port to co-host more than one (AWS RDS
  surfaces the port explicitly and works on any port).
- **Shared server, shared role:** one `NewPostgres` engine runs a single Postgres
  server; each instance becomes its own database, and each master username its
  own login role. Instances that pin the **same** username therefore share one
  role — most notably GCP Cloud SQL, whose root user is always `postgres`. The
  role adopts the most-recently-provisioned password (last writer wins), so with
  the real engine you can drive one live Cloud SQL Postgres instance's
  credentials at a time; a recreated instance's new password takes over cleanly.
  Distinct usernames (the common RDS/Azure case) are fully independent.
- **Redis TLS:** miniredis is plaintext; connect Redis clients with TLS disabled.
  (Azure Cache's `sslPort` field carries the plaintext port here.)
- **Scope limit:** the single-node create path is wired; ElastiCache replication
  groups keep the synthetic endpoint for now.
- **Docker-backed engines** (MySQL, real VMs, containers for ECS/ACI/Cloud Run,
  and the Azure Functions host) live in the sibling `contrib/dockerengine` module,
  which requires a running Docker daemon. Node code execution under the
  functions-framework HTTP contract (GCP gen1) is the remaining no-Docker gap.

## Tests

`go test ./...` here fetches a Postgres binary on first run and starts real
engines, so it runs on demand rather than on every core CI run. `TestRDSPostgresE2E`
and `TestElastiCacheRedisE2E` drive the full real-user flows: AWS SDK create →
connect with a real client → run SQL / Redis commands → delete. `TestLambdaPythonE2E`
and `TestLambdaNodeE2E` do the same for functions: AWS SDK create from a real zip
→ invoke → assert the handler's real output → delete (they skip when `python3` /
`node` is absent).
