// Command cloudemu-server runs the CloudEmu standalone wire-protocol server with
// real data-plane engines wired in — the batteries-included variant of
// `cloudemu serve`. The core `cloudemu` module stays dependency-free, so the
// real engines live in contrib and are composed here in their own module.
//
// Assembly (provider construction, listener binding, the shutdown/snapshot
// lifecycle, engine teardown) is delegated to the shared server/serverkit
// package, so this binary and `cloudemu serve` don't drift; this module only adds
// the real-engine selection and its startup MODE banner. Flag names and defaults
// mirror `cloudemu serve` for parity.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// defaultShutdownTimeout is the grace period for in-flight requests.
const defaultShutdownTimeout = 10 * time.Second

var (
	// errStateFileRequired is returned when --persist is set without --state-file.
	errStateFileRequired = errors.New("--persist requires --state-file")
	// errTLSPairRequired is returned when only one of --tls-cert/--tls-key is set.
	errTLSPairRequired = errors.New("--tls-cert and --tls-key must be given together")
	// errNoProviders is returned when --providers resolves to an empty set.
	errNoProviders = errors.New("no providers selected")
	// errUnknownProvider is returned for a --providers value outside aws/azure/gcp/oci.
	errUnknownProvider = errors.New("unknown provider (want aws, azure, gcp, or oci)")
)

// stringList is a repeatable string flag (e.g. --tls-host a --tls-host b).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)

	return nil
}

// appConfig is the resolved flag/env configuration for one server run.
type appConfig struct {
	engines engineSelection

	providers     []string // parsed provider set: aws,azure,gcp,oci
	host          string
	advertiseHost string
	awsPort       string
	azurePort     string
	gcpPort       string
	ociPort       string
	k8sPort       string

	accountID         string
	azureSubscription string
	region            string
	projectID         string

	latency  time.Duration
	tlsCert  string
	tlsKey   string
	tlsHosts stringList

	admin           bool
	logRequests     bool
	quiet           bool
	enforceAuth     bool
	persist         bool
	stateFile       string
	persistMetaOnly bool
	persistStrategy string
	persistInterval time.Duration
	initDir         string
	endpointsFile   string

	shutdownTimeout time.Duration
	out             io.Writer // banner/diagnostics sink handed to serverkit
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "cloudemu-server:", err)
		os.Exit(1)
	}
}

// run parses configuration, builds the server, prints the engine MODE banner,
// and blocks until a termination signal (or a fatal serve error) arrives.
// serverkit owns the serve/shutdown lifecycle and the endpoint banner.
func run(args []string, stdout, stderr io.Writer) error {
	cfg, err := parseFlags(args, os.Getenv, stderr)
	if err != nil {
		return err
	}

	cfg.out = stdout

	opts, modes, err := buildOptions(&cfg, dockerAvailable)
	if err != nil {
		return err
	}

	a, err := newAppFromOptions(&cfg, opts)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The MODE banner prints before serverkit's endpoint banner: serverkit prints
	// its banner from inside Serve (which then blocks), so this is the last chance
	// to emit synchronously. Degrade warnings always go to stderr; the table is
	// suppressed under --quiet.
	warnDegraded(stderr, modes)

	if !cfg.quiet {
		printEngineModes(stdout, modes)
	}

	// serverkit binds the listeners, prints the endpoint banner, and owns the
	// graceful-shutdown → persistence-snapshot → engine-teardown ordering. Serve
	// blocks until ctx is canceled (SIGINT/SIGTERM) or a listener fails fatally.
	return a.Serve(ctx)
}

