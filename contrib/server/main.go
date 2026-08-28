// Command cloudemu-server runs the CloudEmu standalone wire-protocol server with
// real data-plane engines wired in — the batteries-included variant of
// `cloudemu serve`. The core `cloudemu` module stays dependency-free, so the
// real engines live in contrib and are composed here in their own module.
//
// Assembly (provider construction, listener binding, the shutdown/snapshot
// lifecycle, engine teardown) is delegated to the shared server/serverkit
// package, so this binary and `cloudemu serve` don't drift; this module only adds
// the real-engine selection.
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

// defaultShutdownTimeout is the grace period for in-flight requests.
const defaultShutdownTimeout = 10 * time.Second

// errStateFileRequired is returned when --persist is set without --state-file.
var errStateFileRequired = errors.New("--persist requires --state-file")

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
	admin             bool
	persist           bool
	stateFile         string
	persistMetaOnly   bool
	initDir           string
	shutdownTimeout   time.Duration
	out               io.Writer // banner/diagnostics sink handed to serverkit
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "cloudemu-server:", err)
		os.Exit(1)
	}
}

// run parses configuration, builds the server, and blocks until a termination
// signal (or a fatal serve error) arrives. serverkit owns the serve/shutdown
// lifecycle, so run only wires the signal context and the engine banner.
func run(args []string, stdout, stderr io.Writer) error {
	cfg, err := parseFlags(args, os.Getenv, stderr)
	if err != nil {
		return err
	}

	cfg.out = stdout

	a, err := newApp(&cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	printEngines(&cfg, stdout)

	// serverkit binds the listeners, prints the endpoint banner, and owns the
	// graceful-shutdown → persistence-snapshot → engine-teardown ordering. Serve
	// blocks until ctx is canceled (SIGINT/SIGTERM) or a listener fails fatally.
	return a.Serve(ctx)
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

	fs.BoolVar(&cfg.admin, "admin", true, "mount the /_cloudemu control plane (reset, health) for test isolation")
	fs.BoolVar(&cfg.persist, "persist", false,
		"save state to --state-file on shutdown and restore it on startup (includes object bodies)")
	fs.StringVar(&cfg.stateFile, "state-file", "", "path to the JSON state snapshot (required with --persist)")
	fs.BoolVar(&cfg.persistMetaOnly, "persist-metadata-only", false,
		"persist resource structure but omit object bodies (smaller snapshot)")
	fs.StringVar(&cfg.initDir, "init-dir", "", "apply every *.json seed fixture in this directory on startup")

	fs.DurationVar(&cfg.shutdownTimeout, "shutdown-timeout", defaultShutdownTimeout, "grace period for in-flight requests on shutdown")

	if err := fs.Parse(args); err != nil {
		return appConfig{}, err
	}

	if allReal {
		cfg.engines.applyAllReal()
	}

	if cfg.persist && cfg.stateFile == "" {
		return appConfig{}, errStateFileRequired
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

// printEngines prints the resolved real-engine selection. serverkit prints the
// endpoint banner and SDK hints itself once the listeners are bound.
func printEngines(cfg *appConfig, w io.Writer) {
	fmt.Fprintf(w, "engines: db=%s cache=%s functions=%s compute=%s containers=%s\n",
		cfg.engines.db, cfg.engines.cache, cfg.engines.functions, cfg.engines.compute, cfg.engines.containers)
}
