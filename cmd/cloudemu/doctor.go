package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"

	"github.com/stackshy/cloudemu/v2/server/serveflags"
)

// errPreflightBlocker is returned when a required port is in use, so runDoctor
// exits non-zero after printing the report.
var errPreflightBlocker = errors.New("preflight found a blocker (see above)")

// Status markers for the doctor report. ASCII-only so they render the same on
// every platform (this command is deliberately not Unix-tagged).
const (
	markPass = "[ ok ]"
	markWarn = "[warn]"
	markFail = "[fail]"
)

// enginesImagePointer is the one-line pointer at the :engines image, kept
// identical to the message serve prints (errEnginesNotInLeanBinary) so the CLI
// speaks with one voice about where the real engines live.
const enginesImagePointer = "docker run -p 4566:4566 ghcr.io/stackshy/cloudemu:engines --all-real"

// portCheck is one default endpoint the emulator would bind. required marks a
// port the default provider set (aws,azure,gcp) plus the k8s data-plane bind on
// startup — an in-use required port is a genuine blocker. OCI is opt-in, so a
// busy OCI port is only a warning.
type portCheck struct {
	label    string
	port     string
	required bool
}

// defaultPortChecks returns the ports `cloudemu serve` binds by default, read
// from serveflags so the doctor can never drift from the real defaults.
func defaultPortChecks() []portCheck {
	var c serveflags.CommonConfig

	fs := flag.NewFlagSet("doctor-defaults", flag.ContinueOnError)
	serveflags.RegisterCommon(fs, &c, func(string) string { return "" })
	_ = fs.Parse(nil)

	return []portCheck{
		{"AWS", c.AWSPort, true},
		{"Azure", c.AzurePort, true},
		{"GCP", c.GCPPort, true},
		{"Kubernetes", c.K8sPort, true},
		{"OCI (opt-in)", c.OCIPort, false},
	}
}

// checkPortFree reports whether a TCP listener can be bound at hostPort right
// now. This is a point-in-time probe: a "free" port can still be taken before
// `cloudemu serve` binds it (TOCTOU), which the report wording makes explicit.
func checkPortFree(hostPort string) bool {
	var lc net.ListenConfig

	ln, err := lc.Listen(context.Background(), "tcp", hostPort)
	if err != nil {
		return false
	}

	_ = ln.Close()

	return true
}

// dockerLine reports whether a `docker` binary is on PATH. Docker is needed only
// to run the :engines image, so its absence is a warning, never a failure.
// lookPath is injected (default exec.LookPath) so tests need no real docker.
func dockerLine(lookPath func(string) (string, error)) string {
	if path, err := lookPath("docker"); err == nil {
		return fmt.Sprintf("%s docker found at %s (only needed for the :engines image)", markPass, path)
	}

	return fmt.Sprintf("%s docker not found on PATH — only needed if you run the :engines image", markWarn)
}

// writeDoctorReport writes the preflight report to w and returns the number of
// genuine blockers (required ports in use). portFree and lookPath are injected so
// the report is deterministic under test.
func writeDoctorReport(
	w io.Writer,
	host string,
	ports []portCheck,
	portFree func(string) bool,
	lookPath func(string) (string, error),
) int {
	fmt.Fprintln(w, "cloudemu doctor — preflight check")
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%s version %s (commit %s, built %s by %s)\n", markPass, version, commit, date, builtBy)
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Ports on %s (free = free at check time; a port can still be taken before serve binds it):\n", host)

	blockers := 0

	for _, p := range ports {
		hostPort := net.JoinHostPort(host, p.port)

		if portFree(hostPort) {
			fmt.Fprintf(w, "  %s %-14s %s  free (at check time)\n", markPass, p.label, hostPort)

			continue
		}

		if p.required {
			blockers++

			fmt.Fprintf(w, "  %s %-14s %s  in use — free it or pass a different --%s-port to serve\n",
				markFail, p.label, hostPort, portFlagHint(p.label))

			continue
		}

		fmt.Fprintf(w, "  %s %-14s %s  in use (opt-in; only bound when you start OCI)\n", markWarn, p.label, hostPort)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, dockerLine(lookPath))
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Real engines (postgres/redis/subprocess/docker/localfs) are NOT compiled into this lean")
	fmt.Fprintln(w, "binary. To run them, use the :engines image:")
	fmt.Fprintf(w, "  %s\n", enginesImagePointer)
	fmt.Fprintln(w)

	if blockers == 0 {
		fmt.Fprintf(w, "%s all preflight checks passed — ready to `cloudemu serve`.\n", markPass)
	} else {
		fmt.Fprintf(w, "%s %d required port(s) in use — free them before `cloudemu serve`.\n", markFail, blockers)
	}

	return blockers
}

// portFlagHint maps a port label to the serve flag that overrides it, for the
// fix hint on an in-use required port.
func portFlagHint(label string) string {
	switch label {
	case "AWS":
		return "aws"
	case "Azure":
		return "azure"
	case "GCP":
		return "gcp"
	case "Kubernetes":
		return "k8s"
	default:
		return "oci"
	}
}

// runDoctor runs the preflight check: build version, default ports free, docker
// availability, and where the real engines live. It prints a pass/warn/fail
// summary and returns a non-nil error only when a required port is in use.
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)

	host := fs.String("host", "127.0.0.1", "host/interface the emulator would bind (mirrors serve --host)")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: cloudemu doctor [flags]\n\n"+
			"Preflight check: build version, default ports free, docker availability.\n\nFlags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	blockers := writeDoctorReport(os.Stdout, *host, defaultPortChecks(), checkPortFree, exec.LookPath)
	if blockers > 0 {
		return errPreflightBlocker
	}

	return nil
}
