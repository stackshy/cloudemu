# CLAUDE.md — CloudEmu Development Guide

This file provides context for AI assistants working on the cloudemu codebase.

## Git Rules

### Commits
- Never commit without explicit user consent.
- Do not add Claude Code references or co-author attribution in commit messages.
- Keep commit messages crisp — do not try to explain the entire diff.
- Always pull latest code from remote before starting work.

### Branches & Pushing
- **NEVER push directly to `main` or `development` branches.** This is non-negotiable.
- Before pushing to any other branch, always ask the user for consent first.
- Always work on a feature/fix branch, never directly on protected branches.

### Pull Requests
- Always raise PRs with `development` as the base branch.
- PR description must include a summary of what was changed and why.
- Use `gh pr create --base development` when creating PRs.

### Security
- Never commit secrets, API keys, tokens, passwords, or `.env` files.
- Never commit credentials files (`credentials.json`, `serviceaccount.json`, etc.).
- If a secret is accidentally staged, unstage it immediately and warn the user.
- Never log or print sensitive values (tokens, passwords, keys) in code.

### GitHub CLI & Git Commands
- **Always ask for user consent before running ANY GitHub CLI (`gh`) or git command that interacts with the remote.** This includes but is not limited to: `git push`, `git pull`, `git fetch`, `gh pr create`, `gh pr merge`, `gh pr close`, `gh pr comment`, `gh issue create`, `gh repo clone`, and any other `gh` or remote git operation.
- **`git commit` also requires explicit user consent** — never commit without asking the user first.
- Read-only local git operations (`git diff`, `git status`, `git log`, `git branch --list`, `git stash list`) do NOT require consent.
- Other local operations (`git add`, `git stash`, `git checkout`, `git branch`) do NOT require consent.
- Never run `git push --force` or `git push -f` without explicit user approval.
- When in doubt whether a git/gh command affects the remote or modifies history, ask first.


## Project Overview

**cloudemu** is a zero-cost, in-memory cloud emulation library for Go. It provides mock implementations of 10 cloud services across AWS, Azure, and GCP — designed for testing and development without real cloud accounts, Docker, or network calls.

- **Module:** `github.com/stackshy/cloudemu`
- **Go version:** 1.25.0
- **Entry point:** `cloudemu.go` — exports `NewAWS()`, `NewAzure()`, `NewGCP()`

## Architecture

Three-layer design:

1. **Portable API** (`storage/`, `compute/`, `database/`, etc.) — wraps drivers with cross-cutting concerns (recording, metrics, rate limiting, error injection, latency)
2. **Driver Interfaces** (`*/driver/driver.go`) — minimal Go interfaces each provider must implement
3. **Provider Implementations** (`providers/aws/`, `providers/azure/`, `providers/gcp/`) — in-memory backends using `memstore.Store[V]`

## Key Internal Packages

| Package | Purpose |
|---------|---------|
| `internal/memstore` | Generic thread-safe `Store[V]` — backing store for all mocks |
| `internal/idgen` | ID generators (AWS ARNs, Azure resource IDs, GCP self-links) |
| `statemachine` | Generic FSM for VM lifecycle transitions |
| `pagination` | Generic `Paginate[T]` with base64 page tokens |
| `config` | Options, Clock interface, FakeClock for deterministic time |
| `errors` | Canonical error codes (NotFound, AlreadyExists, etc.) |

## 10 Services Per Provider

| Service | AWS | Azure | GCP |
|---------|-----|-------|-----|
| Storage | `s3` | `blobstorage` | `gcs` |
| Compute | `ec2` | `virtualmachines` | `gce` |
| Database | `dynamodb` | `cosmosdb` | `firestore` |
| Serverless | `lambda` | `functions` | `cloudfunctions` |
| Networking | `vpc` | `vnet` | `gcpvpc` |
| Monitoring | `cloudwatch` | `azuremonitor` | `cloudmonitoring` |
| IAM | `awsiam` | `azureiam` | `gcpiam` |
| DNS | `route53` | `azuredns` | `clouddns` |
| Load Balancer | `elb` | `azurelb` | `gcplb` |
| Message Queue | `sqs` | `servicebus` | `pubsub` |

