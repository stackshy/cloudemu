package athena

import (
	"context"
	"fmt"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// StartQueryExecution runs a query synchronously (there is no real compute
// plane) and returns its id. Re-issuing with the same non-empty
// ClientRequestToken returns the original id.
func (m *Mock) StartQueryExecution(ctx context.Context, in driver.StartQueryExecutionInput) (string, error) {
	if in.QueryString == "" {
		return "", invalidRequest("QueryString is required")
	}

	workGroup := in.WorkGroup
	if workGroup == "" {
		workGroup = driver.DefaultWorkGroup
	}

	wg, ok := m.workGroups.Get(workGroup)
	if !ok {
		return "", notFoundRequest("WorkGroup %s is not found", workGroup)
	}

	if id, dup := m.dedupToken(in.ClientRequestToken); dup {
		return id, nil
	}

	rc, err := effectiveResultConfiguration(in.ResultConfiguration, wg)
	if err != nil {
		return "", err
	}

	qe := m.buildExecution(in, workGroup, wg, rc)
	m.executeStatement(ctx, &qe)

	m.queryExecutions.Set(qe.QueryExecutionID, copyQueryExecution(qe))
	m.recordToken(in.ClientRequestToken, qe.QueryExecutionID)
	m.emitQueryMetrics(ctx, &qe, wg)

	return qe.QueryExecutionID, nil
}

// effectiveResultConfiguration resolves the settings a query runs with. When
// the workgroup overrides client-side settings, only the workgroup's settings
// apply, so an enforced workgroup with no output location fails even if the
// query names one. Otherwise each field comes from the query, with the
// workgroup as the fallback. See
// https://docs.aws.amazon.com/athena/latest/ug/workgroups-settings-override.html.
// A missing output location is rejected like real Athena.
//
//nolint:gocritic // hugeParam: wg passed by value, read-only here
func effectiveResultConfiguration(client *driver.ResultConfiguration, wg driver.WorkGroup) (*driver.ResultConfiguration, error) {
	enforced := wg.Configuration.EnforceWorkGroupConfiguration == nil || *wg.Configuration.EnforceWorkGroupConfiguration

	w := wg.Configuration.ResultConfiguration
	if w == nil {
		w = &driver.ResultConfiguration{}
	}

	cl := client
	if cl == nil || enforced {
		cl = &driver.ResultConfiguration{}
	}

	out := &driver.ResultConfiguration{
		OutputLocation:          firstSet(cl.OutputLocation, w.OutputLocation, ""),
		ExpectedBucketOwner:     firstSet(cl.ExpectedBucketOwner, w.ExpectedBucketOwner, ""),
		EncryptionConfiguration: copyEncryptionConfiguration(firstSet(cl.EncryptionConfiguration, w.EncryptionConfiguration, nil)),
		ACLConfiguration:        copyACLConfiguration(firstSet(cl.ACLConfiguration, w.ACLConfiguration, nil)),
	}

	if out.OutputLocation == "" {
		return nil, invalidRequest("No output location provided. An output location is required either through " +
			"the Workgroup result configuration setting or as an API input.")
	}

	return out, nil
}

// firstSet returns the client value when set, else the workgroup value.
func firstSet[T comparable](clientValue, wgValue, zero T) T {
	if clientValue != zero {
		return clientValue
	}

	return wgValue
}

// resultObjectPath is the S3 object a query's results land in: the output
// location plus "<QueryId>.csv", or ".txt" for DDL and utility statements.
// See https://docs.aws.amazon.com/athena/latest/ug/querying-finding-output-files.html.
func resultObjectPath(location, id, statementType string) string {
	ext := ".csv"
	if statementType != driver.StatementTypeDML {
		ext = ".txt"
	}

	return strings.TrimRight(location, "/") + "/" + id + ext
}

// buildExecution assembles a QueryExecution in the QUEUED-then-terminal shape
// with a fresh id, sequence, and engine version inherited from the workgroup.
// rc is the effective result configuration.
//
//nolint:gocritic // hugeParam: wg passed by value, read-only here
func (m *Mock) buildExecution(
	in driver.StartQueryExecutionInput, workGroup string, wg driver.WorkGroup, rc *driver.ResultConfiguration,
) driver.QueryExecution {
	now := m.now()

	ctx := in.QueryExecutionContext
	if ctx == nil {
		ctx = &driver.QueryExecutionContext{}
	}

	if ctx.Catalog == "" {
		ctx.Catalog = driver.DefaultDataCatalog
	}

	id := idgen.UUID()
	statementType := classifyStatement(in.QueryString)
	rc.OutputLocation = resultObjectPath(rc.OutputLocation, id, statementType)

	return driver.QueryExecution{
		QueryExecutionID:      id,
		Query:                 in.QueryString,
		StatementType:         statementType,
		ResultConfiguration:   rc,
		QueryExecutionContext: copyQueryExecutionContext(ctx),
		WorkGroup:             workGroup,
		EngineVersion:         wg.Configuration.EngineVersion,
		Seq:                   m.seq.Add(1),
		Status: driver.QueryExecutionStatus{
			State:              driver.QueryStateSucceeded,
			SubmissionDateTime: now,
			CompletionDateTime: now,
		},
	}
}

// executeStatement applies the query's catalog side effects (CREATE/DROP
// DATABASE) and settles the execution's terminal state. A DDL failure flips the
// state to FAILED with a reason, mirroring how real Athena surfaces a query that
// was accepted but failed to run.
func (m *Mock) executeStatement(ctx context.Context, qe *driver.QueryExecution) {
	effect := parseDatabaseDDL(qe.Query)
	if effect.action == "" {
		return
	}

	catalog := driver.DefaultDataCatalog
	if qe.QueryExecutionContext != nil && qe.QueryExecutionContext.Catalog != "" {
		catalog = qe.QueryExecutionContext.Catalog
	}

	if reason := m.applyDatabaseDDL(ctx, catalog, &effect); reason != "" {
		qe.Status.State = driver.QueryStateFailed
		qe.Status.StateChangeReason = reason
	}
}

// applyDatabaseDDL runs a CREATE/DROP DATABASE statement against the Glue
// catalog, honoring IF [NOT] EXISTS and RESTRICT/CASCADE. It returns the
// failure reason, or "" on success.
func (m *Mock) applyDatabaseDDL(ctx context.Context, catalog string, effect *ddlEffect) string {
	if effect.syntaxErr != "" {
		return "SYNTAX_ERROR: " + effect.syntaxErr
	}

	catalogID, err := m.glueCatalogID(catalog)
	if err != nil {
		return catalogReason(catalog, err)
	}

	m.importLegacyDatabases(ctx)

	if effect.action == actionCreateDatabase {
		return m.createDatabase(ctx, catalogID, effect)
	}

	return m.dropDatabase(ctx, catalogID, effect)
}

// catalogReason is the StateChangeReason for a catalog that does not resolve.
func catalogReason(catalog string, err error) string {
	if cerrors.IsNotFound(err) {
		return fmt.Sprintf("CATALOG_NOT_FOUND: Catalog '%s' does not exist", catalog)
	}

	return cerrors.Message(err)
}

func (m *Mock) createDatabase(ctx context.Context, catalogID string, effect *ddlEffect) string {
	err := m.catalog.CreateDatabase(ctx, catalogID, gluedriver.Database{
		Name:        effect.database,
		Description: effect.comment,
		LocationURI: effect.location,
		Parameters:  effect.properties,
	})

	switch {
	case err == nil:
		return ""
	case cerrors.GetCode(err) == cerrors.AlreadyExists:
		if effect.ifClause {
			return ""
		}

		return fmt.Sprintf("Database %s already exists", effect.database)
	default:
		return cerrors.Message(err)
	}
}

func (m *Mock) dropDatabase(ctx context.Context, catalogID string, effect *ddlEffect) string {
	if _, err := m.catalog.GetDatabase(ctx, catalogID, effect.database); err != nil {
		if !cerrors.IsNotFound(err) {
			return cerrors.Message(err)
		}

		if effect.ifClause {
			return ""
		}

		return "Database does not exist: " + effect.database
	}

	if !effect.cascade {
		tables, _, err := m.catalog.GetTables(ctx, catalogID, effect.database, gluedriver.TablePagination{MaxResults: 1})
		if err != nil {
			return cerrors.Message(err)
		}

		if len(tables) > 0 {
			return fmt.Sprintf("InvalidOperationException: Database %s is not empty. One or more tables exist.", effect.database)
		}
	}

	if err := m.catalog.DeleteDatabase(ctx, catalogID, effect.database); err != nil && !cerrors.IsNotFound(err) {
		return cerrors.Message(err)
	}

	return ""
}

// dedupToken returns the existing execution id for a client-request token, if
// one was already recorded.
func (m *Mock) dedupToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}

	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()

	id, ok := m.tokens[token]

	return id, ok
}

