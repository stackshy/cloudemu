package timestreamwrite

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/timestreamwrite/driver"
)

// CreateDatabase provisions a database with stable computed fields (arn,
// kmsKeyId, creationTime). When no KMS key is supplied the database reports an
// AWS-managed key ARN, mirroring real Timestream. A duplicate name is a
// ConflictException.
func (m *Mock) CreateDatabase(_ context.Context, in *driver.CreateDatabaseInput) (*driver.Database, error) {
	if in.DatabaseName == "" {
		return nil, validation("DatabaseName is required")
	}

	if _, ok := m.databases.Get(in.DatabaseName); ok {
		return nil, conflict("database %q already exists", in.DatabaseName)
	}

	kmsKey := in.KmsKeyID
	if kmsKey == "" {
		kmsKey = m.defaultKmsKeyARN()
	}

	now := m.now()

	db := driver.Database{
		Arn:             m.databaseARN(in.DatabaseName),
		DatabaseName:    in.DatabaseName,
		KmsKeyID:        kmsKey,
		CreationTime:    now,
		LastUpdatedTime: now,
		Tags:            copyTags(in.Tags),
	}

	m.databases.Set(in.DatabaseName, db)

	out := m.copyDatabase(&db)

	return &out, nil
}

// DescribeDatabase returns the database by name, or a ResourceNotFoundException.
func (m *Mock) DescribeDatabase(_ context.Context, name string) (*driver.Database, error) {
	db, ok := m.databases.Get(name)
	if !ok {
		return nil, notFound("database %q does not exist", name)
	}

	out := m.copyDatabase(&db)

	return &out, nil
}

// UpdateDatabase applies the supplied KMS key, leaving the arn and creationTime
// unchanged; updatedAt is bumped. Only the KMS key is mutable on a database.
func (m *Mock) UpdateDatabase(_ context.Context, in *driver.UpdateDatabaseInput) (*driver.Database, error) {
	ok := m.databases.Update(in.DatabaseName, func(d driver.Database) driver.Database {
		if in.KmsKeyID != "" {
			d.KmsKeyID = in.KmsKeyID
		}

		d.LastUpdatedTime = m.now()

		return d
	})
	if !ok {
		return nil, notFound("database %q does not exist", in.DatabaseName)
	}

	db, _ := m.databases.Get(in.DatabaseName)
	out := m.copyDatabase(&db)

	return &out, nil
}

// DeleteDatabase removes an empty database. A database that still has tables is
// a ValidationException, matching real Timestream (all tables must be deleted
// first).
func (m *Mock) DeleteDatabase(_ context.Context, name string) error {
	if _, ok := m.databases.Get(name); !ok {
		return notFound("database %q does not exist", name)
	}

	if m.tableCount(name) > 0 {
		return validation("the database %q is not empty; delete its tables first", name)
	}

	m.databases.Delete(name)

	return nil
}

// ListDatabases returns a deterministic page of databases ordered by name.
func (m *Mock) ListDatabases(_ context.Context, page driver.Page) (databases []driver.Database, nextToken string, err error) {
	stored := m.databases.SortedValues()

	start, end, next := paginate(len(stored), page)

	out := make([]driver.Database, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, m.copyDatabase(&stored[i]))
	}

	return out, next, nil
}