// parseFlags resolves the configuration from args, with environment-variable
// fallbacks for the engine selectors. Flag names/defaults mirror `cloudemu serve`.
func parseFlags(args []string, getenv func(string) string, out io.Writer) (appConfig, error) {
	fs := flag.NewFlagSet("cloudemu-server", flag.ContinueOnError)
	fs.SetOutput(out)

	var (
		cfg       appConfig
		allReal   bool
		providers string
	)

	fs.StringVar(&cfg.engines.db, "db", engineEnvOr(getenv, "CLOUDEMU_DB"), "database engine: off|postgres|mysql|both")
	fs.StringVar(&cfg.engines.cache, "cache", engineEnvOr(getenv, "CLOUDEMU_CACHE"), "cache engine: off|redis")
	fs.StringVar(&cfg.engines.functions, "functions", engineEnvOr(getenv, "CLOUDEMU_FUNCTIONS"), "function engine: off|subprocess")
	fs.StringVar(&cfg.engines.compute, "compute", engineEnvOr(getenv, "CLOUDEMU_COMPUTE"), "compute engine: off|docker")
	fs.StringVar(&cfg.engines.containers, "containers", engineEnvOr(getenv, "CLOUDEMU_CONTAINERS"), "container engine: off|docker")
	fs.StringVar(&cfg.engines.storage, "storage", engineEnvOr(getenv, "CLOUDEMU_STORAGE"), "storage engine: off|localfs")
	fs.StringVar(&cfg.engines.storageDir, "storage-dir", "",
		"root directory for --storage=localfs (default: a temporary directory)")
	fs.BoolVar(&allReal, "all-real", false,
		"shorthand: postgres + redis + subprocess + docker compute + docker containers + localfs storage")

	fs.StringVar(&providers, "providers", "aws,azure,gcp", "comma-separated providers to start: aws,azure,gcp,oci")
	fs.StringVar(&cfg.host, "host", "127.0.0.1", "host/interface to bind (0.0.0.0 exposes on the network)")
	fs.StringVar(&cfg.advertiseHost, "advertise-host", "",
		"host/IP the Kubernetes data-plane endpoint is advertised at in kubeconfigs and its TLS cert "+
			"(default: --host, or 127.0.0.1 when binding all interfaces such as 0.0.0.0 under Docker)")
	fs.StringVar(&cfg.awsPort, "aws-port", "4566", "port for the AWS endpoint (HTTP)")
	fs.StringVar(&cfg.azurePort, "azure-port", "4568", "port for the Azure endpoint (HTTPS)")
	fs.StringVar(&cfg.gcpPort, "gcp-port", "4569", "port for the GCP endpoint (HTTP)")
	fs.StringVar(&cfg.ociPort, "oci-port", "4571", "port for the OCI endpoint (HTTP)")
	fs.StringVar(&cfg.k8sPort, "k8s-port", "4570", "port for the shared Kubernetes data-plane (HTTPS); empty to disable")
	fs.StringVar(&cfg.accountID, "account-id", "000000000000", "AWS account ID (also GCP/OCI) reported by the emulator")
	fs.StringVar(&cfg.azureSubscription, "azure-subscription", "00000000-0000-0000-0000-000000000000",
		"Azure subscription id reported by the emulator (a GUID)")
	fs.StringVar(&cfg.region, "region", "us-east-1", "default region reported by the emulator")
	fs.StringVar(&cfg.projectID, "project-id", "cloudemu-local", "GCP project ID reported by the emulator")

	fs.DurationVar(&cfg.latency, "latency", 0, "artificial latency added to every emulated call (e.g. 20ms)")
	fs.StringVar(&cfg.tlsCert, "tls-cert", "",
		"PEM cert file for the Azure HTTPS endpoint (default: a self-signed cert generated in memory)")
	fs.StringVar(&cfg.tlsKey, "tls-key", "", "PEM key file matching --tls-cert")
	fs.Var(&cfg.tlsHosts, "tls-host", "extra SAN host/IP for the generated self-signed cert (repeatable)")

	fs.BoolVar(&cfg.admin, "admin", true, "mount the /_cloudemu control plane (reset, health) for test isolation")
	fs.BoolVar(&cfg.logRequests, "log-requests", false, "log every HTTP request (method, path, status, duration)")
	fs.BoolVar(&cfg.quiet, "quiet", false, "suppress the startup banner")
	fs.BoolVar(&cfg.enforceAuth, "enforce-auth", false, "require authentication on each request; off by default")
	registerPersistFlags(fs, &cfg, getenv)
	fs.StringVar(&cfg.initDir, "init-dir", "", "apply every *.json seed fixture in this directory on startup")
	fs.StringVar(&cfg.endpointsFile, "endpoints-file", "", "write the resolved endpoints as JSON to this path")

	fs.DurationVar(&cfg.shutdownTimeout, "shutdown-timeout", defaultShutdownTimeout,
		"grace period for in-flight requests on shutdown")

	if err := fs.Parse(args); err != nil {
		return appConfig{}, err
	}

	if allReal {
		cfg.engines.applyAllReal()
	}

	sel, err := parseProviders(providers)
	if err != nil {
		return appConfig{}, err
	}

	cfg.providers = sel

	if (cfg.tlsCert == "") != (cfg.tlsKey == "") {
		return appConfig{}, errTLSPairRequired
	}

	if cfg.persist && cfg.stateFile == "" {
		return appConfig{}, errStateFileRequired
	}

	warnPersistFlagsWithoutPersist(fs, &cfg, out)

	return cfg, nil
}

