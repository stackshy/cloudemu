package iam

import "testing"

func TestParseRoleDefinitionFilterQuotedLiterals(t *testing.T) {
	tests := []struct {
		raw, wantName, wantType string
	}{
		{"roleName eq 'Reader'", "Reader", ""},
		{"roleName eq 'Reader and Data Access'", "Reader and Data Access", ""},
		{"roleName eq 'Reader and Data Access' and type eq 'CustomRole'", "Reader and Data Access", "CustomRole"},
		{"type eq 'BuiltInRole' AND roleName eq 'Owner'", "Owner", "BuiltInRole"},
		{"roleName eq 'Bob''s role'", "Bob's role", ""},
		{"roleName eq 'it''s ops and dev' and type eq 'CustomRole'", "it's ops and dev", "CustomRole"},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got := parseRoleDefinitionFilter(tc.raw)
			if got.roleName != tc.wantName || got.roleType != tc.wantType {
				t.Fatalf("got name=%q type=%q, want name=%q type=%q", got.roleName, got.roleType, tc.wantName, tc.wantType)
			}
		})
	}
}
