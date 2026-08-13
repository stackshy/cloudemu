package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/topology"
	"github.com/stackshy/cloudemu/v2/persist"
	eksprov "github.com/stackshy/cloudemu/v2/providers/aws/eks"
	"github.com/stackshy/cloudemu/v2/providers/openshift/ocm"
	"github.com/stackshy/cloudemu/v2/seed"
	"github.com/stackshy/cloudemu/v2/server/admin"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
	ocmserver "github.com/stackshy/cloudemu/v2/server/openshift/ocm"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	"github.com/stackshy/cloudemu/v2/services/pricing"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// errStateFileRequired is returned when --persist is set without --state-file.
var errStateFileRequired = errors.New("--persist requires --state-file")

// errUnsupportedSnapshot is returned when a posted snapshot has an unknown
// schema version.
var errUnsupportedSnapshot = errors.New("unsupported snapshot schema version")

// serveConfig holds the resolved serve flags.
type serveConfig struct {
	providers       string
	host            string
	awsPort         string
	azurePort       string
	gcpPort         string
	ociPort         string
	k8sPort         string
	accountID       string
	region          string
	projectID       string
	latency         time.Duration
	tlsCert         string
	tlsKey          string
	tlsHosts        stringList
	endpoints       string
	admin           bool
	logReqs         bool
	quiet           bool
	shutdownTO      time.Duration
	persist         bool
	stateFile       string
	persistMetaOnly bool
	initDir         string
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
	// OCI is not started by default: it stays opt-in until its services land,
	// so the default set never binds a port that serves nothing.
	fs.StringVar(&c.providers, "providers", "aws,azure,gcp", "comma-separated providers to start: aws,azure,gcp,oci")
	fs.StringVar(&c.host, "host", "127.0.0.1", "host/interface to bind (use 0.0.0.0 to expose on the network)")
	fs.StringVar(&c.awsPort, "aws-port", "4566", "port for the AWS endpoint (HTTP)")
	fs.StringVar(&c.azurePort, "azure-port", "4568", "port for the Azure endpoint (HTTPS)")
	fs.StringVar(&c.gcpPort, "gcp-port", "4569", "port for the GCP endpoint (HTTP)")
	fs.StringVar(&c.ociPort, "oci-port", "4571", "port for the OCI endpoint (HTTP)")
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
	fs.BoolVar(&c.persist, "persist", false, "save state to --state-file on shutdown and restore it on startup (includes object bodies)")
	fs.StringVar(&c.stateFile, "state-file", "", "path to the JSON state snapshot (required with --persist)")
	fs.BoolVar(&c.persistMetaOnly, "persist-metadata-only", false, "persist resource structure but omit object bodies (smaller snapshot)")
	fs.StringVar(&c.initDir, "init-dir", "", "apply every *.json seed fixture in this directory on startup")
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
		targets   map[string]seed.Target               // provider → current drivers, for seeding
		netEngine *topology.Engine                     // AWS network-reachability engine (nil if aws not selected)
		discovery map[string]*resourcediscovery.Engine // provider → inventory engine, for cost estimation
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
		freshDiscovery := make(map[string]*resourcediscovery.Engine, len(sel))

		var freshEngine *topology.Engine

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
				// ROSA/OCM: a Red Hat cluster-manager REST surface hosted on
				// the AWS endpoint (rosa is AWS-only). Shares the same data
				// plane so `rosa`-created clusters yield a working oc kubeconfig.
				ocmMock := ocm.New(config.NewOptions(opts...))
				ocmMock.SetK8sAPI(k8s)
				d.OCM = ocmserver.New(ocmMock)
				fresh["aws"] = wrap(awsserver.New(d), "aws", c.logReqs)
				freshTargets["aws"] = seed.Target{Storage: cloud.S3, Database: cloud.DynamoDB, Secrets: cloud.SecretsManager, Compute: cloud.EC2}
				freshEngine = topology.New(cloud.EC2, cloud.VPC, cloud.Route53)
				freshDiscovery["aws"] = cloud.ResourceDiscovery
			case "gcp":
				cloud := cloudemu.NewGCP(opts...)
				d := gcpserver.DriversFrom(cloud)
				d.K8sAPI = k8s
				cloud.GKE.SetK8sAPI(k8s)
				fresh["gcp"] = wrap(gcpserver.New(d), "gcp", c.logReqs)
				freshTargets["gcp"] = seed.Target{Storage: cloud.GCS, Database: cloud.Firestore, Secrets: cloud.SecretManager, Compute: cloud.GCE}
				freshDiscovery["gcp"] = cloud.ResourceDiscovery
			case "azure":
				cloud := cloudemu.NewAzure(opts...)
				d := azureserver.DriversFrom(cloud)
				d.K8sAPI = k8s
				cloud.AKS.SetK8sAPI(k8s)
				cloud.ARO.SetK8sAPI(k8s)
				fresh["azure"] = wrap(azureserver.New(d), "azure", c.logReqs)
				freshTargets["azure"] = seed.Target{Storage: cloud.BlobStorage, Database: cloud.CosmosDB, Secrets: cloud.KeyVault, Compute: cloud.VirtualMachines}
				freshDiscovery["azure"] = cloud.ResourceDiscovery
			case "oci":
				cloud := cloudemu.NewOCI(opts...)
				d := ociserver.DriversFrom(cloud)
				d.K8sAPI = k8s
				fresh["oci"] = wrap(ociserver.New(d), "oci", c.logReqs)
				freshTargets["oci"] = seed.Target{
					Storage: cloud.ObjectStorage, Database: cloud.NoSQL,
					Secrets: cloud.Vault, Compute: cloud.Compute,
				}
			}
		}
		if k8sBackend != nil {
			k8sBackend.Swap(wrap(k8s, "kubernetes", c.logReqs))
		}
		for p, h := range fresh {
			backends[p].Swap(h)
		}
		targets = freshTargets
		netEngine = freshEngine
		discovery = freshDiscovery
	}
	rebuild() // populate the backends before serving

	// Restore persisted state into the freshly-built providers before serving,
	// so the first request already sees the resources from the last run.
	if c.persist {
		if err := restoreState(context.Background(), c.stateFile, targets); err != nil {
			return fmt.Errorf("restore persisted state: %w", err)
		}
	}

	// Apply init fixtures on top of the built (and possibly restored) providers,
	// before serving, so the first request already sees the boot state.
	if c.initDir != "" {
		if err := applyInitDir(context.Background(), c.initDir, targets); err != nil {
			return fmt.Errorf("apply init dir: %w", err)
		}
	}

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
			"warning: --admin control plane is reachable on non-loopback host %q — "+
				"POST /_cloudemu/reset wipes all state, and GET /_cloudemu/snapshot dumps "+
				"all emulated state (including secret values) to any caller; "+
				"pass --admin=false to disable it\n",
			c.host)
	}

	// snapshotFn/restoreFn back /_cloudemu/snapshot; both act on the whole
	// emulator like reset. snapshot captures current state as JSON; restore
	// rebuilds to empty then loads the posted state.
	snapshotFn := func() ([]byte, error) {
		rebuildMu.Lock()
		cur := targets
		rebuildMu.Unlock()

		snap, err := persist.ExportAll(context.Background(), cur, persist.Options{IncludeAssets: true})
		if err != nil {
			return nil, err
		}

		return json.MarshalIndent(snap, "", "  ")
	}
	restoreFn := func(body []byte) error {
		var snap persist.Snapshot
		if err := json.Unmarshal(body, &snap); err != nil {
			return fmt.Errorf("parse snapshot: %w", err)
		}

		if snap.SchemaVersion != persist.SchemaVersion {
			return fmt.Errorf("%w: got %d, want %d", errUnsupportedSnapshot, snap.SchemaVersion, persist.SchemaVersion)
		}

		// Destructive load (reset semantics): wipe to empty, then repopulate. If
		// RestoreAll fails partway the running state is already gone — acceptable
		// for a local emulator, but a future hardening is to restore into a
		// staging build and swap it in only on success.
		rebuild() // wipe to empty before loading

		rebuildMu.Lock()
		cur := targets
		rebuildMu.Unlock()

		return persist.RestoreAll(context.Background(), &snap, cur)
	}

	// extraHandler serves the control-plane endpoints that plug into admin:
	// network reachability (/net/*, AWS-only, needs the topology engine) and
	// cost estimation (/cost, all providers, needs the discovery engines).
	extraHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, admin.Prefix) {
		case "net/can-connect", "net/trace":
			rebuildMu.Lock()
			eng := netEngine
			rebuildMu.Unlock()

			if eng == nil {
				writeNetErr(w, http.StatusServiceUnavailable, "network topology requires the aws provider")

				return
			}

			if strings.HasSuffix(r.URL.Path, "trace") {
				serveTrace(w, r, eng)
			} else {
				serveCanConnect(w, r, eng)
			}
		case "cost":
			rebuildMu.Lock()
			ds := discovery
			rebuildMu.Unlock()

			serveCost(w, r, ds)
		default:
			writeNetErr(w, http.StatusNotFound, "unknown control endpoint")
		}
	})

	// handlerFor fronts a backend with the /_cloudemu control plane. With the
	// admin API off the backend serves directly, so control paths fall through
	// to the wire handlers (whatever they return for an unrouted path). seedFn
	// may be nil (e.g. the Kubernetes port), which disables the seed endpoint.
	handlerFor := func(b *admin.Backend, seedFn func([]byte) (int, error)) http.Handler {
		if c.admin {
			return admin.NewControl(b, rebuild, seedFn, snapshotFn, restoreFn, extraHandler)
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
		case "oci":
			addr = net.JoinHostPort(c.host, c.ociPort)
			eps.OCI = fmt.Sprintf("http://%s", addr)
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
		printBanner(os.Stdout, &eps, c.admin)
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

	// Snapshot after Shutdown so no in-flight request can mutate state mid-read.
	if c.persist {
		if err := snapshotState(context.Background(), c.stateFile, !c.persistMetaOnly, targets); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to save state to %s: %v\n", c.stateFile, err)
		} else if !c.quiet {
			fmt.Fprintf(os.Stdout, "state saved to %s\n", c.stateFile)
		}
	}

	return shutErr
}

