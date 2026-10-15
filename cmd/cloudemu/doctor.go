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
	"strings"

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

// The doctor echoes serve's canonical enginesImagePointer (defined in serve.go)
// so the two commands can't drift on where the real engines live.

// portCheck is one endpoint the emulator can bind. required marks a port that
// serve binds by default, the default providers' plus the k8s data-plane: an
// in-use required port is a genuine blocker, any other is only a warning.
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

	return portChecks(&c)
}

// portChecks returns the ports a serve started with c would bind: a provider
// left out of --providers is only a warning, and an empty --k8s-port is skipped.
func portChecks(c *serveflags.CommonConfig) []portCheck {
	started := map[string]bool{}
	for _, p := range strings.Split(c.Providers, ",") {
		started[strings.TrimSpace(p)] = true
	}

	checks := []portCheck{
		{"AWS", c.AWSPort, started["aws"]},
		{"Azure", c.AzurePort, started["azure"]},
		{"GCP", c.GCPPort, started["gcp"]},
		{"Kubernetes", c.K8sPort, true},
		{"OCI", c.OCIPort, started["oci"]},
	}

	out := checks[:0]

	for _, p := range checks {
		if p.port != "" {
			out = append(out, p)
		}
	}

	return out
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

	return fmt.Sprintf("%s docker not found on PATH (only needed if you run the :engines image)", markWarn)
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
	fmt.Fprintln(w, "cloudemu doctor: preflight check")
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

			fmt.Fprintf(w, "  %s %-14s %s  in use: free it or pass a different --%s-port to serve\n",
				markFail, p.label, hostPort, portFlagHint(p.label))

			continue
		}

		fmt.Fprintf(w, "  %s %-14s %s  in use (not in --providers, so serve won't bind it)\n", markWarn, p.label, hostPort)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, dockerLine(lookPath))
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Real engines (postgres/redis/subprocess/docker/localfs) are NOT compiled into this lean")
	fmt.Fprintln(w, "binary. To run them, use the :engines image:")
	fmt.Fprintf(w, "  %s\n", enginesImagePointer)
	fmt.Fprintln(w)

	if blockers == 0 {
		fmt.Fprintf(w, "%s all preflight checks passed, ready to `cloudemu serve`.\n", markPass)
	} else {
		fmt.Fprintf(w, "%s %d required port(s) in use. Free them before `cloudemu serve`.\n", markFail, blockers)
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

	// The same flags as serve, so `cloudemu doctor <serve args>` checks the ports
	// that serve would bind, for the providers it would start.
	var c serveflags.CommonConfig

	serveflags.RegisterCommon(fs, &c, os.Getenv)

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: cloudemu doctor [serve flags]\n\n"+
			"Preflight check: build version, the ports serve would bind are free, docker availability.\n"+
			"Pass the same --providers and port flags you give serve.\n\nFlags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	blockers := writeDoctorReport(os.Stdout, c.Host, portChecks(&c), checkPortFree, exec.LookPath)
	if blockers > 0 {
		return errPreflightBlocker
	}

	return nil
}
