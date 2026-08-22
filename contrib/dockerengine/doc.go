// Package dockerengine backs CloudEmu's data-plane resources with real Docker
// containers so clients can run true-fidelity workloads against the emulator —
// real VMs (via a backing container), real containers (ECS/Kubernetes/ACI), and
// engines that need Docker for real fidelity (e.g. MySQL). It is the Docker-
// required sibling of contrib/realengine, which covers the no-Docker backings.
//
// It is a separate Go module on purpose: its dependency is the host's Docker
// CLI, which stays out of the core cloudemu module. The in-memory, no-Docker
// default is unchanged; Docker engines are strictly opt-in and only apply when a
// caller wires one via config.With<X>Engine.
//
// On top of the shared low-level plumbing (a runner over the docker CLI and an
// Available check) it provides four engine backings: MySQL (NewMySQL,
// config.DatabaseEngine), VM compute (NewCompute, config.ComputeEngine),
// containers (NewContainers, config.ContainerEngine — ECS/ACI/Cloud Run), and
// Azure Functions (NewAzureFunctions, config.FunctionEngine, via the official
// azure-functions host image).
package dockerengine
