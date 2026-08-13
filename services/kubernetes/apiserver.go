// Package kubernetes provides an in-memory Kubernetes data-plane API server.
//
// Each cluster created via cloudemu's EKS, AKS, or GKE control-plane handlers
// registers a fresh ClusterState with a shared APIServer. Clients connect via
// kubeconfigs that point at <base-url>/k8s/<uid> over validated TLS, and the
// upstream Kubernetes REST surface (core /api/v1 plus the apps, batch,
// networking, rbac, storage, autoscaling, discovery, and policy groups under
// /apis) is served below that prefix, with /scale and /status subresources and
// ?watch=true streaming. A synchronous reconcile engine runs on every write so
// controllers materialize Running Pods, Services get Endpoints, and PVCs bind —
// a minikube-like always-converged cluster. This makes a `client-go` round-trip
// against a cloudemu-emulated EKS/AKS/GKE cluster work end-to-end.
package kubernetes

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
)

// pathPrefix is the URL segment that namespaces every cluster's data plane.
// kubeconfigs returned by EKS/AKS/GKE control-plane handlers point at
// <httptest-server>/k8s/<cluster-uid>.
const pathPrefix = "/k8s/"

// APIServer is the shared in-memory Kubernetes data-plane API server. It
// holds independent ClusterState per registered cluster UID and dispatches
// HTTP requests to the right state based on the URL prefix.
//
// One APIServer instance is wired into all three SDK-compat cloud servers
// (awsserver, azureserver, gcpserver) so kubeconfigs from any provider land
// on the same backend — exactly mirroring real-world Kubernetes, where the
// API is identical across EKS, AKS, and GKE.
type APIServer struct {
	mu       sync.RWMutex
	clusters map[string]*ClusterState
	baseURL  string
	// clock is handed to every ClusterState registered after it is set, so a
	// FakeClock injected before RegisterCluster makes the data plane's
	// timestamps deterministic. Defaults to config.RealClock.
	clock config.Clock

	// admissionEnabled and admissionClient configure the opt-in admission
	// webhook chain (see SetAdmissionEnabled, admission.go). Captured onto
	// each ClusterState at RegisterCluster time, the same way the rest of
	// this server's options are threaded down.
	admissionEnabled bool
	admissionClient  *http.Client
}

// NewAPIServer returns an empty APIServer with no registered clusters.
func NewAPIServer() *APIServer {
	return &APIServer{clusters: make(map[string]*ClusterState), clock: config.RealClock{}}
}

// SetClock sets the clock handed to ClusterStates registered from now on. Wire
// a config.FakeClock before RegisterCluster to make all data-plane timestamps
// deterministic.
func (s *APIServer) SetClock(c config.Clock) {
	if c == nil {
		return
	}

	s.mu.Lock()
	s.clock = c
	s.mu.Unlock()
}

// RegisterCluster allocates fresh state for a new Kubernetes-flavored cluster
// and returns its generated UID. The UID is the path segment that goes into the
// kubeconfig's server URL — kubeconfig "server" becomes "<base>/k8s/<uid>".
// This is what the EKS/AKS/GKE control planes call.
func (s *APIServer) RegisterCluster() (string, *ClusterState) {
	return s.RegisterClusterWithFlavor(FlavorKubernetes)
}

// RegisterClusterWithFlavor allocates fresh state for a new cluster of the given
// flavor. FlavorOpenShift additionally serves the *.openshift.io groups and
// seeds the OpenShift identity singletons — the ROSA/ARO control planes call
// this to back an OpenShift cluster with the same shared data plane.
func (s *APIServer) RegisterClusterWithFlavor(flavor Flavor) (string, *ClusterState) {
	uid := newUID()

	s.mu.Lock()
	state := newClusterState(s.clock, s.admissionEnabled, s.admissionClient, flavor)
	s.clusters[uid] = state
	s.mu.Unlock()

	return uid, state
}

