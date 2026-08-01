package eks

import (
	"crypto/tls"

	"github.com/stackshy/cloudemu/v2/internal/k8spki"
)

// The certificate authority a cluster advertises has to certify the data plane
// it points at, or it is decoration. The CA now lives in internal/k8spki so the
// data-plane serving certificate and all three providers (EKS/AKS/GKE)
// advertise the same authority; these thin wrappers keep the existing EKS and
// cmd/cloudemu call sites unchanged.

// stubCertificate returns the base64 PEM a cluster advertises as its
// certificate authority.
func stubCertificate() string { return k8spki.CertificatePEM() }

// ServingTLSConfig returns a TLS configuration for the Kubernetes data plane,
// carrying a leaf signed by the CA clusters advertise.
func ServingTLSConfig(hosts []string) (*tls.Config, error) {
	return k8spki.ServingTLSConfig(hosts)
}
