package athena

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// StartQueryExecution runs a query synchronously (there is no real compute
// plane) and returns its id. Re-issuing with the same non-empty
// ClientRequestToken returns the original id.
func (m *Mock) StartQueryExecution(_ context.Context, in driver.StartQueryExecutionInput) (string, error) {
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

	if _, err := effectiveOutputLocation(in.ResultConfiguration, wg); err != nil {
		return "", err
	}

	qe := m.buildExecution(in, workGroup, wg)
	m.executeStatement(&qe)

	m.queryExecutions.Set(qe.QueryExecutionID, copyQueryExecution(qe))
	m.recordToken(in.ClientRequestToken, qe.QueryExecutionID)

	return qe.QueryExecutionID, nil
}

// effectiveOutputLocation resolves the query results location from the
// client-side ResultConfiguration or, when the workgroup enforces its
// configuration, from the workgroup. An empty result is an error, matching real
// Athena's "No output location provided" rejection.
//
//nolint:gocritic // hugeParam: wg passed by value, read-only here
func effectiveOutputLocation(rc *driver.ResultConfiguration, wg driver.WorkGroup) (string, error) {
	if rc != nil && rc.OutputLocation != "" {
		return rc.OutputLocation, nil
	}

	wgRC := wg.Configuration.ResultConfiguration
	if wgRC != nil && wgRC.OutputLocation != "" {
		return wgRC.OutputLocation, nil
	}

	return "", invalidRequest("No output location provided. An output location is required either through " +
		"the Workgroup result configuration setting or as an API input.")
}

// buildExecution assembles a QueryExecution in the QUEUED-then-terminal shape
// with a fresh id, sequence, and engine version inherited from the workgroup.
//
//nolint:gocritic // hugeParam: wg passed by value, read-only here
func (m *Mock) buildExecution(in driver.StartQueryExecutionInput, workGroup string, wg driver.WorkGroup) driver.QueryExecution {
	now := m.now()

	ctx := in.QueryExecutionContext
	if ctx == nil {
		ctx = &driver.QueryExecutionContext{}
	}

	if ctx.Catalog == "" {
		ctx.Catalog = driver.DefaultDataCatalog
	}

	return driver.QueryExecution{
		QueryExecutionID:      idgen.UUID(),
		Query:                 in.QueryString,
		StatementType:         classifyStatement(in.QueryString),
		ResultConfiguration:   copyResultConfiguration(in.ResultConfiguration),
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
func (m *Mock) executeStatement(qe *driver.QueryExecution) {
	effect := parseDatabaseDDL(qe.Query)
	if effect.action == "" {
		return
	}

	catalog := driver.DefaultDataCatalog
	if qe.QueryExecutionContext != nil && qe.QueryExecutionContext.Catalog != "" {
		catalog = qe.QueryExecutionContext.Catalog
	}

	if err := m.applyDatabaseDDL(catalog, effect); err != nil {
		qe.Status.State = driver.QueryStateFailed
		qe.Status.StateChangeReason = err.Error()
	}
}

// applyDatabaseDDL mutates the databases store for a CREATE/DROP DATABASE
// statement, honoring the IF [NOT] EXISTS clause.
func (m *Mock) applyDatabaseDDL(catalog string, effect ddlEffect) error {
	if effect.database == "" {
		return invalidRequest("database name is required")
	}

	key := databaseKey(catalog, effect.database)

	switch effect.action {
	case "createDatabase":
		if !m.databases.SetIfAbsent(key, driver.Database{Name: effect.database}) && !effect.ifClause {
			return invalidRequest("Database %s already exists", effect.database)
		}
	case "dropDatabase":
		if !m.databases.Delete(key) && !effect.ifClause {
			return invalidRequest("Database does not exist: %s", effect.database)
		}
	}

	return nil
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
