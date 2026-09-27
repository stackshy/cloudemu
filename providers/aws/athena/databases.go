package athena

import (
	"context"
	"regexp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// GetDatabase returns a database within a data catalog. A missing database is a
// MetadataException "Database <name> not found", like real Athena.
func (m *Mock) GetDatabase(ctx context.Context, catalogName, databaseName string) (*driver.Database, error) {
	if catalogName == "" {
		catalogName = driver.DefaultDataCatalog
	}

	catalogID, err := m.glueCatalogID(catalogName)
	if err != nil {
		return nil, err
	}

	m.importLegacyDatabases(ctx)

	db, err := m.catalog.GetDatabase(ctx, catalogID, databaseName)
	if err != nil {
		if cerrors.IsNotFound(err) {
			err = cerrors.Newf(cerrors.NotFound, "Database %s not found", databaseName)
		}

		return nil, catalogFailure(err)
	}

	out := databaseFromGlue(db)

	return &out, nil
}

// ListDatabases returns the databases in a data catalog, sorted by name.
func (m *Mock) ListDatabases(ctx context.Context, catalogName string, page driver.Pagination) ([]driver.Database, string, error) {
	if err := checkCatalogPage(page); err != nil {
		return nil, "", err
	}

	catalogID, err := m.glueCatalogID(catalogName)
	if err != nil {
		return nil, "", err
	}

	m.importLegacyDatabases(ctx)

	dbs, err := allDatabases(ctx, m.catalog, catalogID)
	if err != nil {
		return nil, "", catalogFailure(err)
	}

	all := make([]driver.Database, 0, len(dbs))
	for i := range dbs {
		all = append(all, databaseFromGlue(&dbs[i]))
	}

	return paginate(all, page)
}

// GetTableMetadata returns a Glue table as Athena reports it.
func (m *Mock) GetTableMetadata(ctx context.Context, catalogName, databaseName, tableName string) (*driver.TableMetadata, error) {
	catalogID, err := m.glueCatalogID(catalogName)
	if err != nil {
		return nil, err
	}

	m.importLegacyDatabases(ctx)

	tbl, err := m.catalog.GetTable(ctx, catalogID, databaseName, tableName)
	if err != nil {
		return nil, catalogFailure(err)
	}

	out := tableMetadataFromGlue(tbl)

	return &out, nil
}

// ListTableMetadata lists a database's Glue tables, sorted by name and
// filtered by a Hive style name pattern.
func (m *Mock) ListTableMetadata(
	ctx context.Context, catalogName, databaseName, expression string, page driver.Pagination,
) ([]driver.TableMetadata, string, error) {
	if err := checkCatalogPage(page); err != nil {
		return nil, "", err
	}

	catalogID, err := m.glueCatalogID(catalogName)
	if err != nil {
		return nil, "", err
	}

	match, err := tableNameMatcher(expression)
	if err != nil {
		return nil, "", err
	}

	m.importLegacyDatabases(ctx)

	tables, err := allTables(ctx, m.catalog, catalogID, databaseName)
	if err != nil {
		return nil, "", catalogFailure(err)
	}

	all := make([]driver.TableMetadata, 0, len(tables))

	for i := range tables {
		if match(tables[i].Name) {
			all = append(all, tableMetadataFromGlue(&tables[i]))
		}
	}

	return paginate(all, page)
}

// maxCatalogResults is the MaxResults ceiling of ListDatabases and
// ListTableMetadata.
const maxCatalogResults = 50

// checkCatalogPage rejects a MaxResults outside 1-50. Zero means unset.
func checkCatalogPage(page driver.Pagination) error {
	if page.MaxResults < 0 || page.MaxResults > maxCatalogResults {
		return invalidRequest("MaxResults must be between 1 and %d", maxCatalogResults)
	}

	return nil
}

// tableNameMatcher compiles the ListTableMetadata Expression. It is a regex
// where a "*" not already after "." means ".*", as in Hive and Glue. Matching
// ignores case and covers the whole name.
func tableNameMatcher(expression string) (func(string) bool, error) {
	if expression == "" {
		return func(string) bool { return true }, nil
	}

	var b strings.Builder

	prev := rune(0)

	for _, r := range expression {
		if r == '*' && prev != '.' {
			b.WriteRune('.')
		}

		b.WriteRune(r)
		prev = r
	}

	re, err := regexp.Compile("(?i)^(?:" + b.String() + ")$")
	if err != nil {
		return nil, invalidRequest("invalid Expression %q", expression)
	}

	return re.MatchString, nil
}

// catalogFailure maps a Glue catalog error onto Athena's MetadataException,
// which is how Athena reports metastore errors such as a missing table. Bad
// input stays an InvalidRequestException.
func catalogFailure(err error) error {
	code := cerrors.GetCode(err)

	ex := driver.ExMetadata
	if code == cerrors.InvalidArgument {
		ex = driver.ExInvalidRequest
	}

	return &driver.APIError{Exception: ex, Err: cerrors.New(code, cerrors.Message(err))}
}

func databaseFromGlue(db *gluedriver.Database) driver.Database {
	return driver.Database{Name: db.Name, Description: db.Description, Parameters: copyStringMap(db.Parameters)}
}

// tableMetadataFromGlue flattens a Glue table the way Athena does: the table
// parameters plus the storage descriptor's formats, location and SerDe.
func tableMetadataFromGlue(t *gluedriver.Table) driver.TableMetadata {
	params := copyStringMap(t.Parameters)
	if params == nil {
		params = map[string]string{}
	}

	var cols []driver.Column

	if sd := t.StorageDescriptor; sd != nil {
		cols = columnsFromGlue(sd.Columns)
		setIfNotEmpty(params, "inputformat", sd.InputFormat)
		setIfNotEmpty(params, "outputformat", sd.OutputFormat)
		setIfNotEmpty(params, "location", sd.Location)

		if sd.SerdeInfo != nil {
			setIfNotEmpty(params, "serde.serialization.lib", sd.SerdeInfo.SerializationLibrary)

			for k, v := range sd.SerdeInfo.Parameters {
				params["serde.param."+k] = v
			}
		}
	}

	return driver.TableMetadata{
		Name:           t.Name,
		CreateTime:     t.CreateTime,
		LastAccessTime: t.LastAccessTime,
		TableType:      t.TableType,
		Columns:        cols,
		PartitionKeys:  columnsFromGlue(t.PartitionKeys),
		Parameters:     params,
	}
}

func columnsFromGlue(in []gluedriver.Column) []driver.Column {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.Column, len(in))
	for i, c := range in {
		out[i] = driver.Column{Name: c.Name, Type: c.Type, Comment: c.Comment}
	}

	return out
}

func setIfNotEmpty(m map[string]string, key, value string) {
	if value != "" {
		m[key] = value
	}
}