// parseProviders converts the --providers flag into a de-duplicated, validated
// provider list. It mirrors `cloudemu serve`'s parse so both entrypoints accept
// the same values (aws, azure, gcp, oci).
func parseProviders(s string) ([]string, error) {
	seen := map[string]bool{}

	var out []string

	for _, raw := range strings.Split(s, ",") {
		p := strings.TrimSpace(strings.ToLower(raw))
		if p == "" {
			continue
		}

		if p != providerAWS && p != providerAzure && p != providerGCP && p != providerOCI {
			return nil, fmt.Errorf("%w: %q", errUnknownProvider, p)
		}

		if !seen[p] {
			seen[p] = true

			out = append(out, p)
		}
	}

	if len(out) == 0 {
		return nil, errNoProviders
	}

	return out, nil
}

// engineEnvOr returns the engine value from the environment for key, defaulting
// to "off" (in-memory) when it is unset/empty.
func engineEnvOr(getenv func(string) string, key string) string {
	if v := getenv(key); v != "" {
		return v
	}

	return engineOff
}

// registerPersistFlags registers the persistence flag group against cfg, keeping
// parseFlags under the statement limit and grouping the knobs that only matter
// with --persist.
func registerPersistFlags(fs *flag.FlagSet, cfg *appConfig, getenv func(string) string) {
	fs.BoolVar(&cfg.persist, "persist", false,
		"save state to --state-file on shutdown and restore it on startup (includes object bodies)")
	fs.StringVar(&cfg.stateFile, "state-file", "", "path to the JSON state snapshot (required with --persist)")
	fs.BoolVar(&cfg.persistMetaOnly, "persist-metadata-only", false,
		"persist resource structure but omit object bodies (smaller snapshot)")
	fs.StringVar(&cfg.persistStrategy, "persist-strategy",
		envStrOr(getenv, "CLOUDEMU_PERSIST_STRATEGY", serverkit.DefaultPersistStrategy),
		"when to save with --persist: scheduled|on-request|on-shutdown|manual (env CLOUDEMU_PERSIST_STRATEGY)")
	fs.DurationVar(&cfg.persistInterval, "persist-interval",
		envDurationOr(getenv, "CLOUDEMU_PERSIST_INTERVAL", serverkit.DefaultPersistInterval),
		"save cadence for --persist-strategy=scheduled (env CLOUDEMU_PERSIST_INTERVAL)")
}

// envStrOr returns the environment value for key, or def when it is unset/empty.
func envStrOr(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}

	return def
}

// envDurationOr parses the environment value for key as a duration, or returns
// def when it is unset/empty/unparseable.
func envDurationOr(getenv func(string) string, key string, def time.Duration) time.Duration {
	v := getenv(key)
	if v == "" {
		return def
	}

	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}

	return d
}

// warnPersistFlagsWithoutPersist prints a warning for each persist-tuning flag
// set explicitly while --persist is off, so the ignored knob is visible.
func warnPersistFlagsWithoutPersist(fs *flag.FlagSet, cfg *appConfig, stderr io.Writer) {
	if cfg.persist {
		return
	}

	set := map[string]bool{}

	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	for _, name := range []string{"persist-strategy", "persist-interval", "persist-metadata-only"} {
		if set[name] {
			fmt.Fprintf(stderr, "warning: --%s has no effect without --persist\n", name)
		}
	}
}

// printEngineModes prints the resolved per-capability engine MODE table.
// serverkit prints the endpoint banner and SDK hints itself once the listeners
// are bound.
func printEngineModes(w io.Writer, modes []engineMode) {
	fmt.Fprintln(w, "engines:")

	for _, m := range modes {
		line := fmt.Sprintf("  %-11s %s", m.capability, m.status)
		if m.detail != "" {
			line += fmt.Sprintf(" (%s)", m.detail)
		}

		fmt.Fprintln(w, line)
	}
}

// warnDegraded emits one stderr line per capability that fell back to in-memory,
// so the degrade is visible even under --quiet (which suppresses the table).
func warnDegraded(stderr io.Writer, modes []engineMode) {
	for _, m := range modes {
		if m.status == modeMemory {
			fmt.Fprintf(stderr, "warning: %s engine fell back to in-memory (%s)\n", m.capability, m.detail)
		}
	}
}
