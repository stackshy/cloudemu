package nosql_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/nosql"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	compartmentA = "ocid1.compartment.oc1..aaaa"
	compartmentB = "ocid1.compartment.oc1..bbbb"

	usersDDL = "CREATE TABLE users (id INTEGER, email STRING, name STRING, PRIMARY KEY (SHARD(id), email))"
)

func newMock(t *testing.T) (*nosql.Mock, *config.FakeClock) {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC))
	m := nosql.New(config.NewOptions(
		config.WithClock(clock),
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(compartmentA),
	))

	return m, clock
}

// provisioned is the limits shape a table needs when it is not on demand.
func provisioned() nosql.TableLimits {
	return nosql.TableLimits{
		MaxReadUnits: 50, MaxWriteUnits: 50, MaxStorageInGBs: 1, CapacityMode: nosql.CapacityProvisioned,
	}
}

func createUsers(t *testing.T, m *nosql.Mock) *nosql.Table {
	t.Helper()

	table, err := m.CreateOCITable(context.Background(), nosql.TableSpec{
		CompartmentID: compartmentA,
		DDLStatement:  usersDDL,
		Limits:        provisioned(),
	})
	require.NoError(t, err)

	return table
}

func TestCreateOCITable(t *testing.T) {
	m, _ := newMock(t)
	table := createUsers(t, m)

	assert.Equal(t, "users", table.Name)
	assert.Equal(t, compartmentA, table.CompartmentID)
	assert.Equal(t, nosql.StateActive, table.LifecycleState)
	assert.Equal(t, []string{"id"}, table.Schema.ShardKey)
	assert.Equal(t, []string{"id", "email"}, table.Schema.PrimaryKey)
	assert.Equal(t, 50, table.Limits.MaxReadUnits)
	assert.NotEmpty(t, table.TimeCreated)
}

// TestTableOCIDShape pins the identifier form real OCI mints for a NoSQL table.
func TestTableOCIDShape(t *testing.T) {
	m, _ := newMock(t)
	table := createUsers(t, m)

	assert.Regexp(t, regexp.MustCompile(`^ocid1\.nosqltable\.oc1\.iad\.a[a-z0-9]+$`), table.ID)
}

