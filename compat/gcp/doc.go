// Package gcp holds CloudEmu's GCP SDK-compatibility suite.
//
// The tests live in *_test.go files and run real google-cloud client libraries
// against the in-process wire server via the shared harness in internal/compat.
// Operation names match the portable driver in docs/coverage/coverage.json, so
// the GCP results land in the same matrix rows as the other providers.
package gcp
