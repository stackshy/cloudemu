package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/version"

	"github.com/stackshy/cloudemu/v2/config"
)

func getServerVersion(t *testing.T, api *APIServer, uid string) version.Info {
	t.Helper()

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/k8s/"+uid+"/version", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /version: status %d: %s", rec.Code, rec.Body.String())
	}

	var info version.Info
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode /version: %v", err)
	}

	return info
}

func assertFullVersionInfo(t *testing.T, info version.Info) {
	t.Helper()

	fields := map[string]string{
		"major": info.Major, "minor": info.Minor, "gitVersion": info.GitVersion,
		"gitCommit": info.GitCommit, "gitTreeState": info.GitTreeState, "buildDate": info.BuildDate,
		"goVersion": info.GoVersion, "compiler": info.Compiler, "platform": info.Platform,
	}
	for name, v := range fields {
		if v == "" {
			t.Errorf("/version %s is empty: %+v", name, info)
		}
	}

	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(info.GitCommit) {
		t.Errorf("gitCommit %q is not a 40-char hex sha", info.GitCommit)
	}

	if info.GitTreeState != "clean" {
		t.Errorf("gitTreeState = %q, want clean", info.GitTreeState)
	}

	if info.Platform != "linux/amd64" {
		t.Errorf("platform = %q, want linux/amd64", info.Platform)
	}
}

func TestServerVersion_DefaultForUnparentedCluster(t *testing.T) {
	api := NewAPIServer()
	uid, _ := api.RegisterCluster()

	info := getServerVersion(t, api, uid)
	assertFullVersionInfo(t, info)

	if info.GitVersion != "v"+defaultKubernetesVersion {
		t.Errorf("gitVersion = %q, want v%s", info.GitVersion, defaultKubernetesVersion)
	}

	if info.Major != "1" || info.Minor != "31" {
		t.Errorf("major/minor = %q/%q, want 1/31", info.Major, info.Minor)
	}
}

func TestServerVersion_PerDistribution(t *testing.T) {
	tests := []struct {
		name      string
		dist      Distribution
		in        string
		wantMinor string
		wantGit   string // exact match when set
		wantRE    string // regexp match when set
	}{
		{name: "eks minor only", dist: DistributionEKS, in: "1.31", wantMinor: "31+", wantRE: `^v1\.31\.0-eks-[0-9a-f]{7}$`},
		{name: "aks full", dist: DistributionAKS, in: "1.30.2", wantMinor: "30", wantGit: "v1.30.2"},
		{name: "aks minor only", dist: DistributionAKS, in: "1.31", wantMinor: "31", wantGit: "v1.31.0"},
		{name: "gke full", dist: DistributionGKE, in: "1.31.4-gke.1256000", wantMinor: "31+", wantGit: "v1.31.4-gke.1256000"},
		{name: "gke minor only", dist: DistributionGKE, in: "1.32", wantMinor: "32+", wantRE: `^v1\.32\.0-gke\.\d+$`},
		{name: "upstream", dist: DistributionUpstream, in: "1.33.1", wantMinor: "33", wantGit: "v1.33.1"},
		{name: "leading v accepted", dist: DistributionAKS, in: "v1.29.7", wantMinor: "29", wantGit: "v1.29.7"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := NewAPIServer()
			uid, _ := api.RegisterCluster()

			if !api.SetClusterVersion(uid, tc.dist, tc.in) {
				t.Fatalf("SetClusterVersion(%q) rejected", tc.in)
			}

			info := getServerVersion(t, api, uid)
			assertFullVersionInfo(t, info)

			if info.Major != "1" || info.Minor != tc.wantMinor {
				t.Errorf("major/minor = %q/%q, want 1/%s", info.Major, info.Minor, tc.wantMinor)
			}

			if tc.wantGit != "" && info.GitVersion != tc.wantGit {
				t.Errorf("gitVersion = %q, want %q", info.GitVersion, tc.wantGit)
			}

			if tc.wantRE != "" && !regexp.MustCompile(tc.wantRE).MatchString(info.GitVersion) {
				t.Errorf("gitVersion = %q, want match %s", info.GitVersion, tc.wantRE)
			}
		})
	}
}