// restoreState loads the snapshot at path (if any) into the freshly-built
// providers. A missing file is not an error — the server just starts empty,
// exactly as it does without --persist. Providers present in the snapshot but
// not running now are skipped.
func restoreState(ctx context.Context, path string, targets map[string]seed.Target) error {
	snap, err := persist.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // first run — nothing to restore
		}

		// A corrupt / truncated / unknown-schema snapshot must not wedge startup
		// on the very stop→start path this feature serves: warn and start empty
		// rather than aborting.
		fmt.Fprintf(os.Stderr, "warning: ignoring unreadable state file %s: %v\n", path, err)

		return nil
	}

	return persist.RestoreAll(ctx, &snap, targets)
}

// costLine is one always-on resource with its estimated monthly cost.
type costLine struct {
	Provider   string  `json:"provider"`
	Service    string  `json:"service"`
	Type       string  `json:"type"`
	ID         string  `json:"id"`
	MonthlyUSD float64 `json:"monthlyUsd"`
}

// serveCost answers GET /_cloudemu/cost with an estimated monthly cost of the
// current inventory (always-on resources only; usage-based services excluded).
func serveCost(w http.ResponseWriter, r *http.Request, engines map[string]*resourcediscovery.Engine) {
	var (
		lines     []costLine
		total     float64
		byService = map[string]float64{}
	)

	for prov, eng := range engines {
		if eng == nil {
			continue
		}

		res, err := eng.ListAll(r.Context())
		if err != nil {
			writeNetErr(w, http.StatusInternalServerError, err.Error())

			return
		}

		for i := range res {
			rr := &res[i]

			est := pricing.Monthly(rr.Provider, rr.Service, rr.Type, rr.SKU, rr.Region, rr.Properties)
			if est <= 0 {
				continue
			}

			lines = append(lines, costLine{Provider: prov, Service: rr.Service, Type: rr.Type, ID: rr.ID, MonthlyUSD: est})
			total += est
			byService[prov+"/"+rr.Service] += est
		}
	}

	writeNetJSON(w, map[string]any{
		"estimatedMonthlyUsd": total,
		"byService":           byService,
		"resources":           lines,
	})
}

