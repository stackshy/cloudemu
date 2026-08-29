// Package serverkit is the shared assembly layer for the standalone emulator.
//
// It builds every selected provider, fronts each with the /_cloudemu admin
// control plane, wires the shared Kubernetes data-plane, persistence, seeding,
// and TLS, binds the listeners, and owns the serve/shutdown/snapshot lifecycle.
// It is the source of truth for cmd/cloudemu/serve.go, and the seam the
// batteries-included contrib/server adopts in a follow-up so the two
// entrypoints don't drift.
//
// Beyond moving serve's proven assembly, serverkit adds one capability the old
// inline code lacked: it tracks the current live *Provider set and Close()es the
// outgoing providers after every rebuild swap (reset/restore) and on final
// shutdown after the snapshot, so real data-plane engines (embedded Postgres,
// miniredis, Docker containers) are torn down rather than leaked. For the
// engineless in-process providers cmd/cloudemu builds, Close is a no-op.
package serverkit

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/topology"
	"github.com/stackshy/cloudemu/v2/persist"
	eksprov "github.com/stackshy/cloudemu/v2/providers/aws/eks"
	"github.com/stackshy/cloudemu/v2/seed"
	"github.com/stackshy/cloudemu/v2/server/admin"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// errUnsupportedSnapshot is returned when a posted snapshot has an unknown
// schema version.
var errUnsupportedSnapshot = errors.New("unsupported snapshot schema version")

// errUnknownStrategy is returned when Config.PersistStrategy is not one of the
// four supported values.
var errUnknownStrategy = errors.New("unknown persist strategy (want scheduled, on-request, on-shutdown, or manual)")

// defaultShutdownTimeout is the grace period applied when Config leaves
// ShutdownTimeout unset.
const defaultShutdownTimeout = 10 * time.Second

// defaultK8sProgressionInterval is the real-time cadence at which the staged
// Pod lifecycle ticker advances Pods when Config leaves the interval unset.
const defaultK8sProgressionInterval = time.Second

// serverReadHeaderTimeout bounds how long a client may take to send request
// headers, closing the Slowloris hole a zero timeout leaves open.
const serverReadHeaderTimeout = 10 * time.Second

// Provider names, shared by the build and bind switches.
const (
	providerAWS   = "aws"
	providerAzure = "azure"
	providerGCP   = "gcp"
	providerOCI   = "oci"
)

// closer is the Close() surface of a *Provider, cascaded across rebuild swaps
// and on shutdown to free the real engines (embedded Postgres, miniredis, Docker
// containers) it wired. Close is idempotent and a no-op for engineless
// providers, so tracking it is safe for cmd/cloudemu and correct for contrib.
type closer interface{ Close() error }

// Config is the flat data seam serverkit assembles a server from. Both
// entrypoints fill it: cmd/cloudemu from its serve flags, contrib/server from
// its engine flags. It carries no behavior.
type Config struct {
	Providers []string // selected providers, already parsed: aws,azure,gcp,oci

	Host          string            // host/interface to bind
	Ports         map[string]string // per-provider bind ports, keys aws/azure/gcp/oci (and optionally k8s)
	K8sPort       string            // shared Kubernetes data-plane port; empty disables it
	AdvertiseHost string            // host the Kubernetes endpoint is advertised at (default: derived from Host)

	// K8sProgression enables KWOK-style staged Pod lifecycle on the data plane:
	// client-created Pods start Pending and visibly advance to Running on a
	// real-time ticker. Default off keeps Pods instant-Running. Controller Pods
	// (Deployments etc.) always come up Running.
	K8sProgression         bool
	K8sProgressionInterval time.Duration // ticker cadence for staged progression (default 1s)

	AzureSubscription string // Azure subscription GUID reported by the emulator (last-wins WithAccountID)

	Admin bool // mount the /_cloudemu control plane

	Persist             bool          // save/restore state around the process lifetime
	StateFile           string        // path to the JSON state snapshot
	PersistMetadataOnly bool          // omit object bodies from the snapshot
	PersistStrategy     string        // when/how to save: scheduled|on-request|on-shutdown|manual (default scheduled)
	PersistInterval     time.Duration // ticker cadence for the scheduled strategy (default 15s)
	InitDir             string        // apply every *.json fixture in this dir on boot

	TLSCert  string   // PEM cert for the Azure HTTPS endpoint (empty: self-signed)
	TLSKey   string   // PEM key matching TLSCert
	TLSHosts []string // extra SAN hosts for the generated self-signed cert

	Latency     time.Duration // artificial latency added to every emulated call
	LogRequests bool          // log every HTTP request
	Quiet       bool          // suppress the startup banner
	EnforceAuth bool          // require authentication on each request

	EndpointsFile   string        // write resolved endpoints as JSON here
	ShutdownTimeout time.Duration // grace period for in-flight requests on shutdown

	BaseOptions []config.Option // identity + (for contrib) engine options every provider is built with
	Out         io.Writer       // banner/diagnostics sink (default: os.Stdout)
}

