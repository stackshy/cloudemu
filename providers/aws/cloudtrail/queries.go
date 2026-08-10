package cloudtrail

import (
	"context"
	"sort"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// queryData wraps a query with a per-item lock, matching the other resource
// types, so a concurrent CancelQuery can't race a DescribeQuery/ListQueries read.
type queryData struct {
	q  driver.Query
	mu sync.RWMutex
}

// StartQuery records an ad-hoc CloudTrail Lake query. There is no real event
// data behind the store, so the query is accepted, stored, and immediately
// marked FINISHED with an empty result set (documented).
func (m *Mock) StartQuery(_ context.Context, edsID, queryString, deliveryS3URI, queryStatement string) (string, error) {
	stmt := queryStatement
	if stmt == "" {
		stmt = queryString
	}

	if stmt == "" {
		return "", errInvalidParameter("QueryStatement is required")
	}

	q := driver.Query{
		ID:               idgen.GenerateID("query-"),
		QueryString:      stmt,
		EventDataStoreID: edsID,
		Status:           driver.QueryStatusFinished,
		CreatedAt:        m.now(),
		DeliveryS3URI:    deliveryS3URI,
	}
	m.queries.Set(q.ID, &queryData{q: q})

	return q.ID, nil
}

// DescribeQuery returns a query's status by ID or generated-query alias.
func (m *Mock) DescribeQuery(_ context.Context, _, queryID, alias string) (*driver.Query, error) {
	id := queryID
	if id == "" {
		id = alias
	}

	d, ok := m.queries.Get(id)
	if !ok {
		return nil, errQueryNotFound(id)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	out := d.q

	return &out, nil
}

// GetQueryResults returns a query's results. Emulated queries have no rows, so
// the page is empty with FINISHED status.
func (m *Mock) GetQueryResults(
	_ context.Context, _, queryID, _ string, _ int32,
) (*driver.QueryResults, error) {
	d, ok := m.queries.Get(queryID)
	if !ok {
		return nil, errQueryNotFound(queryID)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	return &driver.QueryResults{QueryStatus: d.q.Status, ResultRows: []map[string]string{}}, nil
}

// CancelQuery stops a running query and returns its resulting status.
func (m *Mock) CancelQuery(_ context.Context, _, queryID string) (string, error) {
	d, ok := m.queries.Get(queryID)
	if !ok {
		return "", errQueryNotFound(queryID)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.q.Status = driver.QueryStatusCancelled

	return d.q.Status, nil
}

// ListQueries returns queries for a store ordered by ID, paginated.
func (m *Mock) ListQueries(
	_ context.Context, edsID, nextToken string, maxResults int32,
) ([]driver.Query, string, error) {
	all := m.queries.All()

	ids := make([]string, 0, len(all))

	for id, d := range all {
		d.mu.RLock()
		match := edsID == "" || d.q.EventDataStoreID == edsID
		d.mu.RUnlock()

		if match {
			ids = append(ids, id)
		}
	}

	sort.Strings(ids)

	limit := int(maxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	out := make([]driver.Query, 0, len(ids))
	started := nextToken == ""

	for _, id := range ids {
		if !started {
			if id == nextToken {
				started = true
			}

			continue
		}

		if len(out) == limit {
			return out, out[len(out)-1].ID, nil
		}

		d := all[id]

		d.mu.RLock()
		out = append(out, d.q)
		d.mu.RUnlock()
	}

	return out, "", nil
}

// GenerateQuery synthesizes a SQL statement from a natural-language prompt. The
// emulator returns a deterministic template statement (documented synthesis).
func (*Mock) GenerateQuery(
	_ context.Context, edsIDs []string, prompt string,
) (queryAlias, queryStatement string, err error) {
	if prompt == "" {
		return "", "", errInvalidParameter("Prompt is required")
	}

	alias := idgen.GenerateID("genquery-")
	eds := "<event-data-store>"

	if len(edsIDs) > 0 {
		eds = edsIDs[0]
	}

	stmt := "SELECT eventName, eventTime FROM " + eds + " LIMIT 100"

	return alias, stmt, nil
}
