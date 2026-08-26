package servicebus

import (
	"strconv"
	"strings"
)

// messageProps is the subset of a Service Bus message that subscription rule
// filters evaluate against: the well-known system properties (the sys.*
// identifiers a SqlFilter references and the fields a CorrelationFilter
// matches) plus the sender's custom application properties (referenced by bare
// name, or a user. prefix, in a SqlFilter and by key in a CorrelationFilter's
// Properties). https://learn.microsoft.com/azure/service-bus-messaging/topic-filters
type messageProps struct {
	MessageID        string
	CorrelationID    string
	Label            string
	To               string
	ReplyTo          string
	SessionID        string
	ReplyToSessionID string
	ContentType      string
	// Custom holds the sender's user-defined application properties, keyed by
	// the header name they arrived under. Lookups are case-insensitive.
	Custom map[string]string
}

// lookupCustom resolves a user-defined property by name, case-insensitively
// (HTTP header canonicalization mangles the sender's casing), reporting whether
// the message carried it.
func lookupCustom(custom map[string]string, name string) (string, bool) {
	if v, ok := custom[name]; ok {
		return v, true
	}

	for k, v := range custom {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}

	return "", false
}

// rulesMatch reports whether a message matches at least one of a
// subscription's rules. Real Service Bus combines every filter-only rule with
// OR (https://learn.microsoft.com/azure/service-bus-messaging/topic-filters);
// rule actions that annotate/copy the message are not applied here. A
// subscription with no rules at all (shouldn't happen -- every subscription
// is seeded with $Default) imposes no filter.
func rulesMatch(rules []ruleProperties, props *messageProps) bool {
	if len(rules) == 0 {
		return true
	}

	for _, rule := range rules {
		if ruleMatches(&rule, props) {
			return true
		}
	}

	return false
}

func ruleMatches(rule *ruleProperties, props *messageProps) bool {
	if rule.FilterType == filterTypeCorrelation {
		return correlationMatches(rule.CorrelationFilter, props)
	}

	return sqlMatches(rule.SQLFilter, props)
}

// correlationMatches implements CorrelationFilter matching: every field the
// filter sets must equal the message's corresponding property (logical AND);
// unset fields impose no constraint. String comparison is case-sensitive, per
// the docs. User-defined Properties are matched against the message's custom
// application properties.
func correlationMatches(f *correlationFilter, p *messageProps) bool {
	if f == nil {
		return true
	}

	pairs := [...][2]string{
		{f.CorrelationID, p.CorrelationID},
		{f.MessageID, p.MessageID},
		{f.Label, p.Label},
		{f.To, p.To},
		{f.ReplyTo, p.ReplyTo},
		{f.SessionID, p.SessionID},
		{f.ReplyToSessionID, p.ReplyToSessionID},
		{f.ContentType, p.ContentType},
	}

	for _, pair := range pairs {
		want, got := pair[0], pair[1]
		if want != "" && want != got {
			return false
		}
	}

	for name, want := range f.Properties {
		if got, ok := lookupCustom(p.Custom, name); !ok || got != want {
			return false
		}
	}

	return true
}

// sqlProp looks up a message's value for a sys.* SQL filter identifier,
// case-insensitively, reporting whether the identifier is one this evaluator
// understands.
func sqlProp(field string, p *messageProps) (string, bool) {
	switch {
	case strings.EqualFold(field, "CorrelationId"):
		return p.CorrelationID, true
	case strings.EqualFold(field, "MessageId"):
		return p.MessageID, true
	case strings.EqualFold(field, "Label"):
		return p.Label, true
	case strings.EqualFold(field, "To"):
		return p.To, true
	case strings.EqualFold(field, "ReplyTo"):
		return p.ReplyTo, true
	case strings.EqualFold(field, "SessionId"):
		return p.SessionID, true
	case strings.EqualFold(field, "ReplyToSessionId"):
		return p.ReplyToSessionID, true
	case strings.EqualFold(field, "ContentType"):
		return p.ContentType, true
	default:
		return "", false
	}
}

// sqlMatches evaluates a SqlFilter's expression against a message. The grammar
// is OR-of-AND-of-comparison-clauses: top-level OR groups (each an AND-joined
// list of clauses) are tried in turn, and the message matches if any group's
// clauses all hold. A clause is "<field> <op> <value>", where op is one of
// =, !=, <>, <, >, <=, >=; field is a sys.<Name> system property, a bare or
// user.<Name> custom application property; and value is a quoted string or a
// numeric literal. Boolean literals (1=1, 1=0, true, false) work as-is.
//
// The no-drop invariant: a clause this evaluator cannot parse (an unsupported
// operator such as LIKE/IN, malformed syntax, or an unrecognized sys.*
// identifier) is treated as satisfied rather than silently dropping a message a
// real broker would deliver. A well-formed comparison that legitimately fails
// (or references an absent custom property) still excludes the message.
//
// DEFER: LIKE, IN, EXISTS, IS NULL, arithmetic and parentheses are not parsed;
// clauses using them fall back to the no-drop match=true path.
func sqlMatches(f *sqlFilter, p *messageProps) bool {
	if f == nil {
		return true
	}

	return evalSQLExpression(f.SQLExpression, p)
}

