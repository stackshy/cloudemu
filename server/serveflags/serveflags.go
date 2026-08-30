// Package serveflags is the shared, dependency-light source of truth for the
// standalone emulator's command-line flags.
//
// Both serve entrypoints — the lean cmd/cloudemu binary and the
// batteries-included contrib/server (the :engines image) — register their common
// flags from here and build the same serverkit.Config, so the ~30 flags cannot
// drift between the two mains. The engine selectors are a single shared list
// (EngineFlags) both sides range over.
//
// The package imports only flag, os, strconv, strings, time, the core config
// package, and serverkit; it NEVER imports an engine constructor or a contrib package (guarded
// by a dep test), so the lean binary stays free of the heavy engine dependencies.
package serveflags

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// defaultShutdownTimeout is the grace period for in-flight requests on shutdown.
const defaultShutdownTimeout = 10 * time.Second

// defaultK8sProgressionInterval is the Pod-lifecycle ticker cadence when
// --k8s-progression is on and no interval is given.
const defaultK8sProgressionInterval = time.Second

var (
	// ErrTLSPairRequired is returned when only one of --tls-cert/--tls-key is set.
	ErrTLSPairRequired = errors.New("--tls-cert and --tls-key must be given together")
	// ErrStateFileRequired is returned when --persist is set without --state-file.
	ErrStateFileRequired = errors.New("--persist requires --state-file")
	// ErrNoProviders is returned when --providers resolves to an empty set.
	ErrNoProviders = errors.New("no providers selected")
	// ErrUnknownProvider is returned for a --providers value outside aws/azure/gcp/oci.
	ErrUnknownProvider = errors.New("unknown provider (want aws, azure, gcp, or oci)")
)

// StringList is a repeatable string flag (e.g. --tls-host a --tls-host b).
type StringList []string

func (s *StringList) String() string { return strings.Join(*s, ",") }

func (s *StringList) Set(v string) error {
	*s = append(*s, v)

	return nil
}

// CommonConfig is the full set of serve flags shared by both entrypoints. Only
// the engine selectors (EngineFlags) live outside it — those are registered by
// contrib/server and stubbed by the lean binary.
type CommonConfig struct {
	Providers     string
	Host          string
	AdvertiseHost string
	AWSPort       string
	AzurePort     string
	GCPPort       string
	OCIPort       string
	K8sPort       string

	AccountID         string
	AzureSubscription string
	Region            string
	ProjectID         string

	Latency  time.Duration
	TLSCert  string
	TLSKey   string
	TLSHosts StringList

	EndpointsFile string
	Admin         bool
	LogRequests   bool
	Quiet         bool
	EnforceAuth   bool

	ShutdownTimeout time.Duration

	Persist             bool
	StateFile           string
	PersistMetadataOnly bool
	PersistStrategy     string
	PersistInterval     time.Duration

	InitDir string

	K8sProgression         bool
	K8sProgressionInterval time.Duration
	K8sNodes               int
}

