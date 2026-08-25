package cloudsql

import (
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
)

// paginateInstances sorts instances by name and slices out one page using the
// offset-based pageToken and maxResults, mirroring Cloud SQL's instances.list
// paging. An empty/zero maxResults falls back to the shared default page size.
func paginateInstances(items []sqlInstance, pageToken, maxResults string) (pagination.Page[sqlInstance], error) {
	limit := 0

	if maxResults != "" {
		if n, err := strconv.Atoi(maxResults); err == nil && n > 0 {
			limit = n
		}
	}

	return pagination.PaginateSorted(items, func(a, b sqlInstance) bool {
		return a.Name < b.Name
	}, pageToken, limit)
}

// filterInstances applies a Cloud SQL list filter of the form "field:value"
// (substring/"has" match) or "field=value" (exact match). Unrecognized or
// empty filters leave the list unchanged. Supported fields cover the common
// instances.list predicates: name, state, region, databaseVersion,
// backendType.
func filterInstances(items []sqlInstance, filter string) []sqlInstance {
	field, value, exact, ok := parseFilter(filter)
	if !ok {
		return items
	}

	out := make([]sqlInstance, 0, len(items))

	for i := range items {
		if instanceMatches(&items[i], field, value, exact) {
			out = append(out, items[i])
		}
	}

	return out
}

// parseFilter splits a "field<op>value" expression. exact is true for "=" and
// false for ":". ok is false when the filter is empty or malformed.
func parseFilter(filter string) (field, value string, exact, ok bool) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return "", "", false, false
	}

	if i := strings.IndexByte(filter, '='); i > 0 {
		return strings.TrimSpace(filter[:i]), strings.TrimSpace(filter[i+1:]), true, true
	}

	if i := strings.IndexByte(filter, ':'); i > 0 {
		return strings.TrimSpace(filter[:i]), strings.TrimSpace(filter[i+1:]), false, true
	}

	return "", "", false, false
}

// instanceMatches reports whether inst satisfies the parsed predicate. An
// unknown field never matches, so a bogus filter yields an empty list rather
// than silently returning everything.
func instanceMatches(inst *sqlInstance, field, value string, exact bool) bool {
	var got string

	switch strings.ToLower(field) {
	case "name":
		got = inst.Name
	case "state":
		got = inst.State
	case "region":
		got = inst.Region
	case "databaseversion":
		got = inst.DatabaseVersion
	case "backendtype":
		got = inst.BackendType
	default:
		return false
	}

	if exact {
		return strings.EqualFold(got, value)
	}

	return strings.Contains(strings.ToLower(got), strings.ToLower(value))
}
