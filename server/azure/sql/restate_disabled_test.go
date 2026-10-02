package sql

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRestatesDisabledOnlyForDefaults(t *testing.T) {
	defaults := map[string]any{propState: stateDisabled, "emailAccountAdmins": false, "emailAddresses": []string{}}

	tests := []struct {
		name, method, body string
		want               bool
	}{
		{"sole state disabled", http.MethodPut, `{"properties":{"state":"Disabled"}}`, true},
		{"sole state any case", http.MethodPatch, `{"properties":{"state":"disabled"}}`, true},
		{"state enabled", http.MethodPut, `{"properties":{"state":"Enabled"}}`, false},
		{"extra property", http.MethodPut,
			`{"properties":{"state":"Disabled","storageContainerPath":"https://x/y"}}`, false},
		{"default-valued extras", http.MethodPut,
			`{"properties":{"state":"Disabled","emailAccountAdmins":false,"emailAddresses":[],"storageEndpoint":null}}`, true},
		{"non-default extra", http.MethodPut, `{"properties":{"state":"Disabled","emailAccountAdmins":true}}`, false},
		{"no state", http.MethodPut, `{"properties":{"storageContainerPath":"https://x/y"}}`, false},
		{"empty body", http.MethodPut, `{}`, false},
		{"get", http.MethodGet, `{"properties":{"state":"Disabled"}}`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/x", strings.NewReader(tc.body))
			if got := restatesDisabled(r, defaults); got != tc.want {
				t.Fatalf("restatesDisabled = %v, want %v", got, tc.want)
			}
		})
	}
}
