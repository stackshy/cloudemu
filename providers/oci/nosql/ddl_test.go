package nosql_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/nosql"
)

func TestParseCreateTable(t *testing.T) {
	tests := []struct {
		name        string
		statement   string
		expectTable string
		expectShard []string
		expectKey   []string
		expectCols  int
		expectTTL   int
	}{
		{
			name:        "explicit shard and sort key",
			statement:   "CREATE TABLE users (id INTEGER, email STRING, PRIMARY KEY (SHARD(id), email))",
			expectTable: "users",
			expectShard: []string{"id"},
			expectKey:   []string{"id", "email"},
			expectCols:  2,
		},
		{
			name:        "implicit shard key on a single-column primary key",
			statement:   "CREATE TABLE orders (sku STRING, qty INTEGER, PRIMARY KEY (sku))",
			expectTable: "orders",
			expectShard: []string{"sku"},
			expectKey:   []string{"sku"},
			expectCols:  2,
		},
		{
			name:        "implicit shard key is the whole primary key",
			statement:   "CREATE TABLE users (id INTEGER, email STRING, PRIMARY KEY (id, email))",
			expectTable: "users",
			expectShard: []string{"id", "email"},
			expectKey:   []string{"id", "email"},
			expectCols:  2,
		},
		{
			name:        "composite shard key",
			statement:   "CREATE TABLE t (a STRING, b STRING, PRIMARY KEY (SHARD(a, b)))",
			expectTable: "t",
			expectShard: []string{"a", "b"},
			expectKey:   []string{"a", "b"},
			expectCols:  2,
		},
		{
			name:        "if not exists",
			statement:   "CREATE TABLE IF NOT EXISTS t (a STRING, PRIMARY KEY (a))",
			expectTable: "t",
			expectShard: []string{"a"},
			expectKey:   []string{"a"},
			expectCols:  1,
		},
		{
			name: "multi-line with ttl, modifiers and json",
			statement: `CREATE TABLE stream (
				id LONG,
				payload JSON,
				label STRING NOT NULL DEFAULT 'none',
				ts TIMESTAMP(3),
				PRIMARY KEY (SHARD(id))
			) USING TTL 7 DAYS`,
			expectTable: "stream",
			expectShard: []string{"id"},
			expectKey:   []string{"id"},
			expectCols:  4,
			expectTTL:   7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := nosql.ParseDDL(tc.statement)
			require.NoError(t, err)

			assert.Equal(t, nosql.DDLCreateTable, d.Kind)
			assert.Equal(t, tc.expectTable, d.Table)
			assert.Equal(t, tc.expectShard, d.Schema.ShardKey)
			assert.Equal(t, tc.expectKey, d.Schema.PrimaryKey)
			assert.Len(t, d.Schema.Columns, tc.expectCols)
			assert.Equal(t, tc.expectTTL, d.Schema.TTL.Days)
		})
	}
}

func TestParseCreateTableColumnModifiers(t *testing.T) {
	d, err := nosql.ParseDDL(
		"CREATE TABLE t (a STRING, b STRING NOT NULL DEFAULT 'x', c STRING DEFAULT 'hi', PRIMARY KEY (a))")
	require.NoError(t, err)

	require.Len(t, d.Schema.Columns, 3)
	assert.False(t, d.Schema.Columns[0].IsNullable, "a primary key column is never nullable")
	assert.False(t, d.Schema.Columns[1].IsNullable)
	assert.Equal(t, "x", d.Schema.Columns[1].DefaultValue)
	assert.True(t, d.Schema.Columns[2].IsNullable)
	assert.Equal(t, "hi", d.Schema.Columns[2].DefaultValue)
}

// DEFAULT takes one value and NOT NULL may follow or precede it, as the Oracle
// NoSQL grammar allows.
func TestParseDefaultAndNotNullInEitherOrder(t *testing.T) {
	tests := []struct {
		name          string
		column        string
		expectDefault string
	}{
		{name: "default then not null", column: "age INTEGER DEFAULT 0 NOT NULL", expectDefault: "0"},
		{name: "not null then default", column: "age INTEGER NOT NULL DEFAULT 0", expectDefault: "0"},
		{name: "quoted default with spaces", column: "label STRING DEFAULT 'a  b, c' NOT NULL", expectDefault: "a  b, c"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := nosql.ParseDDL("CREATE TABLE t (id INTEGER, " + tc.column + ", PRIMARY KEY (id))")
			require.NoError(t, err)

			require.Len(t, d.Schema.Columns, 2)
			assert.Equal(t, tc.expectDefault, d.Schema.Columns[1].DefaultValue)
			assert.False(t, d.Schema.Columns[1].IsNullable)
		})
	}
}

// Column names fold case, and the key keeps the declared spelling.
func TestParseColumnNamesFoldCase(t *testing.T) {
	d, err := nosql.ParseDDL("CREATE TABLE d2 (ID INTEGER, PRIMARY KEY(id))")
	require.NoError(t, err)

	assert.Equal(t, []string{"ID"}, d.Schema.PrimaryKey)
	assert.Equal(t, []string{"ID"}, d.Schema.ShardKey)

	_, err = nosql.ParseDDL("CREATE TABLE d3 (a STRING, A STRING, PRIMARY KEY (a))")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declared twice")
}

