package azure_test

// Routing regression for the AAD bootstrap endpoints (metadata discovery + the
// OAuth2 token endpoint). Both sit on non-/subscriptions/ paths and so are
// ambiguous with the permissive BlobStorage fallback; they MUST be served by
// the aad handlers, not swallowed by Blob. Like the dispatch-ordering tests, the
// discriminator is the "X-Ms-Version" header the Blob handler stamps and the aad
// handlers never do.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMetadataEndpointRoutesToAAD(t *testing.T) {
	ts := fullAzureServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/metadata/endpoints", nil) //nolint:noctx // short-lived test request
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if v := resp.Header.Get("X-Ms-Version"); v != "" {
		t.Fatalf("X-Ms-Version = %q — Blob fallback swallowed /metadata/endpoints", v)
	}

	body, _ := io.ReadAll(resp.Body)

	var doc struct {
		Name            string `json:"name"`
		ResourceManager string `json:"resourceManager"`
		Graph           string `json:"microsoftGraphResourceId"`
		Authentication  struct {
			LoginEndpoint string `json:"loginEndpoint"`
		} `json:"authentication"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode metadata: %v (body=%s)", err, body)
	}

	if doc.Name == "" || doc.ResourceManager == "" || doc.Graph == "" {
		t.Fatalf("metadata missing a required field: %+v", doc)
	}

	// The document must self-reference the emulator host the request reached.
	if !strings.Contains(doc.ResourceManager, ts.Listener.Addr().String()) {
		t.Errorf("resourceManager = %q, want it to reference %q", doc.ResourceManager, ts.Listener.Addr().String())
	}

	if !strings.Contains(doc.Authentication.LoginEndpoint, ts.Listener.Addr().String()) {
		t.Errorf("loginEndpoint = %q, want it to reference the emulator host", doc.Authentication.LoginEndpoint)
	}
}

func TestTokenEndpointRoutesToAAD(t *testing.T) {
	ts := fullAzureServer(t)

	form := strings.NewReader("grant_type=client_credentials&client_id=abc&client_secret=xyz&scope=x/.default")
	req, err := http.NewRequest( //nolint:noctx // short-lived test request
		http.MethodPost,
		ts.URL+"/11111111-1111-1111-1111-111111111111/oauth2/v2.0/token",
		form,
	)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if v := resp.Header.Get("X-Ms-Version"); v != "" {
		t.Fatalf("X-Ms-Version = %q — Blob fallback swallowed the token path", v)
	}

	body, _ := io.ReadAll(resp.Body)

	var tok struct {
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		t.Fatalf("decode token: %v (body=%s)", err, body)
	}

	if tok.TokenType != "Bearer" || tok.ExpiresIn <= 0 {
		t.Fatalf("bad token envelope: %+v", tok)
	}

	if parts := strings.Split(tok.AccessToken, "."); len(parts) != 3 {
		t.Fatalf("access_token has %d segments, want a 3-part JWT", len(parts))
	}
}