## Provider Factory Wiring

Each provider factory (`providers/aws/aws.go`, etc.) creates all 10 services and wires cross-service dependencies:

```go
// providers/aws/aws.go
p.EC2.SetMonitoring(p.CloudWatch)        // EC2 → CloudWatch auto-metrics

// providers/azure/azure.go
p.VirtualMachines.SetMonitoring(p.Monitor) // VMs → Azure Monitor auto-metrics

// providers/gcp/gcp.go
p.GCE.SetMonitoring(p.CloudMonitoring)    // GCE → Cloud Monitoring auto-metrics
```

## Realistic Cloud Behaviors

These behaviors make cloudemu match real cloud semantics:

### 1. Auto-Metric Generation (Compute → Monitoring)

When `RunInstances` is called, the compute mock automatically pushes 5 metrics to the monitoring service:

| Provider | Namespace | Metrics | Dimension Key |
|----------|-----------|---------|---------------|
| AWS | `AWS/EC2` | CPUUtilization, NetworkIn, NetworkOut, DiskReadOps, DiskWriteOps | `InstanceId` |
| Azure | `Microsoft.Compute/virtualMachines` | Percentage CPU, Network In Total, Network Out Total, Disk Read Operations/Sec, Disk Write Operations/Sec | `resourceId` |
| GCP | `compute.googleapis.com` | instance/cpu/utilization, instance/network/received_bytes_count, instance/network/sent_bytes_count, instance/disk/read_ops_count, instance/disk/write_ops_count | `instance_id` |

Each metric gets 5 backfill datapoints at 1-minute intervals from launch time. Implementation is in `emitInstanceMetrics()` in each compute mock.

### 1b. Lifecycle Metric Emission (Start/Stop/Reboot/Terminate)

All VM lifecycle operations also emit metrics automatically via `emitLifecycleMetrics()`:

| Operation | Values Emitted |
|-----------|---------------|
| `StartInstances` | Running values (CPU=25, Network=1024/512, Disk=100/50; GCP CPU=0.25) |
| `StopInstances` | Zero values (all 0.0) |
| `RebootInstances` | Running values |
| `TerminateInstances` | Zero values |

Each lifecycle call emits **1 datapoint** per metric at `Clock.Now()` (vs the 5-backfill pattern used by `RunInstances`). This allows alarms to detect state changes — e.g., a "low CPU" alarm fires when a VM is stopped.

### 2. Alarm Auto-Evaluation (Monitoring)

`PutMetricData` triggers `evaluateAlarms()` for each affected namespace+metric. The alarm evaluation:

- Collects datapoints within the evaluation window (`Period * EvaluationPeriods`)
- Computes the statistic (Average, Sum, Min, Max, SampleCount)
- Compares against threshold using the alarm's operator
- Updates alarm state to `"ALARM"` or `"OK"`

Supported operators: `GreaterThanThreshold`, `LessThanThreshold`, `GreaterThanOrEqualToThreshold`, `LessThanOrEqualToThreshold`

Implementation: `evaluateAlarms()` and `evaluateComparison()` in each monitoring mock.

### 3. IAM Policy Evaluation

`CheckPermission(principal, action, resource)` evaluates real JSON policy documents:

- Collects all attached policy ARNs (user policies + role policies)
- Parses each policy's JSON document (`policyDoc` / `policyStatement` types)
- Matches actions and resources using `wildcardMatch()` (supports `*` and `?`)
- Explicit `Deny` always overrides `Allow`
- Default is deny if no Allow matches

Implementation: `evaluatePolicy()`, `wildcardMatch()`, `toStringSlice()` in each IAM mock.

### 4. FIFO Message Deduplication

FIFO queues enforce 5-minute deduplication windows:

- `deduplicationIndex map[string]time.Time` tracks when each DeduplicationID was last seen
- If same DeduplicationID is sent within 5 minutes, returns existing MessageID
- After 5 minutes, the same DeduplicationID is accepted as a new message
- `SentAt time.Time` field on message structs tracks send time

