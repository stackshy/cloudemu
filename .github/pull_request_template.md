## Summary

<!-- What does this PR do? Keep it brief — 1-3 bullet points. -->

## Changes

<!-- List the key changes made. -->

## Provider Coverage

- [ ] AWS
- [ ] Azure
- [ ] GCP
- [ ] OCI

## Checklist

- [ ] All tests pass (`go test ./...`)
- [ ] Linter passes (`golangci-lint run --timeout=9m ./...`)
- [ ] Every provider the change applies to implements the same behavior
- [ ] Integration tests added to `cloudemu_test.go`
- [ ] Unit tests added to provider test files
- [ ] Regenerated docs (`go generate ./...` for coverage; `go run ./internal/compatgen` for the compat matrix) and committed the result

## Test Plan

<!-- How was this tested? List the test names or describe manual testing. -->

## Related Issues

<!-- Link related issues: Closes #XX -->
