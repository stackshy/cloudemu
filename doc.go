// Package cloudemu provides zero-cost, in-memory emulation of the AWS, Azure,
// and GCP cloud APIs. It runs three ways: as a standalone server (the
// cloudemu serve binary or the ghcr.io/stackshy/cloudemu Docker image) that any
// app in any language can point at, and in-process from Go via either the
// SDK-compat HTTP server or the typed mock API.
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
//   - cmd/cloudemu: the standalone "cloudemu serve" binary (also shipped as a
//     Docker image) that runs the SDK-compat servers as a long-lived process.
//
//   - features/<name>: cross-cutting capabilities you wrap drivers with —
//     chaos, recorder, metrics, inject, ratelimit, and topology.
//
//   - config, errors: foundational options and the canonical error type.
//
// Every surface builds on the same drivers — the SDK-compat server (in-process
// or standalone via cmd/cloudemu), the Portable API (services/<name>), and the
// cross-cutting features — so a behavior implemented in a driver lights up
// across all of them.
package cloudemu
