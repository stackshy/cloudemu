package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// errStateFileRequired is returned when --persist is set without --state-file.
var errStateFileRequired = errors.New("--persist requires --state-file")

// errEnginesNotInLeanBinary is returned when engine flags/env are passed to the
// lean cloudemu binary, which deliberately does not compile the real engines
// (postgres/redis/subprocess/docker/localfs) — those heavy deps live only in the
// :engines image (contrib/server). Rather than silently ignore the intent, serve
// points the user at the batteries image.
var errEnginesNotInLeanBinary = errors.New(
	"real engines (postgres/redis/subprocess/docker/localfs) aren't compiled into the lean cloudemu binary.\n" +
		"run the batteries image:  docker run -p 4566:4566 ghcr.io/stackshy/cloudemu:engines --all-real\n" +
		"(or build ./contrib/server from a repo checkout). see https://cloudemu.info/docs/standalone-server")

// engineFlag names a real-engine selector understood by the :engines image
// (contrib/server) but only stubbed in the lean binary. Env is its environment
// form ("" when it has none). This single iterable list is the source of truth
// PR-2 will promote into server/serveflags; both the stub registration and the
// detector range over it, so a new engine capability is one line here.
type engineFlag struct{ Name, Env string }

// engineAllRealFlag is the one boolean engine flag; every other engine flag
// takes a string value.
const engineAllRealFlag = "all-real"

//nolint:gochecknoglobals // single source of truth for engine flag names/envs; promoted to server/serveflags in PR-2
var engineFlags = []engineFlag{
	{"db", "CLOUDEMU_DB"},
	{"cache", "CLOUDEMU_CACHE"},
	{"functions", "CLOUDEMU_FUNCTIONS"},
	{"compute", "CLOUDEMU_COMPUTE"},
	{"containers", "CLOUDEMU_CONTAINERS"},
	{"storage", "CLOUDEMU_STORAGE"},
	{"storage-dir", ""},
	{engineAllRealFlag, ""},
}

// serveConfig holds the resolved serve flags.
type serveConfig struct {
	providers         string
	host              string
	advertiseHost     string
	awsPort           string
	azurePort         string
	gcpPort           string
	ociPort           string
	k8sPort           string
	accountID         string
	azureSubscription string
	region            string
	projectID         string
	latency           time.Duration
	tlsCert           string
	tlsKey            string
	tlsHosts          stringList
	endpoints         string
	admin             bool
	logReqs           bool
	quiet             bool
	shutdownTO        time.Duration
	persist           bool
	stateFile         string
	persistMetaOnly   bool
	persistStrategy   string
	persistInterval   time.Duration
	initDir           string
	enforceAuth       bool
	k8sProgression    bool
	k8sProgInterval   time.Duration
}

// stringList is a repeatable string flag (e.g. --tls-host a --tls-host b).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func runServe(args []string) error {
	var c serveConfig

	fs := newServeFlagSet(&c)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// The lean binary can't run real engines; point the user at the :engines
	// image before binding listeners or building providers.
	if enginesRequested(fs, os.Getenv) {
		return errEnginesNotInLeanBinary
	}

	if (c.tlsCert == "") != (c.tlsKey == "") {
		return errors.New("--tls-cert and --tls-key must be given together")
	}

	if c.persist && c.stateFile == "" {
		return errStateFileRequired
	}

	warnPersistFlagsWithoutPersist(fs, &c)

	sel, err := parseProviders(c.providers)
	if err != nil {
		return err
	}

	cfg := c.toServerkitConfig(sel)

	app, err := serverkit.New(&cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return app.Serve(ctx)
}

// newServeFlagSet registers every serve flag against c and returns the flag set.
// Splitting registration out of runServe lets tests inspect flag names/defaults
// (the cross-entrypoint parity guard) without executing a server.
func newServeFlagSet(c *serveConfig) *flag.FlagSet {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	registerRealServeFlags(fs, c)
	// Register the engine flags as inert stubs so the REAL parser handles every
	// syntax (--db=x, --db x, -db) and an engine name that appears as another
	// flag's value stays a value. runServe detects intent via fs.Visit.
	registerEngineStubs(fs)

	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "Usage: cloudemu serve [flags]\n\nStart the standalone emulator. Flags:\n")
		// Hide the engine stubs from --help: render from a real-only clone so the
		// output matches stdlib's exact formatting without the engine flags, then
		// point at the :engines image in a footer.
		realOnly := flag.NewFlagSet("serve", flag.ContinueOnError)
		realOnly.SetOutput(out)
		registerRealServeFlags(realOnly, &serveConfig{})
		realOnly.PrintDefaults()
		fmt.Fprintf(out, "\nReal engines (postgres/redis/subprocess/docker/localfs) live in the "+
			"cloudemu:engines image, not this lean binary:\n"+
			"  docker run -p 4566:4566 ghcr.io/stackshy/cloudemu:engines --all-real\n")
	}

	return fs
}

