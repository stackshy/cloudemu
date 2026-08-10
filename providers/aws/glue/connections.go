package glue

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// connectionData is a connection plus its own lock.
type connectionData struct {
	conn driver.Connection
	mu   sync.RWMutex
}

// validConnectionType reports whether s is one of Glue's modeled ConnectionType
// enum values (a required field on CreateConnection). The set mirrors the
// aws-sdk-go-v2 glue ConnectionType enum exactly, so any type the SDK models is
// accepted and unmodeled ones are rejected.
func validConnectionType(s string) bool {
	switch s {
	case "JDBC", "SFTP", "MONGODB", "KAFKA", "NETWORK", "MARKETPLACE", "CUSTOM",
		"SALESFORCE", "VIEW_VALIDATION_REDSHIFT", "VIEW_VALIDATION_ATHENA",
		"GOOGLEADS", "GOOGLESHEETS", "GOOGLEANALYTICS4", "SERVICENOW", "MARKETO",
		"SAPODATA", "ZENDESK", "JIRACLOUD", "NETSUITEERP", "HUBSPOT", "FACEBOOKADS",
		"INSTAGRAMADS", "ZOHOCRM", "SALESFORCEPARDOT", "SALESFORCEMARKETINGCLOUD",
		"ADOBEANALYTICS", "SLACK", "LINKEDIN", "MIXPANEL", "ASANA", "STRIPE",
		"SMARTSHEET", "DATADOG", "WOOCOMMERCE", "INTERCOM", "SNAPCHATADS", "PAYPAL",
		"QUICKBOOKS", "FACEBOOKPAGEINSIGHTS", "FRESHDESK", "TWILIO", "DOCUSIGNMONITOR",
		"FRESHSALES", "ZOOM", "GOOGLESEARCHCONSOLE", "SALESFORCECOMMERCECLOUD",
		"SAPCONCUR", "DYNATRACE", "MICROSOFTDYNAMIC365FINANCEANDOPS", "MICROSOFTTEAMS",
		"BLACKBAUDRAISEREDGENXT", "MAILCHIMP", "GITLAB", "PENDO", "PRODUCTBOARD",
		"CIRCLECI", "PIPEDIVE", "SENDGRID", "AZURECOSMOS", "AZURESQL", "BIGQUERY",
		"BLACKBAUD", "CLOUDERAHIVE", "CLOUDERAIMPALA", "CLOUDWATCH", "CLOUDWATCHMETRICS",
		"CMDB", "DATALAKEGEN2", "DB2", "DB2AS400", "DOCUMENTDB", "DOMO", "DYNAMODB",
		"GOOGLECLOUDSTORAGE", "HBASE", "KUSTOMER", "MICROSOFTDYNAMICS365CRM", "MONDAY",
		"MYSQL", "OKTA", "OPENSEARCH", "ORACLE", "PIPEDRIVE", "POSTGRESQL", "SAPHANA",
		"SQLSERVER", "SYNAPSE", "TERADATA", "TERADATANOS", "TIMESTREAM", "TPCDS",
		"VERTICA":
		return true
	default:
		return false
	}
}

// CreateConnection creates a Data Catalog connection, atomically.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateConnection(_ context.Context, catalogID string, c driver.Connection) error {
	cat := m.catalogOrDefault(catalogID)

	if !validName(c.Name) {
		return invalidInput("connection name %q is invalid", c.Name)
	}

	if !validConnectionType(c.ConnectionType) {
		return invalidInput("connection type %q is invalid", c.ConnectionType)
	}

	c.CreationTime = m.now()
	c.LastUpdatedTime = c.CreationTime
	stored := copyConnection(c)

	if !m.connections.SetIfAbsent(nameKey(cat, c.Name), &connectionData{conn: stored}) {
		return alreadyExists("Connection already exists: %s", c.Name)
	}

	return nil
}

func (m *Mock) getConnectionData(catalogID, name string) (*connectionData, string, error) {
	cat := m.catalogOrDefault(catalogID)

	if !validName(name) {
		return nil, cat, invalidInput("connection name %q is invalid", name)
	}

	cd, ok := m.connections.Get(nameKey(cat, name))
	if !ok {
		return nil, cat, entityNotFound("Connection not found: %s", name)
	}

	return cd, cat, nil
}

// GetConnection returns a deep copy of a connection.
func (m *Mock) GetConnection(_ context.Context, catalogID, name string) (*driver.Connection, error) {
	cd, _, err := m.getConnectionData(catalogID, name)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := copyConnection(cd.conn)

	return &out, nil
}

// UpdateConnection replaces a connection's mutable fields.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateConnection(_ context.Context, catalogID, name string, c driver.Connection) error {
	cd, _, err := m.getConnectionData(catalogID, name)
	if err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	created := cd.conn.CreationTime
	cd.conn = copyConnection(c)
	cd.conn.Name = name
	cd.conn.CreationTime = created
	cd.conn.LastUpdatedTime = m.now()

	return nil
}

// DeleteConnection removes a connection.
func (m *Mock) DeleteConnection(_ context.Context, catalogID, name string) error {
	_, cat, err := m.getConnectionData(catalogID, name)
	if err != nil {
		return err
	}

	m.connections.Delete(nameKey(cat, name))

	return nil
}

// GetConnections lists connections in a catalog with pagination.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) GetConnections(
	_ context.Context, catalogID string, page driver.TablePagination,
) ([]driver.Connection, string, error) {
	cat := m.catalogOrDefault(catalogID)
	prefix := cat + keySep

	keys := sortedKeys(m.connections.Keys())
	all := make([]driver.Connection, 0, len(keys))

	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		cd, ok := m.connections.Get(key)
		if !ok {
			continue
		}

		cd.mu.RLock()
		all = append(all, copyConnection(cd.conn))
		cd.mu.RUnlock()
	}

	return paginate(all, page)
}

// BatchDeleteConnection deletes several connections, returning a map of the
// failures keyed by name (matching Glue's Errors map).
func (m *Mock) BatchDeleteConnection(
	_ context.Context, catalogID string, names []string,
) (map[string]driver.BatchError, error) {
	for _, n := range names {
		if !validName(n) {
			return nil, invalidInput("connection name %q is invalid", n)
		}
	}

	errs := map[string]driver.BatchError{}

	for _, n := range names {
		if err := m.DeleteConnection(context.Background(), catalogID, n); err != nil {
			errs[n] = driver.BatchError{Name: n, ErrorCode: driver.ExEntityNotFound, ErrorMessage: err.Error()}
		}
	}

	return errs, nil
}

// TestConnection validates a connection exists. The emulator can't reach a real
// data source, so a present connection is reported reachable.
func (m *Mock) TestConnection(_ context.Context, name string) error {
	_, _, err := m.getConnectionData("", name)

	return err
}