// App is a fully-wired, not-yet-serving emulator. New builds and populates the
// backends; Serve binds the listeners and runs until the context is canceled.
type App struct {
	cfg Config
	out io.Writer

	sel           []string
	baseOpts      []config.Option
	advertiseHost string
	k8sPort       string

	backends   map[string]*admin.Backend
	k8sBackend *admin.Backend

	// k8s is the current Kubernetes data-plane server (rebuilt on reset, guarded
	// by rebuildMu). The progression ticker reads it each tick.
	k8s *kubernetes.APIServer
	// k8sTickStop/k8sTickDone manage the opt-in real-time progression ticker
	// goroutine (started in Serve, stopped in shutdown, like the persist flusher).
	k8sTickStop chan struct{}
	k8sTickDone chan struct{}

	// flusher owns every automatic persistence save (nil unless --persist). Its
	// dirty flag is flipped by the request-boundary seam and the mutating admin
	// ops.
	flusher *flusher

	// rebuildMu serializes resets so two concurrent /_cloudemu/reset calls can't
	// interleave and leave providers wired to different Kubernetes instances. It
	// also guards the current-state fields below and the live provider set.
	rebuildMu   sync.Mutex
	targets     map[string]seed.Target
	snapTargets map[string]persist.Services
	netEngine   *topology.Engine
	discovery   map[string]*resourcediscovery.Engine
	providers   []closer // current live providers, Close()d when swapped out

	// exportMu guards an in-flight state export (flusher save or admin snapshot)
	// against provider teardown: every export holds it for read across the whole
	// ExportAll, and closeProviders takes it for write before Close()ing the
	// outgoing providers. So a rebuild/reset can never tear down a real (contrib)
	// embedded-Postgres/redis/Docker engine mid-export. It is a distinct lock from
	// rebuildMu — held only for the export duration, never while swapping — so the
	// two never form a cycle.
	exportMu sync.RWMutex
}

// New builds every selected provider behind its admin backend, restores any
// persisted state, and applies init-dir fixtures — everything up to binding the
// listeners. The returned App is ready to Serve.
func New(cfg *Config) (*App, error) {
	if err := normalizePersist(cfg); err != nil {
		return nil, err
	}

	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	k8sPort := cfg.K8sPort
	if k8sPort == "" {
		k8sPort = cfg.Ports["k8s"]
	}

	a := &App{
		cfg:           *cfg,
		out:           out,
		sel:           cfg.Providers,
		baseOpts:      baseOptsFor(cfg),
		advertiseHost: advertiseHostFor(cfg.AdvertiseHost, cfg.Host),
		k8sPort:       k8sPort,
		backends:      make(map[string]*admin.Backend, len(cfg.Providers)),
	}
	for _, p := range cfg.Providers {
		a.backends[p] = admin.NewBackend(nil)
	}

	if k8sPort != "" {
		a.k8sBackend = admin.NewBackend(nil)
	}

	if cfg.Persist {
		a.flusher = a.newFlusher()
	}

	a.Rebuild() // populate the backends before serving

	if err := a.applyBootState(); err != nil {
		return nil, err
	}

	// reset wipes all state, so warn if it's reachable off the loopback — e.g. a
	// shared instance bound with --host 0.0.0.0, where anyone on the network
	// could POST /_cloudemu/reset.
	if w := dangerWarning(cfg); w != "" {
		fmt.Fprintln(os.Stderr, w)
	}

	return a, nil
}

