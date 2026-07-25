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
  cloudemu serve [flags]     Start the standalone server (see: cloudemu serve -h)
  cloudemu version           Print the version
  cloudemu help              Show this message

Run "cloudemu serve -h" for the full list of serve flags.
`

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
	case "version", "-v", "--version":
		fmt.Println("cloudemu", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "cloudemu: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
