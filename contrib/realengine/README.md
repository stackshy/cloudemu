# cloudemu real engines (opt-in)

Back CloudEmu's relational-database instances with a **real database engine** so
clients can run **actual SQL** against the emulator — not a synthetic endpoint.

This is a **separate Go module** on purpose: its heavyweight dependency (a real
Postgres binary, run without Docker via [`embedded-postgres`](https://github.com/fergusstrange/embedded-postgres))
stays out of the core `cloudemu` module. The in-memory, no-Docker default is
unchanged; real engines are strictly opt-in.

## Use it

```go
import (
    cloudemu "github.com/stackshy/cloudemu/v2"
    "github.com/stackshy/cloudemu/v2/config"
    "github.com/stackshy/cloudemu/v2/contrib/realengine"
)

eng := realengine.NewPostgres(0) // 0 = default port
defer eng.Close()

cloud := cloudemu.NewAWS(config.WithDatabaseEngine(eng))
// serve `cloud` as usual; now RDS/Aurora Postgres instances are backed by a
// real Postgres, and clients connect to the endpoint the SDK returns.
```

When you `CreateDBInstance` with a Postgres engine, the emulator provisions a
real database + login role (using the master credentials you passed) and reports
its real host:port as the instance endpoint. Point any Postgres client at it and
run real SQL. `DeleteDBInstance` tears the database down.

Engines with no real backend configured (or non-Postgres families) keep the
default synthetic endpoint — behaviour is unchanged unless you opt in.

## Scope

- **Supported now:** AWS RDS / Aurora Postgres families (`postgres`,
  `aurora-postgresql`).
- **Planned:** MySQL, Azure/GCP relational, then Lambda code execution and Redis.

## Tests

`go test ./...` here fetches a Postgres binary on first run and starts a real
Postgres, so it is run on demand rather than on every core CI run. `TestRDSPostgresE2E`
drives the full real-user flow: AWS SDK `CreateDBInstance` → connect with a real
Postgres client → run SQL → `DeleteDBInstance`.