// normalizePersist fills in the persistence-strategy defaults and validates the
// strategy. It is a no-op when --persist is off. Defaulting here means both
// entrypoints (and library callers) get the same scheduled/15s behavior without
// each repeating it.
func normalizePersist(cfg *Config) error {
	if !cfg.Persist {
		return nil
	}

	if cfg.PersistStrategy == "" {
		cfg.PersistStrategy = DefaultPersistStrategy
	}

	switch cfg.PersistStrategy {
	case StrategyScheduled, StrategyOnRequest, StrategyOnShutdown, StrategyManual:
	default:
		return fmt.Errorf("%w: %q", errUnknownStrategy, cfg.PersistStrategy)
	}

	if cfg.PersistInterval <= 0 {
		cfg.PersistInterval = DefaultPersistInterval
	}

	return nil
}

// newFlusher builds the App's flusher, injecting a save closure that reads the
// live snapshot targets under rebuildMu (the same guard swapFresh/shutdown use)
// so a concurrent reset can't race the export.
func (a *App) newFlusher() *flusher {
	save := func(ctx context.Context, includeAssets bool) error {
		// Hold the export read-guard across the whole save so a concurrent
		// Rebuild()/reset cannot Close() the providers being exported (see
		// exportMu). rebuildMu is taken only to read the current snapshot targets.
		a.exportMu.RLock()
		defer a.exportMu.RUnlock()

		// Capture the provider targets AND the Kubernetes data plane in ONE
		// rebuildMu section: reading them under separate locks could pair a stale
		// a.k8s from before a reset with fresh targets after it (a dangling-UID
		// hazard). exportSnapshot then captures providers before k8s.
		a.rebuildMu.Lock()
		targets := a.snapTargets
		k8s := a.k8s
		a.rebuildMu.Unlock()

		return snapshotState(ctx, a.cfg.StateFile, includeAssets, targets, k8s)
	}

	return newFlusher(
		a.cfg.PersistStrategy,
		a.cfg.PersistInterval,
		!a.cfg.PersistMetadataOnly, // every save honors the asset setting (bodies on by default)
		save,
		a.out,
		a.cfg.Quiet,
		a.cfg.StateFile,
	)
}

// markDirty records a state mutation for the flusher. It is a no-op when
// persistence is off (flusher nil), so callers need no guard.
func (a *App) markDirty() {
	a.flusher.markDirty()
}

// persistBanner is the persistence summary shown in the startup banner.
func (a *App) persistBanner() persistInfo {
	return persistInfo{
		on:        a.cfg.Persist,
		strategy:  a.cfg.PersistStrategy,
		interval:  a.cfg.PersistInterval,
		stateFile: a.cfg.StateFile,
	}
}

// baseOptsFor clones Config.BaseOptions and appends the latency/auth options, so
// the caller's slice is never mutated and buildProvider's Azure copy starts from
// a stable base.
func baseOptsFor(cfg *Config) []config.Option {
	baseOpts := append([]config.Option(nil), cfg.BaseOptions...)

	if cfg.Latency > 0 {
		baseOpts = append(baseOpts, config.WithLatency(cfg.Latency))
	}

	if cfg.EnforceAuth {
		baseOpts = append(baseOpts, config.WithEnforceAuth())
	}

	return baseOpts
}

// applyBootState restores persisted state and applies init-dir fixtures onto the
// freshly-built providers before serving, so the first request already sees the
// boot state. Restore runs first; init fixtures apply on top.
func (a *App) applyBootState() error {
	if a.cfg.Persist {
		// New() runs this single-threaded before Serve binds listeners, so reading
		// a.snapTargets / a.k8s (both set by the Rebuild() in New) without rebuildMu
		// is safe here.
		if err := restoreState(context.Background(), a.cfg.StateFile, a.snapTargets, a.k8s); err != nil {
			return fmt.Errorf("restore persisted state: %w", err)
		}
	}

	if a.cfg.InitDir != "" {
		if err := applyInitDir(context.Background(), a.cfg.InitDir, a.targets); err != nil {
			return fmt.Errorf("apply init dir: %w", err)
		}
	}

	return nil
}

// Rebuild reconstructs a fresh Kubernetes server and every selected provider
// from scratch and swaps them in atomically — this is what /_cloudemu/reset
// calls to hand a test suite a clean slate without restarting the process. After
// the swap it Close()es the providers it replaced (a no-op when no engine is
// wired) so their real engines are freed rather than leaked.
func (a *App) Rebuild() {
	outgoing := a.swapFresh()
	// Swap-then-close: the fresh handlers already serve new requests before the
	// outgoing providers are torn down, so no request is ever routed to a
	// half-closed engine. A request already in flight against the outgoing
	// handler at the instant of the swap may still be mid-query when its
	// provider's Close runs immediately after — an accepted best-effort tradeoff
	// for a local dev/test tool, consistent with the destructive-reset
	// philosophy, not an oversight. A short drain window is a possible future
	// refinement.
	a.closeProviders(outgoing)
}

