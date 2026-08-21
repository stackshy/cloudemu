// Package azure holds CloudEmu's Azure SDK-compatibility suite.
//
// The tests live in *_test.go files and run real azure-sdk-for-go clients
// against the in-process wire server via the shared harness in internal/compat.
// Operation names match the portable driver in docs/coverage/coverage.json, so
// the Azure results land in the same matrix rows as the other providers.
package azure
