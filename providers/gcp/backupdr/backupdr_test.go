package backupdr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	bdrdriver "github.com/stackshy/cloudemu/v2/services/backupdr/driver"
)

const (
	testProject  = "p"
	testLocation = "us-central1"
	testVault    = "vault-1"
)

func newMock(t *testing.T) (*Mock, *config.FakeClock) {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	return New(config.NewOptions(config.WithProjectID(testProject), config.WithClock(clk))), clk
}

func vaultCfg(id string) *bdrdriver.BackupVaultConfig {
	return &bdrdriver.BackupVaultConfig{
		Project: testProject, Location: testLocation, ID: id,
		Description:                            "d",
		Labels:                                 map[string]string{"env": "dev"},
		BackupMinimumEnforcedRetentionDuration: "86400s",
	}
}

func TestCreateMintsOutputFields(t *testing.T) {
	m, clk := newMock(t)
	ctx := context.Background()

	v, op, err := m.CreateBackupVault(ctx, vaultCfg(testVault))
	if err != nil {
		t.Fatalf("CreateBackupVault: %v", err)
	}

	if !op.Done || !strings.HasPrefix(op.Name, "projects/p/locations/us-central1/operations/") {
		t.Fatalf("operation = %+v", op)
	}

	if v.State != stateActive || v.AccessRestriction != accessWithinOrganization || !v.Deletable() {
		t.Fatalf("defaults not applied: %+v", v)
	}

	if v.ServiceAccount != serviceAccount(testProject) || !strings.HasSuffix(v.ServiceAccount, serviceAccountDomain) {
		t.Fatalf("serviceAccount = %q", v.ServiceAccount)
	}

	if v.UID == "" || v.Etag == "" || !v.CreateTime.Equal(clk.Now()) {
		t.Fatalf("uid/etag/createTime not minted: %+v", v)
	}

	// Returned values never alias the store.
	v.Labels["env"] = "mutated"

	got, err := m.GetBackupVault(ctx, testProject, testLocation, testVault)
	if err != nil {
		t.Fatalf("GetBackupVault: %v", err)
	}

	if got.Labels["env"] != "dev" {
		t.Fatalf("store aliased by returned value: %v", got.Labels)
	}

	if _, _, err := m.CreateBackupVault(ctx, vaultCfg(testVault)); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}
}

func TestCreateValidation(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	cases := map[string]func(*bdrdriver.BackupVaultConfig){
		"missing retention":   func(c *bdrdriver.BackupVaultConfig) { c.BackupMinimumEnforcedRetentionDuration = "" },
		"malformed retention": func(c *bdrdriver.BackupVaultConfig) { c.BackupMinimumEnforcedRetentionDuration = "1d" },
		"negative retention":  func(c *bdrdriver.BackupVaultConfig) { c.BackupMinimumEnforcedRetentionDuration = "-5s" },
		"short id":            func(c *bdrdriver.BackupVaultConfig) { c.ID = "ab" },
		"uppercase id":        func(c *bdrdriver.BackupVaultConfig) { c.ID = "Bad_ID" },
		"slash id":            func(c *bdrdriver.BackupVaultConfig) { c.ID = "a/bc" },
		"trailing hyphen id":  func(c *bdrdriver.BackupVaultConfig) { c.ID = "abc-" },
		"leading hyphen id":   func(c *bdrdriver.BackupVaultConfig) { c.ID = "-abc" },
		"long id":             func(c *bdrdriver.BackupVaultConfig) { c.ID = strings.Repeat("a", 64) },
		"zero retention":      func(c *bdrdriver.BackupVaultConfig) { c.BackupMinimumEnforcedRetentionDuration = "0s" },
		"sub-day retention":   func(c *bdrdriver.BackupVaultConfig) { c.BackupMinimumEnforcedRetentionDuration = "86399s" },
		"over 99y retention":  func(c *bdrdriver.BackupVaultConfig) { c.BackupMinimumEnforcedRetentionDuration = "3124202401s" },
		"bad access":          func(c *bdrdriver.BackupVaultConfig) { c.AccessRestriction = "NOPE" },
		"bad inheritance":     func(c *bdrdriver.BackupVaultConfig) { c.BackupRetentionInheritance = "NOPE" },
		"bad effectiveTime":   func(c *bdrdriver.BackupVaultConfig) { c.EffectiveTime = "yesterday" },
		"wildcard location":   func(c *bdrdriver.BackupVaultConfig) { c.Location = anyLocation },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := vaultCfg(testVault)
			mutate(cfg)

			if _, _, err := m.CreateBackupVault(ctx, cfg); !cerrors.IsInvalidArgument(err) {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}
		})
	}

	// The documented range is 1 day to 99 years, inclusive.
	for _, ok := range []string{"86400s", "86400.5s", "3124202400s"} {
		cfg := vaultCfg("ok-" + strings.ReplaceAll(strings.TrimSuffix(ok, "s"), ".", "-"))

		cfg.BackupMinimumEnforcedRetentionDuration = ok

		if _, _, err := m.CreateBackupVault(ctx, cfg); err != nil {
			t.Fatalf("retention %q rejected: %v", ok, err)
		}
	}
}