// swapFresh builds every fresh handler, swaps them into the backends under the
// rebuild lock, and returns the providers it displaced (for the caller to close
// outside the lock). Building first means a construction panic leaves the
// running set untouched, and all providers in one reset share the single new
// Kubernetes data-plane.
func (a *App) swapFresh() []closer {
	a.rebuildMu.Lock()
	defer a.rebuildMu.Unlock()

	var k8s *kubernetes.APIServer
	if a.k8sBackend != nil {
		k8s = kubernetes.NewAPIServer()
		// Tell the data plane the address it is reachable on, so the
		// managed-Kubernetes control planes can advertise an endpoint that
		// actually answers. https, because the listener is served with a cert
		// signed by the CA DescribeCluster advertises.
		k8s.SetBaseURL("https://" + net.JoinHostPort(a.advertiseHost, a.k8sPort))

		// Opt-in staged Pod lifecycle: enable it on every cluster this data plane
		// registers, so the real-time ticker (started in Serve) can advance them.
		if a.cfg.K8sProgression {
			k8s.SetLifecycleProgression(true)
		}
	}

	a.k8s = k8s

	fresh := make(map[string]http.Handler, len(a.sel))
	freshTargets := make(map[string]seed.Target, len(a.sel))
	freshSnapTargets := make(map[string]persist.Services, len(a.sel))
	freshDiscovery := make(map[string]*resourcediscovery.Engine, len(a.sel))

	var (
		freshEngine    *topology.Engine
		freshProviders []closer
	)

	for _, p := range a.sel {
		b := a.buildProvider(p, k8s)
		fresh[p] = b.handler
		freshTargets[p] = b.target
		freshSnapTargets[p] = b.snap

		if b.discovery != nil {
			freshDiscovery[p] = b.discovery
		}

		if b.engine != nil {
			freshEngine = b.engine
		}

		if b.provider != nil {
			freshProviders = append(freshProviders, b.provider)
		}
	}

	if a.k8sBackend != nil {
		// Wrap the data-plane handler in the dirty seam HERE — at the same point
		// the four cloud providers are wrapped, BEFORE the admin Control fronts it
		// in buildServers. Since #868 makes the Kubernetes data plane part of the
		// persisted surface, a pure-kubectl mutation (which never touches a
		// provider port) must mark state dirty so scheduled/on-request saves catch
		// it. Wrapping here (not in buildServers) keeps admin/health probes — which
		// Control answers before reaching this backend — from dirtying an idle
		// emulator.
		a.k8sBackend.Swap(a.wrapDirty(wrap(k8s, "kubernetes", a.cfg.LogRequests)))
	}

	for p, h := range fresh {
		a.backends[p].Swap(h)
	}

	a.targets = freshTargets
	a.snapTargets = freshSnapTargets
	a.netEngine = freshEngine
	a.discovery = freshDiscovery

	outgoing := a.providers
	a.providers = freshProviders

	return outgoing
}

// builtProvider is one freshly-constructed provider: its wire handler plus the
// cross-cutting hooks (seed target, snapshot services, discovery/topology
// engines, and the closer for engine teardown).
type builtProvider struct {
	handler   http.Handler
	target    seed.Target
	snap      persist.Services
	discovery *resourcediscovery.Engine // nil for oci
	engine    *topology.Engine          // non-nil only for aws (network topology)
	provider  closer                    // nil for oci (no Close/engine teardown)
}

