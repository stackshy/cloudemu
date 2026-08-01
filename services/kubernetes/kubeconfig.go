package kubernetes

import (
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/k8spki"
)

// StubToken is the bearer token returned in every cloudemu-rendered
// kubeconfig. The in-memory APIServer accepts any (or no) bearer token, so
// this string is purely a syntactic placeholder so client-go's auth flow
// doesn't trip.
//
//nolint:gosec // G101: placeholder string for an unauthenticated test backend; not a real credential.
const StubToken = "cloudemu-anonymous"

// RenderKubeconfig returns a syntactically-valid kubeconfig YAML pointing at
// apiServerBase + /k8s/<uid>. Callers (AKS ListClusterAdminCredentials and
// the AKS Mock's Kubeconfig method in particular) hand this to clients as the
// cluster's credentials.
//
// apiServerBase is the URL of the SDK-compat httptest server (e.g. the value
// of httptest.Server.URL); cluster's data-plane URL appends /k8s/<uid> to
// that base. clusterName is what the kubeconfig surfaces in its clusters[],
// users[], and contexts[] entries — typically the cloud's own cluster name.
func RenderKubeconfig(apiServerBase, uid, clusterName string) []byte {
	server := apiServerBase + pathPrefix + uid

	// Advertise the real shared CA the data plane is served with (matching EKS
	// and GKE). Over a plain-HTTP base URL client-go ignores it; over the
	// HTTPS `serve` endpoint it validates the handshake — no skip-verify.
	ca := k8spki.CertificatePEM()

	yaml := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: %s
  cluster:
    server: %s
    certificate-authority-data: %s
users:
- name: %s
  user:
    token: %s
contexts:
- name: %s
  context:
    cluster: %s
    user: %s
    namespace: default
current-context: %s
`, clusterName, server, ca, clusterName, StubToken, clusterName, clusterName, clusterName, clusterName)

	return []byte(yaml)
}
