package servicebus

import (
	"strconv"
	"strings"
)

// messageProps is the subset of a Service Bus message's well-known system
// properties that subscription rule filters evaluate against. These are the
// same properties CorrelationFilter matches and the sys.* identifiers a
// SqlFilter expression can reference:
// https://learn.microsoft.com/azure/service-bus-messaging/topic-filters
type messageProps struct {
	MessageID        string
	CorrelationID    string
	Label            string
	To               string
	ReplyTo          string
	SessionID        string
	ReplyToSessionID string
	ContentType      string
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
// the docs. User-defined Properties are not matched -- see the DEFER note.
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

// sqlMatches evaluates a SqlFilter's expression against a message. Only a
// "basic" grammar is supported: the boolean literals real SDKs generate for
// TrueFilter/FalseFilter (1=1, 1=0, true, false), and an AND-joined list of
// sys.<Field> = 'value' equality predicates over the system properties
// sqlProp recognizes. A clause outside that grammar (arithmetic, LIKE, a
// user-defined property) can't be evaluated without the deferred
// custom-property-header support, so it is treated as satisfied rather than
// silently dropping messages a real broker would deliver.
func sqlMatches(f *sqlFilter, p *messageProps) bool {
	if f == nil {
		return true
	}

	return evalSQLExpression(f.SQLExpression, p)
}

func evalSQLExpression(expr string, p *messageProps) bool {
	expr = strings.TrimSpace(expr)

	switch strings.ToLower(expr) {
	case "", "1=1", "true":
		return true
	case "1=0", "false":
		return false
	}

	for _, clause := range splitSQLAnd(expr) {
		if !evalSQLClause(clause, p) {
			return false
		}
	}

	return true
}

// splitSQLAnd splits a SQL filter expression on top-level "AND" keywords.
// It does not account for AND appearing inside a quoted string literal --
// out of scope for the "basic predicates" this evaluator supports.
func splitSQLAnd(expr string) []string {
	fields := strings.Fields(expr)

	var (
		clauses []string
		cur     []string
	)

	for _, f := range fields {
		if strings.EqualFold(f, "AND") {
			clauses = append(clauses, strings.Join(cur, " "))
			cur = nil

			continue
		}

		cur = append(cur, f)
	}

	clauses = append(clauses, strings.Join(cur, " "))

	return clauses
}

// evalSQLClause evaluates one "sys.<Field> = 'value'" equality predicate.
// Anything else (a user property, a non-equality operator) is unsupported and
// treated as matching -- see evalSQLExpression.
func evalSQLClause(clause string, p *messageProps) bool {
	field, want, ok := parseSQLEquality(clause)
	if !ok {
		return true
	}

	field = strings.TrimPrefix(field, "sys.")

	got, known := sqlProp(field, p)
	if !known {
		return true
	}

	return got == want
}

// minQuotedLiteralLen is the shortest a quoted SQL string literal can be: two
// matching quote characters around an empty string (an empty pair of quotes).
const minQuotedLiteralLen = 2

// parseSQLEquality parses a "<field> = 'value'" or "<field> = \"value\""
// clause, reporting ok=false for anything else.
func parseSQLEquality(clause string) (field, value string, ok bool) {
	idx := strings.Index(clause, "=")
	if idx < 0 {
		return "", "", false
	}

	field = strings.TrimSpace(clause[:idx])

	raw := strings.TrimSpace(clause[idx+1:])
	if len(raw) < minQuotedLiteralLen {
		return "", "", false
	}

	quote := raw[0]
	if (quote != '\'' && quote != '"') || raw[len(raw)-1] != quote {
		return "", "", false
	}

	return field, raw[1 : len(raw)-1], true
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