// buildProvider constructs one provider and its hooks. It shares the single new
// Kubernetes data-plane k8s across every provider in a rebuild.
func (a *App) buildProvider(p string, k8s *kubernetes.APIServer) builtProvider {
	switch p {
	case providerAWS:
		cloud := cloudemu.NewAWS(a.baseOpts...)
		d := awsserver.DriversFrom(cloud)
		d.K8sAPI = k8s
		// Drivers.K8sAPI is only the server's PATH ROUTING for /k8s/{uid}/...;
		// the control-plane mock keeps its own reference and needs it
		// separately, or EKS still advertises the sentinel.
		cloud.EKS.SetK8sAPI(k8s)

		return builtProvider{
			handler:   a.wrapDirty(wrap(awsserver.New(d), providerAWS, a.cfg.LogRequests)),
			target:    seed.Target{Storage: cloud.S3, Database: cloud.DynamoDB, Secrets: cloud.SecretsManager, Compute: cloud.EC2},
			snap:      cloud.SnapshotServices(),
			discovery: cloud.ResourceDiscovery,
			engine:    topology.New(cloud.EC2, cloud.VPC, cloud.Route53),
			provider:  cloud,
		}
	case providerGCP:
		cloud := cloudemu.NewGCP(a.baseOpts...)
		d := gcpserver.DriversFrom(cloud)
		d.K8sAPI = k8s
		cloud.GKE.SetK8sAPI(k8s)

		return builtProvider{
			handler:   a.wrapDirty(wrap(gcpserver.New(d), providerGCP, a.cfg.LogRequests)),
			target:    seed.Target{Storage: cloud.GCS, Database: cloud.Firestore, Secrets: cloud.SecretManager, Compute: cloud.GCE},
			snap:      cloud.SnapshotServices(),
			discovery: cloud.ResourceDiscovery,
			provider:  cloud,
		}
	case providerAzure:
		// Azure subscriptions are GUIDs, unlike the 12-digit AWS account id.
		// Give the Azure provider its own subscription so resource ids and
		// Resource Graph scoping use a real Azure GUID (WithAccountID is
		// last-wins). Copy opts so the override never leaks into another
		// provider's option list.
		azureOpts := make([]config.Option, len(a.baseOpts), len(a.baseOpts)+1)
		copy(azureOpts, a.baseOpts)
		azureOpts = append(azureOpts, config.WithAccountID(a.cfg.AzureSubscription))
		cloud := cloudemu.NewAzure(azureOpts...)
		d := azureserver.DriversFrom(cloud)
		d.K8sAPI = k8s
		cloud.AKS.SetK8sAPI(k8s)

		return builtProvider{
			handler:   a.wrapDirty(wrap(azureserver.New(d), providerAzure, a.cfg.LogRequests)),
			target:    seed.Target{Storage: cloud.BlobStorage, Database: cloud.CosmosDB, Secrets: cloud.KeyVault, Compute: cloud.VirtualMachines},
			snap:      cloud.SnapshotServices(),
			discovery: cloud.ResourceDiscovery,
			provider:  cloud,
		}
	case providerOCI:
		cloud := cloudemu.NewOCI(a.baseOpts...)
		d := ociserver.DriversFrom(cloud)
		d.K8sAPI = k8s
		// OCI has no managed-Kubernetes service, so no SetK8sAPI here, and its
		// *Provider carries no engines to close.
		return builtProvider{
			handler: a.wrapDirty(wrap(ociserver.New(d), providerOCI, a.cfg.LogRequests)),
			target: seed.Target{
				Storage: cloud.ObjectStorage, Database: cloud.NoSQL,
				Secrets: cloud.Vault, Compute: cloud.Compute,
			},
			snap: cloud.SnapshotServices(),
		}
	}

	return builtProvider{}
}

// closeProviders closes each provider best-effort, cascading engine teardown. It
// runs after the swap (or after the shutdown snapshot), never before, and never
// aborts on an error — a failed teardown is logged, not fatal. It takes exportMu
// for write so it blocks until any in-flight export (flusher save or admin
// snapshot) that may still be reading these providers has finished — a real
// engine is never torn down mid-export.
func (a *App) closeProviders(providers []closer) {
	a.exportMu.Lock()
	defer a.exportMu.Unlock()

	for _, p := range providers {
		if err := p.Close(); err != nil {
			fmt.Fprintf(a.out, "warning: closing provider engines: %v\n", err)
		}
	}
}

// seedFor applies a fixture body to a provider's current drivers. It shares
// rebuildMu with reset so a seed and a reset can't run against each other's
// half-built state.
func (a *App) seedFor(provider string) func([]byte) (int, error) {
	return func(fixture []byte) (int, error) {
		f, err := seed.Load(fixture)
		if err != nil {
			return 0, err
		}
		// Read the current Target under the lock, but run Apply outside it: a
		// large fixture (especially with --latency) must not hold the shared
		// reset mutex for its whole duration.
		a.rebuildMu.Lock()
		t := a.targets[provider]
		a.rebuildMu.Unlock()

		if err := seed.Apply(context.Background(), f, t); err != nil {
			return 0, err
		}

		return f.ResourceCount(), nil
	}
}