// TestParseDDLRejectsUnsupported is the contract that keeps the parser from
// accepting a statement it would then ignore: every unsupported construct is
// refused, and the message names what was refused.
func TestParseDDLRejectsUnsupported(t *testing.T) {
	tests := []struct {
		name       string
		statement  string
		expectWord string
	}{
		{
			name:       "empty statement",
			statement:  "  ",
			expectWord: "ddlStatement is required",
		},
		{
			name:       "unknown verb",
			statement:  "TRUNCATE TABLE users",
			expectWord: "TRUNCATE TABLE",
		},
		{
			name:       "drop table is not a ddl statement here",
			statement:  "DROP TABLE users",
			expectWord: "DROP TABLE",
		},
		{
			name:       "create index goes through the indexes endpoint",
			statement:  "CREATE INDEX i ON users (email)",
			expectWord: "CREATE INDEX",
		},
		{
			name:       "three column primary key",
			statement:  "CREATE TABLE t (a STRING, b STRING, c STRING, PRIMARY KEY (SHARD(a), b, c))",
			expectWord: "more than 2 columns",
		},
		{
			name:       "undeclared primary key column",
			statement:  "CREATE TABLE t (a STRING, PRIMARY KEY (z))",
			expectWord: `"z" is not declared`,
		},
		{
			name:       "no primary key",
			statement:  "CREATE TABLE t (a STRING)",
			expectWord: "no PRIMARY KEY",
		},
		{
			name:       "structured column type",
			statement:  "CREATE TABLE t (a STRING, b ARRAY(STRING), PRIMARY KEY (a))",
			expectWord: `"ARRAY" is not supported`,
		},
		{
			name:       "record column type",
			statement:  "CREATE TABLE t (a STRING, b RECORD(x STRING), PRIMARY KEY (a))",
			expectWord: `"RECORD" is not supported`,
		},
		{
			name:       "generated identity column",
			statement:  "CREATE TABLE t (a INTEGER GENERATED ALWAYS AS IDENTITY, PRIMARY KEY (a))",
			expectWord: "unsupported column modifier",
		},
		{
			name:       "mr_counter column",
			statement:  "CREATE TABLE t (a STRING, b INTEGER AS MR_COUNTER, PRIMARY KEY (a))",
			expectWord: "unsupported column modifier",
		},
		{
			name:       "ttl in hours",
			statement:  "CREATE TABLE t (a STRING, PRIMARY KEY (a)) USING TTL 6 HOURS",
			expectWord: "TTL in DAYS",
		},
		{
			name:       "unknown trailing clause",
			statement:  "CREATE TABLE t (a STRING, PRIMARY KEY (a)) WITH SCHEMA FROZEN",
			expectWord: "unsupported clause",
		},
		{
			name:       "child table name",
			statement:  "CREATE TABLE parent.child (a STRING, PRIMARY KEY (a))",
			expectWord: "is not a valid identifier",
		},
		{
			name:       "unbalanced parentheses",
			statement:  "CREATE TABLE t (a STRING, PRIMARY KEY (a)",
			expectWord: "unbalanced parentheses",
		},
		{
			name:       "alter with modify",
			statement:  "ALTER TABLE t (MODIFY a STRING)",
			expectWord: "unsupported ALTER TABLE action",
		},
		{
			name:       "alter freeze schema",
			statement:  "ALTER TABLE t FREEZE SCHEMA",
			expectWord: "unsupported ALTER TABLE clause",
		},
		{
			name:       "not null without a default",
			statement:  "CREATE TABLE t (a STRING, b STRING NOT NULL, PRIMARY KEY (a))",
			expectWord: `"b" is NOT NULL and declares no DEFAULT`,
		},
		{
			name:       "alter adding not null without a default",
			statement:  "ALTER TABLE t (ADD b STRING NOT NULL)",
			expectWord: `"b" is NOT NULL and declares no DEFAULT`,
		},
		{
			name:       "default that does not fit its column",
			statement:  "CREATE TABLE t (a STRING, b INTEGER DEFAULT abc, PRIMARY KEY (a))",
			expectWord: `does not fit column "b"`,
		},
		{
			name:       "default given twice",
			statement:  "CREATE TABLE t (a STRING, b INTEGER DEFAULT 1 DEFAULT 2, PRIMARY KEY (a))",
			expectWord: "DEFAULT twice",
		},
		{
			name:       "shard not leading the primary key",
			statement:  "CREATE TABLE t (a STRING, b STRING, PRIMARY KEY (a, SHARD(b)))",
			expectWord: "SHARD must lead",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := nosql.ParseDDL(tc.statement)

			require.Error(t, err)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
			assert.Contains(t, err.Error(), tc.expectWord)
		})
	}
}

func TestParseAlterTable(t *testing.T) {
	tests := []struct {
		name       string
		statement  string
		expectAdd  int
		expectDrop int
		expectTTL  int
	}{
		{name: "add column", statement: "ALTER TABLE t (ADD nickname STRING)", expectAdd: 1},
		{name: "drop column", statement: "ALTER TABLE t (DROP nickname)", expectDrop: 1},
		{
			name:       "add and drop together",
			statement:  "ALTER TABLE t (ADD a STRING, DROP b)",
			expectAdd:  1,
			expectDrop: 1,
		},
		{name: "ttl", statement: "ALTER TABLE t USING TTL 3 DAYS", expectTTL: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := nosql.ParseDDL(tc.statement)
			require.NoError(t, err)

			assert.Equal(t, nosql.DDLAlterTable, d.Kind)
			assert.Equal(t, "t", d.Table)
			assert.Len(t, d.Alter.AddColumns, tc.expectAdd)
			assert.Len(t, d.Alter.DropColumns, tc.expectDrop)

			if tc.expectTTL > 0 {
				require.NotNil(t, d.Alter.TTL)
				assert.Equal(t, tc.expectTTL, d.Alter.TTL.Days)
			}
		})
	}
}