func evalSQLExpression(expr string, p *messageProps) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true
	}

	for _, group := range splitTopLevel(expr, "OR") {
		if evalSQLAndGroup(group, p) {
			return true
		}
	}

	return false
}

// evalSQLAndGroup reports whether every AND-joined clause in one OR-group holds.
func evalSQLAndGroup(group string, p *messageProps) bool {
	for _, clause := range splitTopLevel(group, "AND") {
		if !evalSQLClause(clause, p) {
			return false
		}
	}

	return true
}

// splitTopLevel splits a SQL filter expression on a top-level boolean keyword
// (OR/AND), case-insensitively. It does not account for the keyword appearing
// inside a quoted string literal -- out of scope for this evaluator.
func splitTopLevel(expr, keyword string) []string {
	fields := strings.Fields(expr)

	var (
		parts []string
		cur   []string
	)

	for _, f := range fields {
		if strings.EqualFold(f, keyword) {
			parts = append(parts, strings.Join(cur, " "))
			cur = nil

			continue
		}

		cur = append(cur, f)
	}

	parts = append(parts, strings.Join(cur, " "))

	return parts
}

// SQL comparison operators the filter grammar understands.
const (
	opEqual        = "="
	opNotEqual     = "!="
	opNotEqualAnsi = "<>"
	opLess         = "<"
	opGreater      = ">"
	opLessEqual    = "<="
	opGreaterEqual = ">="
)

// evalSQLClause evaluates one comparison clause, upholding the no-drop
// invariant for anything it cannot parse or resolve.
func evalSQLClause(clause string, p *messageProps) bool {
	clause = strings.TrimSpace(clause)

	switch strings.ToLower(clause) {
	case "true":
		return true
	case "false":
		return false
	}

	field, op, rawVal, ok := parseSQLClause(clause)
	if !ok {
		return true
	}

	// A literal left-hand side (the boolean-filter tautologies 1=1 / 1=0, or a
	// quoted constant) is compared directly rather than looked up as a property.
	if lit, isLit := sqlLiteral(field); isLit {
		return compareSQL(op, lit, rawVal)
	}

	got, present, recognized := resolveSQLField(field, p)
	if !recognized {
		return true
	}

	if !present {
		return false
	}

	return compareSQL(op, got, rawVal)
}

// sqlLiteral reports whether a clause's left-hand side is itself a literal (a
// numeric constant or a quoted string) rather than a property reference,
// returning its unquoted value.
func sqlLiteral(field string) (string, bool) {
	if _, err := strconv.ParseFloat(field, floatBitSize); err == nil {
		return field, true
	}

	if len(field) >= minQuotedLiteralLen {
		q := field[0]
		if (q == '\'' || q == '"') && field[len(field)-1] == q {
			return field[1 : len(field)-1], true
		}
	}

	return "", false
}

// parseSQLClause splits a clause into its field, comparison operator and raw
// value. It reads the operator greedily so "<=", ">=", "!=" and "<>" are not
// mistaken for their single-character prefixes. A lone "!" is not an operator.
func parseSQLClause(clause string) (field, op, value string, ok bool) {
	i := strings.IndexAny(clause, "=<>!")
	if i < 0 {
		return "", "", "", false
	}

	op = clause[i : i+1]

	if i+1 < len(clause) {
		switch clause[i : i+2] {
		case opLessEqual, opGreaterEqual, opNotEqual, opNotEqualAnsi:
			op = clause[i : i+2]
		}
	}

	if op == "!" {
		return "", "", "", false
	}

	field = strings.TrimSpace(clause[:i])
	value = strings.TrimSpace(clause[i+len(op):])

	if field == "" || value == "" {
		return "", "", "", false
	}

	return field, op, value, true
}

// resolveSQLField resolves a clause's field to its message value. A sys.<Name>
// identifier reads a system property (recognized=false for an unknown one, so
// the caller upholds the no-drop invariant); anything else is a custom
// application property, present only when the message carried it.
func resolveSQLField(field string, p *messageProps) (val string, present, recognized bool) {
	if rest, ok := cutPrefixFold(field, "sys."); ok {
		v, known := sqlProp(rest, p)
		if !known {
			return "", false, false
		}

		return v, true, true
	}

	name := field
	if rest, ok := cutPrefixFold(field, "user."); ok {
		name = rest
	}

	v, ok := lookupCustom(p.Custom, name)

	return v, ok, true
}

// compareSQL evaluates a parsed comparison. Equality falls back to string
// compare when the operands aren't both numeric; relational operators require
// numeric operands and otherwise report no match.
func compareSQL(op, got, rawVal string) bool {
	want := unquoteSQL(rawVal)

	switch op {
	case opEqual:
		return sqlEqual(got, want)
	case opNotEqual, opNotEqualAnsi:
		return !sqlEqual(got, want)
	case opLess, opGreater, opLessEqual, opGreaterEqual:
		return sqlRelational(op, got, want)
	default:
		return true
	}
}

func sqlEqual(got, want string) bool {
	if g, w, ok := twoFloats(got, want); ok {
		return g == w
	}

	return got == want
}

