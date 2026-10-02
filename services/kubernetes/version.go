package kubernetes

import (
	"crypto/sha1" //nolint:gosec // derives a git-commit-shaped id, not a security hash
	"encoding/hex"
	"regexp"
	"runtime"
	"time"

	"k8s.io/apimachinery/pkg/version"

	"github.com/stackshy/cloudemu/v2/config"
)

// defaultKubernetesVersion is what /version reports for a cluster that no cloud
// control plane has given a version, such as one on the standalone Kubernetes
// port. It matches the kubelet version the synthetic nodes report.
const defaultKubernetesVersion = "1.31.0"

// Distribution picks which managed-Kubernetes build a cluster's /version looks
// like. Each cloud stamps its own suffix on gitVersion and some mark the minor
// with a "+".
type Distribution string

// Supported distributions.
const (
	DistributionUpstream Distribution = "upstream"
	DistributionEKS      Distribution = "eks"
	DistributionAKS      Distribution = "aks"
	DistributionGKE      Distribution = "gke"
)

const (
	// buildDate is a fixed, parseable build timestamp. The emulator has no
	// real build of the apiserver, and a fixed value keeps /version stable.
	buildDate = "2025-01-15T00:00:00Z"
	// serverPlatform is what every managed control plane reports.
	serverPlatform = "linux/amd64"
	// eksHashLen is the length of the short commit hash in an EKS gitVersion.
	eksHashLen = 7
	// gkeDefaultBuild is the -gke.N build used when a GKE version has none.
	gkeDefaultBuild = "-gke.0"
)

// versionPattern matches "1.<minor>", "1.<minor>.<patch>" and GKE's
// "1.<minor>.<patch>-gke.<n>", with an optional leading "v".
var versionPattern = regexp.MustCompile(`^v?1\.(\d+)(?:\.(\d+))?(-gke\.\d+)?$`)

// serverVersionFor builds the /version body for a cluster of distribution d at
// Kubernetes version v. It reports false when v is not a concrete 1.x version
// (for example "latest", "-" or an empty string), so callers keep whatever the
// cluster reported before.
func serverVersionFor(d Distribution, v string) (version.Info, bool) {
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return version.Info{}, false
	}

	minor, patch, gkeBuild := m[1], m[2], m[3]
	if patch == "" {
		patch = "0"
	}

	if gkeBuild != "" && d != DistributionGKE {
		return version.Info{}, false
	}

	base := "v1." + minor + "." + patch
	minorField := minor
	commit := commitFor(string(d) + ":" + base + gkeBuild)

	var gitVersion string

	switch d {
	case DistributionEKS:
		// EKS: {"minor":"31+","gitVersion":"v1.31.4-eks-2d5f260"}.
		minorField += "+"
		gitVersion = base + "-eks-" + commit[:eksHashLen]
	case DistributionGKE:
		// GKE: {"minor":"31+","gitVersion":"v1.31.4-gke.1256000"}.
		if gkeBuild == "" {
			gkeBuild = gkeDefaultBuild
		}

		minorField += "+"
		gitVersion = base + gkeBuild
	case DistributionAKS, DistributionUpstream:
		// AKS runs upstream builds: {"minor":"31","gitVersion":"v1.31.2"}.
		gitVersion = base
	}

	return version.Info{
		Major:        "1",
		Minor:        minorField,
		GitVersion:   gitVersion,
		GitCommit:    commit,
		GitTreeState: "clean",
		BuildDate:    buildDate,
		GoVersion:    runtime.Version(),
		Compiler:     runtime.Compiler,
		Platform:     serverPlatform,
	}, true
}

// defaultServerVersion is the /version of a cluster with no cloud parent.
func defaultServerVersion() version.Info {
	info, _ := serverVersionFor(DistributionUpstream, defaultKubernetesVersion)

	return info
}

// commitFor derives a stable 40-char hex commit id from seed, so the same
// cluster version always reports the same gitCommit.
func commitFor(seed string) string {
	sum := sha1.Sum([]byte(seed)) //nolint:gosec // not used for security

	return hex.EncodeToString(sum[:])
}

// SetClusterVersion sets the Kubernetes version the cluster uid reports on
// /version, formatted the way distribution d's control plane formats it. The
// EKS, AKS and GKE providers call it on create and on every version change.
// It drops any switch scheduled by SetClusterVersionAt. It reports false,
// leaving the cluster as it was, when uid is unknown or v is not a concrete
// 1.x version.
func (s *APIServer) SetClusterVersion(uid string, d Distribution, v string) bool {
	state := s.Lookup(uid)
	if state == nil {
		return false
	}

	info, ok := serverVersionFor(d, v)
	if !ok {
		return false
	}

	state.mu.Lock()
	state.serverVersion = info
	state.pendingVersion = nil
	state.mu.Unlock()

	return true
}

// SetClusterVersionAt schedules a version change: the cluster keeps reporting
// its current version until clock reaches at, then reports v. It models a
// control-plane upgrade that is still in progress. When at is not in the
// future it behaves like SetClusterVersion. The return value is as for
// SetClusterVersion.
func (s *APIServer) SetClusterVersionAt(uid string, d Distribution, v string, clock config.Clock, at time.Time) bool {
	if clock == nil || !clock.Now().Before(at) {
		return s.SetClusterVersion(uid, d, v)
	}

	state := s.Lookup(uid)
	if state == nil {
		return false
	}

	info, ok := serverVersionFor(d, v)
	if !ok {
		return false
	}

	state.mu.Lock()
	state.pendingVersion = &pendingServerVersion{info: info, clock: clock, at: at}
	state.mu.Unlock()

	return true
}

// pendingServerVersion is a version change scheduled by SetClusterVersionAt.
type pendingServerVersion struct {
	info  version.Info
	clock config.Clock
	at    time.Time
}

// ServerVersion returns what this cluster reports on /version.
func (s *ClusterState) ServerVersion() version.Info {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if p := s.pendingVersion; p != nil && !p.clock.Now().Before(p.at) {
		return p.info
	}

	return s.serverVersion
}

// targetServerVersionLocked is the version the cluster reports once any
// scheduled switch has happened. A snapshot stores this, since the settle
// windows that drive the switch are not persisted. Caller holds s.mu.
func (s *ClusterState) targetServerVersionLocked() version.Info {
	if s.pendingVersion != nil {
		return s.pendingVersion.info
	}

	return s.serverVersion
}