Implementation is in `SendMessage()` in `sqs.go`, `servicebus.go`, `pubsub.go`.

### 5. Numeric-Aware Database Comparisons

`compareValues(a, b string) int` helper in each database mock:

- Tries `strconv.ParseFloat` on both values
- If both parse as numbers → numeric comparison
- Otherwise → string comparison
- Used by `<`, `>`, `<=`, `>=`, `BETWEEN` operators in both scan filters and query sort conditions

### 6. Full Database Scan Operators

Database scan filters support all operators: `=`, `!=`, `<`, `>`, `<=`, `>=`, `CONTAINS`, `BEGINS_WITH`

Query sort key conditions support: `=`, `<`, `>`, `<=`, `>=`, `BEGINS_WITH`, `BETWEEN`

## Linting

Linting is configured in `.golangci.yml` using golangci-lint v2.

```bash
# Run linter on all packages
golangci-lint run --timeout=9m ./...

# Run linter on a specific package
golangci-lint run --timeout=9m ./providers/aws/ec2/...

# Run with auto-fix for formatting issues
golangci-lint run --timeout=9m --fix ./...
```

### Key Linting Rules

- **Max line length:** 140 characters
- **Max cyclomatic complexity:** 10
- **Max function length:** 100 lines / 50 statements
- **Import ordering:** enforced by `gci` — stdlib, third-party, local module
- **Formatting:** `gofmt` enforced
- **No globals:** `gochecknoglobals` — avoid package-level `var` for mutable state. Use `//nolint:gochecknoglobals // reason` only for truly necessary globals (e.g., atomic counters).
- **No magic numbers:** `mnd` — extract magic numbers as named constants
- **Duplicate code:** `dupl` threshold 100 — use helper functions to reduce duplication
- **Shadow detection:** `govet` with shadow enabled
- **`nolintlint`:** requires explanation for `//nolint` directives

### Before Pushing Code

Always run the linter locally before pushing:

```bash
golangci-lint run --timeout=9m ./...
```

Fix all issues before committing. If a `//nolint` directive is needed, always include an explanation.

## Testing

```bash
go build ./...     # compile all packages
go vet ./...       # static analysis
go test -v ./...   # run all tests

# Run a specific test
go test -v -run TestFunctionName ./...

# Run tests with coverage
go test -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

### Test Conventions

- **Table-driven tests** with `testify` assertions. Do not use `if/else` in test files.
- **Test naming:** `TestXxx` pattern — descriptive names covering the service/behavior being tested.
- **No mocks for internal packages** — tests use the real in-memory implementations directly.
- **Deterministic time:** Use `config.FakeClock` for any time-dependent tests (dedup windows, alarm evaluation, metric timestamps).
- **All 3 providers must be tested** — when adding a feature, add tests covering AWS, Azure, and GCP.
- Tests are in `cloudemu_test.go` (root).

### Test Naming Convention

Tests follow `TestXxx` pattern covering each service and behavior:
- `TestAWSS3Operations`, `TestAzureBlobOperations`, `TestGCPGCSOperations` — storage
- `TestAWSEC2Lifecycle`, etc. — compute with state machine
- `TestScanMissingOperators` — database `<=`, `>=`, `BEGINS_WITH`
- `TestNumericComparison` — numeric-aware database comparisons
- `TestFIFODeduplication` — message queue dedup
- `TestIAMCheckPermission` — IAM policy evaluation
- `TestAlarmAutoEvaluation` — monitoring alarm auto-eval
- `TestAutoMetricGeneration` — compute → monitoring metrics
- `TestAlarmTriggeredByAutoMetrics` — end-to-end: VM launch → alarm fires
- `TestLifecycleStopEmitsZeroMetrics` — stop emits zero metrics, triggers alarm
- `TestLifecycleStartEmitsRunningMetrics` — start emits running metrics, triggers alarm

## Code Patterns

### Adding a New Service

1. Create driver interface in `<service>/driver/driver.go`
2. Create provider implementations in `providers/{aws,azure,gcp}/<service>/`
3. Add field to each `Provider` struct in `providers/{aws,azure,gcp}/{aws,azure,gcp}.go`
4. Initialize in each `New()` factory
5. Add portable API wrapper in `<service>/`
6. Add tests in `cloudemu_test.go`

### Thread Safety

All mock implementations use `sync.RWMutex` for concurrent access. The monitoring mocks release the write lock before calling `evaluateAlarms()` to avoid deadlocks.

### Error Codes

Use `cerrors.New(code, msg)` or `cerrors.Newf(code, fmt, args...)` from `github.com/stackshy/cloudemu/errors`:

- `cerrors.NotFound` — resource doesn't exist
- `cerrors.AlreadyExists` — duplicate create
- `cerrors.InvalidArgument` — bad input
- `cerrors.FailedPrecondition` — illegal state transition
- `cerrors.PermissionDenied` — access denied
- `cerrors.Throttled` — rate limit exceeded

### ID Generation

- AWS: `idgen.ARN(accountID, service, resource)` → `arn:aws:s3:::my-bucket`
- Azure: `idgen.AzureID(subscriptionID, resourceGroup, provider, resource)` → `/subscriptions/.../...`
- GCP: `idgen.GCPID(projectID, collection, resource)` → `projects/.../...`

## File Structure

```
cloudemu.go                         # Entry point: NewAWS(), NewAzure(), NewGCP()
cloudemu_test.go                    # All tests (19 tests)
doc.go                              # Package documentation
go.mod                              # Module definition
config/
    options.go                      # Options, WithClock, WithRegion, etc.
    clock.go                        # Clock interface, RealClock, FakeClock
