# CloudEmu Documentation

CloudEmu is a zero-cost, in-memory cloud emulation library for Go. It provides mock implementations of 16 cloud services across AWS, Azure, and GCP -- designed for testing and development without real cloud accounts, Docker, or network calls.

## Table of Contents

- [Architecture](architecture.md) -- Three-layer design, package structure, cross-service wiring
- [Services](services.md) -- Complete provider resource reference with all operations for 16 services
- [Features](features.md) -- Cross-cutting features: auto-metrics, alarm evaluation, IAM policy checking, FIFO dedup, cost tracking, and more
- [SDK Server](sdk-server.md) -- SDK-compatible HTTP server (use the real aws-sdk-go-v2 against CloudEmu)
- [Topology](topology.md) -- Network topology simulation engine
- [Getting Started](getting-started.md) -- Installation, provider creation, basic examples, configuration

## Quick Links

| Topic | Link |
|-------|------|
| Creating an AWS provider | [Getting Started](getting-started.md#creating-providers) |
| All 16 service operations | [Services Reference](services.md#master-table) |
| Using real AWS SDK clients | [SDK Server](sdk-server.md) |
| Auto-metric generation | [Features](features.md#1-auto-metric-generation) |
| Error injection and rate limiting | [Features](features.md#8-portable-api-cross-cutting-concerns) |
| Cost tracking | [Features](features.md#7-cost-tracking) |
| Configuration options | [Getting Started](getting-started.md#configuration-options) |
| Package structure | [Architecture](architecture.md#package-structure-overview) |
