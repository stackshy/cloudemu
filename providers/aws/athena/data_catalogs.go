package athena

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// Data catalog limits from the CreateDataCatalog API reference.
const (
	maxCatalogNameLen          = 127
	maxFederatedCatalogNameLen = 41
	maxCatalogDescriptionLen   = 1024
	minListCatalogsResults     = 2
)

// Parameter keys a data catalog type needs.
const (
	paramFunction         = "function"
	paramMetadataFunction = "metadata-function"
	paramRecordFunction   = "record-function"
	paramConnectionARN    = "connection-arn"
	paramConnectionType   = "connection-type"
	paramConnectionProps  = "connection-properties"
)

// federatedTagKey is the tag Athena puts on every FEDERATED catalog.
const federatedTagKey = "federated_athena_datacatalog"

// connectionTypes is the Athena ConnectionType enum a FEDERATED catalog may use.
func connectionTypes() map[string]bool {
	out := map[string]bool{}

	for _, t := range []string{
		"DYNAMODB", "MYSQL", "POSTGRESQL", "REDSHIFT", "ORACLE", "SYNAPSE", "SQLSERVER", "DB2",
		"OPENSEARCH", "BIGQUERY", "GOOGLECLOUDSTORAGE", "HBASE", "DOCUMENTDB", "CMDB", "TPCDS",
		"TIMESTREAM", "SAPHANA", "SNOWFLAKE", "DATALAKEGEN2", "DB2AS400",
	} {
		out[t] = true
	}

	return out
}

// isDefaultCatalog reports whether name is the built-in AwsDataCatalog.
func isDefaultCatalog(name string) bool {
	return strings.EqualFold(name, driver.DefaultDataCatalog)
}

// dataCatalogARN is the ARN a data catalog's tags are keyed under.
func (m *Mock) dataCatalogARN(name string) string {
	return idgen.AWSARN("athena", m.opts.Region, m.opts.AccountID, "datacatalog/"+name)
}

func catalogNotFound(name string) error {
	return notFoundRequest("Catalog %s was not found", name)
}

// CreateDataCatalog registers a data catalog. Every type settles to
// CREATE_COMPLETE at once, except a FEDERATED catalog whose Glue connection is
// missing, which is kept as CREATE_FAILED with an Error.
func (m *Mock) CreateDataCatalog(ctx context.Context, in driver.CreateDataCatalogInput) (*driver.DataCatalog, error) {
	if err := validateCatalogName(in.Name, in.Type); err != nil {
		return nil, err
	}

	if err := validateCatalogFields(in.Type, in.Description, in.Parameters); err != nil {
		return nil, err
	}

	dc := driver.DataCatalog{
		Name:        in.Name,
		Description: in.Description,
		Type:        in.Type,
		Parameters:  copyStringMap(in.Parameters),
		Status:      driver.DataCatalogStatusCreateComplete,
	}

	if dc.Parameters == nil {
		dc.Parameters = map[string]string{}
	}

	tags := copyStringMap(in.Tags)

	if in.Type == driver.DataCatalogTypeFederated {
		m.resolveFederated(ctx, &dc)

		if tags == nil {
			tags = map[string]string{}
		}

		tags[federatedTagKey] = "true"
	}

	// m.mu covers the insert and its tags, so a Delete cannot run between them.
	m.mu.Lock()
	defer m.mu.Unlock()

	if isDefaultCatalog(in.Name) || !m.dataCatalogs.SetIfAbsent(in.Name, dc) {
		return nil, &driver.APIError{
			Exception: driver.ExInvalidRequest,
			Err:       cerrors.Newf(cerrors.AlreadyExists, "DataCatalog %s already exists", in.Name),
		}
	}

	m.storeTags(m.dataCatalogARN(in.Name), tags)

	out := copyDataCatalog(dc)

	return &out, nil
}

