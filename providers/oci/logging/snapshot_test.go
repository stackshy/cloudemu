package logging_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocilogging "github.com/stackshy/cloudemu/v2/providers/oci/logging"
)

// TestSnapshotRestoreRoundTrip seeds groups in two compartments, logs inside
// them and ingested entries, snapshots, restores into a fresh mock and asserts
// each resource comes back under its original OCID with its cross-references
// and its entries intact — a log still points at its group, and the entries
// ingested into it survive.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := t.Context()
	src := newMock(t)

	groupA := newGroup(t, src, compartmentA, "app-logs")
	groupB := newGroup(t, src, compartmentB, "other-logs")
	stdout := newCustomLog(t, src, groupA.ID, "stdout")
	audit := newCustomLog(t, src, groupB.ID, "audit")

	when := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	require.NoError(t, src.PutLogs(ctx, stdout.ID, []ocilogging.LogEntryBatch{{
		Source:  "host-a",
		Type:    "com.oraclecloud.custom",
		Subject: "app",
		Entries: []ocilogging.LogEntryItem{
			{ID: "e-1", Data: `{"level":"error"}`, Time: when},
			{ID: "e-2", Data: "plain line", Time: when.Add(time.Minute)},
		},
	}}))

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	t.Run("groups come back under their OCIDs and compartments", func(t *testing.T) {
		got, groupErr := dst.GetGroup(ctx, groupA.ID)
		require.NoError(t, groupErr)
		assert.Equal(t, "app-logs", got.DisplayName)
		assert.Equal(t, compartmentA, got.CompartmentID)
		assert.Equal(t, groupA.TimeCreated, got.TimeCreated)

		other, otherErr := dst.GetGroup(ctx, groupB.ID)
		require.NoError(t, otherErr)
		assert.Equal(t, compartmentB, other.CompartmentID)
	})

	t.Run("a log still resolves through its group", func(t *testing.T) {
		got, logErr := dst.GetLog(ctx, groupA.ID, stdout.ID)
		require.NoError(t, logErr)
		assert.Equal(t, "stdout", got.DisplayName)
		assert.Equal(t, groupA.ID, got.LogGroupID)

		auditLog, auditErr := dst.GetLog(ctx, groupB.ID, audit.ID)
		require.NoError(t, auditErr)
		assert.Equal(t, groupB.ID, auditLog.LogGroupID)
	})

	t.Run("ingested entries survive with their fields", func(t *testing.T) {
		entries, entryErr := dst.Entries(ctx, stdout.ID)
		require.NoError(t, entryErr)
		require.Len(t, entries, 2)

		assert.Equal(t, "e-1", entries[0].ID)
		assert.Equal(t, `{"level":"error"}`, entries[0].Data)
		assert.Equal(t, when, entries[0].Time.UTC())
		assert.Equal(t, "host-a", entries[0].Source)
		assert.Equal(t, "app", entries[0].Subject)
		assert.Equal(t, "e-2", entries[1].ID)
	})

	t.Run("a restored group is searchable", func(t *testing.T) {
		res, searchErr := dst.SearchLogs(ctx, ocilogging.SearchRequest{
			Query:     `search "` + compartmentA + `" | where oracle.logid = '` + stdout.ID + `'`,
			TimeStart: when.Add(-time.Hour),
			TimeEnd:   when.Add(time.Hour),
		})
		require.NoError(t, searchErr)
		require.Len(t, res.Entries, 2)
		assert.Equal(t, groupA.ID, res.Entries[0].LogGroupID)
	})

	t.Run("restoring keeps the mock usable", func(t *testing.T) {
		l := newCustomLog(t, dst, groupA.ID, "stderr")
		assert.NotEqual(t, stdout.ID, l.ID, "a restored mock still mints fresh OCIDs")
	})
}

func TestRestoreRejectsMalformedSnapshot(t *testing.T) {
	m := newMock(t)

	err := m.Restore(t.Context(), []byte("{not json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse snapshot")
}

// TestSnapshotOfAnEmptyMockRestores guards the omitempty fields: an empty
// snapshot must restore cleanly rather than fail on an absent store.
func TestSnapshotOfAnEmptyMockRestores(t *testing.T) {
	ctx := t.Context()
	src := newMock(t)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	groups, err := dst.ListGroups(ctx, compartmentA, "")
	require.NoError(t, err)
	assert.Empty(t, groups)
}
