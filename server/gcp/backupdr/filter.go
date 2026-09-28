package backupdr

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	bdrdriver "github.com/stackshy/cloudemu/v2/services/backupdr/driver"
)

const (
	labelsPrefix = "labels."
	andSep       = " AND "
	descKeyword  = "desc"
	ascKeyword   = "asc"

	filterName        = "name"
	filterState       = "state"
	filterDescription = "description"
	filterAccess      = "accessRestriction"
	filterInheritance = "backupRetentionInheritance"

	orderName       = filterName
	orderCreateTime = "createTime"
	orderUpdateTime = "updateTime"
)

// clause is one `field (=|!=) value` term of an AIP-160 list filter.
type clause struct {
	field  string
	value  string
	negate bool
}

// parseFilter parses the AIP-160 subset ListBackupVaults honors: one or more
// `field = "value"` / `field != "value"` terms joined by AND, where field is
// name (full resource name or bare id), state, description,
// accessRestriction, backupRetentionInheritance or labels.<key>. Anything else
// is INVALID_ARGUMENT rather than silently matching every vault, so a caller
// never mistakes an unfiltered list for a filtered one.
func parseFilter(filter string) ([]clause, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, nil
	}

	terms := strings.Split(filter, andSep)
	out := make([]clause, 0, len(terms))

	for _, term := range terms {
		c, ok := parseClause(term)
		if !ok {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"unsupported filter %q: supported terms are name, state, description, accessRestriction, "+
					"backupRetentionInheritance and labels.<key> compared with = or !=, joined by AND", filter)
		}

		out = append(out, c)
	}

	return out, nil
}

// parseClause splits one `field op value` term; ok is false for an unknown
// field, an unsupported operator, or an empty side.
func parseClause(term string) (clause, bool) {
	op, negate := "=", false

	idx := strings.Index(term, "!=")
	if idx >= 0 {
		op, negate = "!=", true
	} else if idx = strings.Index(term, "="); idx < 0 {
		return clause{}, false
	}

	field := strings.TrimSpace(term[:idx])
	value := strings.TrimSpace(term[idx+len(op):])

	value, ok := unquote(value)
	if !ok || field == "" || value == "" || !filterableField(field) {
		return clause{}, false
	}

	return clause{field: field, value: value, negate: negate}, true
}

// unquote returns a single literal: a "…" or '…' string without its quotes
// (and without the quote character inside it), or a bare token with no
// whitespace. Anything else (e.g. `"a" OR b = "c"`) is not one literal.
func unquote(value string) (string, bool) {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		inner := value[1 : len(value)-1]

		return inner, !strings.ContainsRune(inner, rune(value[0]))
	}

	return value, !strings.ContainsAny(value, " \t\"'")
}

// filterableField reports whether field is one parseFilter can evaluate.
func filterableField(field string) bool {
	switch field {
	case filterName, filterState, filterDescription, filterAccess, filterInheritance:
		return true
	default:
		return strings.HasPrefix(field, labelsPrefix) && len(field) > len(labelsPrefix)
	}
}

// matchesAll reports whether v satisfies every clause.
func matchesAll(v *bdrdriver.BackupVault, clauses []clause) bool {
	for _, c := range clauses {
		if matchClause(v, c) == c.negate {
			return false
		}
	}

	return true
}

// matchClause reports whether v's field equals the clause value (before
// negation). name matches the full resource name or the bare vault id.
func matchClause(v *bdrdriver.BackupVault, c clause) bool {
	switch c.field {
	case filterName:
		return c.value == resourceName(v.Project, v.Location, v.ID) || c.value == v.ID
	case filterState:
		return c.value == v.State
	case filterDescription:
		return c.value == v.Description
	case filterAccess:
		return c.value == v.AccessRestriction
	case filterInheritance:
		return c.value == v.BackupRetentionInheritance
	default:
		got, ok := v.Labels[strings.TrimPrefix(c.field, labelsPrefix)]

		return ok && got == c.value
	}
}

// parseOrderBy parses the orderBy ListBackupVaults honors: a single field of
// name, createTime or updateTime, optionally followed by asc or desc. An empty
// orderBy is name ascending. Anything else is INVALID_ARGUMENT.
func parseOrderBy(orderBy string) (field string, desc bool, err error) {
	parts := strings.Fields(orderBy)

	switch {
	case len(parts) == 0:
		return orderName, false, nil
	case len(parts) > 2: //nolint:mnd // field plus direction
		return "", false, unsupportedOrderBy(orderBy)
	}

	field = parts[0]
	if field != orderName && field != orderCreateTime && field != orderUpdateTime {
		return "", false, unsupportedOrderBy(orderBy)
	}

	if len(parts) == 2 { //nolint:mnd // field plus direction
		switch strings.ToLower(parts[1]) {
		case descKeyword:
			desc = true
		case ascKeyword:
		default:
			return "", false, unsupportedOrderBy(orderBy)
		}
	}

	return field, desc, nil
}

func unsupportedOrderBy(orderBy string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"unsupported orderBy %q: supported are name, createTime or updateTime, optionally followed by asc or desc", orderBy)
}

// vaultLess orders vaults by field (ties broken by resource name), reversed
// when desc.
func vaultLess(field string, desc bool) func(a, b bdrdriver.BackupVault) bool {
	return func(a, b bdrdriver.BackupVault) bool {
		na, nb := resourceName(a.Project, a.Location, a.ID), resourceName(b.Project, b.Location, b.ID)

		var less, equal bool

		switch field {
		case orderCreateTime:
			less, equal = a.CreateTime.Before(b.CreateTime), a.CreateTime.Equal(b.CreateTime)
		case orderUpdateTime:
			less, equal = a.UpdateTime.Before(b.UpdateTime), a.UpdateTime.Equal(b.UpdateTime)
		default:
			less, equal = na < nb, na == nb
		}

		if equal {
			less = na < nb
		}

		if desc {
			return !less && na != nb
		}

		return less
	}
}