errors/                             # Canonical error codes
internal/
    memstore/                       # Generic Store[V]
    idgen/                          # ID generators
statemachine/                       # VM lifecycle FSM
pagination/                         # Generic paginator
providers/
    aws/
        aws.go                      # AWS factory (wires EC2→CloudWatch)
        s3/s3.go
        ec2/ec2.go                  # Has SetMonitoring + emitInstanceMetrics
        dynamodb/dynamodb.go        # Has compareValues + full operators
        lambda/lambda.go
        vpc/vpc.go
        cloudwatch/cloudwatch.go    # Has evaluateAlarms + evaluateComparison
        awsiam/iam.go               # Has evaluatePolicy + wildcardMatch
        route53/route53.go
        elb/elb.go
        sqs/sqs.go                  # Has FIFO deduplication
    azure/
        azure.go                    # Azure factory (wires VMs→Monitor)
        blobstorage/blobstorage.go
        virtualmachines/vm.go       # Has SetMonitoring + emitInstanceMetrics
        cosmosdb/cosmosdb.go        # Has compareValues + full operators
        functions/functions.go
        vnet/vnet.go
        azuremonitor/monitor.go     # Has evaluateAlarms + evaluateComparison
        azureiam/iam.go             # Has evaluatePolicy + wildcardMatch
        azuredns/azuredns.go
        azurelb/azurelb.go
        servicebus/servicebus.go    # Has FIFO deduplication
    gcp/
        gcp.go                      # GCP factory (wires GCE→CloudMonitoring)
        gcs/gcs.go
        gce/gce.go                  # Has SetMonitoring + emitInstanceMetrics
        firestore/firestore.go      # Has compareValues + full operators
        cloudfunctions/cloudfunctions.go
        gcpvpc/gcpvpc.go
        cloudmonitoring/monitoring.go  # Has evaluateAlarms + evaluateComparison
        gcpiam/iam.go               # Has evaluatePolicy + wildcardMatch
        clouddns/clouddns.go
        gcplb/gcplb.go
        pubsub/pubsub.go           # Has FIFO deduplication
```

## Important Notes

- Always keep README.md and CLAUDE.md in sync when making changes
- All 3 providers (AWS, Azure, GCP) must implement the same behaviors — changes should be mirrored across all 3
- The `config.FakeClock` is essential for deterministic testing of time-dependent features (dedup windows, alarm evaluation, metric timestamps)
- Provider factory files wire cross-service dependencies — update these when adding inter-service interactions
