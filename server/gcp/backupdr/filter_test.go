package backupdr

import (
	"testing"
	"time"

	bdrdriver "github.com/stackshy/cloudemu/v2/services/backupdr/driver"
)

func TestFilterClauses(t *testing.T) {
	v := &bdrdriver.BackupVault{
		Project: "p", Location: "l", ID: "v1", State: "ACTIVE", Description: "nightly",
		AccessRestriction: "WITHIN_PROJECT", BackupRetentionInheritance: "INHERIT_VAULT_RETENTION",
		Labels: map[string]string{"env": "prod"},
	}

	cases := map[string]bool{
		`name = "v1"`: true,
		`name = 'projects/p/locations/l/backupVaults/v1'`:          true,
		`description = "nightly"`:                                  true,
		`accessRestriction = WITHIN_PROJECT`:                       true,
		`backupRetentionInheritance != "MATCH_BACKUP_EXPIRE_TIME"`: true,
		`labels.env = "dev"`:                                       false,
		`labels.missing = "x"`:                                     false,
		`state = "ACTIVE" AND description != "nightly"`:            false,
	}

	for filter, want := range cases {
		clauses, err := parseFilter(filter)
		if err != nil {
			t.Fatalf("parseFilter(%q): %v", filter, err)
		}

		if got := matchesAll(v, clauses); got != want {
			t.Fatalf("filter %q matched %v, want %v", filter, got, want)
		}
	}

	for _, bad := range []string{
		`name = "a"b"`, `name = a b`, `labels. = "x"`, `= "x"`, `name = ""`, `name`, `name > "a"`,
	} {
		if _, err := parseFilter(bad); err == nil {
			t.Fatalf("parseFilter(%q) accepted an unsupported filter", bad)
		}
	}
}

func TestVaultLessTieBreak(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := bdrdriver.BackupVault{Project: "p", Location: "l", ID: "a", CreateTime: now}
	b := bdrdriver.BackupVault{Project: "p", Location: "l", ID: "b", CreateTime: now}

	if !vaultLess(orderCreateTime, false)(a, b) || vaultLess(orderCreateTime, false)(b, a) {
		t.Fatalf("equal createTime must break ties by name ascending")
	}

	if !vaultLess(orderCreateTime, true)(b, a) || vaultLess(orderCreateTime, true)(a, a) {
		t.Fatalf("desc must reverse the order and stay irreflexive")
	}
}
