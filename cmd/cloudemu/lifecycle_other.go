//go:build !unix

package main

import "errors"

// runLifecycle is unavailable off Unix: the background daemon relies on
// session detachment (Setsid) and POSIX signals. Non-Unix users run the server
// in the foreground with `cloudemu serve`.
func runLifecycle(_ string, _ []string) error {
	return errors.New("start/stop/status/logs/delete are only supported on Unix/macOS; run `cloudemu serve` instead")
}

// runSnapshot is unavailable off Unix for the same reason as the lifecycle
// commands: it operates on the background daemon's run directory.
func runSnapshot(_ []string) error {
	return errors.New("snapshot is only supported on Unix/macOS")
}

// runNet is unavailable off Unix for the same reason: it talks to the
// background daemon via its run directory.
func runNet(_ []string) error {
	return errors.New("net is only supported on Unix/macOS")
}