func TestCreateOCITableErrors(t *testing.T) {
	tests := []struct {
		name       string
		spec       nosql.TableSpec
		expectCode cerrors.Code
		expectWord string
	}{
		{
			name:       "missing compartment",
			spec:       nosql.TableSpec{DDLStatement: usersDDL, Limits: provisioned()},
			expectCode: cerrors.InvalidArgument,
			expectWord: "compartmentId is required",
		},
		{
			name: "alter statement passed to create",
			spec: nosql.TableSpec{
				CompartmentID: compartmentA, DDLStatement: "ALTER TABLE users (ADD x STRING)", Limits: provisioned(),
			},
			expectCode: cerrors.InvalidArgument,
			expectWord: "CreateTable takes a CREATE TABLE statement",
		},
		{
			name: "on demand table naming read units",
			spec: nosql.TableSpec{
				CompartmentID: compartmentA,
				DDLStatement:  usersDDL,
				Limits:        nosql.TableLimits{MaxReadUnits: 10, CapacityMode: nosql.CapacityOnDemand},
			},
			expectCode: cerrors.InvalidArgument,
			expectWord: "ON_DEMAND table sets no maxReadUnits",
		},
		{
			name: "provisioned table naming no units",
			spec: nosql.TableSpec{
				CompartmentID: compartmentA,
				DDLStatement:  usersDDL,
				Limits:        nosql.TableLimits{CapacityMode: nosql.CapacityProvisioned},
			},
			expectCode: cerrors.InvalidArgument,
			expectWord: "PROVISIONED table sets maxReadUnits",
		},
		{
			name: "unknown capacity mode",
			spec: nosql.TableSpec{
				CompartmentID: compartmentA,
				DDLStatement:  usersDDL,
				Limits:        nosql.TableLimits{CapacityMode: "ELASTIC"},
			},
			expectCode: cerrors.InvalidArgument,
			expectWord: `capacityMode "ELASTIC"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newMock(t)

			_, err := m.CreateOCITable(context.Background(), tc.spec)

			require.Error(t, err)
			assert.Equal(t, tc.expectCode, cerrors.GetCode(err))
			assert.Contains(t, err.Error(), tc.expectWord)
		})
	}
}

func TestCreateOCITableAlreadyExists(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	_, err := m.CreateOCITable(context.Background(), nosql.TableSpec{
		CompartmentID: compartmentA, DDLStatement: usersDDL, Limits: provisioned(),
	})

	require.Error(t, err)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))
}

func TestCreateOCITableIfNotExistsIsIdempotent(t *testing.T) {
	m, _ := newMock(t)
	first := createUsers(t, m)

	second, err := m.CreateOCITable(context.Background(), nosql.TableSpec{
		CompartmentID: compartmentA,
		DDLStatement:  "CREATE TABLE IF NOT EXISTS users (id INTEGER, email STRING, PRIMARY KEY (SHARD(id), email))",
		Limits:        provisioned(),
	})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID)
}

func TestGetOCITableByNameOrOCID(t *testing.T) {
	m, _ := newMock(t)
	table := createUsers(t, m)

	for _, addr := range []string{"users", table.ID} {
		got, err := m.GetOCITable(context.Background(), compartmentA, addr)
		require.NoError(t, err)
		assert.Equal(t, table.ID, got.ID)
	}
}

func TestGetOCITableNotFound(t *testing.T) {
	m, _ := newMock(t)

	_, err := m.GetOCITable(context.Background(), compartmentA, "missing")

	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

// TestListOCITablesFiltersByCompartment is the compartment-isolation contract:
// a table created in one compartment must not appear in another's listing.
func TestListOCITablesFiltersByCompartment(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	_, err := m.CreateOCITable(context.Background(), nosql.TableSpec{
		CompartmentID: compartmentB,
		DDLStatement:  "CREATE TABLE audits (id STRING, PRIMARY KEY (id))",
		Limits:        provisioned(),
	})
	require.NoError(t, err)

	tests := []struct {
		name        string
		compartment string
		filterName  string
		expect      []string
	}{
		{name: "compartment a", compartment: compartmentA, expect: []string{"users"}},
		{name: "compartment b", compartment: compartmentB, expect: []string{"audits"}},
		{name: "unknown compartment lists nothing", compartment: "ocid1.compartment.oc1..zzz", expect: []string{}},
		{name: "name narrows the listing", compartment: compartmentA, filterName: "users", expect: []string{"users"}},
		{name: "name matching nothing", compartment: compartmentA, filterName: "audits", expect: []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.ListOCITables(context.Background(), tc.compartment, tc.filterName)
			require.NoError(t, err)

			names := make([]string, 0, len(got))
			for _, table := range got {
				names = append(names, table.Name)
			}

			assert.Equal(t, tc.expect, names)
		})
	}
}

func TestChangeOCITableCompartment(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	require.NoError(t, m.ChangeOCITableCompartment(context.Background(), compartmentA, "users", compartmentB))

	inA, err := m.ListOCITables(context.Background(), compartmentA, "")
	require.NoError(t, err)
	assert.Empty(t, inA)

	inB, err := m.ListOCITables(context.Background(), compartmentB, "")
	require.NoError(t, err)
	require.Len(t, inB, 1)
	assert.Equal(t, "users", inB[0].Name)
}

func TestUpdateOCITable(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	reclaim := true

	table, err := m.UpdateOCITable(context.Background(), compartmentA, "users", nosql.TableUpdate{
		DDLStatement:      "ALTER TABLE users (ADD nickname STRING)",
		Limits:            &nosql.TableLimits{CapacityMode: nosql.CapacityOnDemand},
		IsAutoReclaimable: &reclaim,
		FreeformTags:      map[string]string{"env": "dev"},
	})
	require.NoError(t, err)

	assert.Len(t, table.Schema.Columns, 4)
	assert.Equal(t, nosql.CapacityOnDemand, table.Limits.CapacityMode)
	assert.True(t, table.IsAutoReclaimable)
	assert.Equal(t, map[string]string{"env": "dev"}, table.FreeformTags)
	assert.Contains(t, table.DDLStatement, "nickname STRING")
}

func TestUpdateOCITableErrors(t *testing.T) {
	tests := []struct {
		name       string
		table      string
		update     nosql.TableUpdate
		expectCode cerrors.Code
		expectWord string
	}{
		{
			name:       "unknown table",
			table:      "missing",
			update:     nosql.TableUpdate{DDLStatement: "ALTER TABLE missing (ADD x STRING)"},
			expectCode: cerrors.NotFound,
			expectWord: "not found",
		},
		{
			name:       "create statement passed to update",
			table:      "users",
			update:     nosql.TableUpdate{DDLStatement: usersDDL},
			expectCode: cerrors.InvalidArgument,
			expectWord: "UpdateTable takes an ALTER TABLE statement",
		},
		{
			name:       "alter names another table",
			table:      "users",
			update:     nosql.TableUpdate{DDLStatement: "ALTER TABLE other (ADD x STRING)"},
			expectCode: cerrors.InvalidArgument,
			expectWord: `names table "other"`,
		},
		{
			name:       "dropping a key column",
			table:      "users",
			update:     nosql.TableUpdate{DDLStatement: "ALTER TABLE users (DROP id)"},
			expectCode: cerrors.InvalidArgument,
			expectWord: "part of the primary key",
		},
		{
			name:       "dropping an undeclared column",
			table:      "users",
			update:     nosql.TableUpdate{DDLStatement: "ALTER TABLE users (DROP nope)"},
			expectCode: cerrors.NotFound,
			expectWord: "is not declared",
		},
		{
			name:       "adding a column twice",
			table:      "users",
			update:     nosql.TableUpdate{DDLStatement: "ALTER TABLE users (ADD name STRING)"},
			expectCode: cerrors.AlreadyExists,
			expectWord: "already declared",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newMock(t)
			createUsers(t, m)

			_, err := m.UpdateOCITable(context.Background(), compartmentA, tc.table, tc.update)

			require.Error(t, err)
			assert.Equal(t, tc.expectCode, cerrors.GetCode(err))
			assert.Contains(t, err.Error(), tc.expectWord)
		})
	}
}

func TestDeleteOCITable(t *testing.T) {
	m, _ := newMock(t)
	table := createUsers(t, m)

	require.NoError(t, m.DeleteOCITable(context.Background(), compartmentA, table.ID))

	_, err := m.GetOCITable(context.Background(), compartmentA, "users")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	assert.Equal(t, cerrors.NotFound,
		cerrors.GetCode(m.DeleteOCITable(context.Background(), compartmentA, "users")))
}

func TestOCIRowRoundTrip(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	_, err := m.PutOCIRow(context.Background(), compartmentA, "users",
		map[string]any{"id": float64(1), "email": "a@example.com", "name": "Ada"}, "")
	require.NoError(t, err)

	row, err := m.GetOCIRow(context.Background(), compartmentA, "users", map[string]string{"id": "1", "email": "a@example.com"})
	require.NoError(t, err)

	assert.Equal(t, "Ada", row.Value["name"])
	// An INTEGER column comes back as an integer, not JSON's float64.
	assert.Equal(t, int64(1), row.Value["id"])
	assert.Empty(t, row.TimeOfExpiration)

	deleted, err := m.DeleteOCIRow(context.Background(), compartmentA, "users",
		map[string]string{"id": "1", "email": "a@example.com"})
	require.NoError(t, err)
	assert.True(t, deleted)

	_, err = m.GetOCIRow(context.Background(), compartmentA, "users", map[string]string{"id": "1", "email": "a@example.com"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestDeleteOCIRowReportsAbsence(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	deleted, err := m.DeleteOCIRow(context.Background(), compartmentA, "users", map[string]string{"id": "9", "email": "x@y.z"})
	require.NoError(t, err)
	assert.False(t, deleted)
}

func TestPutOCIRowOptions(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	row := map[string]any{"id": float64(1), "email": "a@example.com", "name": "Ada"}

	_, err := m.PutOCIRow(context.Background(), compartmentA, "users", row, nosql.OptionIfPresent)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	_, err = m.PutOCIRow(context.Background(), compartmentA, "users", row, nosql.OptionIfAbsent)
	require.NoError(t, err)

	_, err = m.PutOCIRow(context.Background(), compartmentA, "users", row, nosql.OptionIfAbsent)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	_, err = m.PutOCIRow(context.Background(), compartmentA, "users", row, "MAYBE")
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

func TestPutOCIRowValidation(t *testing.T) {
	tests := []struct {
		name       string
		value      map[string]any
		expectWord string
	}{
		{
			name:       "undeclared column",
			value:      map[string]any{"id": float64(1), "email": "a@b.c", "bogus": "x"},
			expectWord: `column "bogus" is not declared`,
		},
		{
			name:       "missing key column",
			value:      map[string]any{"id": float64(1)},
			expectWord: `primary key column "email" is missing`,
		},
		{
			name:       "string in an integer column",
			value:      map[string]any{"id": "one", "email": "a@b.c"},
			expectWord: `does not fit column "id" of type INTEGER`,
		},
		{
			name:       "fractional value in an integer column",
			value:      map[string]any{"id": 1.5, "email": "a@b.c"},
			expectWord: `does not fit column "id" of type INTEGER`,
		},
		{
			name:       "number in a string column",
			value:      map[string]any{"id": float64(1), "email": float64(2)},
			expectWord: `does not fit column "email" of type STRING`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newMock(t)
			createUsers(t, m)

			_, err := m.PutOCIRow(context.Background(), compartmentA, "users", tc.value, "")

			require.Error(t, err)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
			assert.Contains(t, err.Error(), tc.expectWord)
		})
	}
}

func TestGetOCIRowRejectsNonKeyColumn(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	_, err := m.GetOCIRow(context.Background(), compartmentA, "users", map[string]string{"name": "Ada"})

	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "is not a primary key column")
}

// TestTableTTLExpiresRows exercises OCI's table-level USING TTL, which expires
// a row a fixed span after it is written rather than at an attribute's value.
func TestTableTTLExpiresRows(t *testing.T) {
	m, clock := newMock(t)

	_, err := m.CreateOCITable(context.Background(), nosql.TableSpec{
		CompartmentID: compartmentA,
		DDLStatement:  "CREATE TABLE sessions (id STRING, PRIMARY KEY (id)) USING TTL 2 DAYS",
		Limits:        provisioned(),
	})
	require.NoError(t, err)

	written, err := m.PutOCIRow(context.Background(), compartmentA, "sessions", map[string]any{"id": "s1"}, "")
	require.NoError(t, err)
	assert.NotEmpty(t, written.TimeOfExpiration)

	row, err := m.GetOCIRow(context.Background(), compartmentA, "sessions", map[string]string{"id": "s1"})
	require.NoError(t, err)
	assert.NotEmpty(t, row.TimeOfExpiration)
	// The expiry is metadata, not a column the caller sees in the value.
	assert.Len(t, row.Value, 1)

	clock.Advance(3 * 24 * time.Hour)

	_, err = m.GetOCIRow(context.Background(), compartmentA, "sessions", map[string]string{"id": "s1"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestOCIIndexes(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	spec := nosql.IndexSpec{Name: "byName", Columns: []string{"name"}}

	idx, err := m.CreateOCIIndex(context.Background(), compartmentA, "users", spec, false)
	require.NoError(t, err)
	assert.Equal(t, nosql.StateActive, idx.LifecycleState)
	assert.Equal(t, []nosql.IndexKey{{ColumnName: "name"}}, idx.Keys)

	_, err = m.CreateOCIIndex(context.Background(), compartmentA, "users", spec, false)
	require.Error(t, err)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	again, err := m.CreateOCIIndex(context.Background(), compartmentA, "users", spec, true)
	require.NoError(t, err)
	assert.Equal(t, "byName", again.Name)

	got, err := m.GetOCIIndex(context.Background(), compartmentA, "users", "byName")
	require.NoError(t, err)
	assert.Equal(t, "byName", got.Name)

	list, err := m.ListOCIIndexes(context.Background(), compartmentA, "users", "")
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, m.DeleteOCIIndex(context.Background(), compartmentA, "users", "byName", false))

	err = m.DeleteOCIIndex(context.Background(), compartmentA, "users", "byName", false)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	require.NoError(t, m.DeleteOCIIndex(context.Background(), compartmentA, "users", "byName", true))
}

func TestCreateOCIIndexRejectsUndeclaredColumn(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	_, err := m.CreateOCIIndex(context.Background(), compartmentA, "users",
		nosql.IndexSpec{Name: "bad", Columns: []string{"nope"}}, false)

	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), `index column "nope" is not declared`)
}

func TestQueryOCISelectAndDelete(t *testing.T) {
	m, _ := newMock(t)
	createUsers(t, m)

	for _, email := range []string{"a@x.com", "b@x.com"} {
		_, err := m.PutOCIRow(context.Background(), compartmentA, "users",
			map[string]any{"id": float64(1), "email": email, "name": "Ada"}, "")
		require.NoError(t, err)
	}

	_, err := m.PutOCIRow(context.Background(), compartmentA, "users",
		map[string]any{"id": float64(2), "email": "c@x.com", "name": "Grace"}, "")
	require.NoError(t, err)

	rows, err := m.QueryOCI(context.Background(), compartmentA, "SELECT * FROM users", 0)
	require.NoError(t, err)
	assert.Len(t, rows, 3)

	rows, err = m.QueryOCI(context.Background(), compartmentA, "SELECT * FROM users WHERE name = 'Ada'", 0)
	require.NoError(t, err)
	assert.Len(t, rows, 2)

	rows, err = m.QueryOCI(context.Background(), compartmentA, "SELECT * FROM users", 1)
	require.NoError(t, err)
	assert.Len(t, rows, 1)

	// The multi-delete path: OCI's REST API has no MultiDelete operation, so
	// DELETE FROM over the query endpoint is how several rows go at once.
	rows, err = m.QueryOCI(context.Background(), compartmentA, "DELETE FROM users WHERE id = 1", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 2, rows[0]["NumRowsDeleted"])

	rows, err = m.QueryOCI(context.Background(), compartmentA, "SELECT * FROM users", 0)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}

func TestQueryOCIRejectsUnsupported(t *testing.T) {
	tests := []struct {
		name        string
		compartment string
		statement   string
		expectCode  cerrors.Code
		expectWord  string
	}{
		{
			name:       "no compartment",
			statement:  "SELECT * FROM users",
			expectCode: cerrors.InvalidArgument,
			expectWord: "compartmentId is required",
		},
		{
			name:        "insert",
			compartment: compartmentA,
			statement:   "INSERT INTO users VALUES (1, 'a@x.com')",
			expectCode:  cerrors.InvalidArgument,
			expectWord:  "unsupported statement",
		},
		{
			name:        "column projection",
			compartment: compartmentA,
			statement:   "SELECT name FROM users",
			expectCode:  cerrors.InvalidArgument,
			expectWord:  "only SELECT * is supported",
		},
		{
			name:        "order by",
			compartment: compartmentA,
			statement:   "SELECT * FROM users ORDER BY name",
			expectCode:  cerrors.InvalidArgument,
			expectWord:  "unsupported clause",
		},
		{
			name:        "range condition",
			compartment: compartmentA,
			statement:   "SELECT * FROM users WHERE id > 1",
			expectCode:  cerrors.InvalidArgument,
			expectWord:  "unsupported condition",
		},
		{
			name:        "undeclared column in the condition",
			compartment: compartmentA,
			statement:   "SELECT * FROM users WHERE nope = 'x'",
			expectCode:  cerrors.InvalidArgument,
			expectWord:  `column "nope" is not declared`,
		},
		{
			name:        "table in another compartment",
			compartment: compartmentB,
			statement:   "SELECT * FROM users",
			expectCode:  cerrors.NotFound,
			expectWord:  "not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newMock(t)
			createUsers(t, m)

			_, err := m.QueryOCI(context.Background(), tc.compartment, tc.statement, 0)

			require.Error(t, err)
			assert.Equal(t, tc.expectCode, cerrors.GetCode(err))
			assert.Contains(t, err.Error(), tc.expectWord)
		})
	}
}

// --- portable driver surface ---

func TestPortableTableCRUD(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	cfg := driver.TableConfig{Name: "portable", PartitionKey: "pk", SortKey: "sk"}
	require.NoError(t, m.CreateTable(ctx, cfg))

	err := m.CreateTable(ctx, cfg)
	require.Error(t, err)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	got, err := m.DescribeTable(ctx, "portable")
	require.NoError(t, err)
	assert.Equal(t, "pk", got.PartitionKey)
	assert.Equal(t, "sk", got.SortKey)

	names, err := m.ListTables(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"portable"}, names)

	// A table created portably still reports the DDL OCI callers expect.
	table, err := m.GetOCITable(ctx, compartmentA, "portable")
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE portable (pk STRING, sk STRING, PRIMARY KEY (SHARD(pk), sk))", table.DDLStatement)
	assert.Equal(t, nosql.CapacityOnDemand, table.Limits.CapacityMode)

	require.NoError(t, m.DeleteTable(ctx, "portable"))

	_, err = m.DescribeTable(ctx, "portable")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteTable(ctx, "portable")))
}

func TestPortableTableErrors(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(m.CreateTable(ctx, driver.TableConfig{})))
	assert.Equal(t, cerrors.InvalidArgument,
		cerrors.GetCode(m.CreateTable(ctx, driver.TableConfig{Name: "t"})))
}

func TestPortableItemCRUD(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t", PartitionKey: "pk", SortKey: "sk"}))

	item := map[string]any{"pk": "a", "sk": "1", "v": "x"}
	require.NoError(t, m.PutItem(ctx, "t", item))

	got, err := m.GetItem(ctx, "t", map[string]any{"pk": "a", "sk": "1"})
	require.NoError(t, err)
	assert.Equal(t, "x", got["v"])

	updated, err := m.UpdateItem(ctx, driver.UpdateItemInput{
		Table: "t",
		Key:   map[string]any{"pk": "a", "sk": "1"},
		Actions: []driver.UpdateAction{
			{Action: "SET", Field: "v", Value: "y"},
			{Action: "REMOVE", Field: "gone"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "y", updated["v"])

	_, err = m.UpdateItem(ctx, driver.UpdateItemInput{
		Table:   "t",
		Key:     map[string]any{"pk": "a", "sk": "1"},
		Actions: []driver.UpdateAction{{Action: "MULTIPLY", Field: "v"}},
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	require.NoError(t, m.DeleteItem(ctx, "t", map[string]any{"pk": "a", "sk": "1"}))

	_, err = m.GetItem(ctx, "t", map[string]any{"pk": "a", "sk": "1"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestPortableItemOpsOnMissingTable(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.PutItem(ctx, "nope", map[string]any{})))

	_, err := m.GetItem(ctx, "nope", map[string]any{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteItem(ctx, "nope", map[string]any{})))

	_, err = m.Scan(ctx, driver.ScanInput{Table: "nope"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.Query(ctx, driver.QueryInput{Table: "nope"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestPortableQueryAndScan(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t", PartitionKey: "pk", SortKey: "sk"}))
	require.NoError(t, m.BatchPutItems(ctx, "t", []map[string]any{
		{"pk": "a", "sk": "1", "v": "x"},
		{"pk": "a", "sk": "2", "v": "y"},
		{"pk": "b", "sk": "1", "v": "z"},
	}))

	res, err := m.Query(ctx, driver.QueryInput{
		Table:        "t",
		KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "a"},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Count)

	res, err = m.Query(ctx, driver.QueryInput{
		Table:        "t",
		KeyCondition: driver.KeyCondition{PartitionKey: "pk", PartitionVal: "a", SortOp: "=", SortVal: "2"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.Count)
	assert.Equal(t, "y", res.Items[0]["v"])

	res, err = m.Scan(ctx, driver.ScanInput{Table: "t", Filters: []driver.ScanFilter{{Field: "v", Op: "=", Value: "z"}}})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Count)

	_, err = m.Query(ctx, driver.QueryInput{Table: "t", IndexName: "missing"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	items, err := m.BatchGetItems(ctx, "t", []map[string]any{{"pk": "a", "sk": "1"}, {"pk": "z", "sk": "9"}})
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

func TestPortableTransactWriteItems(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t", PartitionKey: "pk"}))
	require.NoError(t, m.PutItem(ctx, "t", map[string]any{"pk": "gone"}))

	require.NoError(t, m.TransactWriteItems(ctx, "t",
		[]map[string]any{{"pk": "new"}},
		[]map[string]any{{"pk": "gone"}}))

	res, err := m.Scan(ctx, driver.ScanInput{Table: "t"})
	require.NoError(t, err)
	require.Equal(t, 1, res.Count)
	assert.Equal(t, "new", res.Items[0]["pk"])

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.TransactWriteItems(ctx, "nope", nil, nil)))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.BatchPutItems(ctx, "nope", nil)))
}

func TestPortableIndexes(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{
		Name: "t", PartitionKey: "pk", SortKey: "sk",
		GSIs: []driver.GSIConfig{{Name: "bySK", PartitionKey: "sk"}},
	}))

	list, err := m.ListIndexes(ctx, "t")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "bySK", list[0].Name)

	info, err := m.DescribeIndex(ctx, "t", "bySK")
	require.NoError(t, err)
	assert.Equal(t, nosql.StateActive, info.Status)

	_, err = m.CreateIndex(ctx, "t", driver.GSIConfig{Name: "bySK", PartitionKey: "sk"})
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	require.NoError(t, m.DeleteIndex(ctx, "t", "bySK"))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteIndex(ctx, "t", "bySK")))

	_, err = m.DescribeIndex(ctx, "t", "bySK")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.CreateIndex(ctx, "nope", driver.GSIConfig{Name: "x", PartitionKey: "pk"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestPortableAttributeTTL(t *testing.T) {
	m, clock := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t", PartitionKey: "pk"}))
	require.NoError(t, m.UpdateTTL(ctx, "t", driver.TTLConfig{Enabled: true, AttributeName: "expiresAt"}))

	cfg, err := m.DescribeTTL(ctx, "t")
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)

	require.NoError(t, m.PutItem(ctx, "t", map[string]any{
		"pk": "a", "expiresAt": clock.Now().Add(time.Hour).Unix(),
	}))

	_, err = m.GetItem(ctx, "t", map[string]any{"pk": "a"})
	require.NoError(t, err)

	clock.Advance(2 * time.Hour)

	_, err = m.GetItem(ctx, "t", map[string]any{"pk": "a"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestUpdateTTLRejectsReservedAttribute(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t", PartitionKey: "pk"}))

	err := m.UpdateTTL(ctx, "t", driver.TTLConfig{Enabled: true, AttributeName: "_ttlExpiration"})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	err = m.UpdateTTL(ctx, "t", driver.TTLConfig{Enabled: true})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

// TestStreamsAreUnimplemented pins the deliberate gap: OCI NoSQL publishes no
// DynamoDB-Streams or Cosmos-change-feed equivalent, so the portable stream
// operations report that rather than silently returning nothing.
func TestStreamsAreUnimplemented(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t", PartitionKey: "pk"}))

	err := m.UpdateStreamConfig(ctx, "t", driver.StreamConfig{Enabled: true})
	require.Error(t, err)
	assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "no change stream")

	_, err = m.GetStreamRecords(ctx, "t", 10, "")
	require.Error(t, err)
	assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(err))

	// An unknown table is still a 404 rather than the capability gap.
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.UpdateStreamConfig(ctx, "nope", driver.StreamConfig{})))

	_, err = m.GetStreamRecords(ctx, "nope", 10, "")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestPortableTags(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t", PartitionKey: "pk"}))
	require.NoError(t, m.TagResource(ctx, "t", map[string]string{"env": "dev", "team": "core"}))

	tags, err := m.ListTagsOfResource(ctx, "t")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"env": "dev", "team": "core"}, tags)

	require.NoError(t, m.UntagResource(ctx, "t", []string{"team"}))

	tags, err = m.ListTagsOfResource(ctx, "t")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"env": "dev"}, tags)

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.TagResource(ctx, "nope", nil)))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.UntagResource(ctx, "nope", nil)))

	_, err = m.ListTagsOfResource(ctx, "nope")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

// TestTableNamesScopePerCompartment is the per-compartment naming contract:
// real OCI scopes a NoSQL table name to its compartment, so the same name in
// two compartments is two tables, each addressing its own rows.
func TestTableNamesScopePerCompartment(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	inA := createUsers(t, m)

	inB, err := m.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentB,
		DDLStatement:  usersDDL,
		Limits:        provisioned(),
	})
	require.NoError(t, err)
	assert.NotEqual(t, inA.ID, inB.ID)

	got, err := m.GetOCITable(ctx, compartmentB, "users")
	require.NoError(t, err)
	assert.Equal(t, inB.ID, got.ID)

	_, err = m.PutOCIRow(ctx, compartmentB, "users",
		map[string]any{"id": float64(1), "email": "b@example.com", "name": "Bea"}, "")
	require.NoError(t, err)

	_, err = m.GetOCIRow(ctx, compartmentA, "users", map[string]string{"id": "1", "email": "b@example.com"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	listA, err := m.ListOCITables(ctx, compartmentA, "")
	require.NoError(t, err)
	require.Len(t, listA, 1)
	assert.Equal(t, inA.ID, listA[0].ID)

	require.NoError(t, m.DeleteOCITable(ctx, compartmentB, "users"))

	_, err = m.GetOCITable(ctx, compartmentA, "users")
	require.NoError(t, err)
}

// TestGetOCITableRejectsOtherCompartment pins that naming a table from a
// compartment that does not hold it is a 404, whether it is addressed by name
// or by the OCID the handler checks the compartment of.
func TestGetOCITableRejectsOtherCompartment(t *testing.T) {
	m, _ := newMock(t)
	table := createUsers(t, m)

	_, err := m.GetOCITable(context.Background(), compartmentB, "users")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	// An OCID is unique across the tenancy, so it still resolves; the handler
	// is what collapses the compartment mismatch into a 404.
	got, err := m.GetOCITable(context.Background(), compartmentB, table.ID)
	require.NoError(t, err)
	assert.Equal(t, compartmentA, got.CompartmentID)
}

// TestChangeOCITableCompartmentNameTaken refuses a move that would collide
// with a table of the same name already in the destination.
func TestChangeOCITableCompartmentNameTaken(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	createUsers(t, m)

	_, err := m.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentB,
		DDLStatement:  usersDDL,
		Limits:        provisioned(),
	})
	require.NoError(t, err)

	err = m.ChangeOCITableCompartment(ctx, compartmentA, "users", compartmentB)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))

	require.Error(t, m.ChangeOCITableCompartment(ctx, compartmentA, "users", ""))
}

// TestPortableAddressesTablesAcrossCompartments pins how the portable driver,
// whose shape carries no compartment, addresses a table once names are scoped:
// the compartment new resources default to, then the sole compartment holding
// that name.
func TestPortableAddressesTablesAcrossCompartments(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	_, err := m.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentB,
		DDLStatement:  "CREATE TABLE audits (id STRING, PRIMARY KEY (id))",
		Limits:        provisioned(),
	})
	require.NoError(t, err)

	// Held by one compartment only, so the portable driver reaches it.
	cfg, err := m.DescribeTable(ctx, "audits")
	require.NoError(t, err)
	assert.Equal(t, "id", cfg.PartitionKey)

	names, err := m.ListTables(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"audits"}, names)

	// Duplicated across compartments, one of them the default: that one wins.
	createUsers(t, m)

	_, err = m.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentB,
		DDLStatement:  usersDDL,
		Limits:        provisioned(),
	})
	require.NoError(t, err)

	require.NoError(t, m.PutItem(ctx, "users", map[string]any{"id": int64(1), "email": "a@example.com"}))

	row, err := m.GetOCIRow(ctx, compartmentA, "users", map[string]string{"id": "1", "email": "a@example.com"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), row.Value["id"])

	_, err = m.GetOCIRow(ctx, compartmentB, "users", map[string]string{"id": "1", "email": "a@example.com"})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

// TestPortableSkipsAmbiguousNames leaves a name held by two non-default
// compartments unaddressable rather than picking one of them.
func TestPortableSkipsAmbiguousNames(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	for _, c := range []string{compartmentB, "ocid1.compartment.oc1..cccc"} {
		_, err := m.CreateOCITable(ctx, nosql.TableSpec{
			CompartmentID: c,
			DDLStatement:  "CREATE TABLE audits (id STRING, PRIMARY KEY (id))",
			Limits:        provisioned(),
		})
		require.NoError(t, err)
	}

	_, err := m.DescribeTable(ctx, "audits")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	names, err := m.ListTables(ctx)
	require.NoError(t, err)
	assert.Empty(t, names)
}

// TestQueryOCIScopesTableByCompartment runs the same statement in two
// compartments and gets each compartment's own rows back.
func TestQueryOCIScopesTableByCompartment(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	createUsers(t, m)

	_, err := m.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentB,
		DDLStatement:  usersDDL,
		Limits:        provisioned(),
	})
	require.NoError(t, err)

	_, err = m.PutOCIRow(ctx, compartmentA, "users",
		map[string]any{"id": float64(1), "email": "a@example.com", "name": "Ada"}, "")
	require.NoError(t, err)

	rows, err := m.QueryOCI(ctx, compartmentA, "SELECT * FROM users", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "Ada", rows[0]["name"])

	rows, err = m.QueryOCI(ctx, compartmentB, "SELECT * FROM users", 0)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// TestPortableQuerySortOperators runs every sort-key operator the portable
// driver declares, on both a lexical and a numeric sort key: the comparison
// orders numerically when both sides parse as numbers and lexically otherwise.
func TestPortableQuerySortOperators(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t", PartitionKey: "pk", SortKey: "sk"}))
	require.NoError(t, m.BatchPutItems(ctx, "t", []map[string]any{
		{"pk": "a", "sk": "2"},
		{"pk": "a", "sk": "10"},
		{"pk": "a", "sk": "30"},
		{"pk": "a", "sk": "beta"},
	}))

	tests := []struct {
		name   string
		op     string
		val    any
		end    any
		expect []string
	}{
		{name: "less than orders numerically", op: nosql.OpLessThan, val: "30", expect: []string{"10", "2"}},
		{name: "greater than", op: nosql.OpGreaterThan, val: "2", expect: []string{"10", "30", "beta"}},
		{name: "less or equal", op: nosql.OpLessEqual, val: "10", expect: []string{"10", "2"}},
		// "beta" parses as no number, so it is compared lexically and sorts above every digit.
		{name: "greater or equal", op: nosql.OpGreaterEqual, val: "30", expect: []string{"30", "beta"}},
		{name: "between spans both ends", op: nosql.OpBetween, val: "2", end: "10", expect: []string{"10", "2"}},
		{name: "begins with is lexical", op: nosql.OpBeginsWith, val: "be", expect: []string{"beta"}},
		{name: "contains is lexical", op: nosql.OpContains, val: "et", expect: []string{"beta"}},
		{name: "not equal", op: nosql.OpNotEqual, val: "beta", expect: []string{"10", "2", "30"}},
		{name: "an unknown operator matches nothing", op: "LIKE", val: "beta"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := m.Query(ctx, driver.QueryInput{
				Table: "t",
				KeyCondition: driver.KeyCondition{
					PartitionKey: "pk", PartitionVal: "a",
					SortOp: tc.op, SortVal: tc.val, SortValEnd: tc.end,
				},
			})
			require.NoError(t, err)

			got := make([]string, 0, len(res.Items))
			for _, item := range res.Items {
				got = append(got, item["sk"].(string))
			}

			assert.ElementsMatch(t, tc.expect, got)
		})
	}
}

// TestPortableScanFilterOperators runs the same comparisons through Scan's
// filters, which is the other caller of the shared operator.
func TestPortableScanFilterOperators(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t", PartitionKey: "pk"}))
	require.NoError(t, m.BatchPutItems(ctx, "t", []map[string]any{
		{"pk": "a", "n": "5", "s": "alpha"},
		{"pk": "b", "n": "40", "s": "beta"},
	}))

	tests := []struct {
		name   string
		filter driver.ScanFilter
		expect int
	}{
		{name: "numeric less than", filter: driver.ScanFilter{Field: "n", Op: nosql.OpLessThan, Value: "40"}, expect: 1},
		{name: "numeric greater equal", filter: driver.ScanFilter{Field: "n", Op: nosql.OpGreaterEqual, Value: "5"}, expect: 2},
		{name: "lexical greater than", filter: driver.ScanFilter{Field: "s", Op: nosql.OpGreaterThan, Value: "alpha"}, expect: 1},
		{name: "lexical less equal", filter: driver.ScanFilter{Field: "s", Op: nosql.OpLessEqual, Value: "alpha"}, expect: 1},
		{name: "contains", filter: driver.ScanFilter{Field: "s", Op: nosql.OpContains, Value: "eta"}, expect: 1},
		{name: "begins with", filter: driver.ScanFilter{Field: "s", Op: nosql.OpBeginsWith, Value: "al"}, expect: 1},
		{name: "not equal", filter: driver.ScanFilter{Field: "s", Op: nosql.OpNotEqual, Value: "beta"}, expect: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := m.Scan(ctx, driver.ScanInput{Table: "t", Filters: []driver.ScanFilter{tc.filter}})
			require.NoError(t, err)
			assert.Equal(t, tc.expect, res.Count)
		})
	}
}

// TestPortableQueryOnIndex orders and filters on an index's own key columns
// rather than the table's.
func TestPortableQueryOnIndex(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	_, err := m.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentA,
		DDLStatement: "CREATE TABLE people (pk STRING, sk STRING, city STRING, age STRING, " +
			"PRIMARY KEY (SHARD(pk), sk))",
		Limits: provisioned(),
	})
	require.NoError(t, err)

	_, err = m.CreateOCIIndex(ctx, compartmentA, "people",
		nosql.IndexSpec{Name: "byCity", Columns: []string{"city", "age"}}, false)
	require.NoError(t, err)

	require.NoError(t, m.BatchPutItems(ctx, "people", []map[string]any{
		{"pk": "1", "sk": "a", "city": "pune", "age": "30"},
		{"pk": "2", "sk": "b", "city": "pune", "age": "40"},
		{"pk": "3", "sk": "c", "city": "goa", "age": "50"},
	}))

	res, err := m.Query(ctx, driver.QueryInput{
		Table:     "people",
		IndexName: "byCity",
		KeyCondition: driver.KeyCondition{
			PartitionKey: "city", PartitionVal: "pune", SortOp: nosql.OpGreaterThan, SortVal: "30",
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.Count)
	assert.Equal(t, "2", res.Items[0]["pk"])
}

// TestTypedColumnCoercion covers every column type a row value is fitted to,
// through the wire's string key form and the JSON body's decoded form.
func TestTypedColumnCoercion(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	ddl := "CREATE TABLE typed (id LONG, ratio DOUBLE, score FLOAT, amount NUMBER, ok BOOLEAN, " +
		"blob BINARY, at TIMESTAMP, doc JSON, PRIMARY KEY (id))"

	_, err := m.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentA, DDLStatement: ddl, Limits: provisioned(),
	})
	require.NoError(t, err)

	value := map[string]any{
		"id": float64(7), "ratio": 1.5, "score": 2.5, "amount": 3.5, "ok": true,
		"blob": "YWJj", "at": "2026-08-21T12:00:00Z", "doc": map[string]any{"k": "v"},
	}

	_, err = m.PutOCIRow(ctx, compartmentA, "typed", value, "")
	require.NoError(t, err)

	// The LONG key round-trips through parseTyped's string form.
	row, err := m.GetOCIRow(ctx, compartmentA, "typed", map[string]string{"id": "7"})
	require.NoError(t, err)
	assert.Equal(t, int64(7), row.Value["id"])
	assert.InEpsilon(t, 1.5, row.Value["ratio"], 1e-9)
	assert.Equal(t, true, row.Value["ok"])
	assert.Equal(t, "YWJj", row.Value["blob"])
	assert.Equal(t, map[string]any{"k": "v"}, row.Value["doc"])
}

func TestTypedColumnCoercionErrors(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	ddl := "CREATE TABLE typed (id LONG, ratio DOUBLE, ok BOOLEAN, at TIMESTAMP, PRIMARY KEY (id))"

	_, err := m.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentA, DDLStatement: ddl, Limits: provisioned(),
	})
	require.NoError(t, err)

	tests := []struct {
		name  string
		value map[string]any
	}{
		{name: "a LONG takes no fraction", value: map[string]any{"id": 1.5}},
		{name: "a LONG takes no string", value: map[string]any{"id": "seven"}},
		{name: "a DOUBLE takes no string", value: map[string]any{"id": float64(1), "ratio": "1.5"}},
		{name: "a BOOLEAN takes no string", value: map[string]any{"id": float64(1), "ok": "yes"}},
		{name: "a TIMESTAMP takes no number", value: map[string]any{"id": float64(1), "at": float64(1)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.PutOCIRow(ctx, compartmentA, "typed", tc.value, "")
			require.Error(t, err)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
		})
	}

	// A key value that does not parse as its column's type is refused too.
	for _, key := range []map[string]string{{"id": "seven"}} {
		_, err := m.GetOCIRow(ctx, compartmentA, "typed", key)
		require.Error(t, err)
		assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
	}
}

// TestTypedColumnDefaults fills an absent column from its DDL default, parsed
// into the column's type rather than left as the declared text.
func TestTypedColumnDefaults(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	ddl := "CREATE TABLE defs (id LONG, hits LONG DEFAULT 3, ratio DOUBLE DEFAULT 1.5, " +
		"ok BOOLEAN DEFAULT true, tag STRING, PRIMARY KEY (id))"

	_, err := m.CreateOCITable(ctx, nosql.TableSpec{
		CompartmentID: compartmentA, DDLStatement: ddl, Limits: provisioned(),
	})
	require.NoError(t, err)

	_, err = m.PutOCIRow(ctx, compartmentA, "defs", map[string]any{"id": float64(1)}, "")
	require.NoError(t, err)

	row, err := m.GetOCIRow(ctx, compartmentA, "defs", map[string]string{"id": "1"})
	require.NoError(t, err)
	assert.Equal(t, int64(3), row.Value["hits"])
	assert.InEpsilon(t, 1.5, row.Value["ratio"], 1e-9)
	assert.Equal(t, true, row.Value["ok"])
	assert.Nil(t, row.Value["tag"])
}

// captureMonitoring records the metric data the mock publishes. The embedded
// interface stays nil: emitMetric calls PutMetricData and nothing else.
type captureMonitoring struct {
	mondriver.Monitoring

	data []mondriver.MetricDatum
}

func (c *captureMonitoring) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	c.data = append(c.data, data...)

	return nil
}

// TestSetMonitoringPublishesUnits points the mock at a monitoring service and
// checks read and write unit consumption reaches it, dimensioned by table.
func TestSetMonitoringPublishesUnits(t *testing.T) {
	m, _ := newMock(t)
	mon := &captureMonitoring{}
	m.SetMonitoring(mon)

	ctx := context.Background()
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "t", PartitionKey: "pk"}))
	require.NoError(t, m.PutItem(ctx, "t", map[string]any{"pk": "a"}))

	_, err := m.GetItem(ctx, "t", map[string]any{"pk": "a"})
	require.NoError(t, err)

	names := make([]string, 0, len(mon.data))
	for _, d := range mon.data {
		assert.Equal(t, "oci_nosql", d.Namespace)
		assert.Equal(t, "t", d.Dimensions["tableName"])

		names = append(names, d.MetricName)
	}

	assert.Subset(t, names, []string{"ReadUnits", "WriteUnits"})
}