func TestValidateOnlyDoesNotPersist(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	cfg := vaultCfg(testVault)
	cfg.ValidateOnly = true

	if _, _, err := m.CreateBackupVault(ctx, cfg); err != nil {
		t.Fatalf("validateOnly create: %v", err)
	}

	if _, err := m.GetBackupVault(ctx, testProject, testLocation, testVault); !cerrors.IsNotFound(err) {
		t.Fatalf("validateOnly create persisted the vault: %v", err)
	}
}

func TestUpdateMaskAndEtag(t *testing.T) {
	m, clk := newMock(t)
	ctx := context.Background()

	created, _, err := m.CreateBackupVault(ctx, vaultCfg(testVault))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	clk.Advance(time.Minute)

	patch := &bdrdriver.BackupVaultConfig{
		Project: testProject, Location: testLocation, ID: testVault,
		Description: "new", Labels: map[string]string{"x": "y"}, Etag: created.Etag,
	}

	updated, _, err := m.UpdateBackupVault(ctx, patch, []string{"description"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.Description != "new" || updated.Labels["env"] != "dev" {
		t.Fatalf("mask not honored: %+v", updated)
	}

	if updated.Etag == created.Etag || !updated.UpdateTime.After(created.UpdateTime) {
		t.Fatalf("etag/updateTime not rotated: %q -> %q", created.Etag, updated.Etag)
	}

	// Stale etag (the create-time one) is rejected.
	if _, _, err := m.UpdateBackupVault(ctx, patch, []string{"labels"}); !errors.Is(err, bdrdriver.ErrEtagMismatch) {
		t.Fatalf("stale etag err = %v, want ErrEtagMismatch", err)
	}

	// snake_case mask paths are accepted.
	patch.Etag = ""
	patch.BackupMinimumEnforcedRetentionDuration = "172800s"

	got, _, err := m.UpdateBackupVault(ctx, patch, []string{"backup_minimum_enforced_retention_duration"})
	if err != nil || got.BackupMinimumEnforcedRetentionDuration != "172800s" {
		t.Fatalf("snake_case mask: %v %+v", err, got)
	}

	for _, bad := range [][]string{nil, {"state"}, {"etag"}, {"bogus"}} {
		if _, _, err := m.UpdateBackupVault(ctx, patch, bad); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("mask %v err = %v, want InvalidArgument", bad, err)
		}
	}

	patch.BackupMinimumEnforcedRetentionDuration = "-1s"
	if _, _, err := m.UpdateBackupVault(ctx, patch, []string{fieldRetention}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("negative retention patch err = %v", err)
	}
}

func TestDeleteSemantics(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	v, _, err := m.CreateBackupVault(ctx, vaultCfg(testVault))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	req := &bdrdriver.DeleteBackupVaultRequest{Project: testProject, Location: testLocation, ID: testVault}

	if err := m.SetUsage(testProject, testLocation, testVault, 3, 1024); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}

	if _, err := m.DeleteBackupVault(ctx, req); !cerrors.IsFailedPrecondition(err) || errors.Is(err, bdrdriver.ErrEtagMismatch) {
		t.Fatalf("non-empty delete err = %v, want FailedPrecondition", err)
	}

	req.Etag = "stale"
	if _, err := m.DeleteBackupVault(ctx, req); !errors.Is(err, bdrdriver.ErrEtagMismatch) {
		t.Fatalf("stale etag delete err = %v", err)
	}

	req.Etag = v.Etag
	req.Force = true

	if _, err := m.DeleteBackupVault(ctx, req); err != nil {
		t.Fatalf("force delete: %v", err)
	}

	if _, err := m.DeleteBackupVault(ctx, req); !cerrors.IsNotFound(err) {
		t.Fatalf("second delete err = %v, want NotFound", err)
	}

	req.AllowMissing = true

	if op, err := m.DeleteBackupVault(ctx, req); err != nil || !op.Done {
		t.Fatalf("allowMissing delete: %v %+v", err, op)
	}
}

