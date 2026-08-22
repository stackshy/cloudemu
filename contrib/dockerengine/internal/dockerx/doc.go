// Package dockerx is the shared low-level plumbing every Docker-backed engine in
// this module builds on: a Runner over the docker CLI, an Available check, and
// the small helpers (SanitizeName, EnvArgs) engines use to assemble argv.
package dockerx
