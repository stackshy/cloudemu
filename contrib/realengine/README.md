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

- **RDS Postgres:** `CreateDBInstance` provisions a real database + login role
  (using your master credentials); connect any Postgres client and run real SQL.
- **ElastiCache Redis:** `CreateCacheCluster` starts a real Redis; the node
  endpoint the SDK returns is a real Redis address — connect any Redis client.

`DeleteDBInstance` / `DeleteCacheCluster` tears the backing engine down. Resources
with no engine configured (or unsupported families) keep the default synthetic
endpoint — behaviour is unchanged unless you opt in.

## Scope

- **Supported now:** AWS RDS / Aurora **Postgres** (`postgres`,
  `aurora-postgresql`); AWS ElastiCache **Redis**.
- **Planned:** Azure/GCP relational + cache, MySQL (needs Docker for real
  fidelity), then Lambda code execution.

## Tests

`go test ./...` here fetches a Postgres binary on first run and starts real
engines, so it runs on demand rather than on every core CI run. `TestRDSPostgresE2E`
and `TestElastiCacheRedisE2E` drive the full real-user flows: AWS SDK create →
connect with a real client → run SQL / Redis commands → delete.
