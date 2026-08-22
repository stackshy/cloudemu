// Package dockerengine is the module root for CloudEmu's Docker-backed data-plane
// engines: opt-in backings that run real Docker containers so clients can run
// true-fidelity workloads against the emulator — real VMs (via a backing
// container), real containers (ECS/ACI/Cloud Run), a real MySQL server, and a
// real Azure Functions host. It is the Docker-required sibling of
// contrib/realengine, which covers the no-Docker backings.
//
// It is a separate Go module on purpose: its dependency is the host's Docker
// CLI, which stays out of the core cloudemu module. The in-memory, no-Docker
// default is unchanged; Docker engines are strictly opt-in and only apply when a
// caller wires one via config.With<X>Engine.
//
// The engines live in one subpackage each, and every one exposes a New
// constructor:
//
//   - mysql.New(port)             config.DatabaseEngine (real mysql:8.0 container)
//   - compute.New(...Option)      config.ComputeEngine  (VM boot script in a container)
//   - container.New()             config.ContainerEngine (ECS/ACI/Cloud Run)
//   - azurefunctions.New(...Option) config.FunctionEngine (official azure-functions host image)
//
// The shared low-level plumbing (a Runner over the docker CLI and an Available
// check) lives in the internal/dockerx subpackage.
//
// This root package carries no code; import the subpackage for the engine you
// need.
package dockerengine
