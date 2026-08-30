package keyvault_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// selfSignedPolicy is a minimal self-signed CreateCertificate body.
const selfSignedPolicy = `{
  "policy": {
    "key_props": {"exportable": true, "kty": "RSA", "key_size": 2048},
    "secret_props": {"contentType": "application/x-pkcs12"},
    "x509_props": {"subject": "CN=mycert.example.com", "sans": {"dns_names": ["mycert.example.com"]}},
    "issuer": {"name": "Self"}
  }
}`

// certRoundTrip issues a raw Key Vault REST request carrying a bearer token (so
// the challenge preamble is skipped) and returns the status, content type and
// body. Certificate flows have no dedicated SDK in this module, so the tests
// drive the wire protocol directly.
func certRoundTrip(t *testing.T, client *http.Client, method, url, body string) (int, string, []byte) {
	t.Helper()

	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer fake")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, resp.Header.Get("Content-Type"), raw
}

func decodeCert(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode JSON %q: %v", raw, err)
	}

	return m
}

// TestCertificatesRoundTrip exercises the full core certificate CRUD lifecycle
// over raw REST: create (self-signed) → get → list → versions → delete.
func TestCertificatesRoundTrip(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{KeyVault: cloud.KeyVault})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := ts.Client()
	base := ts.URL + "/default"

	// Create.
	status, ct, raw := certRoundTrip(t, client, http.MethodPost, base+"/certificates/mycert/create", selfSignedPolicy)
	if status != http.StatusAccepted {
		t.Fatalf("create status = %d (%s), want 202\nbody: %s", status, ct, raw)
	}

	op := decodeCert(t, raw)
	if op["status"] != "completed" {
		t.Fatalf("create operation status = %v, want completed\nbody: %s", op["status"], raw)
	}

	if tgt, _ := op["target"].(string); !strings.Contains(tgt, "/certificates/mycert/") {
		t.Fatalf("create operation target = %v, want a /certificates/mycert/<version> URL", op["target"])
	}

	// Get current version.
	status, _, raw = certRoundTrip(t, client, http.MethodGet, base+"/certificates/mycert", "")
	if status != http.StatusOK {
		t.Fatalf("get status = %d, want 200\nbody: %s", status, raw)
	}

	bundle := decodeCert(t, raw)
	if cer, _ := bundle["cer"].(string); cer == "" {
		t.Fatalf("get bundle has empty cer (self-signed cert not returned)\nbody: %s", raw)
	}

	if x5t, _ := bundle["x5t"].(string); x5t == "" {
		t.Fatalf("get bundle has empty x5t thumbprint\nbody: %s", raw)
	}

	if kid, _ := bundle["kid"].(string); !strings.Contains(kid, "/keys/mycert/") {
		t.Fatalf("get bundle kid = %v, want a /keys/mycert/<version> URL", bundle["kid"])
	}

	if pol, ok := bundle["policy"].(map[string]any); !ok || pol["issuer"] == nil {
		t.Fatalf("get bundle policy did not round-trip the submitted policy\nbody: %s", raw)
	}

	// List certificates.
	status, _, raw = certRoundTrip(t, client, http.MethodGet, base+"/certificates", "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, want 200\nbody: %s", status, raw)
	}

	list := decodeCert(t, raw)
	if vals, _ := list["value"].([]any); len(vals) != 1 {
		t.Fatalf("list returned %d certificates, want 1\nbody: %s", len(vals), raw)
	}

	// A second create adds a version.
	if status, _, raw = certRoundTrip(t, client, http.MethodPost, base+"/certificates/mycert/create", selfSignedPolicy); status != http.StatusAccepted {
		t.Fatalf("second create status = %d, want 202\nbody: %s", status, raw)
	}

	status, _, raw = certRoundTrip(t, client, http.MethodGet, base+"/certificates/mycert/versions", "")
	if status != http.StatusOK {
		t.Fatalf("list versions status = %d, want 200\nbody: %s", status, raw)
	}

	versions := decodeCert(t, raw)
	if vals, _ := versions["value"].([]any); len(vals) != 2 {
		t.Fatalf("list versions returned %d, want 2\nbody: %s", len(vals), raw)
	}

	// Delete (soft) then confirm the live get is a 404 CertificateNotFound.
	status, _, raw = certRoundTrip(t, client, http.MethodDelete, base+"/certificates/mycert", "")
	if status != http.StatusOK {
		t.Fatalf("delete status = %d, want 200\nbody: %s", status, raw)
	}

	deleted := decodeCert(t, raw)
	if rid, _ := deleted["recoveryId"].(string); !strings.Contains(rid, "/deletedcertificates/mycert") {
		t.Fatalf("delete recoveryId = %v, want a /deletedcertificates/mycert URL", deleted["recoveryId"])
	}

	status, _, raw = certRoundTrip(t, client, http.MethodGet, base+"/certificates/mycert", "")
	if status != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404\nbody: %s", status, raw)
	}
}

