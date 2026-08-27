package sshpublickeys_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const apiVersion = "?api-version=2023-09-01"

func newSSHServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{SSHPublicKeys: cloud.VirtualMachines})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func sshKeyPath(name string) string {
	return "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/sshPublicKeys/" + name
}

func putSSHKey(t *testing.T, ts *httptest.Server, name string) {
	t.Helper()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+sshKeyPath(name)+apiVersion,
		strings.NewReader(`{"location":"eastus","properties":{}}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT %s: status %d body=%s", name, resp.StatusCode, dump)
	}
}

// TestGenerateKeyPairReturnsPrivateKey verifies the generateKeyPair action
// returns a non-empty private key (PEM) and public key (OpenSSH), and that the
// generated public key is persisted onto the resource.
func TestGenerateKeyPairReturnsPrivateKey(t *testing.T) {
	ts := newSSHServer(t)
	putSSHKey(t, ts, "key-gen")

	req, _ := http.NewRequest(http.MethodPost, ts.URL+sshKeyPath("key-gen")+"/generateKeyPair"+apiVersion, http.NoBody)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("generateKeyPair status=%d body=%s", resp.StatusCode, dump)
	}

	var out struct {
		PublicKey  string `json:"publicKey"`
		PrivateKey string `json:"privateKey"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.PrivateKey, "PRIVATE KEY") {
		t.Errorf("privateKey is not a PEM block: %q", out.PrivateKey)
	}

	if !strings.HasPrefix(out.PublicKey, "ssh-rsa ") {
		t.Errorf("publicKey is not OpenSSH form: %q", out.PublicKey)
	}

	// The generated public key must be persisted onto the resource.
	getResp, err := ts.Client().Get(ts.URL + sshKeyPath("key-gen") + apiVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	var got struct {
		Properties struct {
			PublicKey string `json:"publicKey"`
		} `json:"properties"`
	}

	if err := json.NewDecoder(getResp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if got.Properties.PublicKey != out.PublicKey {
		t.Errorf("resource publicKey=%q not persisted from generateKeyPair (%q)", got.Properties.PublicKey, out.PublicKey)
	}
}