func TestListScopesAndWildcard(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	for _, loc := range []string{"us-central1", "europe-west1"} {
		cfg := vaultCfg("vault-" + loc)
		cfg.Location = loc

		if _, _, err := m.CreateBackupVault(ctx, cfg); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	one, _ := m.ListBackupVaults(ctx, testProject, "us-central1")
	all, _ := m.ListBackupVaults(ctx, testProject, anyLocation)
	other, _ := m.ListBackupVaults(ctx, "other", anyLocation)

	if len(one) != 1 || len(all) != 2 || len(other) != 0 {
		t.Fatalf("list sizes = %d/%d/%d, want 1/2/0", len(one), len(all), len(other))
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	v, _, err := m.CreateBackupVault(ctx, vaultCfg(testVault))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored, _ := newMock(t)
	if err := restored.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := restored.GetBackupVault(ctx, testProject, testLocation, testVault)
	if err != nil || got.UID != v.UID || got.Etag != v.Etag {
		t.Fatalf("restored vault = %+v (err %v), want uid/etag of %+v", got, err, v)
	}

	_, op, err := restored.CreateBackupVault(ctx, vaultCfg("vault-2"))
	if err != nil || !strings.Contains(op.Name, "/operation-2-") {
		t.Fatalf("opSeq not restored: %v %+v", err, op)
	}
}

func TestCreateAcceptsDocumentedIDs(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	for _, id := range []string{"abc", "1ab", "vault-2", strings.Repeat("a", 63)} {
		if _, _, err := m.CreateBackupVault(ctx, vaultCfg(id)); err != nil {
			t.Fatalf("id %q rejected: %v", id, err)
		}
	}
}

func TestRetentionLock(t *testing.T) {
	m, clk := newMock(t)
	ctx := context.Background()

	lockAt := clk.Now().Add(time.Hour).Format(time.RFC3339)

	cfg := vaultCfg(testVault)
	cfg.BackupMinimumEnforcedRetentionDuration = "172800s"
	cfg.EffectiveTime = lockAt

	if _, _, err := m.CreateBackupVault(ctx, cfg); err != nil {
		t.Fatalf("create: %v", err)
	}

	patch := func(retention, effective string, mask ...string) error {
		_, _, err := m.UpdateBackupVault(ctx, &bdrdriver.BackupVaultConfig{
			Project: testProject, Location: testLocation, ID: testVault,
			BackupMinimumEnforcedRetentionDuration: retention, EffectiveTime: effective,
		}, mask)

		return err
	}

	// Before the effective time the lock is not in force: both may change.
	if err := patch("86400s", "", fieldRetention); err != nil {
		t.Fatalf("pre-lock decrease: %v", err)
	}

	if err := patch("", lockAt, fieldEffectiveTime); err != nil {
		t.Fatalf("pre-lock effectiveTime change: %v", err)
	}

	clk.Advance(2 * time.Hour)

	if err := patch("3600s", "", fieldRetention); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("sub-day retention err = %v, want InvalidArgument", err)
	}

	if err := patch("86399.5s", "", fieldRetention); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("sub-day fractional retention err = %v, want InvalidArgument", err)
	}

	if err := patch("86400s", "", fieldRetention); err != nil {
		t.Fatalf("locked, unchanged retention: %v", err)
	}

	if err := patch("259200s", "", fieldRetention); err != nil {
		t.Fatalf("locked increase: %v", err)
	}

	if err := patch("172800s", "", fieldRetention); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("locked decrease err = %v, want FailedPrecondition", err)
	}

	later := clk.Now().Add(time.Hour).Format(time.RFC3339)
	if err := patch("", later, fieldEffectiveTime); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("locked effectiveTime move err = %v, want FailedPrecondition", err)
	}

	if err := patch("", "", fieldEffectiveTime); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("locked effectiveTime clear err = %v, want FailedPrecondition", err)
	}

	got, err := m.GetBackupVault(ctx, testProject, testLocation, testVault)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.BackupMinimumEnforcedRetentionDuration != "259200s" || got.EffectiveTime != lockAt {
		t.Fatalf("locked vault changed: retention %q effectiveTime %q", got.BackupMinimumEnforcedRetentionDuration, got.EffectiveTime)
	}
}

