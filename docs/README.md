# CloudEmu Documentation

CloudEmu is an in-memory emulator for the AWS, Azure, GCP, OCI and Kubernetes APIs. You can run it as a standalone server (`cloudemu serve` or the `ghcr.io/stackshy/cloudemu` Docker image) and point an app in any language at a local endpoint. From Go you can also use it in-process, through the SDK-compat HTTP server or the typed mock API. Either way you test and develop without a cloud account, network access or a bill. Drivers are in-memory by default; when you need real workloads, you can back them with opt-in [real engines](features.md#11-real-data-plane-engines-opt-in) (real SQL, Redis or function code).

## Table of Contents

- [Architecture](architecture.md): the three-layer design, package map, cross-service wiring
- [Structure & Naming](STRUCTURE.md): service names, file naming, per-directory layout, and where new code goes
- [Services](services.md): curated per-provider resource reference for the core service categories
- [Features](features.md): auto-metrics, alarm evaluation, IAM policy checks, FIFO dedup, cost tracking, and more
- [SDK Server](sdk-server.md): the in-process SDK-compatible HTTP server (use the real aws-sdk-go-v2 against CloudEmu)
- [Standalone Server](standalone-server.md): run CloudEmu as a local dev cloud (`cloudemu serve` / Docker) and point any language at it
- [Integration](integration.md): connect your existing app and tests to CloudEmu
- [Terraform / OpenTofu](terraform.md): run Terraform/OpenTofu against CloudEmu (the `cloudemu-tf` wrapper or a manual provider config)
- [Topology](topology.md): the network topology engine
- [Chaos](chaos.md): fault, latency and throttling injection in the service layer
- [Persistence](persistence.md): snapshot and restore the whole emulator's state (opt-in, keeps resource IDs)
- [Getting Started](getting-started.md): installation, creating providers, basic examples, configuration
- [OCI Conventions](oci-conventions.md): the rules every OCI service implementation follows
- [Capability coverage](coverage/README.md): every service and operation, generated from the code

## Quick Links

| Topic | Link |
|-------|------|
| Creating an AWS provider | [Getting Started](getting-started.md#creating-providers) |
| All service operations | [Capability coverage](coverage/README.md) |
| Core service categories | [Services Reference](services.md#master-table) |
| Using real AWS SDK clients | [SDK Server](sdk-server.md) |
| Running Terraform/OpenTofu | [Terraform](terraform.md) |
| Integrating into your app | [Integration](integration.md) |
| Auto-metric generation | [Features](features.md#1-auto-metric-generation) |
| Error injection and rate limiting | [Features](features.md#8-portable-api-cross-cutting-concerns) |
| Cost tracking | [Features](features.md#7-cost-tracking) |
| Real data-plane engines | [Features](features.md#11-real-data-plane-engines-opt-in) |
| Snapshot & restore state | [Persistence](persistence.md) |
| Configuration options | [Getting Started](getting-started.md#configuration-options) |
| Package structure | [Architecture](architecture.md#package-map) |
