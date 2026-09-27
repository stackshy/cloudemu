package eks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

func dataPlaneVersion(t *testing.T, api *kubernetes.APIServer, uid string) (gitVersion, minor string) {
	t.Helper()

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/k8s/"+uid+"/version", http.NoBody))

	var body struct {
		Minor      string `json:"minor"`
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /version: %v (%s)", err, rec.Body.String())
	}

	return body.GitVersion, body.Minor
}

func TestK8sVersion_FollowsClusterVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	api := kubernetes.NewAPIServer()
	m.SetK8sAPI(api)

	if _, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name: "c1", Version: "1.31", RoleArn: "arn:aws:iam::123456789012:role/eks",
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	uid := m.k8sUIDs["c1"]

	git, minor := dataPlaneVersion(t, api, uid)
	if want := "v" + serverPatchVersion("1.31") + "-eks-"; !strings.HasPrefix(git, want) {
		t.Fatalf("after create: gitVersion %q, want prefix %q (kubelet patch)", git, want)
	}

	if !regexp.MustCompile(`^v1\.31\.\d+-eks-[0-9a-f]{7}$`).MatchString(git) || minor != "31+" {
		t.Fatalf("after create: gitVersion %q minor %q, want v1.31.x-eks-<hash> / 31+", git, minor)
	}

	if _, err := m.UpdateClusterVersion(ctx, "c1", eksdriver.ClusterVersionUpdate{Version: "1.32"}); err != nil {
		t.Fatalf("UpdateClusterVersion: %v", err)
	}

	git, minor = dataPlaneVersion(t, api, uid)
	if !regexp.MustCompile(`^v1\.32\.\d+-eks-[0-9a-f]{7}$`).MatchString(git) || minor != "32+" {
		t.Fatalf("after upgrade: gitVersion %q minor %q, want v1.32.x-eks-<hash> / 32+", git, minor)
	}
}

func TestK8sVersion_DefaultsToEKSDefault(t *testing.T) {
	m := newTestMock()
	api := kubernetes.NewAPIServer()
	m.SetK8sAPI(api)

	if _, err := m.CreateCluster(context.Background(), eksdriver.ClusterConfig{
		Name: "c1", RoleArn: "arn:aws:iam::123456789012:role/eks",
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	git, _ := dataPlaneVersion(t, api, m.k8sUIDs["c1"])
	if !regexp.MustCompile(`^v` + regexp.QuoteMeta(defaultKubernetesVersion) + `\.\d+-eks-`).MatchString(git) {
		t.Fatalf("gitVersion %q does not carry the EKS default %s", git, defaultKubernetesVersion)
	}
}
