# Contributing to CloudEmu

Thanks for helping out. This page covers the dev setup and how changes get merged.

## Getting started

1. Fork the repository.
2. Clone your fork:
   ```bash
   git clone https://github.com/<your-username>/cloudemu.git
   cd cloudemu
   ```
3. Create a feature branch from `development`:
   ```bash
   git checkout development
   git checkout -b feature/your-feature-name
   ```

## Development setup

Requirements:
- Go 1.25.0+
- golangci-lint v2

```bash
go build ./...     # compile all packages
go test ./...      # run all tests
go vet ./...       # static analysis
```

## Code standards

- Max line length: 140 characters
- Max cyclomatic complexity: 10
- Max function length: 100 lines / 50 statements
- No magic numbers. Use named constants.
- Import order: stdlib, third-party, local module (enforced by `gci`)
- Thread safety: all mock implementations must use `sync.RWMutex`

### Linting

Run the linter before you open a PR:

```bash
golangci-lint run --timeout=9m ./...
```

Fix every issue. If you need a `//nolint` directive, add a comment explaining why.

## Making changes

### Adding a feature to an existing service

1. Add types and methods to the driver interface (`services/<service>/driver/driver.go`).
2. Implement them in all 3 providers (AWS, Azure, GCP).
3. Wire them through the portable API layer (`services/<service>/<service>.go`).
4. Add integration tests to `cloudemu_test.go`.
5. Add unit tests to each provider's test file.
6. Run the linter and the full test suite.

### Adding a new service

1. Create the driver interface in `services/<service>/driver/driver.go`.
2. Create provider implementations in `providers/{aws,azure,gcp}/<service>/`.
3. Add a field to each Provider struct.
4. Initialize it in each `New()` factory.
5. Add the portable API wrapper.
6. Add tests.

### Rules

- All 3 providers (AWS, Azure, GCP) must implement the same behavior.
- Use `cerrors.New()` / `cerrors.Newf()` for error codes.
- Use `config.FakeClock` for deterministic time in tests.
- Use `memstore.Store[V]` for in-memory storage.
- Use `idgen` for cloud-native IDs.

## Submitting changes

1. Make sure the tests pass: `go test ./...`
2. Make sure the linter passes: `golangci-lint run --timeout=9m ./...`
3. Push your branch and open a PR against `development`.
4. In the PR description, say what changed and why.

## Reporting issues

- Use GitHub Issues for bugs and feature requests.
- For bugs, include steps to reproduce.
- Add the labels that apply (aws, azure, gcp, enhancement, bug).

## Releases (maintainers)

Pushing a `v*` tag triggers `.github/workflows/release.yml`. It runs
GoReleaser, which builds cross-platform binaries, publishes a GitHub Release with
`checksums.txt`, and pushes a Homebrew cask to `github.com/stackshy/homebrew-tap`
(this is what makes `brew install stackshy/tap/cloudemu` work).

One-time setup for the Homebrew push:

1. The public tap repo `github.com/stackshy/homebrew-tap` must exist.
2. Add a repository secret named `HOMEBREW_TAP_TOKEN`: a Personal Access Token
   with write access to the tap repo. GoReleaser uses it to commit the cask.
   Without it, the release still publishes and only the Homebrew push is skipped.

Check config changes locally before tagging:

```sh
go run github.com/goreleaser/goreleaser/v2@latest check
go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish
```

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
