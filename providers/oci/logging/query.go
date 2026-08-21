package logging

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// OCID prefixes a search scope's segments must carry.
const (
	prefixCompartment = "ocid1.compartment."
	prefixLogGroup    = "ocid1.loggroup."
	prefixLog         = "ocid1.log."
)

// maxScopeSegments is compartment/logGroup/log.
const maxScopeSegments = 3

// operatorChars are the characters a comparison operator is built from.
const operatorChars = "=!<>~"

// The comparison operators a where clause may use.
const (
	opEqual    = "="
	opNotEqual = "!="
)

// parseSearchQuery parses the subset of OCI's search query language CloudEmu
// models: a search clause, an optional where clause of = and != comparisons
// joined by and, and an optional sort by datetime. Everything else is rejected
// by name.
func parseSearchQuery(query string) (*searchQuery, error) {
	stages := splitOutsideQuotes(query, '|')
	if len(stages) == 0 || strings.TrimSpace(stages[0]) == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "searchQuery is required")
	}

	keyword, rest := splitKeyword(stages[0])
	if keyword != stageSearch {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"a search query must begin with the search clause, got %q", keyword)
	}

	scopes, err := parseScopes(rest)
	if err != nil {
		return nil, err
	}

	q := &searchQuery{scopes: scopes}

	for _, stage := range stages[1:] {
		if err := q.applyStage(stage); err != nil {
			return nil, err
		}
	}

	return q, nil
}

// applyStage folds one pipeline stage into the query.
func (q *searchQuery) applyStage(stage string) error {
	keyword, rest := splitKeyword(stage)

	switch keyword {
	case stageWhere:
		conds, err := parseConditions(rest)
		if err != nil {
			return err
		}

		q.conditions = append(q.conditions, conds...)

		return nil
	case stageSort:
		return q.applySort(rest)
	case "":
		return cerrors.New(cerrors.InvalidArgument, "empty stage in search query")
	default:
		return cerrors.Newf(cerrors.InvalidArgument,
			"unsupported search operator %q; CloudEmu's OCI Logging search models %s", keyword, supportedOperators)
	}
}

// applySort parses `sort by datetime [asc|desc]`. Entries are ordered by time,
// so a sort on any other field is refused rather than ignored.
func (q *searchQuery) applySort(rest string) error {
	fields := strings.Fields(rest)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "by") {
		return cerrors.New(cerrors.InvalidArgument, "sort must be written as 'sort by <field> [asc|desc]'")
	}

	field := strings.ToLower(strings.TrimSuffix(fields[1], ","))
	field = strings.TrimPrefix(field, "logcontent.")

	if field != fieldDatetime && field != fieldTime {
		return cerrors.Newf(cerrors.InvalidArgument,
			"sort by %q is not modeled; CloudEmu's OCI Logging search sorts by datetime only", fields[1])
	}

	if len(fields) > 2 { //nolint:mnd // the optional direction
		switch strings.ToLower(fields[2]) {
		case "asc":
			q.sortDesc = false
		case "desc":
			q.sortDesc = true
		default:
			return cerrors.Newf(cerrors.InvalidArgument, "sort direction %q is not asc or desc", fields[2])
		}
	}

	if len(fields) > 3 { //nolint:mnd // nothing follows the direction
		return cerrors.New(cerrors.InvalidArgument, "sort takes a single field and an optional direction")
	}

	return nil
}

// parseScopes parses the comma-separated, quoted targets of a search clause.
func parseScopes(rest string) ([]searchScope, error) {
	parts := splitOutsideQuotes(rest, ',')

	scopes := make([]searchScope, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		literal, ok := unquote(part)
		if !ok {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"search target %s must be quoted, as \"compartmentId[/logGroupId[/logId]]\"", part)
		}

		s, err := parseScope(literal)
		if err != nil {
			return nil, err
		}

		scopes = append(scopes, s)
	}

	if len(scopes) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"the search clause names no target; expected \"compartmentId[/logGroupId[/logId]]\"")
	}

	return scopes, nil
}

// parseScope parses one compartment/logGroup/log target. Real OCI addresses
// each segment by OCID, and a name in their place is refused rather than
// quietly matching nothing.
func parseScope(literal string) (searchScope, error) {
	segments := strings.Split(literal, "/")
	if len(segments) > maxScopeSegments {
		return searchScope{}, cerrors.Newf(cerrors.InvalidArgument,
			"search target %q has %d segments; expected compartmentId[/logGroupId[/logId]]",
			literal, len(segments))
	}

	prefixes := []string{prefixCompartment, prefixLogGroup, prefixLog}
	names := []string{"compartment", "log group", "log"}

	var s searchScope

	for i, segment := range segments {
		if !strings.HasPrefix(segment, prefixes[i]) {
			return searchScope{}, cerrors.Newf(cerrors.InvalidArgument,
				"search target segment %q is not a %s OCID; CloudEmu's OCI Logging search addresses each "+
					"segment by OCID", segment, names[i])
		}
	}

	s.compartmentID = segments[0]

	if len(segments) > 1 {
		s.logGroupID = segments[1]
	}

	if len(segments) > 2 { //nolint:mnd // the log is the third segment
		s.logID = segments[2]
	}

	return s, nil
}

