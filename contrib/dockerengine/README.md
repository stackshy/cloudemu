# cloudemu docker engines (opt-in, Docker-required)

Back CloudEmu's data-plane resources with **real engines that run in Docker**, so
clients can run real workloads against the emulator. This is a **separate Go
module** so its Docker-oriented dependencies stay out of the core `cloudemu`
module; it is strictly opt-in.

Unlike `contrib/realengine` (no-Docker: embedded-postgres, miniredis), the engines
here **require a running Docker daemon**. Tests skip cleanly when Docker is absent.

## Layout

Each engine lives in its own subpackage and exposes a `New` constructor; the
shared low-level plumbing (a `Runner` over the docker CLI and an `Available`
check) lives in `internal/dockerx`:

| Subpackage        | Constructor                    | Engine interface          |
|-------------------|--------------------------------|---------------------------|
| `mysql`           | `mysql.New(port)`              | `config.DatabaseEngine`   |
| `compute`         | `compute.New(...Option)`       | `config.ComputeEngine`    |
| `container`       | `container.New()`              | `config.ContainerEngine`  |
| `azurefunctions`  | `azurefunctions.New(...Option)`| `config.FunctionEngine`   |

## Engines

- **`mysql.New(port)`** — a `config.DatabaseEngine` backed by a real `mysql:8.0`
  container. Wire it with `config.WithDatabaseEngine(...)` (or compose it with a
  Postgres engine via `dbengine.NewMultiEngine`). Creating a MySQL instance
  (AWS RDS `mysql`, GCP Cloud SQL `MYSQL_8_0`, Azure MySQL Flexible) provisions a
  real database + login user; connect any MySQL client to the endpoint the SDK
  returns and run real SQL.
- **`compute.New()`** — a `config.ComputeEngine` that boots a VM's UserData script
  in a real container (default `alpine:3.20`) and captures its output. Wire it
  with `config.WithComputeEngine(...)`; an AWS EC2 `RunInstances` runs the boot
  script and `GetConsoleOutput` returns what it printed.
- **`container.New()`** — a `config.ContainerEngine` that runs container workloads
  in real Docker (run-to-completion or detached). Wire it with
  `config.WithContainerEngine(...)`; it backs AWS ECS tasks, Azure Container
  Instances container groups, and GCP Cloud Run jobs — the SDK surfaces real exit
  codes, state, and logs.
- **`azurefunctions.New()`** — a `config.FunctionEngine` backed by the official
  `mcr.microsoft.com/azure-functions/python` host image, so a deployed Azure
  Functions app actually executes. Wire it with `config.WithFunctionEngine(...)`;
  a `zipdeploy`ed function runs in the real Functions host and Invoke returns its
  real result.

## MySQL scope & caveats

- **One container, many databases.** A single `mysql.New` engine runs one `mysql:8.0`
  container; each instance becomes its own database with its own login user.
- **Port:** `mysql.New(0)` binds **3306** (Azure/GCP never surface a port; clients
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

## Compute / container / Azure Functions caveats

- **Compute (`compute.New`)** maps the request's AMI/image id to one base image
  (default `alpine:3.20`) — there is no real registry mapping — and runs the boot
  script via `docker exec`, capturing combined stdout/stderr as the console log.
- **Containers (`container.New`)** run each task/group container as a real
  container; run-to-completion (ECS RunTask, Cloud Run jobs, ACI `restartPolicy:
  Never`) blocks until exit so status/exit-code/logs are real, detached otherwise.
- **Azure Functions (`azurefunctions.New`)** bind-mounts the extracted function-app
  zip into the host image and waits for the host to index it. The host images are
  amd64-only; on Apple Silicon the engine runs them under emulation with
  `--platform linux/amd64` + `DOTNET_EnableWriteXorExecute=0` (a no-op on native
  amd64), overridable via `WithFunctionsPlatform`.

## Tests

`go test ./...` here starts real containers and **skips** when the Docker daemon
is unavailable. Full real-user flows: `TestRDSMySQLE2E`/`TestAzureMySQLFlexE2E`
(MySQL), `TestComputeEC2ConsoleOutputE2E` (EC2 boot script → console output),
`TestContainerECSRunTaskE2E`/`TestACIContainerGroupE2E`/`TestCloudRunJobE2E`
(containers), and `TestAzureFunctionsE2E` (a real Python function doubles its
input inside the azure-functions host). `TestMySQLNoCrossTenantGrant` /
`TestMySQLRejectsRootUser` guard the MySQL isolation semantics above.