// recordToken remembers the execution id minted for a client-request token.
func (m *Mock) recordToken(token, id string) {
	if token == "" {
		return
	}

	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()

	m.tokens[token] = id
}

// GetQueryExecution returns a query execution by id.
func (m *Mock) GetQueryExecution(_ context.Context, id string) (*driver.QueryExecution, error) {
	qe, ok := m.queryExecutions.Get(id)
	if !ok {
		return nil, notFoundRequest("QueryExecution %s is not found", id)
	}

	out := copyQueryExecution(qe)

	return &out, nil
}

// GetQueryResults returns the rows a SUCCEEDED execution produced. Emulated
// executions (DDL, and any statement with no compute plane) have no rows, so the
// result set is empty; a FAILED execution is rejected as real Athena does.
func (m *Mock) GetQueryResults(_ context.Context, id string, _ driver.Pagination) (*driver.QueryResults, error) {
	qe, ok := m.queryExecutions.Get(id)
	if !ok {
		return nil, notFoundRequest("QueryExecution %s is not found", id)
	}

	if qe.Status.State == driver.QueryStateFailed {
		return nil, invalidRequest("Query did not finish successfully. Final query state: FAILED")
	}

	return &driver.QueryResults{}, nil
}

// StopQueryExecution is a no-op on an already-terminal execution; it errors only
// when the id is unknown.
func (m *Mock) StopQueryExecution(_ context.Context, id string) error {
	if _, ok := m.queryExecutions.Get(id); !ok {
		return notFoundRequest("QueryExecution %s is not found", id)
	}

	return nil
}

// ListQueryExecutions returns execution ids in a workgroup (default "primary"),
// most-recent-first (descending sequence).
//
//nolint:gocritic // unnamedResult: (ids, nextToken, err) is idiomatic and self-explanatory
func (m *Mock) ListQueryExecutions(_ context.Context, workGroup string, page driver.Pagination) ([]string, string, error) {
	if workGroup == "" {
		workGroup = driver.DefaultWorkGroup
	}

	type entry struct {
		id  string
		seq int64
	}

	entries := make([]entry, 0)

	for _, id := range m.queryExecutions.Keys() {
		qe, ok := m.queryExecutions.Get(id)
		if !ok || qe.WorkGroup != workGroup {
			continue
		}

		entries = append(entries, entry{id: id, seq: qe.Seq})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].seq > entries[j].seq })

	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.id
	}

	return paginate(ids, page)
}