// parseConditions parses a where clause: comparisons joined by and.
func parseConditions(rest string) ([]condition, error) {
	if strings.ContainsAny(stripQuoted(rest), "()") {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"parenthesized where clauses are not modeled; CloudEmu joins comparisons with and")
	}

	for _, word := range wordsOutsideQuotes(rest) {
		switch strings.ToLower(word) {
		case "or", "not":
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"the %q operator is not modeled in a where clause; CloudEmu joins comparisons with and",
				strings.ToLower(word))
		}
	}

	parts := splitOnWord(rest, "and")

	conds := make([]condition, 0, len(parts))

	for _, part := range parts {
		c, err := parseCondition(part)
		if err != nil {
			return nil, err
		}

		conds = append(conds, c)
	}

	if len(conds) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "where takes at least one comparison")
	}

	return conds, nil
}

// parseCondition parses one `field = 'value'` or `field != 'value'`.
func parseCondition(part string) (condition, error) {
	part = strings.TrimSpace(part)
	if part == "" {
		return condition{}, cerrors.New(cerrors.InvalidArgument, "empty comparison in where clause")
	}

	idx, op := findOperator(part)
	if idx < 0 {
		return condition{}, cerrors.Newf(cerrors.InvalidArgument,
			"where comparison %q has no operator; CloudEmu models = and !=", part)
	}

	if op != opEqual && op != opNotEqual {
		return condition{}, cerrors.Newf(cerrors.InvalidArgument,
			"the %q operator is not modeled; CloudEmu's OCI Logging search models = and !=, "+
				"with * as the wildcard", op)
	}

	field := strings.TrimSpace(part[:idx])
	if field == "" {
		return condition{}, cerrors.Newf(cerrors.InvalidArgument, "where comparison %q names no field", part)
	}

	literal := strings.TrimSpace(part[idx+len(op):])
	if literal == "" {
		return condition{}, cerrors.Newf(cerrors.InvalidArgument, "where comparison %q has no value", part)
	}

	ref, err := resolveField(field)
	if err != nil {
		return condition{}, err
	}

	pattern, ok := unquote(literal)
	if !ok {
		pattern = literal
	}

	return condition{field: ref, negated: op == opNotEqual, pattern: pattern}, nil
}

// findOperator returns the offset and text of the first comparison operator
// outside a quoted literal.
func findOperator(s string) (offset int, operator string) {
	var quote rune

	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case strings.ContainsRune(operatorChars, r):
			end := i
			for end < len(s) && strings.ContainsRune(operatorChars, rune(s[end])) {
				end++
			}

			return i, s[i:end]
		}
	}

	return -1, ""
}

// splitOutsideQuotes splits on sep, ignoring separators inside a quoted
// literal.
func splitOutsideQuotes(s string, sep rune) []string {
	var (
		out   []string
		buf   strings.Builder
		quote rune
	)

	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == sep:
			out = append(out, buf.String())
			buf.Reset()

			continue
		}

		buf.WriteRune(r)
	}

	return append(out, buf.String())
}

// splitOnWord splits on a bare keyword outside quoted literals.
func splitOnWord(s, word string) []string {
	var (
		out  []string
		cur  []string
		seen = strings.EqualFold
	)

	for _, token := range tokenize(s) {
		if !token.quoted && seen(token.text, word) {
			out = append(out, strings.Join(cur, " "))
			cur = nil

			continue
		}

		cur = append(cur, token.raw)
	}

	return append(out, strings.Join(cur, " "))
}

// wordsOutsideQuotes returns the bare tokens of s, skipping quoted literals.
func wordsOutsideQuotes(s string) []string {
	var out []string

	for _, token := range tokenize(s) {
		if !token.quoted {
			out = append(out, token.text)
		}
	}

	return out
}

// stripQuoted returns s with every quoted literal removed, so a structural
// check does not trip over punctuation inside a value.
func stripQuoted(s string) string {
	var (
		buf   strings.Builder
		quote rune
	)

	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		default:
			buf.WriteRune(r)
		}
	}

	return buf.String()
}

// token is one whitespace-delimited piece of a clause. raw keeps the quotes a
// literal was written with; text drops them.
type token struct {
	raw    string
	text   string
	quoted bool
}

// tokenize splits a clause on whitespace, keeping a quoted literal whole.
func tokenize(s string) []token {
	var (
		out   []token
		buf   strings.Builder
		quote rune
		saw   bool
	)

	flush := func() {
		if buf.Len() == 0 {
			return
		}

		raw := buf.String()
		text, _ := unquote(raw)

		out = append(out, token{raw: raw, text: text, quoted: saw})

		buf.Reset()

		saw = false
	}

	for _, r := range s {
		switch {
		case quote != 0:
			buf.WriteRune(r)

			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			saw = true

			buf.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			buf.WriteRune(r)
		}
	}

	flush()

	return out
}

// unquote strips a matching pair of surrounding quotes, reporting whether the
// text was quoted.
func unquote(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 { //nolint:mnd // a quoted literal is at least a pair of quotes
		return s, false
	}

	first, last := s[0], s[len(s)-1]
	if (first == '\'' || first == '"') && first == last {
		return s[1 : len(s)-1], true
	}

	return s, false
}

// splitKeyword splits a stage into its leading keyword, lowercased, and the
// rest of the clause.
func splitKeyword(stage string) (keyword, rest string) {
	stage = strings.TrimSpace(stage)

	idx := strings.IndexFunc(stage, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' })
	if idx < 0 {
		return strings.ToLower(stage), ""
	}

	return strings.ToLower(stage[:idx]), strings.TrimSpace(stage[idx+1:])
}