// snapshot captures current state as JSON. It acts on the whole emulator, like
// reset, so a call to any provider port covers every provider.
func (a *App) snapshot() ([]byte, error) {
	// Same export read-guard as the flusher save: a concurrent reset must not tear
	// down the providers this export reads (see exportMu).
	a.exportMu.RLock()
	defer a.exportMu.RUnlock()

	// Capture provider targets AND the Kubernetes data plane in one rebuildMu
	// section (see the flusher save's note); exportSnapshot enforces the
	// providers-before-Kubernetes ordering, shared with the flusher path so the
	// two never drift.
	a.rebuildMu.Lock()
	cur := a.snapTargets
	k8s := a.k8s
	a.rebuildMu.Unlock()

	snap, err := exportSnapshot(context.Background(), cur, k8s, true)
	if err != nil {
		return nil, err
	}

	return json.MarshalIndent(snap, "", "  ")
}

// restore rebuilds to empty then loads the posted state. The load is destructive
// (reset semantics): rebuild wipes to empty first, so a RestoreAll that fails
// partway leaves an empty store, not a half-old/half-new mix — acceptable for a
// local emulator. The rebuild also Close()es the about-to-be-wiped providers,
// reaping their engines.
func (a *App) restore(body []byte) error {
	var snap persist.Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		return fmt.Errorf("parse snapshot: %w", err)
	}

	if snap.SchemaVersion != persist.SchemaVersion {
		return fmt.Errorf("%w: got %d, want %d", errUnsupportedSnapshot, snap.SchemaVersion, persist.SchemaVersion)
	}

	a.Rebuild() // wipe to empty before loading

	// Capture the (post-Rebuild) provider targets AND the fresh Kubernetes data
	// plane in one rebuildMu section, so the k8s-restore step below targets the
	// SAME APIServer instance the providers were just wired to.
	a.rebuildMu.Lock()
	cur := a.snapTargets
	k8s := a.k8s
	a.rebuildMu.Unlock()

	if err := persist.RestoreAll(context.Background(), &snap, cur); err != nil {
		return err
	}

	if err := restoreKubernetes(context.Background(), &snap, k8s); err != nil {
		return err
	}

	// Mark dirty so a restore-then-crash with no subsequent provider request
	// still persists the freshly-restored state on the next tick/final save.
	a.markDirty()

	return nil
}

// extraHandler serves the control-plane endpoints that plug into admin: network
// reachability (/net/*, AWS-only, needs the topology engine) and cost estimation
// (/cost, all providers, needs the discovery engines).
func (a *App) extraHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, admin.Prefix) {
		case "net/can-connect", "net/trace":
			a.rebuildMu.Lock()
			eng := a.netEngine
			a.rebuildMu.Unlock()

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
			a.rebuildMu.Lock()
			ds := a.discovery
			a.rebuildMu.Unlock()

			serveCost(w, r, ds)
		default:
			writeNetErr(w, http.StatusNotFound, "unknown control endpoint")
		}
	})
}

// handlerFor fronts a backend with the /_cloudemu control plane. With the admin
// API off the backend serves directly. seedFn may be nil (e.g. the Kubernetes
// port), which disables the seed endpoint.
func (a *App) handlerFor(b *admin.Backend, seedFn func([]byte) (int, error)) http.Handler {
	if !a.cfg.Admin {
		return b
	}

	reset := a.Rebuild
	// The admin plane bypasses the wrapDirty request seam (it dispatches inside
	// Control before reaching the wrapped backend), so the three mutating admin
	// ops must mark dirty themselves: reset and seed here, restore inside
	// App.restore. Missing any of these means a mutate-then-crash silently loses
	// the change. restore marks dirty on its own; wrap only reset and seed.
	if a.cfg.Persist {
		reset = func() {
			a.Rebuild()
			a.markDirty()
		}

		if seedFn != nil {
			inner := seedFn
			seedFn = func(fixture []byte) (int, error) {
				n, err := inner(fixture)
				if err == nil {
					a.markDirty()
				}

				return n, err
			}
		}
	}

	return admin.NewControl(b, reset, seedFn, a.snapshot, a.restore, a.extraHandler())
}