// SetAdmissionEnabled turns the admission webhook chain on or off for
// clusters registered after the call. Default is false: MutatingWebhook/
// ValidatingWebhookConfiguration objects still store and round-trip through
// `kubectl apply`, but are never invoked — a create/update/patch behaves
// exactly as before. Outbound HTTPS calls to webhook endpoints fight
// cloudemu's zero-network, deterministic-by-default pillar, so this is
// opt-in rather than derived from the presence of webhook configs.
func (s *APIServer) SetAdmissionEnabled(enabled bool) {
	s.mu.Lock()
	s.admissionEnabled = enabled
	s.mu.Unlock()
}

// SetAdmissionHTTPClient overrides the HTTP client used to call admission
// webhooks for clusters registered after the call. Tests use this to inject
// a client that trusts an httptest.NewTLSServer's certificate instead of
// wiring up real TLS trust for a fake webhook endpoint.
func (s *APIServer) SetAdmissionHTTPClient(c *http.Client) {
	s.mu.Lock()
	s.admissionClient = c
	s.mu.Unlock()
}

// DeregisterCluster removes a cluster's state. Called by control-plane
// handlers on DeleteCluster. Idempotent.
func (s *APIServer) DeregisterCluster(uid string) {
	s.mu.Lock()
	delete(s.clusters, uid)
	s.mu.Unlock()
}

// Lookup returns the ClusterState for uid, or nil if it doesn't exist.
func (s *APIServer) Lookup(uid string) *ClusterState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.clusters[uid]
}

// SetBaseURL records the URL at which the APIServer is reachable — typically
// the URL of the httptest server it's registered on. Control-plane handlers
// (EKS/AKS/GKE) read this back via BaseURL() when rendering kubeconfigs so
// the kubeconfig's "server:" field points at a host that actually answers.
//
// Callers set this after httptest.NewServer returns, before issuing any
// CreateCluster RPC that needs to emit a working kubeconfig.
func (s *APIServer) SetBaseURL(url string) {
	s.mu.Lock()
	s.baseURL = url
	s.mu.Unlock()
}

// BaseURL returns the URL set by SetBaseURL, or "" if none was set.
func (s *APIServer) BaseURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.baseURL
}

// Matches reports whether r is rooted under the /k8s/ prefix and should be
// served by this APIServer. Used by the SDK-compat cloud servers to route
// requests away from S3/EC2/EKS-control-plane and into the data plane.
func (*APIServer) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, pathPrefix)
}

// ServeHTTP peels off /k8s/{uid} and dispatches the remainder of the URL
// into the matching ClusterState's handler. Unknown UIDs return 404 in the
// Kubernetes Status shape so client-go decodes the error correctly.
func (s *APIServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// OpenAPI is cluster-independent and its v3 serverRelativeURL follow-ups
	// arrive without the /k8s/<uid> prefix, so answer them here before the UID
	// dispatch (which would 404 on the missing cluster segment).
	if serveOpenAPI(w, r) {
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, pathPrefix)

	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		writeNotFound(w, "k8s api: cluster UID missing from path")

		return
	}

	uid := rest[:slash]

	state := s.Lookup(uid)
	if state == nil {
		writeNotFound(w, "k8s api: unknown cluster "+uid)

		return
	}

	// Rewrite the request URL so downstream handlers see the standard
	// Kubernetes paths (/api/v1/..., /apis/apps/v1/...) without the cluster
	// prefix.
	r.URL.Path = rest[slash:]

	// The OpenShift OAuth server (oc login) needs the cluster's ABSOLUTE URL to
	// advertise its endpoints, so it is dispatched here — where the host and UID
	// are still known — rather than from ClusterState, which only sees the
	// stripped path.
	if state.flavor == FlavorOpenShift && isOpenShiftOAuthPath(r.URL.Path) {
		state.serveOAuth(w, r, requestScheme(r)+"://"+r.Host+pathPrefix+uid)

		return
	}

	state.ServeHTTP(w, r)
}

// requestScheme returns "https" when the request arrived over TLS, else "http".
// The OAuth metadata endpoints must be absolute and scheme-correct so oc follows
// them back to the same server.
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}

	return "http"
}

// newUID returns a fresh 32-char lowercase hex string used as the cluster's
// data-plane identifier in the kubeconfig.
func newUID() string {
	var b [16]byte

	_, _ = rand.Read(b[:])

	return hex.EncodeToString(b[:])
}