// registerEngineStubs registers each engine flag as an inert stub whose target is
// discarded. Their only purpose is to let the flag tokenizer accept the flag in
// any form; detection is done afterwards via fs.Visit, never these values.
func registerEngineStubs(fs *flag.FlagSet) {
	for _, f := range engineFlags {
		if f.Name == engineAllRealFlag {
			_ = fs.Bool(f.Name, false, "")

			continue
		}

		_ = fs.String(f.Name, "", "")
	}
}

// isEngineFlag reports whether name is one of the real-engine selector flags.
func isEngineFlag(name string) bool {
	for _, f := range engineFlags {
		if f.Name == name {
			return true
		}
	}

	return false
}

// enginesRequested reports whether the user asked for a real engine — either by
// setting an engine flag on the command line (detected via fs.Visit after a real
// parse, so an engine name that is another flag's value does not count) or via a
// non-empty engine env var.
func enginesRequested(fs *flag.FlagSet, getenv func(string) string) bool {
	set := false

	fs.Visit(func(f *flag.Flag) {
		if isEngineFlag(f.Name) {
			set = true
		}
	})

	if set {
		return true
	}

	for _, f := range engineFlags {
		if f.Env != "" && getenv(f.Env) != "" {
			return true
		}
	}

	return false
}

// registerRealServeFlags registers every real serve flag against c. It is called
// both for the live flag set and (with a throwaway config) to render --help
// without the engine stubs.
func registerRealServeFlags(fs *flag.FlagSet, c *serveConfig) {
	// OCI is not started by default: it stays opt-in until its services land, so
	// the default set never binds a port that serves nothing.
	fs.StringVar(&c.providers, "providers", "aws,azure,gcp", "comma-separated providers to start: aws,azure,gcp,oci")
	fs.StringVar(&c.host, "host", "127.0.0.1", "host/interface to bind (use 0.0.0.0 to expose on the network)")
	fs.StringVar(&c.advertiseHost, "advertise-host", "",
		"host/IP the Kubernetes data-plane endpoint is advertised at in kubeconfigs and its TLS cert "+
			"(default: --host, or 127.0.0.1 when binding all interfaces such as 0.0.0.0 under Docker)")
	fs.StringVar(&c.awsPort, "aws-port", "4566", "port for the AWS endpoint (HTTP)")
	fs.StringVar(&c.azurePort, "azure-port", "4568", "port for the Azure endpoint (HTTPS)")
	fs.StringVar(&c.gcpPort, "gcp-port", "4569", "port for the GCP endpoint (HTTP)")
	fs.StringVar(&c.ociPort, "oci-port", "4571", "port for the OCI endpoint (HTTP)")
	fs.StringVar(&c.k8sPort, "k8s-port", "4570", "port for the shared Kubernetes data-plane (HTTPS); empty to disable")
	fs.StringVar(&c.accountID, "account-id", "000000000000", "AWS account ID (also GCP/OCI) reported by the emulator")
	fs.StringVar(&c.azureSubscription, "azure-subscription", "00000000-0000-0000-0000-000000000000",
		"Azure subscription id reported by the emulator (a GUID; real Azure SDKs/CLIs require one)")
	fs.StringVar(&c.region, "region", "us-east-1", "default region reported by the emulator")
	fs.StringVar(&c.projectID, "project-id", "cloudemu-local", "GCP project ID reported by the emulator")
	fs.DurationVar(&c.latency, "latency", 0, "artificial latency added to every emulated call (e.g. 20ms)")
	fs.StringVar(&c.tlsCert, "tls-cert", "", "PEM cert file for the Azure HTTPS endpoint (default: a self-signed cert generated in memory)")
	fs.StringVar(&c.tlsKey, "tls-key", "", "PEM key file matching --tls-cert")
	fs.Var(&c.tlsHosts, "tls-host", "extra SAN host/IP for the generated self-signed cert (repeatable)")
	fs.StringVar(&c.endpoints, "endpoints-file", "", "write the resolved endpoints as JSON to this path")
	fs.BoolVar(&c.admin, "admin", true, "mount the /_cloudemu control plane (reset, health) for test isolation")
	fs.BoolVar(&c.logReqs, "log-requests", false, "log every HTTP request (method, path, status, duration)")
	fs.BoolVar(&c.quiet, "quiet", false, "suppress the startup banner")
	fs.DurationVar(&c.shutdownTO, "shutdown-timeout", 10*time.Second, "grace period for in-flight requests on shutdown")
	fs.BoolVar(&c.persist, "persist", false, "save state to --state-file on shutdown and restore it on startup (includes object bodies)")
	fs.StringVar(&c.stateFile, "state-file", "", "path to the JSON state snapshot (required with --persist)")
	fs.BoolVar(&c.persistMetaOnly, "persist-metadata-only", false, "persist resource structure but omit object bodies (smaller snapshot)")
	fs.StringVar(&c.persistStrategy, "persist-strategy", envOr("CLOUDEMU_PERSIST_STRATEGY", serverkit.DefaultPersistStrategy),
		"when to save with --persist: scheduled|on-request|on-shutdown|manual (env CLOUDEMU_PERSIST_STRATEGY)")
	fs.DurationVar(&c.persistInterval, "persist-interval",
		envDurationOr("CLOUDEMU_PERSIST_INTERVAL", serverkit.DefaultPersistInterval),
		"save cadence for --persist-strategy=scheduled (env CLOUDEMU_PERSIST_INTERVAL)")
	fs.StringVar(&c.initDir, "init-dir", "", "apply every *.json seed fixture in this directory on startup")
	fs.BoolVar(&c.k8sProgression, "k8s-progression", envBoolOr("CLOUDEMU_K8S_PROGRESSION", false),
		"Kubernetes: client-created Pods start Pending and visibly progress to Running on a ticker "+
			"(default off = instant Running; env CLOUDEMU_K8S_PROGRESSION)")
	fs.DurationVar(&c.k8sProgInterval, "k8s-progression-interval",
		envDurationOr("CLOUDEMU_K8S_PROGRESSION_INTERVAL", time.Second),
		"cadence of the --k8s-progression Pod-lifecycle ticker (env CLOUDEMU_K8S_PROGRESSION_INTERVAL)")
	fs.BoolVar(&c.enforceAuth, "enforce-auth", false,
		"require authentication on each request; off by default. AWS: verify the SigV4 signature against a registered IAM access "+
			"key (403 on failure) and enforce IAM authorization — long-term (AKIA) keys are verified, STS temporary (ASIA) "+
			"credentials are accepted unverified for now, authorization is enforced for JSON-RPC services only, and applies only to "+
			"principals that have IAM policies. Azure: validate each request's Bearer token claims (accepted audience, expiry, a "+
			"principal claim) and reject missing/malformed/expired/wrong-audience tokens with 401 — the token SIGNATURE is NOT "+
			"verified (no Azure AD signing key), so this is claims-based authentication only; RBAC authorization is a follow-up")
}

