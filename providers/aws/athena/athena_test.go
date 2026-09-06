package athena

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(1_700_000_000, 0))))

	return New(opts)
}

func requireNoError(t *testing.T, err error, msg string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestSeedPrimaryWorkGroupAndDefaultCatalog(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	wg, err := m.GetWorkGroup(ctx, "primary")
	requireNoError(t, err, "GetWorkGroup primary")

	if wg.State != driver.WorkGroupStateEnabled {
		t.Fatalf("primary state = %q", wg.State)
	}

	dc, err := m.GetDataCatalog(ctx, "")
	requireNoError(t, err, "GetDataCatalog default")

	if dc.Name != driver.DefaultDataCatalog {
		t.Fatalf("default catalog = %q", dc.Name)
	}
}

func TestCreateWorkGroupMaterializesDefaults(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	err := m.CreateWorkGroup(ctx, driver.WorkGroup{
		Name: "wg",
		Configuration: driver.WorkGroupConfiguration{
			// Explicit false must survive; the other booleans are unset and
			// should materialize to their real defaults.
			EnforceWorkGroupConfiguration: ptr(false),
		},
	})
	requireNoError(t, err, "CreateWorkGroup")

	wg, err := m.GetWorkGroup(ctx, "wg")
	requireNoError(t, err, "GetWorkGroup")

	cfg := wg.Configuration
	if cfg.EnforceWorkGroupConfiguration == nil || *cfg.EnforceWorkGroupConfiguration {
		t.Fatalf("explicit-false enforce not preserved: %v", cfg.EnforceWorkGroupConfiguration)
	}

	if cfg.PublishCloudWatchMetricsEnabled == nil || *cfg.PublishCloudWatchMetricsEnabled {
		t.Fatalf("publish default should be false: %v", cfg.PublishCloudWatchMetricsEnabled)
	}

	if cfg.RequesterPaysEnabled == nil || *cfg.RequesterPaysEnabled {
		t.Fatalf("requester default should be false: %v", cfg.RequesterPaysEnabled)
	}

	if cfg.EngineVersion.SelectedEngineVersion != driver.EngineVersionAuto ||
		cfg.EngineVersion.EffectiveEngineVersion != driver.DefaultEffectiveEngineVersion {
		t.Fatalf("engine version not materialized: %+v", cfg.EngineVersion)
	}

	if wg.CreationTime.IsZero() {
		t.Fatalf("creation time not stamped")
	}
}

func TestEngineVersionExplicitSelectionIsEffective(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	err := m.CreateWorkGroup(ctx, driver.WorkGroup{
		Name: "wg",
		Configuration: driver.WorkGroupConfiguration{
			EngineVersion: driver.EngineVersion{SelectedEngineVersion: "Athena engine version 2"},
		},
	})
	requireNoError(t, err, "CreateWorkGroup")

	wg, err := m.GetWorkGroup(ctx, "wg")
	requireNoError(t, err, "GetWorkGroup")

	if wg.Configuration.EngineVersion.EffectiveEngineVersion != "Athena engine version 2" {
		t.Fatalf("effective should mirror explicit selection: %+v", wg.Configuration.EngineVersion)
	}
}

func TestCreateWorkGroupDuplicateAndBadCutoff(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateWorkGroup(ctx, driver.WorkGroup{Name: "wg"}), "CreateWorkGroup")

	if err := m.CreateWorkGroup(ctx, driver.WorkGroup{Name: "wg"}); err == nil {
		t.Fatalf("expected duplicate error")
	}

	err := m.CreateWorkGroup(ctx, driver.WorkGroup{
		Name:          "small",
		Configuration: driver.WorkGroupConfiguration{BytesScannedCutoffPerQuery: ptr(int64(5))},
	})
	if err == nil {
		t.Fatalf("expected cutoff-below-minimum error")
	}
}