// resolveFederated fills a FEDERATED catalog's ConnectionType, and marks it
// CREATE_FAILED when its connection-arn names a Glue connection that does not
// exist. It runs with no Athena lock held.
func (m *Mock) resolveFederated(ctx context.Context, dc *driver.DataCatalog) {
	if ct := dc.Parameters[paramConnectionType]; ct != "" {
		dc.ConnectionType = ct

		return
	}

	lookup, ok := m.catalog.(connectionLookup)
	if !ok {
		return
	}

	connARN := dc.Parameters[paramConnectionARN]
	name := connARN[strings.LastIndex(connARN, "/")+1:]

	conn, err := lookup.GetConnection(ctx, "", name)
	if err != nil {
		dc.Status = driver.DataCatalogStatusCreateFailed
		dc.Error = "Glue connection " + name + " not found"

		return
	}

	if connectionTypes()[strings.ToUpper(conn.ConnectionType)] {
		dc.ConnectionType = strings.ToUpper(conn.ConnectionType)
	}
}

// GetDataCatalog returns a registered data catalog by name.
func (m *Mock) GetDataCatalog(_ context.Context, name string) (*driver.DataCatalog, error) {
	if name == "" {
		name = driver.DefaultDataCatalog
	}

	dc, ok := m.dataCatalogs.Get(name)
	if !ok {
		return nil, catalogNotFound(name)
	}

	out := copyDataCatalog(dc)
	if out.Parameters == nil {
		out.Parameters = map[string]string{}
	}

	return &out, nil
}

// ListDataCatalogs returns the registered data-catalog summaries, sorted by
// name.
func (m *Mock) ListDataCatalogs(_ context.Context, page driver.Pagination) ([]driver.DataCatalogSummary, string, error) {
	if page.MaxResults != 0 && (page.MaxResults < minListCatalogsResults || page.MaxResults > maxCatalogResults) {
		return nil, "", invalidRequest("MaxResults must be between %d and %d", minListCatalogsResults, maxCatalogResults)
	}

	names := sortedKeys(m.dataCatalogs.Keys())
	all := make([]driver.DataCatalogSummary, 0, len(names))

	for _, name := range names {
		dc, ok := m.dataCatalogs.Get(name)
		if !ok {
			continue
		}

		all = append(all, driver.DataCatalogSummary{
			CatalogName:    dc.Name,
			Type:           dc.Type,
			Status:         dc.Status,
			ConnectionType: dc.ConnectionType,
			Error:          dc.Error,
		})
	}

	return paginate(all, page)
}

