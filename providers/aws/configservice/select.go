package configservice

import (
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// selectQuery is the minimal parsed form of a SelectResourceConfig /
// SelectAggregateResourceConfig expression the emulator supports: a projection
// (either "*" or an explicit list of top-level fields) plus an optional
// WHERE resourceType = '...' equality filter.
type selectQuery struct {
	projectAll   bool
	fields       []string // requested top-level fields when !projectAll
	resourceType string   // WHERE resourceType filter; empty = no filter
	hasResFilter bool
}

// fieldResourceType is the projectable / filterable resourceType column name.
const fieldResourceType = "resourcetype"

// selectFieldValue returns the projected value for a supported top-level field,
// or ok=false if the field is not projectable.
func selectFieldValue(field string, i *driver.ConfigurationItem) (value any, ok bool) {
	switch field {
	case fieldResourceType:
		return i.ResourceType, true
	case "resourceid":
		return i.ResourceID, true
	case "resourcename":
		return i.ResourceName, true
	case "arn":
		return i.Arn, true
	case "awsregion":
		return i.AwsRegion, true
	case "accountid":
		return i.AccountID, true
	case "configuration":
		return json.RawMessage(orNull(i.Configuration)), true
	case "configurationitemstatus":
		return i.ConfigurationState, true
	case "tags":
		return i.Tags, true
	default:
		return nil, false
	}
}

func orNull(s string) string {
	if s == "" {
		return "null"
	}

	return s
}

// invalidExpression returns a typed InvalidExpressionException.
func invalidExpression(format string, args ...any) error {
	return tagged(driver.ExInvalidExpression, invalidArgCode, format, args...)
}

// parseSelect parses the supported subset of the Config SQL SELECT grammar. An
// expression using syntax outside the supported subset (unknown projected field,
// a WHERE clause the emulator can't model) is a typed InvalidExpressionException
// rather than silently-wrong rows.
func parseSelect(expression string) (selectQuery, error) {
	q := selectQuery{}

	expr := strings.TrimSpace(expression)
	// Drop a single trailing semicolon if present.
	expr = strings.TrimSuffix(expr, ";")

	lower := strings.ToLower(expr)
	if !strings.HasPrefix(lower, "select ") {
		return q, invalidExpression("expression must start with SELECT")
	}

	rest := strings.TrimSpace(expr[len("select "):])
	lowerRest := strings.ToLower(rest)

	// Split off an optional WHERE clause.
	whereIdx := indexKeyword(lowerRest, "where")

	selectPart := rest
	wherePart := ""

	if whereIdx >= 0 {
		selectPart = strings.TrimSpace(rest[:whereIdx])
		wherePart = strings.TrimSpace(rest[whereIdx+len("where"):])
	}

	if err := q.parseProjection(selectPart); err != nil {
		return q, err
	}

	if wherePart != "" {
		if err := q.parseWhere(wherePart); err != nil {
			return q, err
		}
	}

	return q, nil
}

func (q *selectQuery) parseProjection(selectPart string) error {
	if selectPart == "" {
		return invalidExpression("SELECT projection is empty")
	}

	if selectPart == "*" {
		q.projectAll = true

		return nil
	}

	for _, f := range strings.Split(selectPart, ",") {
		field := strings.ToLower(strings.TrimSpace(f))
		if field == "" {
			return invalidExpression("empty projected field")
		}

		if _, ok := selectFieldValue(field, &driver.ConfigurationItem{}); !ok {
			return invalidExpression("unsupported projected field %q", field)
		}

		q.fields = append(q.fields, field)
	}

	return nil
}

func (q *selectQuery) parseWhere(wherePart string) error {
	// Only a single equality on resourceType is supported.
	const equalityParts = 2

	eq := strings.SplitN(wherePart, "=", equalityParts)
	if len(eq) != equalityParts {
		return invalidExpression("unsupported WHERE clause %q", wherePart)
	}

	col := strings.ToLower(strings.TrimSpace(eq[0]))
	if col != fieldResourceType {
		return invalidExpression("unsupported WHERE column %q (only resourceType is supported)", col)
	}

	val := strings.TrimSpace(eq[1])
	val = strings.Trim(val, "'\"")

	if val == "" {
		return invalidExpression("WHERE resourceType value is empty")
	}

	q.resourceType = val
	q.hasResFilter = true

	return nil
}

// indexKeyword returns the byte index of a standalone lowercase keyword in s
// (bounded by spaces), or -1. s must already be lowercased.
func indexKeyword(s, kw string) int {
	from := 0

	for {
		i := strings.Index(s[from:], kw)
		if i < 0 {
			return -1
		}

		abs := from + i
		beforeOK := abs == 0 || s[abs-1] == ' '
		afterIdx := abs + len(kw)
		afterOK := afterIdx >= len(s) || s[afterIdx] == ' '

		if beforeOK && afterOK {
			return abs
		}

		from = abs + len(kw)
	}
}

// projectItem renders a single configuration item into the query's projection.
func (q *selectQuery) projectItem(item *driver.ConfigurationItem) (string, error) {
	if q.projectAll {
		if item.Configuration == "" {
			return "{}", nil
		}

		return item.Configuration, nil
	}

	row := make(map[string]any, len(q.fields))

	for _, f := range q.fields {
		v, _ := selectFieldValue(f, item)
		row[f] = v
	}

	b, err := json.Marshal(row)
	if err != nil {
		return "", err
	}

	return string(b), nil
}
