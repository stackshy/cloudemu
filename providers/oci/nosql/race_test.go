package nosql_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/nosql"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// The table store hands back pointers, so an ALTER mutates the very record a
// concurrent GetTable is projecting, and a row write mutates the store a
// concurrent scan is walking. These tests fail under -race if the Mock's mutex
// is dropped from either path.

const raceGoroutines = 16

func TestConcurrentRowsAndTableReads(t *testing.T) {
	t.Parallel()

	m, _ := newMock(t)
	ctx := context.Background()
	createUsers(t, m)

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(5)

		go func() {
			defer wg.Done()

			_, err := m.PutOCIRow(ctx, "users", map[string]any{
				"id": float64(i), "email": fmt.Sprintf("u%d@x.com", i), "name": "n",
			}, "")
			assert.NoError(t, err)
		}()

		go func() {
			defer wg.Done()

			// Missing rows are expected while the writers are still running.
			_, _ = m.GetOCIRow(ctx, "users", map[string]string{
				"id": fmt.Sprint(i), "email": fmt.Sprintf("u%d@x.com", i),
			})
		}()

		go func() {
			defer wg.Done()

			_, err := m.GetOCITable(ctx, "users")
			assert.NoError(t, err)
		}()

		go func() {
			defer wg.Done()

			_, err := m.ListOCITables(ctx, compartmentA, "")
			assert.NoError(t, err)
		}()

		go func() {
			defer wg.Done()

			_, err := m.Scan(ctx, driver.ScanInput{Table: "users"})
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	res, err := m.Scan(ctx, driver.ScanInput{Table: "users", Limit: raceGoroutines * 2})
	require.NoError(t, err)
	assert.Equal(t, raceGoroutines, res.Count)
}

// TestConcurrentTableMutationAndProjection alters the schema while readers
// project it, which is the path where a store pointer is mutated in place.
func TestConcurrentTableMutationAndProjection(t *testing.T) {
	t.Parallel()

	m, _ := newMock(t)
	ctx := context.Background()
	createUsers(t, m)

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(4)

		go func() {
			defer wg.Done()

			// Exactly one goroutine wins each column name; the rest see
			// AlreadyExists, never a corrupted column list.
			_, err := m.UpdateOCITable(ctx, "users", nosql.TableUpdate{
				DDLStatement: fmt.Sprintf("ALTER TABLE users (ADD c%d STRING)", i),
			})
			assert.NoError(t, err)
		}()

		go func() {
			defer wg.Done()

			_, err := m.CreateOCIIndex(ctx, "users",
				nosql.IndexSpec{Name: fmt.Sprintf("i%d", i), Columns: []string{"name"}}, true)
			assert.NoError(t, err)
		}()

		go func() {
			defer wg.Done()

			table, err := m.GetOCITable(ctx, "users")
			if assert.NoError(t, err) {
				assert.NotEmpty(t, table.Schema.PrimaryKey)
			}
		}()

		go func() {
			defer wg.Done()

			_, err := m.ListOCIIndexes(ctx, "users", "")
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	table, err := m.GetOCITable(ctx, "users")
	require.NoError(t, err)
	assert.Len(t, table.Schema.Columns, 3+raceGoroutines)

	indexes, err := m.ListOCIIndexes(ctx, "users", "")
	require.NoError(t, err)
	assert.Len(t, indexes, raceGoroutines)
}

// TestConcurrentQueryDelete runs the multi-delete path against concurrent
// writers; the deletes and the writes must not interleave inside one store.
func TestConcurrentQueryDelete(t *testing.T) {
	t.Parallel()

	m, _ := newMock(t)
	ctx := context.Background()
	createUsers(t, m)

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(2)

		go func() {
			defer wg.Done()

			_, err := m.PutOCIRow(ctx, "users", map[string]any{
				"id": float64(i), "email": "x@y.z", "name": "n",
			}, "")
			assert.NoError(t, err)
		}()

		go func() {
			defer wg.Done()

			_, err := m.QueryOCI(ctx, compartmentA, fmt.Sprintf("DELETE FROM users WHERE id = %d", i), 0)
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	_, err := m.QueryOCI(ctx, compartmentA, "SELECT * FROM users", 0)
	assert.NoError(t, err)
	assert.Equal(t, cerrors.OK, cerrors.GetCode(err))
}
