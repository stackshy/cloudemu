// Package cloudemu provides zero-cost, in-memory cloud emulation of
// AWS, Azure, and GCP cloud services for Go.
//
// cloudemu follows a three-layer architecture:
//
//   - Portable API: High-level types (storage.Bucket, compute.Compute, etc.)
//     that wrap drivers with cross-cutting concerns like recording, metrics,
//     rate limiting, and error injection.
//
//   - Driver Interfaces: Minimal contracts (storage/driver, compute/driver, etc.)
//     that each provider must implement.
//
//   - Provider Implementations: In-memory backends (providers/aws/s3, providers/azure/blobstorage,
//     providers/gcp/gcs, etc.) powered by a generic memstore.
//
// 10 cloud services are covered across all three providers: Storage, Compute,
// Database, Serverless, Networking, Monitoring, IAM, DNS, Load Balancer,
// and Message Queue.
package cloudemu
