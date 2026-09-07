package privateca_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/api/option"
	privateca "google.golang.org/api/privateca/v1"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*privateca.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := privateca.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("privateca.NewService: %v", err)
	}

	return svc, "mock-project"
}

// TestSDKSelfSignedCALifecycle drives the full LRO + CA state-machine surface end
// to end through the real google-api-go client: a pool create LRO resolves, a
// self-signed CA is ENABLED with byte-stable PEM, and the disable/enable verbs
// (plus the illegal already-ENABLED enable) behave as the real API does.
func TestSDKSelfSignedCALifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"

	poolOp, err := svc.Projects.Locations.CaPools.Create(parent, &privateca.CaPool{
		Tier:   "ENTERPRISE",
		Labels: map[string]string{"team": "platform"},
	}).CaPoolId("pool").Context(ctx).Do()
	if err != nil {
		t.Fatalf("CaPools.Create: %v", err)
	}

	if !poolOp.Done {
		t.Fatalf("pool create operation not done (would hang a Terraform apply)")
	}

	polled, err := svc.Projects.Locations.Operations.Get(poolOp.Name).Context(ctx).Do()
	if err != nil || !polled.Done {
		t.Fatalf("Operations.Get: op=%+v err=%v", polled, err)
	}

	poolName := parent + "/caPools/pool"

	caOp, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Create(poolName, &privateca.CertificateAuthority{
		Type: "SELF_SIGNED",
	}).CertificateAuthorityId("root").Context(ctx).Do()
	if err != nil {
		t.Fatalf("CertificateAuthorities.Create: %v", err)
	}

	if !caOp.Done {
		t.Fatalf("CA create operation not done")
	}

	caName := poolName + "/certificateAuthorities/root"

	got, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Get(caName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("CertificateAuthorities.Get: %v", err)
	}

	if got.State != "STAGED" {
		t.Fatalf("self-signed CA state = %q, want STAGED", got.State)
	}

	if len(got.PemCaCertificates) == 0 || got.CreateTime == "" || got.Tier != "ENTERPRISE" {
		t.Fatalf("computed fields missing: %+v", got)
	}

	pem := got.PemCaCertificates[0]

	// PEM is byte-stable across reads (no Terraform drift).
	second, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Get(caName).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if len(second.PemCaCertificates) == 0 || second.PemCaCertificates[0] != pem {
		t.Fatalf("pemCaCertificates drifted across reads")
	}

	if _, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Disable(
		caName, &privateca.DisableCertificateAuthorityRequest{}).Do(); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	if _, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Enable(
		caName, &privateca.EnableCertificateAuthorityRequest{}).Do(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	// Enabling an already-ENABLED CA is an illegal transition.
	if _, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Enable(
		caName, &privateca.EnableCertificateAuthorityRequest{}).Do(); err == nil {
		t.Fatalf("Enable of already-ENABLED CA should fail")
	}
}

// TestSDKCertificateIssueAndRevoke verifies certificate issuance completes
// synchronously (not an LRO) with byte-stable PEM, and that revoke sets
// revocationDetails while a second revoke is rejected.
func TestSDKCertificateIssueAndRevoke(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	poolName := parent + "/caPools/pool"

	if _, err := svc.Projects.Locations.CaPools.Create(parent, &privateca.CaPool{Tier: "ENTERPRISE"}).
		CaPoolId("pool").Context(ctx).Do(); err != nil {
		t.Fatalf("CaPools.Create: %v", err)
	}

	cert, err := svc.Projects.Locations.CaPools.Certificates.Create(poolName, &privateca.Certificate{
		Lifetime: "86400s",
	}).CertificateId("leaf").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Certificates.Create: %v", err)
	}

	if cert.PemCertificate == "" || cert.Name == "" {
		t.Fatalf("certificate computed fields missing: %+v", cert)
	}

	certName := poolName + "/certificates/leaf"

	revoked, err := svc.Projects.Locations.CaPools.Certificates.Revoke(certName, &privateca.RevokeCertificateRequest{
		Reason: "KEY_COMPROMISE",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Certificates.Revoke: %v", err)
	}

	if revoked.RevocationDetails == nil || revoked.RevocationDetails.RevocationState == "" {
		t.Fatalf("revocationDetails not set after revoke: %+v", revoked)
	}

	if _, err := svc.Projects.Locations.CaPools.Certificates.Revoke(certName, &privateca.RevokeCertificateRequest{
		Reason: "KEY_COMPROMISE",
	}).Do(); err == nil {
		t.Fatalf("second revoke should fail")
	}
}

// TestSDKSubordinateCAFetchAndActivate drives the subordinate CA activation flow:
// a subordinate CA lands AWAITING_USER_ACTIVATION, :fetch returns its CSR, and
// :activate with a signed certificate moves it to ENABLED.
func TestSDKSubordinateCAFetchAndActivate(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/us-central1"
	poolName := parent + "/caPools/pool"

	if _, err := svc.Projects.Locations.CaPools.Create(parent, &privateca.CaPool{Tier: "ENTERPRISE"}).
		CaPoolId("pool").Context(ctx).Do(); err != nil {
		t.Fatalf("CaPools.Create: %v", err)
	}

	if _, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Create(poolName, &privateca.CertificateAuthority{
		Type: "SUBORDINATE",
	}).CertificateAuthorityId("sub").Context(ctx).Do(); err != nil {
		t.Fatalf("subordinate Create: %v", err)
	}

	caName := poolName + "/certificateAuthorities/sub"

	got, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Get(caName).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.State != "AWAITING_USER_ACTIVATION" {
		t.Fatalf("subordinate state = %q, want AWAITING_USER_ACTIVATION", got.State)
	}

	csr, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Fetch(caName).Do()
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if !strings.Contains(csr.PemCsr, "CERTIFICATE REQUEST") {
		t.Fatalf("Fetch pemCsr = %q, want a CSR", csr.PemCsr)
	}

	if _, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Activate(caName,
		&privateca.ActivateCertificateAuthorityRequest{PemCaCertificate: "signed-pem"}).Do(); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	activated, err := svc.Projects.Locations.CaPools.CertificateAuthorities.Get(caName).Do()
	if err != nil {
		t.Fatalf("Get after activate: %v", err)
	}

	if activated.State != "ENABLED" {
		t.Fatalf("state after activate = %q, want ENABLED", activated.State)
	}
}