func TestValidateOnlyRecordsNoOperation(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateBackupVault(ctx, vaultCfg(testVault)); err != nil {
		t.Fatalf("create: %v", err)
	}

	before := len(m.operations.All())

	dry := vaultCfg("dry-run")
	dry.ValidateOnly = true

	_, op, err := m.CreateBackupVault(ctx, dry)
	if err != nil || !op.Done || op.Name != "" {
		t.Fatalf("validateOnly create op = %+v, err %v; want done and unnamed", op, err)
	}

	patch := vaultCfg(testVault)
	patch.ValidateOnly = true

	if _, op, err = m.UpdateBackupVault(ctx, patch, []string{fieldDescription}); err != nil || op.Name != "" {
		t.Fatalf("validateOnly update op = %+v, err %v", op, err)
	}

	for _, req := range []*bdrdriver.DeleteBackupVaultRequest{
		{Project: testProject, Location: testLocation, ID: testVault, ValidateOnly: true},
		{Project: testProject, Location: testLocation, ID: "absent", ValidateOnly: true, AllowMissing: true},
	} {
		if op, err = m.DeleteBackupVault(ctx, req); err != nil || op.Name != "" {
			t.Fatalf("validateOnly delete %s op = %+v, err %v", req.ID, op, err)
		}
	}

	if after := len(m.operations.All()); after != before {
		t.Fatalf("validateOnly recorded %d operations", after-before)
	}

	if _, err := m.GetBackupVault(ctx, testProject, testLocation, testVault); err != nil {
		t.Fatalf("validateOnly delete removed the vault: %v", err)
	}
}

func TestGetOperation(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	_, op, err := m.CreateBackupVault(ctx, vaultCfg(testVault))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := m.GetOperation(ctx, op.Name)
	if err != nil || got.Type != opCreate || got.TargetName != resourceName(testProject, testLocation, testVault) {
		t.Fatalf("GetOperation(%s) = %+v, %v", op.Name, got, err)
	}

	unknown, err := m.GetOperation(ctx, "projects/p/locations/l/operations/nope")
	if err != nil || !unknown.Done || unknown.Type != "" {
		t.Fatalf("unknown op = %+v, %v", unknown, err)
	}
}