func TestServerVersion_EKSHashMatchesGitCommit(t *testing.T) {
	api := NewAPIServer()
	uid, _ := api.RegisterCluster()
	api.SetClusterVersion(uid, DistributionEKS, "1.31")

	info := getServerVersion(t, api, uid)

	hash := info.GitVersion[len(info.GitVersion)-7:]
	if info.GitCommit[:7] != hash {
		t.Errorf("gitCommit %q does not start with the gitVersion hash %q", info.GitCommit, hash)
	}
}

func TestServerVersion_UnparseableKeepsCurrent(t *testing.T) {
	api := NewAPIServer()
	uid, _ := api.RegisterCluster()
	api.SetClusterVersion(uid, DistributionGKE, "1.31.4-gke.100")

	for _, bad := range []string{"", "latest", "-", "1", "1.x", "2.31", "1.31.4-eks.1"} {
		if api.SetClusterVersion(uid, DistributionGKE, bad) {
			t.Errorf("SetClusterVersion(%q) accepted, want rejected", bad)
		}
	}

	if got := getServerVersion(t, api, uid).GitVersion; got != "v1.31.4-gke.100" {
		t.Errorf("gitVersion changed to %q after rejected updates", got)
	}

	if api.SetClusterVersion("no-such-uid", DistributionGKE, "1.31") {
		t.Error("SetClusterVersion on unknown uid reported success")
	}
}

func TestServerVersion_ClustersAreIndependent(t *testing.T) {
	api := NewAPIServer()
	a, _ := api.RegisterCluster()
	b, _ := api.RegisterCluster()

	api.SetClusterVersion(a, DistributionAKS, "1.30.2")
	api.SetClusterVersion(b, DistributionAKS, "1.31.1")

	if got := getServerVersion(t, api, a).GitVersion; got != "v1.30.2" {
		t.Errorf("cluster a gitVersion = %q", got)
	}

	if got := getServerVersion(t, api, b).GitVersion; got != "v1.31.1" {
		t.Errorf("cluster b gitVersion = %q", got)
	}
}

func TestServerVersion_SnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := NewAPIServer()
	uid, _ := src.RegisterCluster()
	src.SetClusterVersion(uid, DistributionEKS, "1.32")
	want := getServerVersion(t, src, uid)

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := NewAPIServer()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := getServerVersion(t, dst, uid); got != want {
		t.Errorf("restored /version = %+v, want %+v", got, want)
	}
}

// TestServerVersion_ScheduledSwitch checks that SetClusterVersionAt keeps the
// current /version until the given instant, then reports the new one, and that
// a snapshot taken mid-switch restores the target version.
func TestServerVersion_ScheduledSwitch(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	api := NewAPIServer()
	uid, _ := api.RegisterCluster()
	api.SetClusterVersion(uid, DistributionEKS, "1.31.4")

	if !api.SetClusterVersionAt(uid, DistributionEKS, "1.32.1", fc, fc.Now().Add(time.Second)) {
		t.Fatal("SetClusterVersionAt reported false for a known cluster")
	}

	if got := getServerVersion(t, api, uid).Minor; got != "31+" {
		t.Fatalf("before switch minor = %q, want 31+", got)
	}

	data, err := api.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	fc.Advance(time.Second)

	if got := getServerVersion(t, api, uid).Minor; got != "32+" {
		t.Fatalf("after switch minor = %q, want 32+", got)
	}

	dst := NewAPIServer()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := getServerVersion(t, dst, uid).Minor; got != "32+" {
		t.Fatalf("restored minor = %q, want 32+", got)
	}

	// A later immediate set drops the pending switch.
	api.SetClusterVersionAt(uid, DistributionEKS, "1.33.0", fc, fc.Now().Add(time.Second))
	api.SetClusterVersion(uid, DistributionEKS, "1.32.1")
	fc.Advance(time.Second)

	if got := getServerVersion(t, api, uid).Minor; got != "32+" {
		t.Fatalf("after overriding set minor = %q, want 32+", got)
	}
}
