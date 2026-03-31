# Contributing to CloudEmu

Thank you for your interest in contributing to CloudEmu! This guide will help you get started.

## Getting Started

1. Fork the repository
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

## Development Setup

**Requirements:**
- Go 1.25.0+
- golangci-lint v2

```bash
go build ./...     # compile all packages
go test ./...      # run all tests
go vet ./...       # static analysis
```

## Code Standards

- **Max line length:** 140 characters
- **Max cyclomatic complexity:** 10
- **Max function length:** 100 lines / 50 statements
- **No magic numbers** — use named constants
- **Import ordering:** stdlib, third-party, local module (enforced by `gci`)
- **Thread safety:** all mock implementations must use `sync.RWMutex`

### Linting

Run the linter before submitting:

```bash
golangci-lint run --timeout=9m ./...
```

Fix all issues. If a `//nolint` directive is needed, always include an explanation.

## Making Changes

### Adding a New Feature to an Existing Service

1. Add types and methods to the driver interface (`<service>/driver/driver.go`)
2. Implement in **all 3 providers** (AWS, Azure, GCP)
3. Wire through the portable API layer (`<service>/<service>.go`)
4. Add integration tests to `cloudemu_test.go`
5. Add unit tests to each provider test file
6. Run linter and full test suite

### Adding a New Service

1. Create driver interface in `<service>/driver/driver.go`
2. Create provider implementations in `providers/{aws,azure,gcp}/<service>/`
3. Add field to each Provider struct
4. Initialize in each `New()` factory
5. Add portable API wrapper
6. Add tests

### Important Rules

- All 3 providers (AWS, Azure, GCP) must implement the same behaviors
- Use `cerrors.New()` / `cerrors.Newf()` for error codes
- Use `config.FakeClock` for deterministic time in tests
- Use `memstore.Store[V]` for in-memory storage
- Use `idgen` for cloud-native ID generation

## Submitting Changes

1. Ensure all tests pass: `go test ./...`
2. Ensure linter passes: `golangci-lint run --timeout=9m ./...`
3. Push your branch and create a PR against `development`
4. Include a summary of what changed and why in the PR description

## Reporting Issues

- Use GitHub Issues to report bugs or request features
- Include steps to reproduce for bug reports
- Tag issues with appropriate labels (aws, azure, gcp, enhancement, bug)

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