// serveCanConnect answers GET /_cloudemu/net/can-connect?from&to&port&protocol
// with the engine's ConnectivityResult as JSON.
func serveCanConnect(w http.ResponseWriter, r *http.Request, eng *topology.Engine) {
	q := r.URL.Query()

	from, to := q.Get("from"), q.Get("to")
	if from == "" || to == "" {
		writeNetErr(w, http.StatusBadRequest, "from and to instance IDs are required")

		return
	}

	port, err := netPort(q.Get("port"))
	if err != nil {
		writeNetErr(w, http.StatusBadRequest, err.Error())

		return
	}

	proto := q.Get("protocol")
	if proto == "" {
		proto = "tcp"
	}

	res, err := eng.CanConnect(r.Context(), topology.ConnectivityQuery{
		SrcInstanceID: from, DstInstanceID: to, Port: port, Protocol: proto,
	})
	if err != nil {
		writeNetErr(w, http.StatusBadRequest, err.Error())

		return
	}

	writeNetJSON(w, res)
}

// serveTrace answers GET /_cloudemu/net/trace?from&to (to is a destination IP)
// with the route hops as JSON.
func serveTrace(w http.ResponseWriter, r *http.Request, eng *topology.Engine) {
	q := r.URL.Query()

	from, dest := q.Get("from"), q.Get("to")
	if from == "" || dest == "" {
		writeNetErr(w, http.StatusBadRequest, "from instance ID and to IP are required")

		return
	}

	hops, err := eng.TraceRoute(r.Context(), from, dest)
	if err != nil {
		writeNetErr(w, http.StatusBadRequest, err.Error())

		return
	}

	writeNetJSON(w, map[string]any{"hops": hops})
}

