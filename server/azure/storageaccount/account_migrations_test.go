package storageaccount_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAccountMigrationsDefault pins GET accountMigrations/default, which
// azurerm 5.x reads on every storage account refresh and accepts only as 200.
func TestAccountMigrationsDefault(t *testing.T) {
	s := newSettingsServer(t)

	code, out := s.do(http.MethodGet, settingsAcct+"/accountMigrations/default", "")
	require.Equal(t, http.StatusOK, code, string(out))

	var got struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Type       string `json:"type"`
		Properties struct {
			TargetSkuName   string  `json:"targetSkuName"`
			MigrationStatus *string `json:"migrationStatus"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(out, &got))

	assert.Equal(t, settingsAcct+"/accountMigrations/default", got.ID)
	assert.Equal(t, "default", got.Name)
	assert.Equal(t, "Microsoft.Storage/storageAccounts/accountMigrations", got.Type)
	assert.Equal(t, "Standard_GRS", got.Properties.TargetSkuName)
	assert.Nil(t, got.Properties.MigrationStatus, "no migration was ever started")

	// The account is untouched by the read.
	code, out = s.do(http.MethodGet, settingsAcct, "")
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, string(out), `"Standard_GRS"`)
}

func TestAccountMigrationsRoutes(t *testing.T) {
	s := newSettingsServer(t)

	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"list", http.MethodGet, settingsAcct + "/accountMigrations", http.StatusOK},
		{"other name", http.MethodGet, settingsAcct + "/accountMigrations/other", http.StatusNotFound},
		{"put", http.MethodPut, settingsAcct + "/accountMigrations/default", http.StatusMethodNotAllowed},
		{"delete", http.MethodDelete, settingsAcct + "/accountMigrations/default", http.StatusMethodNotAllowed},
		{
			"missing account", http.MethodGet,
			"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/nope/accountMigrations/default",
			http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out := s.do(tc.method, tc.path, "{}")
			assert.Equal(t, tc.want, code, string(out))
		})
	}

	// The account survives the rejected writes.
	code, _ := s.do(http.MethodGet, settingsAcct, "")
	assert.Equal(t, http.StatusOK, code)
}
