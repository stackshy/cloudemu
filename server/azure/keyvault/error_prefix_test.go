package keyvault_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	azurekeyvault "github.com/stackshy/cloudemu/v2/providers/azure/keyvault"
	"github.com/stackshy/cloudemu/v2/server/azure/keyvault"
)

// TestGetSecretNotFoundMessageHasNoInternalPrefix guards the data-plane
// writeCErr helper in handler.go: fetching a secret that does not exist must
// surface a clean message, not the internal "NotFound: " taxonomy prefix that
// err.Error() on a *cerrors.Error carries.
func TestGetSecretNotFoundMessageHasNoInternalPrefix(t *testing.T) {
	mock := azurekeyvault.New(config.NewOptions())
	h := keyvault.New(mock)

	ts := httptest.NewServer(h)
	defer ts.Close()

	url := ts.URL + "/myvault/secrets/missing-secret?api-version=7.4"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	req.Header.Set("Authorization", "Bearer fake-token")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want %d", resp.StatusCode, http.StatusNotFound)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Error.Code != "SecretNotFound" {
		t.Errorf("code=%q want SecretNotFound", body.Error.Code)
	}

	if strings.Contains(body.Error.Message, "NotFound:") {
		t.Errorf("message=%q leaks internal error-code prefix", body.Error.Message)
	}
}
