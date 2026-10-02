package sql

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func retentionMock(t *testing.T, dbs ...string) *Mock {
	t.Helper()

	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatal(err)
	}

	for _, db := range dbs {
		if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv", Name: db}); err != nil {
			t.Fatal(err)
		}
	}

	return m
}

func TestShortTermRetentionDefaultsAndValidation(t *testing.T) {
	ctx := context.Background()
	m := retentionMock(t, "db1")

	p, err := m.GetShortTermRetention(ctx, "srv", "db1")
	if err != nil || p.RetentionDays != 7 || p.DiffBackupIntervalInHours != 12 {
		t.Fatalf("default = %+v, %v; want 7/12", p, err)
	}

	cases := []struct {
		days, hours int
		ok          bool
	}{
		{14, 24, true}, {35, 12, true}, {1, 12, true}, {36, 12, false}, {-1, 12, false}, {7, 6, false},
	}

	for _, tc := range cases {
		_, err := m.SetShortTermRetention(ctx, &rdsdriver.ShortTermRetentionPolicy{
			Server: "srv", Database: "db1", RetentionDays: tc.days, DiffBackupIntervalInHours: tc.hours,
		})
		if (err == nil) != tc.ok || (!tc.ok && !cerrors.IsInvalidArgument(err)) {
			t.Errorf("Set(%d, %d) err = %v, want ok=%v", tc.days, tc.hours, err, tc.ok)
		}
	}

	if p, _ := m.GetShortTermRetention(ctx, "srv", "db1"); p.RetentionDays != 1 {
		t.Errorf("after sets RetentionDays = %d, want 1 (last valid write)", p.RetentionDays)
	}

	if _, err := m.GetShortTermRetention(ctx, "srv", "nope"); !cerrors.IsNotFound(err) {
		t.Errorf("missing database err = %v, want NotFound", err)
	}
}

func TestRetentionDeleteIsExactAndServerCascades(t *testing.T) {
	ctx := context.Background()
	m := retentionMock(t, "db1", "db10")

	for _, db := range []string{"db1", "db10"} {
		if _, err := m.SetShortTermRetention(ctx, &rdsdriver.ShortTermRetentionPolicy{
			Server: "srv", Database: db, RetentionDays: 21,
		}); err != nil {
			t.Fatal(err)
		}

		if _, err := m.SetLongTermRetention(ctx, &rdsdriver.LongTermRetentionPolicy{
			Server: "srv", Database: db, WeeklyRetention: "P2W",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.DeleteDatabase(ctx, "srv", "db1"); err != nil {
		t.Fatal(err)
	}

	if _, ok := m.str.Get("srv/db1"); ok {
		t.Error("db1 short-term policy survived its database")
	}

	if p, _ := m.GetShortTermRetention(ctx, "srv", "db10"); p.RetentionDays != 21 {
		t.Errorf("db10 policy = %+v, want 21 days kept", p)
	}

	if p, _ := m.GetLongTermRetention(ctx, "srv", "db10"); p.WeeklyRetention != "P2W" || p.MonthlyRetention != "PT0S" {
		t.Errorf("db10 LTR = %+v", p)
	}

	if _, err := m.SetConnectionPolicy(ctx, "srv", "proxy"); err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteCluster(ctx, "srv"); err != nil {
		t.Fatal(err)
	}

	if m.str.Len()+m.ltr.Len()+m.connPolicies.Len() != 0 {
		t.Error("server delete left retention or connection policies behind")
	}
}

func TestConnectionPolicy(t *testing.T) {
	ctx := context.Background()
	m := retentionMock(t)

	if v, err := m.GetConnectionPolicy(ctx, "srv"); err != nil || v != "Default" {
		t.Fatalf("default = %q, %v", v, err)
	}

	if v, err := m.SetConnectionPolicy(ctx, "srv", "redirect"); err != nil || v != "Redirect" {
		t.Errorf("set redirect = %q, %v", v, err)
	}

	if _, err := m.SetConnectionPolicy(ctx, "srv", "fast"); !cerrors.IsInvalidArgument(err) {
		t.Errorf("bad type err = %v", err)
	}

	if _, err := m.GetConnectionPolicy(ctx, "nope"); !cerrors.IsNotFound(err) {
		t.Errorf("missing server err = %v", err)
	}
}

func TestRetentionSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := retentionMock(t, "db1")

	if _, err := m.SetShortTermRetention(ctx, &rdsdriver.ShortTermRetentionPolicy{
		Server: "srv", Database: "db1", RetentionDays: 30, DiffBackupIntervalInHours: 24,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.SetConnectionPolicy(ctx, "srv", "Proxy"); err != nil {
		t.Fatal(err)
	}

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	r := newTestMock()
	if err := r.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	if p, _ := r.GetShortTermRetention(ctx, "srv", "db1"); p.RetentionDays != 30 || p.DiffBackupIntervalInHours != 24 {
		t.Errorf("restored STR = %+v", p)
	}

	if v, _ := r.GetConnectionPolicy(ctx, "srv"); v != "Proxy" {
		t.Errorf("restored connection policy = %q", v)
	}
}
