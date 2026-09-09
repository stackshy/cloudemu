package iam_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	azureiam "github.com/stackshy/cloudemu/v2/providers/azure/iam"
	"github.com/stackshy/cloudemu/v2/server/azure/iam"
)

// TestDeleteRoleDefinitionNotFoundMessageHasNoInternalPrefix guards the local
// writeCErr helper in operations.go: deleting a role definition that does not
// exist must surface a clean message, not the internal "NotFound: " taxonomy
// prefix that err.Error() on a *cerrors.Error carries.
func TestDeleteRoleDefinitionNotFoundMessageHasNoInternalPrefix(t *testing.T) {
	drv := azureiam.New(config.NewOptions())
	h := iam.New(drv)

	ts := httptest.NewServer(h)
	defer ts.Close()

	url := ts.URL + testScope + "/providers/Microsoft.Authorization/roleDefinitions/missing-role-id"

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
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

	if strings.Contains(body.Error.Message, "NotFound:") {
		t.Errorf("message=%q leaks internal error-code prefix", body.Error.Message)
	}
}
