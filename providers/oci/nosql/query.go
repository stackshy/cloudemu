package nosql

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Statement kinds the query endpoint runs. OCI's NoSQL REST API has no
// multi-delete operation of its own; DELETE FROM over the query endpoint is
// how several rows go at once.
const (
	dmlSelect = "SELECT"
	dmlDelete = "DELETE FROM"
)

// deletedRowsField is the column OCI reports a DELETE's row count in.
const deletedRowsField = "NumRowsDeleted"

// condition is one `column = literal` equality test.
type condition struct {
	Column string
	Value  string
}

// QueryOCI runs one SELECT or DELETE statement against a table in the given
// compartment. Only `SELECT *` and `DELETE FROM`, each with AND-ed equality
// conditions on declared columns, are parsed; anything else is refused by
// name rather than accepted and quietly reinterpreted.
func (m *Mock) QueryOCI(_ context.Context, compartmentID, statement string, limit int) ([]map[string]any, error) {
	if compartmentID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	stmt := normaliseStatement(statement)
	upper := strings.ToUpper(stmt)

	switch {
	case strings.HasPrefix(upper, dmlSelect):
		return m.runSelect(compartmentID, stmt, limit)
	case strings.HasPrefix(upper, dmlDelete):
		return m.runDelete(compartmentID, stmt)
	}

	return nil, cerrors.Newf(cerrors.InvalidArgument,
		"unsupported statement %q; CloudEmu's query endpoint runs SELECT * and DELETE FROM over one table "+
			"with AND-ed equality conditions", leadingWords(stmt))
}

func (m *Mock) runSelect(compartmentID, stmt string, limit int) ([]map[string]any, error) {
	table, conds, err := parseDML(stmt, dmlSelect)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.scopedTable(table, compartmentID)
	if err != nil {
		return nil, err
	}

	matched, err := m.matchRows(t, conds)
	if err != nil {
		return nil, err
	}

	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}

	out := make([]map[string]any, 0, len(matched))
	for _, item := range matched {
		out = append(out, visible(item))
	}

	return out, nil
}

func (m *Mock) runDelete(compartmentID, stmt string) ([]map[string]any, error) {
	table, conds, err := parseDML(stmt, dmlDelete)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.scopedTable(table, compartmentID)
	if err != nil {
		return nil, err
	}

	matched, err := m.matchRows(t, conds)
	if err != nil {
		return nil, err
	}

	for _, item := range matched {
		t.items.Delete(itemKey(t, item))
	}

	return []map[string]any{{deletedRowsField: len(matched)}}, nil
}

// scopedTable resolves a table and checks it is visible from the caller's
// compartment. Callers must hold m.mu.
func (m *Mock) scopedTable(name, compartmentID string) (*tableData, error) {
	t, err := m.resolve(name)
	if err != nil {
		return nil, err
	}

	if !t.Scope.Matches(scope.Scope{Compartment: compartmentID}) {
		return nil, cerrors.Newf(cerrors.NotFound, "table %q not found", name)
	}

	return t, nil
}

// matchRows returns the unexpired rows satisfying every condition, in a
// deterministic order. Callers must hold m.mu.
func (m *Mock) matchRows(t *tableData, conds []condition) ([]map[string]any, error) {
	for _, c := range conds {
		if columnIndex(t, c.Column) < 0 {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "column %q is not declared on table %q", c.Column, t.Name)
		}
	}

	var matched []map[string]any

	for _, key := range sortedKeys(t) {
		item, ok := t.items.Get(key)
		if !ok || m.expired(t, item) {
			continue
		}

		if rowMatches(item, conds) {
			matched = append(matched, item)
		}
	}

	return matched, nil
}

func sortedKeys(t *tableData) []string {
	keys := t.items.Keys()
	sort.Strings(keys)

	return keys
}