func TestUpdateWorkGroupConfigurationDelta(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateWorkGroup(ctx, driver.WorkGroup{
		Name: "wg",
		Configuration: driver.WorkGroupConfiguration{
			BytesScannedCutoffPerQuery: ptr(int64(20_000_000)),
			ResultConfiguration:        &driver.ResultConfiguration{OutputLocation: "s3://a/"},
		},
	}), "CreateWorkGroup")

	// Delta: remove cutoff, flip enforce, change output location; leave publish
	// untouched (nil setter).
	requireNoError(t, m.UpdateWorkGroup(ctx, "wg", driver.WorkGroupUpdate{
		Description: ptr("d"),
		State:       ptr(driver.WorkGroupStateDisabled),
		ConfigurationUpdates: &driver.WorkGroupConfigurationUpdates{
			RemoveBytesScannedCutoffPerQuery: true,
			EnforceWorkGroupConfiguration:    ptr(false),
			ResultConfigurationUpdates:       &driver.ResultConfigurationUpdates{OutputLocation: "s3://b/"},
		},
	}), "UpdateWorkGroup")

	wg, err := m.GetWorkGroup(ctx, "wg")
	requireNoError(t, err, "GetWorkGroup")

	if wg.Configuration.BytesScannedCutoffPerQuery != nil {
		t.Fatalf("cutoff not removed")
	}

	if wg.Configuration.EnforceWorkGroupConfiguration == nil || *wg.Configuration.EnforceWorkGroupConfiguration {
		t.Fatalf("enforce delta not applied")
	}

	if wg.Configuration.ResultConfiguration.OutputLocation != "s3://b/" {
		t.Fatalf("output location not updated: %q", wg.Configuration.ResultConfiguration.OutputLocation)
	}

	if wg.State != driver.WorkGroupStateDisabled || wg.Description != "d" {
		t.Fatalf("top-level state/description not updated: %q %q", wg.State, wg.Description)
	}
}

func TestDeleteWorkGroupPrimaryRejectedAndNonEmpty(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if err := m.DeleteWorkGroup(ctx, "primary", false); err == nil {
		t.Fatalf("primary must not be deletable")
	}

	requireNoError(t, m.CreateWorkGroup(ctx, driver.WorkGroup{Name: "wg"}), "CreateWorkGroup")
	_, err := m.CreateNamedQuery(ctx, driver.NamedQuery{Name: "q", Database: "d", QueryString: "SELECT 1", WorkGroup: "wg"})
	requireNoError(t, err, "CreateNamedQuery")

	if err := m.DeleteWorkGroup(ctx, "wg", false); err == nil {
		t.Fatalf("non-empty workgroup must reject non-recursive delete")
	}

	requireNoError(t, m.DeleteWorkGroup(ctx, "wg", true), "recursive delete")

	if _, err := m.GetWorkGroup(ctx, "wg"); err == nil {
		t.Fatalf("workgroup should be gone")
	}
}

func TestNamedQueryByteExact(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	const q = "SELECT  a,\n  b\nFROM t  --keep spaces\n"

	id, err := m.CreateNamedQuery(ctx, driver.NamedQuery{Name: "n", Database: "d", QueryString: q})
	requireNoError(t, err, "CreateNamedQuery")

	got, err := m.GetNamedQuery(ctx, id)
	requireNoError(t, err, "GetNamedQuery")

	if got.QueryString != q {
		t.Fatalf("query not byte-exact: %q", got.QueryString)
	}

	if got.WorkGroup != driver.DefaultWorkGroup {
		t.Fatalf("default workgroup = %q", got.WorkGroup)
	}

	requireNoError(t, m.DeleteNamedQuery(ctx, id), "DeleteNamedQuery")

	if _, err := m.GetNamedQuery(ctx, id); err == nil {
		t.Fatalf("named query should be gone")
	}
}

func TestQueryExecutionImmediateSucceededAndDDL(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, err := m.StartQueryExecution(ctx, driver.StartQueryExecutionInput{
		QueryString:         "CREATE DATABASE analytics",
		ResultConfiguration: &driver.ResultConfiguration{OutputLocation: "s3://out/"},
	})
	requireNoError(t, err, "StartQueryExecution")

	qe, err := m.GetQueryExecution(ctx, id)
	requireNoError(t, err, "GetQueryExecution")

	if qe.Status.State != driver.QueryStateSucceeded {
		t.Fatalf("state = %q reason=%q", qe.Status.State, qe.Status.StateChangeReason)
	}

	if qe.StatementType != driver.StatementTypeDDL {
		t.Fatalf("statement type = %q", qe.StatementType)
	}

	db, err := m.GetDatabase(ctx, "", "analytics")
	requireNoError(t, err, "GetDatabase")

	if db.Name != "analytics" {
		t.Fatalf("database name = %q", db.Name)
	}

	// DROP DATABASE removes it.
	_, err = m.StartQueryExecution(ctx, driver.StartQueryExecutionInput{
		QueryString:         "DROP DATABASE analytics",
		ResultConfiguration: &driver.ResultConfiguration{OutputLocation: "s3://out/"},
	})
	requireNoError(t, err, "StartQueryExecution drop")

	if _, err := m.GetDatabase(ctx, "", "analytics"); err == nil {
		t.Fatalf("database should be dropped")
	}
}

func TestStartQueryExecutionRequiresOutputLocation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	// primary enforces its config but has no result location and none supplied.
	_, err := m.StartQueryExecution(ctx, driver.StartQueryExecutionInput{QueryString: "SELECT 1"})
	if err == nil {
		t.Fatalf("expected missing-output-location error")
	}
}

