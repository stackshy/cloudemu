// Command cloudemu-server runs the CloudEmu standalone wire-protocol server with
// real data-plane engines wired in — the batteries-included variant of
// `cloudemu serve`. The core `cloudemu` module stays dependency-free, so the
// real engines live in contrib and are composed here in their own module.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	// readHeaderTimeout bounds how long a client may take to send request
	// headers, guarding the listeners against slow-header stalls.
	readHeaderTimeout = 10 * time.Second
	// defaultShutdownTimeout is the grace period for in-flight requests.
	defaultShutdownTimeout = 10 * time.Second
)

// appConfig is the resolved flag/env configuration for one server run.
type appConfig struct {
	engines           engineSelection
	host              string
	awsPort           string
	azurePort         string
	gcpPort           string
	accountID         string
	azureSubscription string
	region            string
	projectID         string
	shutdownTimeout   time.Duration
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "cloudemu-server:", err)
		os.Exit(1)
	}
}

// run parses configuration, builds and starts the server, and blocks until a
// termination signal (or a fatal serve error) arrives, then shuts down cleanly.
func run(args []string, stdout, stderr io.Writer) error {
	cfg, err := parseFlags(args, os.Getenv, stderr)
	if err != nil {
		return err
	}

	a, err := newApp(&cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errc := a.serve()
	printBanner(a, stdout)

	var serveErr error

	select {
	case <-ctx.Done():
		fmt.Fprintln(stdout, "\nshutting down…")
	case serveErr = <-errc:
		stop()
	}

	// Always shut down — HTTP servers then Provider.Close() (engine teardown) —
	// on both a termination signal and a fatal post-bind serve error, so a real
	// engine's containers/subprocesses are never left running.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
	defer cancel()

	return errors.Join(serveErr, a.shutdown(shutdownCtx))
}

// parseFlags resolves the configuration from args, with environment-variable
// fallbacks for the engine selectors.
func parseFlags(args []string, getenv func(string) string, out io.Writer) (appConfig, error) {
	fs := flag.NewFlagSet("cloudemu-server", flag.ContinueOnError)
	fs.SetOutput(out)

	var cfg appConfig

	var allReal bool

	fs.StringVar(&cfg.engines.db, "db", engineEnvOr(getenv, "CLOUDEMU_DB"), "database engine: off|postgres|mysql|both")
	fs.StringVar(&cfg.engines.cache, "cache", engineEnvOr(getenv, "CLOUDEMU_CACHE"), "cache engine: off|redis")
	fs.StringVar(&cfg.engines.functions, "functions", engineEnvOr(getenv, "CLOUDEMU_FUNCTIONS"), "function engine: off|subprocess")
	fs.StringVar(&cfg.engines.compute, "compute", engineEnvOr(getenv, "CLOUDEMU_COMPUTE"), "compute engine: off|docker")
	fs.StringVar(&cfg.engines.containers, "containers", engineEnvOr(getenv, "CLOUDEMU_CONTAINERS"), "container engine: off|docker")
	fs.BoolVar(&allReal, "all-real", false, "shorthand: postgres + redis + subprocess + docker compute + docker containers")

	fs.StringVar(&cfg.host, "host", "127.0.0.1", "host/interface to bind (0.0.0.0 exposes on the network)")
	fs.StringVar(&cfg.awsPort, "aws-port", "4566", "port for the AWS endpoint (HTTP)")
	fs.StringVar(&cfg.azurePort, "azure-port", "4568", "port for the Azure endpoint (HTTPS)")
	fs.StringVar(&cfg.gcpPort, "gcp-port", "4569", "port for the GCP endpoint (HTTP)")
	fs.StringVar(&cfg.accountID, "account-id", "000000000000", "AWS account ID (also GCP) reported by the emulator")
	fs.StringVar(&cfg.azureSubscription, "azure-subscription", "00000000-0000-0000-0000-000000000000",
		"Azure subscription id reported by the emulator (a GUID)")
	fs.StringVar(&cfg.region, "region", "us-east-1", "default region reported by the emulator")
	fs.StringVar(&cfg.projectID, "project-id", "cloudemu-local", "GCP project ID reported by the emulator")
	fs.DurationVar(&cfg.shutdownTimeout, "shutdown-timeout", defaultShutdownTimeout, "grace period for in-flight requests on shutdown")

	if err := fs.Parse(args); err != nil {
		return appConfig{}, err
	}

	if allReal {
		cfg.engines.applyAllReal()
	}

	return cfg, nil
}

// engineEnvOr returns the engine value from the environment for key, defaulting
// to "off" (in-memory) when it is unset/empty.
func engineEnvOr(getenv func(string) string, key string) string {
	if v := getenv(key); v != "" {
		return v
	}

	return engineOff
}

// printBanner prints the resolved endpoints and a client-setup hint.
func printBanner(a *app, w io.Writer) {
	fmt.Fprintln(w, "cloudemu-server (batteries-included) listening:")

	for _, s := range a.servers {
		fmt.Fprintf(w, "  %-6s %s\n", s.name, s.url(a.displayHost))
	}

	fmt.Fprintf(w, "engines: db=%s cache=%s functions=%s compute=%s containers=%s\n",
		a.cfg.engines.db, a.cfg.engines.cache, a.cfg.engines.functions, a.cfg.engines.compute, a.cfg.engines.containers)
	fmt.Fprintln(w, `point your tools at these endpoints — e.g. run: eval "$(cloudemu env)"`)
}
