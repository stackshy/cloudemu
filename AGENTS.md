# AGENTS.md

Guidance for AI agents working in the cloudemu repository. (Human contributors: see [CONTRIBUTING.md](CONTRIBUTING.md).)

## What cloudemu is

Zero-cost, in-memory emulation of AWS, Azure, and GCP cloud **APIs**. It runs three ways: as a standalone server (the `cloudemu serve` binary or the `ghcr.io/stackshy/cloudemu` Docker image) that any app in any language points at, and in-process from Go via either the SDK-compat HTTP server or the typed mock API. It emulates control surfaces, not a real cloud — it does not run workloads/containers, serve real traffic, authenticate requests, or enforce quotas. State is in-memory and resettable, so it is ephemeral by default (lost on process exit unless saved); persistence is opt-in — the whole emulator's state can be snapshotted to one JSON file and restored identity-preservingly across all four providers (`--persist`, `snapshot save`/`load`, the `/_cloudemu/snapshot` endpoint, the `persist` package). See the [README](README.md) for the full framing and scope.

## Where the capabilities are

Do not scrape prose to answer "what can cloudemu do." Use the generated, can't-drift sources:

- [docs/coverage/README.md](docs/coverage/README.md) — human index: every service, every operation, native name per provider.
- [docs/coverage/coverage.json](docs/coverage/coverage.json) — the full capability set, machine-readable (parse this instead of scraping the docs).

These are produced from the driver interfaces in `services/*/driver` by `go generate`, so they never promise a capability the code lacks.

## Architecture (one paragraph)

Three layers: a portable API (`services/<svc>/`) wraps a driver interface (`services/<svc>/driver/`), which each provider implements in `providers/{aws,azure,gcp,oci}/<native>/` with `memstore`-backed mocks. AWS, Azure, and GCP are fully implemented. OCI (`providers/oci/`) is in progress — its foundation is in place and services land one at a time, so a service not yet built reads as `nil`; consult `docs/coverage/` for which ones exist rather than any prose. Full detail: [docs/architecture.md](docs/architecture.md).

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
