package timestreamwrite

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/timestreamwrite/driver"
)

// defaultMagneticStoreWriteProperties is the block reported for a table that
// never set one: magnetic-store writes disabled. Stored once at create, it
// never drifts an IaC plan.
//
//nolint:gochecknoglobals // static default document
var defaultMagneticStoreWriteProperties = json.RawMessage(`{"EnableMagneticStoreWrites":false}`)

// defaultSchema is the composite partition key Timestream assigns when a table
// is created without a schema: a single MEASURE partition key. Stored once at
// create, it round-trips stably across reads.
//
//nolint:gochecknoglobals // static default document
var defaultSchema = json.RawMessage(`{"CompositePartitionKey":[{"Type":"MEASURE"}]}`)

// CreateTable adds a table to an existing database with stable computed fields
// (arn, status ACTIVE, creationTime). The parent database must exist
// (ResourceNotFoundException otherwise), and a duplicate table name is a
// ConflictException. RetentionProperties, MagneticStoreWriteProperties and
// Schema round-trip verbatim; omitted blocks report the real-API defaults.
func (m *Mock) CreateTable(_ context.Context, in *driver.CreateTableInput) (*driver.Table, error) {
	if in.DatabaseName == "" {
		return nil, validation("DatabaseName is required")
	}

	if in.TableName == "" {
		return nil, validation("TableName is required")
	}

	if _, ok := m.databases.Get(in.DatabaseName); !ok {
		return nil, notFound("database %q does not exist", in.DatabaseName)
	}

	key := tableKey(in.DatabaseName, in.TableName)
	if _, ok := m.tables.Get(key); ok {
		return nil, conflict("table %q already exists in database %q", in.TableName, in.DatabaseName)
	}

	magneticProps := copyRaw(in.MagneticStoreWriteProperties)
	if magneticProps == nil {
		magneticProps = copyRaw(defaultMagneticStoreWriteProperties)
	}

	schema := copyRaw(in.Schema)
	if schema == nil {
		schema = copyRaw(defaultSchema)
	}

	now := m.now()

	table := driver.Table{
		Arn:                          m.tableARN(in.DatabaseName, in.TableName),
		TableName:                    in.TableName,
		DatabaseName:                 in.DatabaseName,
		TableStatus:                  driver.TableStatusActive,
		RetentionProperties:          copyRetention(in.RetentionProperties),
		MagneticStoreWriteProperties: magneticProps,
		Schema:                       schema,
		CreationTime:                 now,
		LastUpdatedTime:              now,
		Tags:                         copyTags(in.Tags),
	}

	m.tables.Set(key, table)

	out := copyTable(&table)

	return &out, nil
}

// DescribeTable returns a table by database and name, or a
// ResourceNotFoundException.
func (m *Mock) DescribeTable(_ context.Context, databaseName, tableName string) (*driver.Table, error) {
	table, ok := m.tables.Get(tableKey(databaseName, tableName))
	if !ok {
		return nil, notFound("table %q does not exist in database %q", tableName, databaseName)
	}

	out := copyTable(&table)

	return &out, nil
}

// UpdateTable applies the supplied blocks, leaving omitted parameters unchanged.
// The computed arn, status and creationTime are preserved; updatedAt is bumped.
func (m *Mock) UpdateTable(_ context.Context, in *driver.UpdateTableInput) (*driver.Table, error) {
	key := tableKey(in.DatabaseName, in.TableName)

	ok := m.tables.Update(key, func(t driver.Table) driver.Table {
		if in.RetentionProperties != nil {
			t.RetentionProperties = copyRetention(in.RetentionProperties)
		}

		if in.MagneticStoreWriteProperties != nil {
			t.MagneticStoreWriteProperties = copyRaw(in.MagneticStoreWriteProperties)
		}

		if in.Schema != nil {
			t.Schema = copyRaw(in.Schema)
		}

		t.LastUpdatedTime = m.now()

		return t
	})
	if !ok {
		return nil, notFound("table %q does not exist in database %q", in.TableName, in.DatabaseName)
	}

	table, _ := m.tables.Get(key)
	out := copyTable(&table)

	return &out, nil
}

// DeleteTable removes a table, or a ResourceNotFoundException if it is missing.
func (m *Mock) DeleteTable(_ context.Context, databaseName, tableName string) error {
	key := tableKey(databaseName, tableName)
	if _, ok := m.tables.Get(key); !ok {
		return notFound("table %q does not exist in database %q", tableName, databaseName)
	}

	m.tables.Delete(key)

	return nil
}

// ListTables returns a deterministic page of tables ordered by key. When
// databaseName is non-empty the result is filtered to that database, which must
// exist (ResourceNotFoundException otherwise).
func (m *Mock) ListTables(
	_ context.Context, databaseName string, page driver.Page,
) (tables []driver.Table, nextToken string, err error) {
	if databaseName != "" {
		if _, ok := m.databases.Get(databaseName); !ok {
			return nil, "", notFound("database %q does not exist", databaseName)
		}
	}

	stored := m.tables.SortedValues()

	filtered := make([]driver.Table, 0, len(stored))

	for i := range stored {
		if databaseName == "" || stored[i].DatabaseName == databaseName {
			filtered = append(filtered, stored[i])
		}
	}

	start, end, next := paginate(len(filtered), page)

	out := make([]driver.Table, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copyTable(&filtered[i]))
	}

	return out, next, nil
}
