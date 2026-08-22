# cloudemu real engines (opt-in)

Back CloudEmu's data-plane resources with **real engines** so clients can run
**real SQL / real Redis commands** against the emulator — not a synthetic
endpoint.

This is a **separate Go module** on purpose: its heavyweight dependencies (a real
Postgres binary via [`embedded-postgres`](https://github.com/fergusstrange/embedded-postgres),
a real Redis via [`miniredis`](https://github.com/alicebob/miniredis)) stay out of
the core `cloudemu` module — both run **without Docker**. The in-memory, no-Docker
default is unchanged; real engines are strictly opt-in.

## Use it

```go
import (
    cloudemu "github.com/stackshy/cloudemu/v2"
    "github.com/stackshy/cloudemu/v2/config"
    "github.com/stackshy/cloudemu/v2/contrib/realengine"
)

db := realengine.NewPostgres(0) // real Postgres for RDS/Aurora
rd := realengine.NewRedis()     // real Redis for ElastiCache
defer db.Close()
defer rd.Close()

cloud := cloudemu.NewAWS(
    config.WithDatabaseEngine(db),
    config.WithCacheEngine(rd),
)
// serve `cloud` as usual; RDS Postgres instances and ElastiCache Redis clusters
// are now backed by real engines, and clients connect to the endpoint the SDK
// returns.
```

Creating a Postgres database (RDS/Aurora Postgres, Azure PostgreSQL Flexible
Server, GCP Cloud SQL Postgres) provisions a real database + login role using
your master credentials; creating a Redis cache (ElastiCache, Azure Cache, GCP
Memorystore) starts a real Redis. Connect any Postgres/Redis client to the
endpoint the SDK returns and run real SQL / commands. Deleting the resource
tears the backing engine down. Resources with no engine configured (or
unsupported families) keep the default synthetic endpoint — unchanged unless you
opt in.

## Scope & caveats

- **Supported now:** Postgres for AWS RDS/Aurora and Azure PostgreSQL Flexible
  Server (no-Docker via embedded-postgres); Redis for AWS ElastiCache, Azure
  Cache for Redis, and GCP Memorystore (no-Docker via miniredis).
- **Postgres port:** Azure and GCP never surface a port in their SDK responses —
  clients always use 5432 — so `NewPostgres(0)` binds **5432** by default and a
  real client connects using only the SDK endpoint. Only one Postgres server can
  bind 5432 per host; pass an explicit port to co-host more than one (AWS RDS
  surfaces the port explicitly and works on any port).
- **Redis TLS:** miniredis is plaintext; connect Redis clients with TLS disabled.
  (Azure Cache's `sslPort` field carries the plaintext port here.)
- **Scope limit:** the single-node create path is wired; ElastiCache replication
  groups keep the synthetic endpoint for now.
- **Planned:** GCP Cloud SQL Postgres, MySQL (needs Docker for real fidelity),
  then Lambda code execution and real containers/VMs.

## Tests

`go test ./...` here fetches a Postgres binary on first run and starts real
engines, so it runs on demand rather than on every core CI run. `TestRDSPostgresE2E`
and `TestElastiCacheRedisE2E` drive the full real-user flows: AWS SDK create →
connect with a real client → run SQL / Redis commands → delete.