// RegisterCommon registers every common serve flag against c. getenv is injected
// (cmd passes os.Getenv; tests pass a stub) so the environment fallbacks stay
// testable. The engine selectors are registered separately by each entrypoint.
func RegisterCommon(fs *flag.FlagSet, c *CommonConfig, getenv func(string) string) {
	// OCI is not started by default: it stays opt-in until its services land, so
	// the default set never binds a port that serves nothing.
	fs.StringVar(&c.Providers, "providers", "aws,azure,gcp", "comma-separated providers to start: aws,azure,gcp,oci")
	fs.StringVar(&c.Host, "host", "127.0.0.1", "host/interface to bind (use 0.0.0.0 to expose on the network)")
	fs.StringVar(&c.AdvertiseHost, "advertise-host", "",
		"host/IP the Kubernetes data-plane endpoint is advertised at in kubeconfigs and its TLS cert "+
			"(default: --host, or 127.0.0.1 when binding all interfaces such as 0.0.0.0 under Docker)")
	fs.StringVar(&c.AWSPort, "aws-port", "4566", "port for the AWS endpoint (HTTP)")
	fs.StringVar(&c.AzurePort, "azure-port", "4568", "port for the Azure endpoint (HTTPS)")
	fs.StringVar(&c.GCPPort, "gcp-port", "4569", "port for the GCP endpoint (HTTP)")
	fs.StringVar(&c.OCIPort, "oci-port", "4571", "port for the OCI endpoint (HTTP)")
	fs.StringVar(&c.K8sPort, "k8s-port", "4570", "port for the shared Kubernetes data-plane (HTTPS); empty to disable")
	fs.StringVar(&c.AccountID, "account-id", "000000000000", "AWS account ID (also GCP/OCI) reported by the emulator")
	fs.StringVar(&c.AzureSubscription, "azure-subscription", "00000000-0000-0000-0000-000000000000",
		"Azure subscription id reported by the emulator (a GUID; real Azure SDKs/CLIs require one)")
	fs.StringVar(&c.Region, "region", "us-east-1", "default region reported by the emulator")
	fs.StringVar(&c.ProjectID, "project-id", "cloudemu-local", "GCP project ID reported by the emulator")
	fs.DurationVar(&c.Latency, "latency", 0, "artificial latency added to every emulated call (e.g. 20ms)")
	fs.StringVar(&c.TLSCert, "tls-cert", "", "PEM cert file for the Azure HTTPS endpoint (default: a self-signed cert generated in memory)")
	fs.StringVar(&c.TLSKey, "tls-key", "", "PEM key file matching --tls-cert")
	fs.Var(&c.TLSHosts, "tls-host", "extra SAN host/IP for the generated self-signed cert (repeatable)")
	fs.StringVar(&c.EndpointsFile, "endpoints-file", "", "write the resolved endpoints as JSON to this path")
	fs.BoolVar(&c.Admin, "admin", true, "mount the /_cloudemu control plane (reset, health) for test isolation")
	fs.BoolVar(&c.LogRequests, "log-requests", false, "log every HTTP request (method, path, status, duration)")
	fs.BoolVar(&c.Quiet, "quiet", false, "suppress the startup banner")
	fs.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", defaultShutdownTimeout, "grace period for in-flight requests on shutdown")
	fs.StringVar(&c.InitDir, "init-dir", "", "apply every *.json seed fixture in this directory on startup")

	registerPersistFlags(fs, c, getenv)
	registerK8sProgressionFlags(fs, c, getenv)
	registerEnforceAuthFlag(fs, c)
}

// registerPersistFlags registers the persistence flag group, whose knobs only
// matter with --persist.
func registerPersistFlags(fs *flag.FlagSet, c *CommonConfig, getenv func(string) string) {
	fs.BoolVar(&c.Persist, "persist", false, "save state to --state-file on shutdown and restore it on startup (includes object bodies)")
	fs.StringVar(&c.StateFile, "state-file", "", "path to the JSON state snapshot (required with --persist)")
	fs.BoolVar(&c.PersistMetadataOnly, "persist-metadata-only", false, "persist resource structure but omit object bodies (smaller snapshot)")
	fs.StringVar(&c.PersistStrategy, "persist-strategy", envStrOr(getenv, "CLOUDEMU_PERSIST_STRATEGY", serverkit.DefaultPersistStrategy),
		"when to save with --persist: scheduled|on-request|on-shutdown|manual (env CLOUDEMU_PERSIST_STRATEGY)")
	fs.DurationVar(&c.PersistInterval, "persist-interval",
		envDurationOr(getenv, "CLOUDEMU_PERSIST_INTERVAL", serverkit.DefaultPersistInterval),
		"save cadence for --persist-strategy=scheduled (env CLOUDEMU_PERSIST_INTERVAL)")
}

// registerK8sProgressionFlags registers the KWOK-style staged Pod lifecycle knobs.
func registerK8sProgressionFlags(fs *flag.FlagSet, c *CommonConfig, getenv func(string) string) {
	fs.BoolVar(&c.K8sProgression, "k8s-progression", envBoolOr(getenv, "CLOUDEMU_K8S_PROGRESSION", false),
		"Kubernetes: client-created Pods start Pending and visibly progress to Running on a ticker "+
			"(default off = instant Running; env CLOUDEMU_K8S_PROGRESSION)")
	fs.DurationVar(&c.K8sProgressionInterval, "k8s-progression-interval",
		envDurationOr(getenv, "CLOUDEMU_K8S_PROGRESSION_INTERVAL", defaultK8sProgressionInterval),
		"cadence of the --k8s-progression Pod-lifecycle ticker (env CLOUDEMU_K8S_PROGRESSION_INTERVAL)")
	fs.IntVar(&c.K8sNodes, "k8s-nodes", envIntOr(getenv, "CLOUDEMU_K8S_NODES", 1),
		"Kubernetes: number of synthetic nodes each cluster seeds, fixed at creation (default 1 = single node; "+
			">1 seeds a tainted control-plane node plus workers and turns on the first-fit scheduler with "+
			"nodeSelector/taints/resource-request placement; env CLOUDEMU_K8S_NODES)")
}