// netPort parses an optional port query value (empty → 0 = any).
func netPort(s string) (int, error) {
	if s == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", s, err)
	}

	return n, nil
}

func writeNetJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")

	b, err := json.Marshal(v)
	if err != nil {
		writeNetErr(w, http.StatusInternalServerError, err.Error())

		return
	}

	_, _ = w.Write(b)
}

func writeNetErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// applyInitDir applies every *.json fixture in dir (lexical order) to every
// running provider on boot, bringing the emulator up to a known state. A
// missing dir is a no-op. A parse error fails startup (clear misconfiguration);
// an apply error only warns and continues, so a fixture that collides with
// already-restored state can't wedge the boot.
func applyInitDir(ctx context.Context, dir string, targets map[string]seed.Target) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	names := make([]string, 0, len(entries))

	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}

	sort.Strings(names)

	for _, name := range names {
		if err := applyInitFile(ctx, filepath.Join(dir, name), name, targets); err != nil {
			return err
		}
	}

	return nil
}

// applyInitFile loads one fixture file and applies it to every provider. A load
// (parse) error is returned; per-provider apply errors are warned and skipped.
func applyInitFile(ctx context.Context, path, name string, targets map[string]seed.Target) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	f, err := seed.Load(data)
	if err != nil {
		return fmt.Errorf("init fixture %s: %w", name, err)
	}

	for prov, t := range targets {
		// IgnoreExisting so a resource that already exists (from restored state or
		// an earlier init file) is skipped rather than aborting the rest of the
		// fixture; other errors still warn.
		if err := seed.Apply(ctx, f, t, seed.IgnoreExisting()); err != nil {
			fmt.Fprintf(os.Stderr, "warning: init %s on %s: %v\n", name, prov, err)
		}
	}

	return nil
}

// snapshotState exports every running provider's state and writes the snapshot
// file. Called after Shutdown, so the providers are quiescent.
func snapshotState(ctx context.Context, path string, includeAssets bool, targets map[string]seed.Target) error {
	snap, err := persist.ExportAll(ctx, targets, persist.Options{IncludeAssets: includeAssets})
	if err != nil {
		return err
	}

	return snap.WriteFile(path)
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