// Serve binds every listener, starts serving, and blocks until ctx is canceled
// or a listener fails fatally. On cancellation it gracefully shuts the servers
// down, writes the persistence snapshot (if enabled), then closes the live
// providers.
func (a *App) Serve(ctx context.Context) error {
	servers, eps, err := a.buildServers()
	if err != nil {
		return err
	}

	listeners, err := bindListeners(servers)
	if err != nil {
		return err
	}

	errCh := serveAll(servers, listeners)

	// Start the background persistence flusher once the listeners are live, so
	// scheduled/on-request saves run for the whole serving lifetime.
	a.flusher.Start()

	// Start the opt-in Kubernetes staged-lifecycle ticker (default off). Like the
	// flusher, it is lifecycle-managed: started here, stopped in shutdown.
	a.startK8sTicker()

	if !a.cfg.Quiet {
		printBanner(a.out, &eps, a.cfg.Admin, a.persistBanner())
	}

	if a.cfg.EndpointsFile != "" {
		if err := eps.writeFile(a.cfg.EndpointsFile); err != nil {
			return fmt.Errorf("write endpoints file: %w", err)
		}
	}

	select {
	case err := <-errCh:
		// A listener failed fatally. Still tear down cleanly — stop the flusher
		// (final save + drain) and close the providers — so the goroutine and any
		// real engines aren't leaked; then surface the original serve error, not
		// the shutdown error.
		_ = a.shutdown(servers)

		return err
	case <-ctx.Done():
		if !a.cfg.Quiet {
			fmt.Fprintln(a.out, "\nshutting down…")
		}
	}

	return a.shutdown(servers)
}

// buildServers assembles the per-provider (and Kubernetes) HTTP servers and the
// endpoint set advertised to clients.
func (a *App) buildServers() ([]*namedServer, endpointSet, error) {
	var servers []*namedServer

	eps := endpointSet{}

	for _, p := range a.sel {
		var (
			addr   string
			tlsCfg *tls.Config
			isTLS  bool
		)

		switch p {
		case providerAWS:
			addr = net.JoinHostPort(a.cfg.Host, a.cfg.Ports[providerAWS])
			eps.AWS = fmt.Sprintf("http://%s", addr)
		case providerGCP:
			addr = net.JoinHostPort(a.cfg.Host, a.cfg.Ports[providerGCP])
			eps.GCP = fmt.Sprintf("http://%s", addr)
		case providerOCI:
			addr = net.JoinHostPort(a.cfg.Host, a.cfg.Ports[providerOCI])
			eps.OCI = fmt.Sprintf("http://%s", addr)
		case providerAzure:
			addr = net.JoinHostPort(a.cfg.Host, a.cfg.Ports[providerAzure])

			var err error

			if tlsCfg, err = tlsConfig(&a.cfg, addr); err != nil {
				return nil, eps, fmt.Errorf("azure TLS: %w", err)
			}

			isTLS = true
			eps.Azure = fmt.Sprintf("https://%s", addr)
		}

		servers = append(servers, &namedServer{
			name: p,
			srv: &http.Server{
				Addr:              addr,
				Handler:           a.handlerFor(a.backends[p], a.seedFor(p)),
				TLSConfig:         tlsCfg,
				ReadHeaderTimeout: serverReadHeaderTimeout,
			},
			tls: isTLS,
		})
	}

	if a.k8sBackend != nil {
		addr := net.JoinHostPort(a.cfg.Host, a.k8sPort)

		// The cert must certify the advertised host (what clients dial), plus the
		// loopback names, plus any extra --tls-host SANs. k8s uses its own eksprov
		// cert — the --tls-cert override applies only to the Azure listener.
		k8sTLS, err := eksprov.ServingTLSConfig(k8sCertHosts(a.advertiseHost, a.cfg.TLSHosts))
		if err != nil {
			return nil, eps, fmt.Errorf("kubernetes data-plane TLS: %w", err)
		}

		servers = append(servers, &namedServer{
			name: "kubernetes",
			srv: &http.Server{
				Addr:              addr,
				Handler:           a.handlerFor(a.k8sBackend, nil),
				TLSConfig:         k8sTLS,
				ReadHeaderTimeout: serverReadHeaderTimeout,
			},
			tls: true,
		})
		// Show the reachable (advertised) endpoint, not the bind address.
		eps.Kubernetes = fmt.Sprintf("https://%s", net.JoinHostPort(a.advertiseHost, a.k8sPort))
	}

	return servers, eps, nil
}