// registerEnforceAuthFlag registers --enforce-auth with the full SigV4/Bearer
// usage. This is the single shared usage string for both entrypoints.
func registerEnforceAuthFlag(fs *flag.FlagSet, c *CommonConfig) {
	fs.BoolVar(&c.EnforceAuth, "enforce-auth", false,
		"require authentication on each request; off by default. AWS: verify the SigV4 signature against a registered IAM access "+
			"key (403 on failure) and enforce IAM authorization — long-term (AKIA) keys are verified, STS temporary (ASIA) "+
			"credentials are accepted unverified for now, authorization is enforced for JSON-RPC services only, and applies only to "+
			"principals that have IAM policies. Azure: validate each request's Bearer token claims (accepted audience, expiry, a "+
			"principal claim) and reject missing/malformed/expired/wrong-audience tokens with 401 — the token SIGNATURE is NOT "+
			"verified (no Azure AD signing key), so this is claims-based authentication only; RBAC authorization is a follow-up")
}

// Validate checks the cross-field constraints both entrypoints share: --tls-cert
// and --tls-key must be given together, and --persist requires --state-file.
func (c *CommonConfig) Validate() error {
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return ErrTLSPairRequired
	}

	if c.Persist && c.StateFile == "" {
		return ErrStateFileRequired
	}

	return nil
}

// ToServerkitConfig maps the resolved common flags onto a serverkit.Config for
// the given (already parsed) provider selection. The three identity options
// (account/region/project) go into BaseOptions here; contrib appends its engine
// options to BaseOptions afterwards.
func (c *CommonConfig) ToServerkitConfig(providers []string) serverkit.Config {
	return serverkit.Config{
		Providers: providers,
		Host:      c.Host,
		Ports: map[string]string{
			"aws":   c.AWSPort,
			"azure": c.AzurePort,
			"gcp":   c.GCPPort,
			"oci":   c.OCIPort,
		},
		K8sPort:                c.K8sPort,
		AdvertiseHost:          c.AdvertiseHost,
		K8sProgression:         c.K8sProgression,
		K8sProgressionInterval: c.K8sProgressionInterval,
		K8sNodes:               c.K8sNodes,
		AzureSubscription:      c.AzureSubscription,
		Admin:                  c.Admin,
		Persist:                c.Persist,
		StateFile:              c.StateFile,
		PersistMetadataOnly:    c.PersistMetadataOnly,
		PersistStrategy:        c.PersistStrategy,
		PersistInterval:        c.PersistInterval,
		InitDir:                c.InitDir,
		TLSCert:                c.TLSCert,
		TLSKey:                 c.TLSKey,
		TLSHosts:               c.TLSHosts,
		Latency:                c.Latency,
		LogRequests:            c.LogRequests,
		Quiet:                  c.Quiet,
		EnforceAuth:            c.EnforceAuth,
		EndpointsFile:          c.EndpointsFile,
		ShutdownTimeout:        c.ShutdownTimeout,
		BaseOptions: []config.Option{
			config.WithAccountID(c.AccountID),
			config.WithRegion(c.Region),
			config.WithProjectID(c.ProjectID),
		},
		Out: os.Stdout,
	}
}

// ParseProviders converts the --providers flag into a de-duplicated, validated
// provider list. Both entrypoints use it so they accept the same values.
func ParseProviders(s string) ([]string, error) {
	seen := map[string]bool{}

	var out []string

	for _, raw := range strings.Split(s, ",") {
		p := strings.TrimSpace(strings.ToLower(raw))
		if p == "" {
			continue
		}

		if p != "aws" && p != "azure" && p != "gcp" && p != "oci" {
			return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, p)
		}

		if !seen[p] {
			seen[p] = true

			out = append(out, p)
		}
	}

	if len(out) == 0 {
		return nil, ErrNoProviders
	}

	return out, nil
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

// envIntOr reads the environment value for key as a base-10 integer, or returns
// def when it is unset/empty/unparseable.
func envIntOr(getenv func(string) string, key string, def int) int {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return def
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}

	return n
}

// envBoolOr reads the environment value for key as a boolean (1/true/yes/on,
// case-insensitive), or returns def when it is unset/empty/unrecognized.
func envBoolOr(getenv func(string) string, key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