func sqlRelational(op, got, want string) bool {
	g, w, ok := twoFloats(got, want)
	if !ok {
		return false
	}

	switch op {
	case opLess:
		return g < w
	case opGreater:
		return g > w
	case opLessEqual:
		return g <= w
	case opGreaterEqual:
		return g >= w
	default:
		return false
	}
}

// floatBitSize is the precision strconv.ParseFloat parses SQL numeric literals at.
const floatBitSize = 64

// twoFloats parses both operands as numbers, reporting ok=false if either isn't.
func twoFloats(a, b string) (x, y float64, ok bool) {
	x, err := strconv.ParseFloat(a, floatBitSize)
	if err != nil {
		return 0, 0, false
	}

	y, err = strconv.ParseFloat(b, floatBitSize)
	if err != nil {
		return 0, 0, false
	}

	return x, y, true
}

// unquoteSQL strips a matching pair of single or double quotes from a string
// literal, leaving unquoted (numeric/bareword) values untouched.
func unquoteSQL(raw string) string {
	if len(raw) >= minQuotedLiteralLen {
		q := raw[0]
		if (q == '\'' || q == '"') && raw[len(raw)-1] == q {
			return raw[1 : len(raw)-1]
		}
	}

	return raw
}

// minQuotedLiteralLen is the shortest a quoted SQL string literal can be: two
// matching quote characters around an empty string (an empty pair of quotes).
const minQuotedLiteralLen = 2

// cutPrefixFold cuts a case-insensitive prefix from s, reporting whether it matched.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}

	return "", false
}

// defaultLockDurationSeconds is the peek-lock visibility window a queue or
// subscription gets when its LockDuration property is absent, matching
// Service Bus' documented PT1M default.
const defaultLockDurationSeconds = 60

// lockDurationSeconds converts an ISO-8601 duration -- the subset Service
// Bus' LockDuration property uses, e.g. "PT30S", "PT1M", "PT1M30S" -- to
// whole seconds, defaulting to defaultLockDurationSeconds when s is empty or
// not in that shape.
func lockDurationSeconds(s string) int {
	rest, ok := strings.CutPrefix(s, "PT")
	if !ok {
		return defaultLockDurationSeconds
	}

	total := 0

	for _, unit := range [...]struct {
		suffix byte
		mult   int
	}{{'H', 3600}, {'M', 60}, {'S', 1}} {
		idx := strings.IndexByte(rest, unit.suffix)
		if idx < 0 {
			continue
		}

		n, err := strconv.Atoi(rest[:idx])
		if err != nil {
			return defaultLockDurationSeconds
		}

		total += n * unit.mult
		rest = rest[idx+1:]
	}

	if total <= 0 {
		return defaultLockDurationSeconds
	}

	return total
}

// secondsPerDay is the whole-seconds length of the ISO-8601 date component "D".
const secondsPerDay = 86400

// ttlSecondsFromISO converts a DefaultMessageTimeToLive ISO-8601 duration
// (e.g. "PT5S", "PT1M", "P1DT2H") to whole seconds, returning 0 for "no TTL":
// an empty value, the effectively-unlimited maxTimeToLive sentinel, or anything
// it cannot cleanly parse (fractional seconds, weeks/months/years). Only the
// day/hour/minute/second subset Service Bus emits is supported.
func ttlSecondsFromISO(s string) int {
	if s == "" || s == maxTimeToLive {
		return 0
	}

	rest, ok := strings.CutPrefix(s, "P")
	if !ok {
		return 0
	}

	datePart, timePart := rest, ""
	if i := strings.IndexByte(rest, 'T'); i >= 0 {
		datePart, timePart = rest[:i], rest[i+1:]
	}

	days, ok := scanDurationField(datePart, 'D')
	if !ok {
		return 0
	}

	secs, ok := scanTimeComponents(timePart)
	if !ok {
		return 0
	}

	total := days*secondsPerDay + secs
	if total <= 0 {
		return 0
	}

	return total
}

// scanTimeComponents sums the H/M/S components of an ISO-8601 duration's time
// part, reporting ok=false if any leftover text remains (an unsupported unit).
func scanTimeComponents(timePart string) (int, bool) {
	total := 0

	for _, unit := range [...]struct {
		suffix byte
		mult   int
	}{{'H', 3600}, {'M', 60}, {'S', 1}} {
		idx := strings.IndexByte(timePart, unit.suffix)
		if idx < 0 {
			continue
		}

		n, err := strconv.Atoi(timePart[:idx])
		if err != nil {
			return 0, false
		}

		total += n * unit.mult
		timePart = timePart[idx+1:]
	}

	if timePart != "" {
		return 0, false
	}

	return total, true
}

// scanDurationField reads the integer preceding suffix in part, returning (0,
// true) when the suffix is absent and (0, false) when the number won't parse.
func scanDurationField(part string, suffix byte) (int, bool) {
	idx := strings.IndexByte(part, suffix)
	if idx < 0 {
		return 0, true
	}

	n, err := strconv.Atoi(part[:idx])
	if err != nil {
		return 0, false
	}

	return n, true
}