// TestCertificateNotFoundIs404 confirms a missing certificate returns a proper
// Key Vault 404 with error code CertificateNotFound.
func TestCertificateNotFoundIs404(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{KeyVault: cloud.KeyVault})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	status, _, raw := certRoundTrip(t, ts.Client(), http.MethodGet, ts.URL+"/default/certificates/does-not-exist", "")
	if status != http.StatusNotFound {
		t.Fatalf("get missing certificate status = %d, want 404\nbody: %s", status, raw)
	}

	body := decodeCert(t, raw)

	errObj, ok := body["error"].(map[string]any)
	if !ok || errObj["code"] != "CertificateNotFound" {
		t.Fatalf("error body = %s, want error.code == CertificateNotFound", raw)
	}
}

// TestCertificatesNotMisroutedToStorage is the core regression: against the
// FULL emulator (Key Vault registered alongside the permissive Table/Blob/Queue
// storage fallbacks), a certificate request must reach the Key Vault handler
// and NOT fall through to storage — which used to answer the azcertificates SDK
// a garbage HTTP 400 odata.error "PartitionKey and RowKey are required".
func TestCertificatesNotMisroutedToStorage(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		KeyVault:     cloud.KeyVault,
		BlobStorage:  cloud.BlobStorage,
		QueueStorage: cloud.QueueStorage,
		TableStorage: cloud.TableStorage,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := ts.Client()

	// POST create must land in Key Vault (202 + cert operation), not the Table
	// Storage handler's 400 odata.error.
	status, ct, raw := certRoundTrip(t, client, http.MethodPost, ts.URL+"/default/certificates/mycert/create", selfSignedPolicy)
	if status != http.StatusAccepted {
		t.Fatalf("full-server create status = %d (%s), want 202 — cert request mis-routed to storage?\nbody: %s", status, ct, raw)
	}

	if bytes.Contains(raw, []byte("odata.error")) || bytes.Contains(raw, []byte("PartitionKey")) {
		t.Fatalf("create response is a storage error, not a Key Vault response:\n%s", raw)
	}

	if op := decodeCert(t, raw); op["status"] != "completed" {
		t.Fatalf("create operation status = %v, want completed\nbody: %s", op["status"], raw)
	}

	// GET /certificates must likewise reach Key Vault (200 list), not the
	// Blob/Queue handler's XML 400.
	status, ct, raw = certRoundTrip(t, client, http.MethodGet, ts.URL+"/default/certificates", "")
	if status != http.StatusOK {
		t.Fatalf("full-server list status = %d (%s), want 200 — mis-routed to storage?\nbody: %s", status, ct, raw)
	}

	if !strings.Contains(ct, "application/json") {
		t.Fatalf("full-server list content-type = %q, want application/json (XML means storage caught it)", ct)
	}
}

// TestCertificatesIsolatedPerVault confirms certificates in two vaults
// (distinguished only by request host) are two independent namespaces.
func TestCertificatesIsolatedPerVault(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{KeyVault: cloud.KeyVault})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := hostRedirectTransport(ts.Listener.Addr().String())

	// Create "shared" in vault A.
	if status, _, raw := certRoundTrip(t, client, http.MethodPost,
		"https://vault-a.vault.azure.net/certificates/shared/create", selfSignedPolicy); status != http.StatusAccepted {
		t.Fatalf("create in vault A status = %d\nbody: %s", status, raw)
	}

	// Vault B must not see it.
	if status, _, raw := certRoundTrip(t, client, http.MethodGet,
		"https://vault-b.vault.azure.net/certificates/shared", ""); status != http.StatusNotFound {
		t.Fatalf("get in vault B status = %d, want 404 (vault A cert leaked)\nbody: %s", status, raw)
	}

	// Vault A still has exactly its own certificate.
	status, _, raw := certRoundTrip(t, client, http.MethodGet,
		"https://vault-a.vault.azure.net/certificates", "")
	if status != http.StatusOK {
		t.Fatalf("list in vault A status = %d\nbody: %s", status, raw)
	}

	if vals, _ := decodeCert(t, raw)["value"].([]any); len(vals) != 1 {
		t.Fatalf("vault A list = %d certificates, want 1\nbody: %s", len(vals), raw)
	}
}