// UpdateDataCatalog changes a catalog's type, description or parameters. Nil
// fields keep their stored value, and the result is checked again for its type.
func (m *Mock) UpdateDataCatalog(_ context.Context, in driver.UpdateDataCatalogInput) error {
	if isDefaultCatalog(in.Name) {
		return invalidRequest("%s cannot be modified", driver.DefaultDataCatalog)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dc, ok := m.dataCatalogs.Get(in.Name)
	if !ok {
		return catalogNotFound(in.Name)
	}

	if in.Type == driver.DataCatalogTypeFederated || dc.Type == driver.DataCatalogTypeFederated {
		return invalidRequest("FEDERATED data catalogs cannot be updated")
	}

	if in.Description != nil {
		dc.Description = *in.Description
	}

	if in.Parameters != nil {
		dc.Parameters = copyStringMap(in.Parameters)
	}

	if err := validateCatalogFields(in.Type, dc.Description, dc.Parameters); err != nil {
		return err
	}

	dc.Type = in.Type
	m.dataCatalogs.Set(in.Name, dc)

	return nil
}

// DeleteDataCatalog removes a catalog and its tags. The Glue databases a GLUE
// catalog points at stay, since the catalog is only a registration.
func (m *Mock) DeleteDataCatalog(_ context.Context, name string, deleteCatalogOnly bool) (*driver.DataCatalog, error) {
	if isDefaultCatalog(name) {
		return nil, invalidRequest("%s cannot be deleted", driver.DefaultDataCatalog)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dc, ok := m.dataCatalogs.Get(name)
	if !ok {
		return nil, catalogNotFound(name)
	}

	federated := dc.Type == driver.DataCatalogTypeFederated
	if deleteCatalogOnly && !federated {
		return nil, invalidRequest("DeleteCatalogOnly is only supported for FEDERATED data catalogs")
	}

	m.dataCatalogs.Delete(name)
	m.deleteTags(m.dataCatalogARN(name))

	out := copyDataCatalog(dc)
	if federated {
		out.Status = driver.DataCatalogStatusDeleteComplete
	}

	return &out, nil
}

// validateCatalogName checks a catalog name against the API limits.
func validateCatalogName(name, catalogType string) error {
	limit := maxCatalogNameLen
	if catalogType == driver.DataCatalogTypeFederated {
		limit = maxFederatedCatalogNameLen
	}

	federated := catalogType == driver.DataCatalogTypeFederated
	bad := func(r rune) bool { return !catalogNameRune(r, federated) }

	if name == "" || len(name) > limit || strings.IndexFunc(name, bad) >= 0 {
		return invalidRequest("Name must be 1-%d alphanumeric, underscore, at sign or hyphen characters", limit)
	}

	return nil
}

// catalogNameRune reports whether r may appear in a catalog name. The API
// docs allow alphanumerics, "_", "@" and "-", and a backslash too for
// FEDERATED names.
func catalogNameRune(r rune, federated bool) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '_', r == '@', r == '-':
		return true
	default:
		return federated && r == '\\'
	}
}

// validateCatalogFields checks the type, description and the parameters the
// type needs.
func validateCatalogFields(catalogType, description string, params map[string]string) error {
	if len(description) > maxCatalogDescriptionLen {
		return invalidRequest("Description must be at most %d characters", maxCatalogDescriptionLen)
	}

	switch catalogType {
	case driver.DataCatalogTypeLambda:
		return validateLambdaParams(params)
	case driver.DataCatalogTypeHive:
		if params[paramMetadataFunction] == "" {
			return invalidRequest("HIVE data catalogs require the %s parameter", paramMetadataFunction)
		}
	case driver.DataCatalogTypeGlue:
		if params[catalogIDParam] == "" {
			return invalidRequest("GLUE data catalogs require the %s parameter", catalogIDParam)
		}
	case driver.DataCatalogTypeFederated:
		return validateFederatedParams(params)
	default:
		return invalidRequest("Type must be one of LAMBDA, GLUE, HIVE or FEDERATED")
	}

	return nil
}

// validateLambdaParams needs either function, or both metadata-function and
// record-function, but not both forms.
func validateLambdaParams(params map[string]string) error {
	composite := params[paramFunction] != ""
	meta := params[paramMetadataFunction] != ""
	record := params[paramRecordFunction] != ""

	if composite && (meta || record) {
		return invalidRequest("LAMBDA data catalogs take %s or %s and %s, not both",
			paramFunction, paramMetadataFunction, paramRecordFunction)
	}

	if !composite && !(meta && record) {
		return invalidRequest("LAMBDA data catalogs require %s, or both %s and %s",
			paramFunction, paramMetadataFunction, paramRecordFunction)
	}

	return nil
}

// validateFederatedParams needs either connection-arn, or a known
// connection-type with connection-properties, but not both.
func validateFederatedParams(params map[string]string) error {
	connARN := params[paramConnectionARN]
	connType := params[paramConnectionType]

	if (connARN == "") == (connType == "") {
		return invalidRequest("FEDERATED data catalogs require one of %s or %s", paramConnectionARN, paramConnectionType)
	}

	if connType == "" {
		return nil
	}

	if !connectionTypes()[connType] {
		return invalidRequest("unsupported %s %s", paramConnectionType, connType)
	}

	if params[paramConnectionProps] == "" {
		return invalidRequest("%s requires %s", paramConnectionType, paramConnectionProps)
	}

	return nil
}