// shutdown gracefully stops the servers, writes the persistence snapshot after
// they are quiescent (so no in-flight request can mutate state mid-read), and
// only then closes the live providers — engines must stay readable through the
// snapshot.
func (a *App) shutdown(servers []*namedServer) error {
	to := a.cfg.ShutdownTimeout
	if to <= 0 {
		to = defaultShutdownTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), to)
	defer cancel()

	var shutErr error
	for _, s := range servers {
		if err := s.srv.Shutdown(ctx); err != nil && shutErr == nil {
			shutErr = err
		}
	}

	// The flusher owns every automatic save: Stop drains any in-flight periodic
	// save, then performs the single deterministic final save (unless the
	// strategy is manual). This replaces the old unconditional shutdown snapshot,
	// so manual genuinely never saves and no stale tick can rename over the final
	// write. The final save runs while the providers are still live (closed
	// below), keeping the engines readable through the export.
	a.flusher.Stop(ctx)
	a.stopK8sTicker()

	a.rebuildMu.Lock()
	cur := a.providers
	a.rebuildMu.Unlock()
	a.closeProviders(cur)

	return shutErr
}

// startK8sTicker launches the real-time staged-lifecycle ticker when progression
// is enabled. Each tick snapshots the current data-plane server under rebuildMu
// (a reset swaps it) and advances every cluster's Pods. No-op when progression
// is off or the data plane is disabled.
func (a *App) startK8sTicker() {
	if !a.cfg.K8sProgression || a.k8sBackend == nil {
		return
	}

	interval := a.cfg.K8sProgressionInterval
	if interval <= 0 {
		interval = defaultK8sProgressionInterval
	}

	a.k8sTickStop = make(chan struct{})
	a.k8sTickDone = make(chan struct{})

	go func() {
		defer close(a.k8sTickDone)

		t := time.NewTicker(interval)
		defer t.Stop()

		for {
			select {
			case <-a.k8sTickStop:
				return
			case <-t.C:
				a.rebuildMu.Lock()
				k8s := a.k8s
				a.rebuildMu.Unlock()

				// The ticker mutates Pods from this background goroutine, bypassing
				// the HTTP dirty seam, so mark dirty here when a Pod actually
				// advanced a stage — otherwise a --k8s-progression save could lag
				// the live staged state. Only on real change, never on an idle tick.
				if k8s != nil && k8s.TickAll() {
					a.markDirty()
				}
			}
		}
	}()
}

// stopK8sTicker stops the progression ticker and waits for it to drain.
// Idempotent — a no-op when the ticker was never started.
func (a *App) stopK8sTicker() {
	if a.k8sTickStop == nil {
		return
	}

	close(a.k8sTickStop)
	<-a.k8sTickDone
	a.k8sTickStop = nil
}

// namedServer is one endpoint's HTTP server plus how it is served.
type namedServer struct {
	name string
	srv  *http.Server
	tls  bool
}

// bindListeners binds every listener up front so a port clash fails fast, before
// a banner promises endpoints that never came up. A partial failure closes the
// listeners already opened.
func bindListeners(servers []*namedServer) ([]net.Listener, error) {
	listeners := make([]net.Listener, len(servers))

	var lc net.ListenConfig

	for i, s := range servers {
		ln, err := lc.Listen(context.Background(), "tcp", s.srv.Addr)
		if err != nil {
			for _, l := range listeners[:i] {
				l.Close()
			}

			return nil, fmt.Errorf("bind %s (%s): %w", s.name, s.srv.Addr, err)
		}

		listeners[i] = ln
	}

	return listeners, nil
}

// serveAll starts every bound listener in its own goroutine and returns a
// channel reporting the first fatal serve error.
func serveAll(servers []*namedServer, listeners []net.Listener) <-chan error {
	errCh := make(chan error, len(servers))

	for i, s := range servers {
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

	return errCh
}

// dangerWarning returns the non-loopback admin warning, or "" when it doesn't
// apply. reset wipes all state and snapshot dumps it (secrets included), so an
// admin control plane reachable off the loopback is called out.
func dangerWarning(cfg *Config) string {
	if !cfg.Admin || isLoopbackHost(cfg.Host) {
		return ""
	}

	return fmt.Sprintf(
		"warning: --admin control plane is reachable on non-loopback host %q — "+
			"POST /_cloudemu/reset wipes all state, and GET /_cloudemu/snapshot dumps "+
			"all emulated state (including secret values) to any caller; "+
			"pass --admin=false to disable it",
		cfg.Host)
}
