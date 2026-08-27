# CloudEmu Documentation

CloudEmu is a zero-cost, in-memory emulation of the AWS, Azure, GCP, OCI, and Kubernetes cloud APIs. Run it as a standalone server (`cloudemu serve` or the `ghcr.io/stackshy/cloudemu` Docker image) and point any app in any language at a local endpoint, or use it in-process from Go via the SDK-compat HTTP server or the typed mock API -- for testing and development without real cloud accounts, network, or bill. Drivers are in-memory by default and can be backed by opt-in [real engines](features.md#11-real-data-plane-engines-opt-in) (real SQL/Redis/function code) when you need real workloads.

## Table of Contents

- [Architecture](architecture.md) -- Three-layer design, package structure, cross-service wiring
- [Structure & Naming](STRUCTURE.md) -- Canonical service names, file-naming rule, per-directory layout, and where new code goes
- [Services](services.md) -- Complete provider resource reference with all operations across every supported service
- [Features](features.md) -- Cross-cutting features: auto-metrics, alarm evaluation, IAM policy checking, FIFO dedup, cost tracking, and more
- [SDK Server](sdk-server.md) -- SDK-compatible HTTP server (use the real aws-sdk-go-v2 against CloudEmu)
- [Standalone Server](standalone-server.md) -- Run CloudEmu as a local dev cloud (`cloudemu serve` / Docker), point any language at it
- [Integration](integration.md) -- Wire CloudEmu into your real app and tests (not a throwaway demo)
- [Terraform / OpenTofu](terraform.md) -- Run real Terraform/OpenTofu against CloudEmu (the `cloudemu-tf` wrapper + manual provider config)
- [Topology](topology.md) -- Network topology simulation engine
- [Chaos](chaos.md) -- Fault, latency, and throttling injection across the service layer
- [Persistence](persistence.md) -- Snapshot and restore the whole emulator's state (opt-in, identity-preserving)
- [Getting Started](getting-started.md) -- Installation, provider creation, basic examples, configuration
- [OCI Conventions](oci-conventions.md) -- The contract every OCI service implementation follows

## Quick Links

| Topic | Link |
|-------|------|
| Creating an AWS provider | [Getting Started](getting-started.md#creating-providers) |
| All service operations | [Services Reference](services.md#master-table) |
| Using real AWS SDK clients | [SDK Server](sdk-server.md) |
| Running Terraform/OpenTofu | [Terraform](terraform.md) |
| Integrating into your app | [Integration](integration.md) |
| Auto-metric generation | [Features](features.md#1-auto-metric-generation) |
| Error injection and rate limiting | [Features](features.md#8-portable-api-cross-cutting-concerns) |
| Cost tracking | [Features](features.md#7-cost-tracking) |
| Real data-plane engines | [Features](features.md#11-real-data-plane-engines-opt-in) |
| Snapshot & restore state | [Persistence](persistence.md) |
| Configuration options | [Getting Started](getting-started.md#configuration-options) |
| Package structure | [Architecture](architecture.md#package-structure-overview) |
