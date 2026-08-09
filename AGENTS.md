# AGENTS.md

Guidance for AI agents working in the cloudemu repository. (Human contributors: see [CONTRIBUTING.md](CONTRIBUTING.md).)

## What cloudemu is

Zero-cost, in-memory emulation of AWS, Azure, and GCP cloud **APIs** for Go tests. It emulates control surfaces, not a real cloud — it does not run workloads/containers, serve real traffic, authenticate requests, enforce quotas, or persist state across restarts. See the "What it is / isn't" section of the [README](README.md).

## Where the capabilities are

Do not scrape prose to answer "what can cloudemu do." Use the generated, can't-drift sources:

- [docs/coverage/README.md](docs/coverage/README.md) — human index: every service, every operation, native name per provider.
- [docs/coverage/coverage.json](docs/coverage/coverage.json) — the same data, machine-readable.
- [llms-full.txt](llms-full.txt) — single-file plain-text dump for one-fetch ingest.

These are produced from the driver interfaces in `services/*/driver` by `go generate`, so they never promise a capability the code lacks.

## Architecture (one paragraph)

Three layers: a portable API (`services/<svc>/`) wraps a driver interface (`services/<svc>/driver/`), which each provider implements in `providers/{aws,azure,gcp}/<native>/` with `memstore`-backed mocks. AWS, Azure, and GCP are fully implemented. OCI (`providers/oci/`) is a foundation-only scaffold — its service fields are `nil` and it emulates nothing yet. Full detail: [docs/architecture.md](docs/architecture.md).

## Build, test, lint

```bash
go build ./...
go test ./...
golangci-lint run --timeout=9m ./...
```

Run all three before proposing a change. Lint must be clean (0 issues).

## Conventions that matter

- **Mirror across providers.** A behavior added to one provider should be added to AWS, Azure, and GCP unless the capability genuinely doesn't exist there.
- **Regenerate coverage after interface or wiring changes.** If you touch a `services/*/driver` interface or wire a service into a provider factory, run `go generate ./...` and commit the updated `docs/coverage/` output.
- **Per-service non-goals** are hand-maintained in `docs/coverage/nongoals/<service>.md` and inlined by the generator; the rest of `docs/coverage/` is generated — do not edit it by hand.
- **Deterministic time** via `config.FakeClock` for time-dependent tests.