func TestStartQueryExecutionIdempotentToken(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	in := driver.StartQueryExecutionInput{
		QueryString:         "SELECT 1",
		ClientRequestToken:  "tok-1",
		ResultConfiguration: &driver.ResultConfiguration{OutputLocation: "s3://out/"},
	}

	id1, err := m.StartQueryExecution(ctx, in)
	requireNoError(t, err, "start 1")

	id2, err := m.StartQueryExecution(ctx, in)
	requireNoError(t, err, "start 2")

	if id1 != id2 {
		t.Fatalf("idempotency token produced different ids: %q %q", id1, id2)
	}
}

func TestListQueryExecutionsMostRecentFirst(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateWorkGroup(ctx, driver.WorkGroup{Name: "wg"}), "CreateWorkGroup")

	var ids []string

	for i := 0; i < 3; i++ {
		id, err := m.StartQueryExecution(ctx, driver.StartQueryExecutionInput{
			QueryString:         "SELECT 1",
			WorkGroup:           "wg",
			ResultConfiguration: &driver.ResultConfiguration{OutputLocation: "s3://out/"},
		})
		requireNoError(t, err, "StartQueryExecution")

		ids = append(ids, id)
	}

	got, _, err := m.ListQueryExecutions(ctx, "wg", driver.Pagination{})
	requireNoError(t, err, "ListQueryExecutions")

	if len(got) != 3 || got[0] != ids[2] || got[2] != ids[0] {
		t.Fatalf("not most-recent-first: got %v want reverse of %v", got, ids)
	}
}

func TestTagsLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	const arn = "arn:aws:athena:us-east-1:000000000000:workgroup/wg"

	// No tags yet.
	got, err := m.ListTagsForResource(ctx, arn)
	requireNoError(t, err, "ListTagsForResource empty")

	if len(got) != 0 {
		t.Fatalf("expected no tags, got %v", got)
	}

	requireNoError(t, m.TagResource(ctx, arn, map[string]string{"env": "prod", "team": "data"}), "TagResource")

	got, err = m.ListTagsForResource(ctx, arn)
	requireNoError(t, err, "ListTagsForResource")

	if got["env"] != "prod" || got["team"] != "data" {
		t.Fatalf("tags not stored: %v", got)
	}

	requireNoError(t, m.UntagResource(ctx, arn, []string{"team"}), "UntagResource")

	got, err = m.ListTagsForResource(ctx, arn)
	requireNoError(t, err, "ListTagsForResource after untag")

	if _, ok := got["team"]; ok || got["env"] != "prod" {
		t.Fatalf("untag wrong: %v", got)
	}
}

func TestCreateWorkGroupInlineTags(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateWorkGroup(ctx, driver.WorkGroup{
		Name: "wg",
		Tags: map[string]string{"cost": "eng"},
	}), "CreateWorkGroup")

	got, err := m.ListTagsForResource(ctx, m.workGroupARN("wg"))
	requireNoError(t, err, "ListTagsForResource")

	if got["cost"] != "eng" {
		t.Fatalf("inline tags not readable: %v", got)
	}
}

func TestGetQueryResultsEmptyForSucceeded(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, err := m.StartQueryExecution(ctx, driver.StartQueryExecutionInput{
		QueryString:         "CREATE DATABASE d1",
		ResultConfiguration: &driver.ResultConfiguration{OutputLocation: "s3://out/"},
	})
	requireNoError(t, err, "StartQueryExecution")

	res, err := m.GetQueryResults(ctx, id, driver.Pagination{})
	requireNoError(t, err, "GetQueryResults")

	if len(res.Rows) != 0 || len(res.ColumnInfo) != 0 {
		t.Fatalf("expected empty result set, got %+v", res)
	}

	if _, err := m.GetQueryResults(ctx, "missing", driver.Pagination{}); err == nil {
		t.Fatalf("expected not-found for missing execution")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	requireNoError(t, m.CreateWorkGroup(ctx, driver.WorkGroup{
		Name:          "wg",
		Configuration: driver.WorkGroupConfiguration{EnforceWorkGroupConfiguration: ptr(false)},
	}), "CreateWorkGroup")
	_, err := m.CreateNamedQuery(ctx, driver.NamedQuery{Name: "n", Database: "d", QueryString: "SELECT 1", WorkGroup: "wg"})
	requireNoError(t, err, "CreateNamedQuery")

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err, "Snapshot")

	restored := New(config.NewOptions())
	requireNoError(t, restored.Restore(ctx, data), "Restore")

	wg, err := restored.GetWorkGroup(ctx, "wg")
	requireNoError(t, err, "GetWorkGroup after restore")

	if wg.Configuration.EnforceWorkGroupConfiguration == nil || *wg.Configuration.EnforceWorkGroupConfiguration {
		t.Fatalf("explicit-false enforce not restored")
	}
}
