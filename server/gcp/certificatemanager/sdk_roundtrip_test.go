package certificatemanager_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	certificatemanager "google.golang.org/api/certificatemanager/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*certificatemanager.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := certificatemanager.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("certificatemanager.NewService: %v", err)
	}

	return svc, "mock-project"
}

// TestSDKDNSAuthorizationLifecycle exercises the classic Certificate Manager
// drift point: the computed dnsResourceRecord{name,type,data} must be minted
// once from the domain and stay byte-identical on the create response and every
// later read.
func TestSDKDNSAuthorizationLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/global"
	name := parent + "/dnsAuthorizations/auth"

	op, err := svc.Projects.Locations.DnsAuthorizations.Create(parent, &certificatemanager.DnsAuthorization{
		Domain:      "example.com",
		Description: "prod authorization",
		Labels:      map[string]string{"team": "platform"},
	}).DnsAuthorizationId("auth").Context(ctx).Do()
	if err != nil {
		t.Fatalf("DnsAuthorizations.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done (would hang a Terraform apply)")
	}

	polled, err := svc.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get: %v", err)
	}

	if !polled.Done {
		t.Fatalf("polled operation not done")
	}

	got, err := svc.Projects.Locations.DnsAuthorizations.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("DnsAuthorizations.Get: %v", err)
	}

	if got.Name != name || got.CreateTime == "" || got.UpdateTime == "" {
		t.Fatalf("computed fields missing: name=%q createTime=%q updateTime=%q", got.Name, got.CreateTime, got.UpdateTime)
	}

	if got.Domain != "example.com" || got.Labels["team"] != "platform" || got.Description != "prod authorization" {
		t.Fatalf("inputs not round-tripped: %+v", got)
	}

	rr := got.DnsResourceRecord
	if rr == nil || rr.Type != "CNAME" || rr.Name != "_acme-challenge.example.com" || rr.Data == "" {
		t.Fatalf("dnsResourceRecord not minted correctly: %+v", rr)
	}

	// Stable across reads (no Terraform drift on the computed record).
	second, err := svc.Projects.Locations.DnsAuthorizations.Get(name).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.DnsResourceRecord == nil || second.DnsResourceRecord.Name != rr.Name ||
		second.DnsResourceRecord.Data != rr.Data || second.DnsResourceRecord.Type != rr.Type {
		t.Fatalf("dnsResourceRecord unstable across reads (drift): %+v vs %+v", rr, second.DnsResourceRecord)
	}

	if second.CreateTime != got.CreateTime {
		t.Fatalf("createTime unstable across reads (drift)")
	}

	// Patch description + labels via updateMask -> LRO; domain + record untouched.
	patchOp, err := svc.Projects.Locations.DnsAuthorizations.Patch(name, &certificatemanager.DnsAuthorization{
		Description: "prod authorization v2",
		Labels:      map[string]string{"team": "platform", "env": "prod"},
	}).UpdateMask("description,labels").Context(ctx).Do()
	if err != nil {
		t.Fatalf("DnsAuthorizations.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.DnsAuthorizations.Get(name).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Description != "prod authorization v2" || updated.Labels["env"] != "prod" {
		t.Fatalf("patch not applied: %+v", updated)
	}

	if updated.Domain != "example.com" || updated.DnsResourceRecord == nil ||
		updated.DnsResourceRecord.Data != rr.Data {
		t.Fatalf("masked patch mutated immutable fields: %+v", updated)
	}

	delOp, err := svc.Projects.Locations.DnsAuthorizations.Delete(name).Do()
	if err != nil {
		t.Fatalf("DnsAuthorizations.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("delete operation not done")
	}

	if _, err := svc.Projects.Locations.DnsAuthorizations.Get(name).Do(); err == nil {
		t.Fatalf("expected 404 after delete")
	}
}

// TestSDKManagedCertificateLifecycle covers a managed certificate referencing a
// DNS authorization, the computed managed.state, and its verbatim round-trip.
func TestSDKManagedCertificateLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	parent := "projects/" + project + "/locations/global"
	certName := parent + "/certificates/cert"

	authName := parent + "/dnsAuthorizations/auth"
	if _, err := svc.Projects.Locations.DnsAuthorizations.Create(parent,
		&certificatemanager.DnsAuthorization{Domain: "example.com"}).DnsAuthorizationId("auth").Do(); err != nil {
		t.Fatalf("seed dns authorization: %v", err)
	}

	op, err := svc.Projects.Locations.Certificates.Create(parent, &certificatemanager.Certificate{
		Description: "managed leaf",
		Scope:       "DEFAULT",
		Labels:      map[string]string{"tier": "edge"},
		Managed: &certificatemanager.ManagedCertificate{
			Domains:           []string{"example.com", "www.example.com"},
			DnsAuthorizations: []string{authName},
		},
	}).CertificateId("cert").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Certificates.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	got, err := svc.Projects.Locations.Certificates.Get(certName).Do()
	if err != nil {
		t.Fatalf("Certificates.Get: %v", err)
	}

	if got.Name != certName || got.CreateTime == "" || got.Scope != "DEFAULT" || got.Description != "managed leaf" {
		t.Fatalf("computed/input fields wrong: %+v", got)
	}

	if got.Managed == nil || len(got.Managed.Domains) != 2 || got.Managed.Domains[0] != "example.com" ||
		len(got.Managed.DnsAuthorizations) != 1 || !strings.HasSuffix(got.Managed.DnsAuthorizations[0], "/auth") {
		t.Fatalf("managed block not round-tripped: %+v", got.Managed)
	}

	if got.Managed.State != "PROVISIONING" {
		t.Fatalf("managed.state = %q, want PROVISIONING", got.Managed.State)
	}

	if got.SelfManaged != nil {
		t.Fatalf("selfManaged should be absent on a managed certificate")
	}

	// Stable across reads.
	second, err := svc.Projects.Locations.Certificates.Get(certName).Do()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if second.Managed.State != got.Managed.State || second.CreateTime != got.CreateTime {
		t.Fatalf("computed fields unstable across reads (drift)")
	}

	// List.
	list, err := svc.Projects.Locations.Certificates.List(parent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Certificates) != 1 || !strings.HasSuffix(list.Certificates[0].Name, "/cert") {
		t.Fatalf("list = %+v", list.Certificates)
	}

	// Patch description + labels via updateMask; managed block untouched.
	patchOp, err := svc.Projects.Locations.Certificates.Patch(certName, &certificatemanager.Certificate{
		Description: "managed leaf v2",
		Labels:      map[string]string{"tier": "edge", "owner": "sre"},
	}).UpdateMask("description,labels").Do()
	if err != nil {
		t.Fatalf("Certificates.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.Certificates.Get(certName).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Description != "managed leaf v2" || updated.Labels["owner"] != "sre" {
		t.Fatalf("patch not applied: %+v", updated)
	}

	if updated.Managed == nil || len(updated.Managed.Domains) != 2 {
		t.Fatalf("managed block mutated by masked patch: %+v", updated.Managed)
	}
}

// TestSDKSelfManagedCertificatePrivateKeyNotEchoed verifies that a self-managed
// certificate's write-only pemPrivateKey is accepted on create but never
// returned on a read, matching the real API (the Terraform provider treats it as
// a sensitive input it never reads back).
func TestSDKSelfManagedCertificatePrivateKeyNotEchoed(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/global"
	certName := parent + "/certificates/self"

	op, err := svc.Projects.Locations.Certificates.Create(parent, &certificatemanager.Certificate{
		SelfManaged: &certificatemanager.SelfManagedCertificate{
			PemCertificate: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
			PemPrivateKey:  "-----BEGIN PRIVATE KEY-----\nSECRET\n-----END PRIVATE KEY-----",
		},
	}).CertificateId("self").Do()
	if err != nil {
		t.Fatalf("Certificates.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	got, err := svc.Projects.Locations.Certificates.Get(certName).Do()
	if err != nil {
		t.Fatalf("Certificates.Get: %v", err)
	}

	if got.SelfManaged == nil || got.SelfManaged.PemCertificate == "" {
		t.Fatalf("selfManaged.pemCertificate should round-trip: %+v", got.SelfManaged)
	}

	if got.SelfManaged.PemPrivateKey != "" {
		t.Fatalf("pemPrivateKey must never be echoed on read, got %q", got.SelfManaged.PemPrivateKey)
	}
}

// TestSDKCertificateOneofValidation rejects a certificate that sets both or
// neither of managed / self_managed, matching the real API's 400.
func TestSDKCertificateOneofValidation(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/global"

	both := &certificatemanager.Certificate{
		Managed:     &certificatemanager.ManagedCertificate{Domains: []string{"x.com"}},
		SelfManaged: &certificatemanager.SelfManagedCertificate{PemCertificate: "c", PemPrivateKey: "k"},
	}
	if _, err := svc.Projects.Locations.Certificates.Create(parent, both).CertificateId("both").Do(); err == nil {
		t.Fatalf("expected 400 when both managed and self_managed set")
	}

	neither := &certificatemanager.Certificate{Description: "empty"}
	if _, err := svc.Projects.Locations.Certificates.Create(parent, neither).CertificateId("neither").Do(); err == nil {
		t.Fatalf("expected 400 when neither managed nor self_managed set")
	}
}

// TestSDKCertificateMapLifecycle covers the certificate-map collection.
func TestSDKCertificateMapLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/global"
	mapName := parent + "/certificateMaps/app-map"

	op, err := svc.Projects.Locations.CertificateMaps.Create(parent, &certificatemanager.CertificateMap{
		Description: "app entrypoint",
		Labels:      map[string]string{"app": "web"},
	}).CertificateMapId("app-map").Do()
	if err != nil {
		t.Fatalf("CertificateMaps.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create operation not done")
	}

	got, err := svc.Projects.Locations.CertificateMaps.Get(mapName).Do()
	if err != nil {
		t.Fatalf("CertificateMaps.Get: %v", err)
	}

	if got.Name != mapName || got.CreateTime == "" || got.Description != "app entrypoint" || got.Labels["app"] != "web" {
		t.Fatalf("map fields wrong: %+v", got)
	}

	patchOp, err := svc.Projects.Locations.CertificateMaps.Patch(mapName, &certificatemanager.CertificateMap{
		Description: "app entrypoint v2",
	}).UpdateMask("description").Do()
	if err != nil {
		t.Fatalf("CertificateMaps.Patch: %v", err)
	}

	if !patchOp.Done {
		t.Fatalf("patch operation not done")
	}

	updated, err := svc.Projects.Locations.CertificateMaps.Get(mapName).Do()
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if updated.Description != "app entrypoint v2" || updated.Labels["app"] != "web" {
		t.Fatalf("patch not applied cleanly: %+v", updated)
	}
}

// TestSDKNotFoundAndDuplicate covers 404 and 409 on the certificates collection.
func TestSDKNotFoundAndDuplicate(t *testing.T) {
	svc, project := newSDKClient(t)
	parent := "projects/" + project + "/locations/global"

	if _, err := svc.Projects.Locations.Certificates.Get(parent + "/certificates/ghost").Do(); err == nil {
		t.Fatalf("expected NOT_FOUND")
	}

	c := &certificatemanager.Certificate{
		SelfManaged: &certificatemanager.SelfManagedCertificate{PemCertificate: "c", PemPrivateKey: "k"},
	}

	if _, err := svc.Projects.Locations.Certificates.Create(parent, c).CertificateId("dup").Do(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	if _, err := svc.Projects.Locations.Certificates.Create(parent, c).CertificateId("dup").Do(); err == nil {
		t.Fatalf("expected ALREADY_EXISTS on duplicate create")
	}
}
