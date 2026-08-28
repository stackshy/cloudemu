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
	initDir           string
	enforceAuth       bool
}

// stringList is a repeatable string flag (e.g. --tls-host a --tls-host b).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var c serveConfig
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
	fs.StringVar(&c.initDir, "init-dir", "", "apply every *.json seed fixture in this directory on startup")
	fs.BoolVar(&c.enforceAuth, "enforce-auth", false,
		"require authentication on each request; off by default. AWS: verify the SigV4 signature against a registered IAM access "+
			"key (403 on failure) and enforce IAM authorization — long-term (AKIA) keys are verified, STS temporary (ASIA) "+
			"credentials are accepted unverified for now, authorization is enforced for JSON-RPC services only, and applies only to "+
			"principals that have IAM policies. Azure: validate each request's Bearer token claims (accepted audience, expiry, a "+
			"principal claim) and reject missing/malformed/expired/wrong-audience tokens with 401 — the token SIGNATURE is NOT "+
			"verified (no Azure AD signing key), so this is claims-based authentication only; RBAC authorization is a follow-up")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: cloudemu serve [flags]\n\nStart the standalone emulator. Flags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (c.tlsCert == "") != (c.tlsKey == "") {
		return errors.New("--tls-cert and --tls-key must be given together")
	}
	if c.persist && c.stateFile == "" {
		return errStateFileRequired
	}

	sel, err := parseProviders(c.providers)
	if err != nil {
		return err
	}

	cfg := serverkit.Config{
		Providers: sel,
		Host:      c.host,
		Ports: map[string]string{
			"aws":   c.awsPort,
			"azure": c.azurePort,
			"gcp":   c.gcpPort,
			"oci":   c.ociPort,
		},
		K8sPort:             c.k8sPort,
		AdvertiseHost:       c.advertiseHost,
		AzureSubscription:   c.azureSubscription,
		Admin:               c.admin,
		Persist:             c.persist,
		StateFile:           c.stateFile,
		PersistMetadataOnly: c.persistMetaOnly,
		InitDir:             c.initDir,
		TLSCert:             c.tlsCert,
		TLSKey:              c.tlsKey,
		TLSHosts:            c.tlsHosts,
		Latency:             c.latency,
		LogRequests:         c.logReqs,
		Quiet:               c.quiet,
		EnforceAuth:         c.enforceAuth,
		EndpointsFile:       c.endpoints,
		ShutdownTimeout:     c.shutdownTO,
		BaseOptions: []config.Option{
			config.WithAccountID(c.accountID),
			config.WithRegion(c.region),
			config.WithProjectID(c.projectID),
		},
		Out: os.Stdout,
	}

	app, err := serverkit.New(&cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return app.Serve(ctx)
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