// toServerkitConfig maps the resolved serve flags onto a serverkit.Config for
// the given provider selection.
func (c *serveConfig) toServerkitConfig(sel []string) serverkit.Config {
	return serverkit.Config{
		Providers: sel,
		Host:      c.host,
		Ports: map[string]string{
			"aws":   c.awsPort,
			"azure": c.azurePort,
			"gcp":   c.gcpPort,
			"oci":   c.ociPort,
		},
		K8sPort:                c.k8sPort,
		AdvertiseHost:          c.advertiseHost,
		K8sProgression:         c.k8sProgression,
		K8sProgressionInterval: c.k8sProgInterval,
		AzureSubscription:      c.azureSubscription,
		Admin:                  c.admin,
		Persist:                c.persist,
		StateFile:              c.stateFile,
		PersistMetadataOnly:    c.persistMetaOnly,
		PersistStrategy:        c.persistStrategy,
		PersistInterval:        c.persistInterval,
		InitDir:                c.initDir,
		TLSCert:                c.tlsCert,
		TLSKey:                 c.tlsKey,
		TLSHosts:               c.tlsHosts,
		Latency:                c.latency,
		LogRequests:            c.logReqs,
		Quiet:                  c.quiet,
		EnforceAuth:            c.enforceAuth,
		EndpointsFile:          c.endpoints,
		ShutdownTimeout:        c.shutdownTO,
		BaseOptions: []config.Option{
			config.WithAccountID(c.accountID),
			config.WithRegion(c.region),
			config.WithProjectID(c.projectID),
		},
		Out: os.Stdout,
	}
}

// warnPersistFlagsWithoutPersist prints a one-line warning when a persist-tuning
// flag was set explicitly but --persist is off, so the ignored knob is visible.
func warnPersistFlagsWithoutPersist(fs *flag.FlagSet, c *serveConfig) {
	if c.persist {
		return
	}

	set := map[string]bool{}

	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	for _, name := range []string{"persist-strategy", "persist-interval", "persist-metadata-only"} {
		if set[name] {
			fmt.Fprintf(os.Stderr, "warning: --%s has no effect without --persist\n", name)
		}
	}
}

// envOr returns the environment value for key, or def when it is unset/empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return def
}

// envDurationOr parses the environment value for key as a duration, or returns
// def when it is unset/empty/unparseable.
func envDurationOr(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}

	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}

	return d
}

// envBoolOr reads the environment value for key as a boolean (1/true/yes/on,
// case-insensitive), or returns def when it is unset/empty/unrecognized.
func envBoolOr(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func parseProviders(s string) ([]string, error) {
	seen := map[string]bool{}

	var out []string

	for _, raw := range strings.Split(s, ",") {
		p := strings.TrimSpace(strings.ToLower(raw))
		if p == "" {
			continue
		}

		if p != "aws" && p != "azure" && p != "gcp" && p != "oci" {
			return nil, fmt.Errorf("unknown provider %q (want aws, azure, gcp, or oci)", p)
		}

		if !seen[p] {
			seen[p] = true

			out = append(out, p)
		}
	}

	if len(out) == 0 {
		return nil, errors.New("no providers selected")
	}

	return out, nil
}
