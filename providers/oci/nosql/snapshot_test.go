package nosql_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/nosql"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// TestSnapshotRestoreRoundTrip seeds two compartments, rows, an index, tags and
// an attribute TTL, snapshots, restores into a fresh mock and asserts every
// table comes back under its original OCID and compartment with its own rows —
// the row store nests inside each table, so it is the part a dump loses first.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := t.Context()
	src, _ := newMock(t)

	inA := createUsers(t, src)

	inB, err := src.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentB,
		DDLStatement:  usersDDL,
		Limits:        provisioned(),
		FreeformTags:  map[string]string{"env": "prod"},
	})
	require.NoError(t, err)

	_, err = src.CreateOCIIndex(ctx, compartmentA, "users",
		nosql.IndexSpec{Name: "byName", Columns: []string{"name"}}, false)
	require.NoError(t, err)

	require.NoError(t, src.UpdateTTL(ctx, "users", driver.TTLConfig{Enabled: true, AttributeName: "expiresAt"}))

	_, err = src.PutOCIRow(ctx, compartmentA, "users",
		map[string]any{"id": float64(1), "email": "a@example.com", "name": "Ada"}, "")
	require.NoError(t, err)

	_, err = src.PutOCIRow(ctx, compartmentB, "users",
		map[string]any{"id": float64(2), "email": "b@example.com", "name": "Bea"}, "")
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst, _ := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	// Both tables restored under their own OCID and compartment.
	gotA, err := dst.GetOCITable(ctx, compartmentA, "users")
	require.NoError(t, err)
	assert.Equal(t, inA.ID, gotA.ID)
	assert.Equal(t, []string{"id", "email"}, gotA.Schema.PrimaryKey)

	gotB, err := dst.GetOCITable(ctx, compartmentB, "users")
	require.NoError(t, err)
	assert.Equal(t, inB.ID, gotB.ID)
	assert.Equal(t, map[string]string{"env": "prod"}, gotB.FreeformTags)

	// Addressable by OCID too, so the names store round-tripped.
	byOCID, err := dst.GetOCITable(ctx, compartmentB, inB.ID)
	require.NoError(t, err)
	assert.Equal(t, compartmentB, byOCID.CompartmentID)

	// The index survived.
	indexes, err := dst.ListOCIIndexes(ctx, compartmentA, "users", "")
	require.NoError(t, err)
	require.Len(t, indexes, 1)
	assert.Equal(t, "byName", indexes[0].Name)

	// The attribute-based TTL is unexported on the table, so it needs the
	// custom JSON methods to travel at all.
	ttl, err := dst.DescribeTTL(ctx, "users")
	require.NoError(t, err)
	assert.True(t, ttl.Enabled)
	assert.Equal(t, "expiresAt", ttl.AttributeName)

	// Each table's own rows came back, typed as their columns declare rather
	// than as the float64 JSON decodes every number to.
	row, err := dst.GetOCIRow(ctx, compartmentA, "users", map[string]string{"id": "1", "email": "a@example.com"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), row.Value["id"])
	assert.Equal(t, "Ada", row.Value["name"])

	row, err = dst.GetOCIRow(ctx, compartmentB, "users", map[string]string{"id": "2", "email": "b@example.com"})
	require.NoError(t, err)
	assert.Equal(t, "Bea", row.Value["name"])

	// A row is not visible from the other compartment's table.
	_, err = dst.GetOCIRow(ctx, compartmentA, "users", map[string]string{"id": "2", "email": "b@example.com"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

// TestSnapshotRestoreKeepsRowExpiry pins that a row written under a table-level
// TTL still expires at the time it was stamped with, rather than losing its
// expiry to the dump or coming back as an already-expired float.
func TestSnapshotRestoreKeepsRowExpiry(t *testing.T) {
	ctx := t.Context()
	src, clock := newMock(t)

	_, err := src.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentA,
		DDLStatement:  "CREATE TABLE sessions (id STRING, PRIMARY KEY (id)) USING TTL 2 DAYS",
		Limits:        provisioned(),
	})
	require.NoError(t, err)

	written, err := src.PutOCIRow(ctx, compartmentA, "sessions", map[string]any{"id": "s1"}, "")
	require.NoError(t, err)
	require.NotEmpty(t, written.TimeOfExpiration)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst, dstClock := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	row, err := dst.GetOCIRow(ctx, compartmentA, "sessions", map[string]string{"id": "s1"})
	require.NoError(t, err)
	assert.Equal(t, written.TimeOfExpiration, row.TimeOfExpiration)

	// And it still expires on time on the far side.
	clock.Advance(3 * 24 * time.Hour)
	dstClock.Advance(3 * 24 * time.Hour)

	_, err = dst.GetOCIRow(ctx, compartmentA, "sessions", map[string]string{"id": "s1"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

// TestSnapshotRestoreEmptyAndMalformed confirms an empty mock round-trips
// cleanly and that a restored table's row store is usable rather than nil.
func TestSnapshotRestoreEmptyAndMalformed(t *testing.T) {
	ctx := t.Context()
	src, _ := newMock(t)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst, _ := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	names, err := dst.ListTables(ctx)
	require.NoError(t, err)
	assert.Empty(t, names)

	// A table with no rows restores with a store that can be written to.
	createUsers(t, src)

	data, err = src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst, _ = newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	_, err = dst.PutOCIRow(ctx, compartmentA, "users",
		map[string]any{"id": float64(1), "email": "a@example.com", "name": "Ada"}, "")
	require.NoError(t, err)

	// Malformed input is reported, not panicked on.
	for _, bad := range []string{"", "not json", `{"tables":5}`, `{"names":[1,2]}`} {
		require.Error(t, dst.Restore(ctx, json.RawMessage(bad)))
	}
}
