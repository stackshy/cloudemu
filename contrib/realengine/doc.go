// Package realengine is the umbrella for CloudEmu's opt-in real data-plane
// engines. Each engine lives in its own subpackage and backs a family of
// emulated resources with a real, no-Docker implementation, so client code can
// run actual SQL, real Redis commands, or real function code against CloudEmu
// instead of a synthetic endpoint:
//
//   - postgres  — a real embedded PostgreSQL server behind RDS/Aurora, Azure
//     PostgreSQL Flexible Server, Cloud SQL, and AlloyDB.
//     Wire in: config.WithDatabaseEngine(postgres.New(0)).
//   - redis     — in-process Redis servers (miniredis) behind ElastiCache,
//     Azure Cache for Redis, and Memorystore.
//     Wire in: config.WithCacheEngine(redis.New()).
//   - functions — a subprocess runtime (Python/Node) behind AWS Lambda and GCP
//     Cloud Functions.
//     Wire in: config.WithFunctionEngine(functions.New()).
//
// It is a separate module on purpose: the engines' heavyweight dependencies
// stay out of the core cloudemu module, preserving the in-memory, no-Docker
// default.
package realengine
