// Package cloudemu provides zero-cost, in-memory cloud emulation of
// AWS, Azure, and GCP cloud services for Go.
//
// The repository is organized by role so new services and features slot into
// predictable places:
//
//   - services/<name>: the emulated cloud services. Each holds the Portable API
//     type (e.g. services/storage's storage.Bucket) plus its driver interface
//     under services/<name>/driver.
//
//   - providers/{aws,azure,gcp}: in-memory backends implementing the drivers.
//
//   - server/{aws,azure,gcp}: SDK-compat HTTP servers that speak each cloud's
//     real wire protocol, so unmodified SDK clients drive the backends.
//
//   - features/<name>: cross-cutting capabilities you wrap drivers with —
//     chaos, recorder, metrics, inject, ratelimit, and topology.
//
//   - config, errors: foundational options and the canonical error type.
//
// The three surfaces build on the same drivers: the SDK-compat server (the
// primary entrypoint), the Portable API (services/<name>), and the cross-cutting
// features, so a behavior implemented in a driver lights up across all of them.
package cloudemu
