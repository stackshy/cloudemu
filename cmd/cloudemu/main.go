// Command cloudemu runs the in-memory cloud emulator as a long-lived,
// out-of-process HTTP server. Point real AWS, Azure, and GCP SDK clients at
// the printed endpoints and they talk to cloudemu over the network exactly as
// they would to the real cloud — no code changes, no accounts, no Docker.
//
// This is purely additive: the in-process test-double API
// (cloudemu.NewAWS(), server/*.New(Drivers{...})) is unchanged.
package main

import (
	"fmt"
	"os"
)

const usage = `cloudemu — in-memory AWS/Azure/GCP emulator

Usage:
  cloudemu start [flags]     Start the emulator in the background (accepts serve flags)
  cloudemu stop              Stop the background emulator
  cloudemu status            Show whether the emulator is running and its endpoints
  cloudemu logs [-f]         Print (or follow) the background emulator's log
  cloudemu delete            Stop the emulator and remove its run directory
  cloudemu serve [flags]     Run the server in the foreground (see: cloudemu serve -h)
  cloudemu version           Print the version
  cloudemu help              Show this message

Lifecycle commands keep run state (pid, log, endpoints) under ~/.cloudemu by
default; override with --home <dir>. Run "cloudemu serve -h" for serve flags.
`

// Lifecycle subcommand names. Defined here (not in the Unix-tagged
// lifecycle.go) so the dispatch compiles on every platform.
const (
	cmdStart  = "start"
	cmdStop   = "stop"
	cmdStatus = "status"
	cmdLogs   = "logs"
	cmdDelete = "delete"
)

// version is overridable at build time with
// -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "cloudemu:", err)
			os.Exit(1)
		}
	case cmdStart, cmdStop, cmdStatus, cmdLogs, cmdDelete:
		if err := runLifecycle(os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "cloudemu:", err)
			os.Exit(1)
		}
	case "version", "-v", "--version":
		fmt.Println("cloudemu", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "cloudemu: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
