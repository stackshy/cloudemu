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
			name:        "implicit shard key is the leading primary key column",
			statement:   "CREATE TABLE orders (sku STRING, qty INTEGER, PRIMARY KEY (sku))",
			expectTable: "orders",
			expectShard: []string{"sku"},
			expectKey:   []string{"sku"},
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
		"CREATE TABLE t (a STRING, b STRING NOT NULL, c STRING DEFAULT 'hi', PRIMARY KEY (a))")
	require.NoError(t, err)

	require.Len(t, d.Schema.Columns, 3)
	assert.True(t, d.Schema.Columns[0].IsNullable)
	assert.False(t, d.Schema.Columns[1].IsNullable)
	assert.Equal(t, "hi", d.Schema.Columns[2].DefaultValue)
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
			name:       "composite shard key",
			statement:  "CREATE TABLE t (a STRING, b STRING, PRIMARY KEY (SHARD(a, b)))",
			expectWord: "composite shard keys",
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
