package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	eksprov "github.com/stackshy/cloudemu/v2/providers/aws/eks"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/seed"
	"github.com/stackshy/cloudemu/v2/server/admin"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// serveConfig holds the resolved serve flags.
type serveConfig struct {
	providers  string
	host       string
	awsPort    string
	azurePort  string
	gcpPort    string
	k8sPort    string
	accountID  string
	region     string
	projectID  string
	latency    time.Duration
	tlsCert    string
	tlsKey     string
	tlsHosts   stringList
	endpoints  string
	admin      bool
	logReqs    bool
	quiet      bool
	shutdownTO time.Duration
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
	fs.StringVar(&c.providers, "providers", "aws,azure,gcp", "comma-separated providers to start: aws,azure,gcp")
	fs.StringVar(&c.host, "host", "127.0.0.1", "host/interface to bind (use 0.0.0.0 to expose on the network)")
	fs.StringVar(&c.awsPort, "aws-port", "4566", "port for the AWS endpoint (HTTP)")
	fs.StringVar(&c.azurePort, "azure-port", "4568", "port for the Azure endpoint (HTTPS)")
	fs.StringVar(&c.gcpPort, "gcp-port", "4569", "port for the GCP endpoint (HTTP)")
	fs.StringVar(&c.k8sPort, "k8s-port", "4570", "port for the shared Kubernetes data-plane (HTTPS); empty to disable")
	fs.StringVar(&c.accountID, "account-id", "000000000000", "AWS account ID / Azure subscription ID reported by the emulator")
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

	sel, err := parseProviders(c.providers)
	if err != nil {
		return err
	}

	opts := []config.Option{
		config.WithAccountID(c.accountID),
		config.WithRegion(c.region),
		config.WithProjectID(c.projectID),
	}
	if c.latency > 0 {
		opts = append(opts, config.WithLatency(c.latency))
	}

	var (
		servers []*namedServer
		eps     = endpointSet{}
	)

	// Each provider (and the shared Kubernetes data-plane) sits behind a
	// hot-swappable backend. rebuild reconstructs a fresh Kubernetes server and
	// every selected provider from scratch and swaps them in atomically — this
	// is what /_cloudemu/reset calls to hand a test suite a clean slate without
	// restarting the process.
	backends := make(map[string]*admin.Backend, len(sel))
	for _, p := range sel {
		backends[p] = admin.NewBackend(nil)
	}
	var k8sBackend *admin.Backend
	if c.k8sPort != "" {
		k8sBackend = admin.NewBackend(nil)
	}

	var (
		rebuildMu sync.Mutex
		targets   map[string]seed.Target // provider → current drivers, for seeding
	)
	rebuild := func() {
		// Serialise resets so two concurrent /_cloudemu/reset calls can't
		// interleave and leave providers wired to different Kubernetes
		// instances.
		rebuildMu.Lock()
		defer rebuildMu.Unlock()

		// Build every fresh handler first, then swap them in. Building first
		// means a construction panic leaves the running set untouched, and all
		// providers in one reset share the single new Kubernetes data-plane
		// (so a kubeconfig from any control plane reaches the same backend).
		var k8s *kubernetes.APIServer
		if k8sBackend != nil {
			k8s = kubernetes.NewAPIServer()
			// Tell the data plane the address it is reachable on, so the
			// managed-Kubernetes control planes can advertise an endpoint that
			// actually answers. Without this BaseURL() is "", every
			// withK8sEndpoint bails, and DescribeCluster / GetCluster hand back
			// the Wave 1 sentinel — which a real client-go tool cannot resolve.
			//
			// The data-plane listener binds c.host:c.k8sPort below; this must
			// stay in step with it.
			// https, because the listener below is served with a certificate
			// signed by the CA DescribeCluster advertises. A caller building a
			// rest.Config from Endpoint plus CertificateAuthority can then
			// validate what it dials; over plain HTTP that CA certifies
			// nothing and the handshake fails.
			k8s.SetBaseURL("https://" + net.JoinHostPort(c.host, c.k8sPort))
		}
		fresh := make(map[string]http.Handler, len(sel))
		freshTargets := make(map[string]seed.Target, len(sel))
		for _, p := range sel {
			switch p {
			case "aws":
				cloud := cloudemu.NewAWS(opts...)
				d := awsserver.DriversFrom(cloud)
				d.K8sAPI = k8s
				// Drivers.K8sAPI is only the server's PATH ROUTING for
				// /k8s/{uid}/...; the control-plane mock keeps its own
				// reference and needs it separately, or EKS still advertises
				// the sentinel.
				cloud.EKS.SetK8sAPI(k8s)
				fresh["aws"] = wrap(awsserver.New(d), "aws", c.logReqs)
				freshTargets["aws"] = seed.Target{Storage: cloud.S3, Database: cloud.DynamoDB, Secrets: cloud.SecretsManager, Compute: cloud.EC2}
			case "gcp":
				cloud := cloudemu.NewGCP(opts...)
				d := gcpserver.DriversFrom(cloud)
				d.K8sAPI = k8s
				cloud.GKE.SetK8sAPI(k8s)
				fresh["gcp"] = wrap(gcpserver.New(d), "gcp", c.logReqs)
				freshTargets["gcp"] = seed.Target{Storage: cloud.GCS, Database: cloud.Firestore, Secrets: cloud.SecretManager, Compute: cloud.GCE}
			case "azure":
				cloud := cloudemu.NewAzure(opts...)
				d := azureserver.DriversFrom(cloud)
				d.K8sAPI = k8s
				cloud.AKS.SetK8sAPI(k8s)
				fresh["azure"] = wrap(azureserver.New(d), "azure", c.logReqs)
				freshTargets["azure"] = seed.Target{Storage: cloud.BlobStorage, Database: cloud.CosmosDB, Secrets: cloud.KeyVault, Compute: cloud.VirtualMachines}
			}
		}
		if k8sBackend != nil {
			k8sBackend.Swap(wrap(k8s, "kubernetes", c.logReqs))
		}
		for p, h := range fresh {
			backends[p].Swap(h)
		}
		targets = freshTargets
	}
	rebuild() // populate the backends before serving

	// seedFor applies a fixture body to a provider's current drivers. It shares
	// rebuildMu with reset so a seed and a reset can't run against each other's
	// half-built state.
	seedFor := func(provider string) func([]byte) (int, error) {
		return func(fixture []byte) (int, error) {
			f, err := seed.Load(fixture)
			if err != nil {
				return 0, err
			}
			// Read the current Target under the lock, but run Apply outside it:
			// a large fixture (especially with --latency) must not hold the
			// shared reset mutex for its whole duration.
			rebuildMu.Lock()
			t := targets[provider]
			rebuildMu.Unlock()
			if err := seed.Apply(context.Background(), f, t); err != nil {
				return 0, err
			}
			return f.ResourceCount(), nil
		}
	}

	// reset wipes all state, so warn if it's reachable off the loopback — e.g.
	// a shared instance bound with --host 0.0.0.0, where anyone on the network
	// could POST /_cloudemu/reset.
	if c.admin && !isLoopbackHost(c.host) {
		fmt.Fprintf(os.Stderr,
			"warning: --admin control plane (POST /_cloudemu/reset wipes all state) is reachable on non-loopback host %q; pass --admin=false to disable it\n",
			c.host)
	}

	// handlerFor fronts a backend with the /_cloudemu control plane. With the
	// admin API off the backend serves directly, so control paths fall through
	// to the wire handlers (whatever they return for an unrouted path). seedFn
	// may be nil (e.g. the Kubernetes port), which disables the seed endpoint.
	handlerFor := func(b *admin.Backend, seedFn func([]byte) (int, error)) http.Handler {
		if c.admin {
			return admin.NewControl(b, rebuild, seedFn)
		}
		return b
	}

	for _, p := range sel {
		var (
			addr   string
			tlsCfg *tls.Config
			isTLS  bool
		)
		switch p {
		case "aws":
			addr = net.JoinHostPort(c.host, c.awsPort)
			eps.AWS = fmt.Sprintf("http://%s", addr)
		case "gcp":
			addr = net.JoinHostPort(c.host, c.gcpPort)
			eps.GCP = fmt.Sprintf("http://%s", addr)
		case "azure":
			addr = net.JoinHostPort(c.host, c.azurePort)
			var err error
			if tlsCfg, err = tlsConfig(c, addr); err != nil {
				return fmt.Errorf("azure TLS: %w", err)
			}
			isTLS = true
			eps.Azure = fmt.Sprintf("https://%s", addr)
		}
		servers = append(servers, &namedServer{
			name: p,
			srv:  &http.Server{Addr: addr, Handler: handlerFor(backends[p], seedFor(p)), TLSConfig: tlsCfg},
			tls:  isTLS,
		})
	}

	if k8sBackend != nil {
		addr := net.JoinHostPort(c.host, c.k8sPort)

		k8sTLS, err := eksprov.ServingTLSConfig([]string{c.host, "localhost", "127.0.0.1"})
		if err != nil {
			return fmt.Errorf("kubernetes data-plane TLS: %w", err)
		}

		servers = append(servers, &namedServer{
			name: "kubernetes",
			srv:  &http.Server{Addr: addr, Handler: handlerFor(k8sBackend, nil), TLSConfig: k8sTLS},
			tls:  true,
		})
		eps.Kubernetes = fmt.Sprintf("https://%s", addr)
	}

	// Bind every listener before serving so a port clash fails fast, before
	// we print a banner promising endpoints that never came up.
	listeners := make([]net.Listener, len(servers))
	for i, s := range servers {
		ln, err := net.Listen("tcp", s.srv.Addr)
		if err != nil {
			for _, l := range listeners[:i] {
				l.Close()
			}
			return fmt.Errorf("bind %s (%s): %w", s.name, s.srv.Addr, err)
		}
		listeners[i] = ln
	}

	errCh := make(chan error, len(servers))
	for i, s := range servers {
		s := s
		ln := listeners[i]
		go func() {
			var err error
			if s.tls {
				err = s.srv.ServeTLS(ln, "", "")
			} else {
				err = s.srv.Serve(ln)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s: %w", s.name, err)
			}
		}()
	}

	if !c.quiet {
		printBanner(os.Stdout, eps, c.admin)
	}
	if c.endpoints != "" {
		if err := eps.writeFile(c.endpoints); err != nil {
			return fmt.Errorf("write endpoints file: %w", err)
		}
	}

	// Block until a signal or a fatal serve error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-sigCh:
		if !c.quiet {
			fmt.Fprintln(os.Stdout, "\nshutting down…")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.shutdownTO)
	defer cancel()
	var shutErr error
	for _, s := range servers {
		if err := s.srv.Shutdown(ctx); err != nil && shutErr == nil {
			shutErr = err
		}
	}
	return shutErr
}

type namedServer struct {
	name string
	srv  *http.Server
	tls  bool
}

// isLoopbackHost reports whether binding to h keeps the server local-only.
func isLoopbackHost(h string) bool {
	switch h {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func parseProviders(s string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range strings.Split(s, ",") {
		p := strings.TrimSpace(strings.ToLower(raw))
		if p == "" {
			continue
		}
		if p != "aws" && p != "azure" && p != "gcp" {
			return nil, fmt.Errorf("unknown provider %q (want aws, azure, or gcp)", p)
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