func rowMatches(item map[string]any, conds []condition) bool {
	for _, c := range conds {
		if fmt.Sprintf("%v", item[c.Column]) != c.Value {
			return false
		}
	}

	return true
}

// parseDML splits "<verb> [*] FROM table [WHERE conds]" and refuses the
// clauses the mock does not run.
func parseDML(stmt, verb string) (table string, conds []condition, err error) {
	rest := strings.TrimSpace(stmt[len(verb):])

	if verb == dmlSelect {
		if !strings.HasPrefix(rest, "*") {
			return "", nil, cerrors.New(cerrors.InvalidArgument,
				"only SELECT * is supported; column projections, aggregates and joins are not")
		}

		rest = strings.TrimSpace(rest[1:])

		if !strings.HasPrefix(strings.ToUpper(rest), "FROM ") {
			return "", nil, cerrors.New(cerrors.InvalidArgument, "SELECT * must be followed by FROM <table>")
		}

		rest = strings.TrimSpace(rest[len("FROM "):])
	}

	words := strings.Fields(rest)
	if len(words) == 0 {
		return "", nil, cerrors.New(cerrors.InvalidArgument, "statement names no table")
	}

	if table, err = requireIdentifier(words[0], "table name"); err != nil {
		return "", nil, err
	}

	tail := strings.Join(words[1:], " ")
	if tail == "" {
		return table, nil, nil
	}

	if !strings.HasPrefix(strings.ToUpper(tail), "WHERE ") {
		return "", nil, cerrors.Newf(cerrors.InvalidArgument,
			"unsupported clause %q; CloudEmu runs a WHERE of AND-ed equality conditions and nothing else", tail)
	}

	conds, err = parseConditions(tail[len("WHERE "):])

	return table, conds, err
}

// parseConditions reads `col = literal [AND col = literal]...`.
func parseConditions(clause string) ([]condition, error) {
	var out []condition

	for _, part := range splitAnd(clause) {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"unsupported condition %q; CloudEmu compares a column to a literal with =", part)
		}

		col, err := requireIdentifier(part[:eq], "column name")
		if err != nil {
			return nil, err
		}

		literal, err := parseLiteral(strings.TrimSpace(part[eq+1:]))
		if err != nil {
			return nil, err
		}

		out = append(out, condition{Column: col, Value: literal})
	}

	if len(out) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "WHERE names no conditions")
	}

	return out, nil
}

// splitAnd splits on the AND keyword, case-insensitively.
func splitAnd(clause string) []string {
	var (
		out   []string
		words = strings.Fields(clause)
		cur   []string
	)

	for _, w := range words {
		if strings.EqualFold(w, "AND") {
			out = append(out, strings.Join(cur, " "))
			cur = nil

			continue
		}

		if strings.EqualFold(w, "OR") {
			// Reported as an unsupported condition by parseConditions, which
			// sees the OR still embedded in the part it cannot read.
			cur = append(cur, w)

			continue
		}

		cur = append(cur, w)
	}

	if len(cur) > 0 {
		out = append(out, strings.Join(cur, " "))
	}

	return out
}

// parseLiteral normalises a quoted string, number or boolean to the text form
// row values are compared in.
func parseLiteral(raw string) (string, error) {
	if raw == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "condition names no value")
	}

	if (strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`)) ||
		(strings.HasPrefix(raw, `'`) && strings.HasSuffix(raw, `'`)) {
		if len(raw) < 2 { //nolint:mnd // a quoted literal is at least both quotes
			return "", cerrors.Newf(cerrors.InvalidArgument, "unterminated literal %q", raw)
		}

		return raw[1 : len(raw)-1], nil
	}

	if strings.EqualFold(raw, "true") || strings.EqualFold(raw, "false") {
		return strings.ToLower(raw), nil
	}

	if _, err := strconv.ParseFloat(raw, 64); err != nil {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"literal %q is not a quoted string, a number or a boolean", raw)
	}

	return raw, nil
}
