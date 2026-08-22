# cloudemu docker engines (opt-in, Docker-required)

Back CloudEmu's data-plane resources with **real engines that run in Docker**, so
clients can run real workloads against the emulator. This is a **separate Go
module** so its Docker-oriented dependencies stay out of the core `cloudemu`
module; it is strictly opt-in.

Unlike `contrib/realengine` (no-Docker: embedded-postgres, miniredis), the engines
here **require a running Docker daemon**. Tests skip cleanly when Docker is absent.

## Engines

- **`NewMySQL(port)`** — a `config.DatabaseEngine` backed by a real `mysql:8.0`
  container. Wire it with `config.WithDatabaseEngine(...)` (or compose it with a
  Postgres engine via `dbengine.NewMultiEngine`). Creating a MySQL instance
  (AWS RDS `mysql`, GCP Cloud SQL `MYSQL_8_0`, Azure MySQL Flexible) provisions a
  real database + login user; connect any MySQL client to the endpoint the SDK
  returns and run real SQL.

Compute and container engines are added in later waves.

## MySQL scope & caveats

- **One container, many databases.** A single `NewMySQL` engine runs one `mysql:8.0`
  container; each instance becomes its own database with its own login user.
- **Port:** `NewMySQL(0)` binds **3306** (Azure/GCP never surface a port; clients
  always use 3306). AWS RDS surfaces the port, so any port works. Only one
  container can bind a given host port.
- **Shared master username = last writer wins.** MySQL accounts are server-global,
  so two instances that pin the **same** master username share one account. Each
  provision **recreates** that account scoped to its own database with its own
  password — so the most-recently-created instance works fully, an earlier
  instance sharing the username loses access (grants and password), and there is
  **no cross-tenant leak** between their databases. Distinct usernames are fully
  independent (the common case). Drive one live instance per shared username.
- **`root` is reserved.** A master username of `root` is rejected at provision
  (real RDS/Azure reserve it too), rather than reporting success with credentials
  that could not authenticate.
- **TLS:** the container speaks plaintext on the published port; connect with TLS
  disabled.

## Tests

`go test ./...` here starts real containers and **skips** when the Docker daemon
is unavailable. `TestRDSMySQLE2E` / `TestAzureMySQLFlexE2E` drive the full
real-user flow (SDK create → connect a real MySQL client → SQL → delete);
`TestMySQLNoCrossTenantGrant` / `TestMySQLRejectsRootUser` guard the isolation
semantics above.
