//go:build !unix

package main

import "errors"

// runLifecycle is unavailable off Unix: the background daemon relies on
// session detachment (Setsid) and POSIX signals. Non-Unix users run the server
// in the foreground with `cloudemu serve`.
func runLifecycle(_ string, _ []string) error {
	return errors.New("start/stop/status/logs/delete are only supported on Unix/macOS; run `cloudemu serve` instead")
}
