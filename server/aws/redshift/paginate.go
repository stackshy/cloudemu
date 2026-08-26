package redshift

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// Redshift describe operations page on Marker + MaxRecords. MaxRecords is
// constrained to [20, 100] with a default of 100, matching the real API.
const (
	minMaxRecords     = 20
	maxMaxRecords     = 100
	defaultMaxRecords = 100
)

// paginateRedshift stable-sorts items by their identifier, then slices one
// Marker/MaxRecords page. It returns the page (its NextPageToken is the Marker
// to echo, empty when the result is not truncated). An unreadable Marker yields
// an error the caller surfaces as InvalidParameterValue. Shared by all four
// Redshift Describe handlers so their paging behaves identically.
func paginateRedshift[T any](items []T, id func(T) string, marker, maxRecords string) (pagination.Page[T], error) {
	less := func(a, b T) bool { return id(a) < id(b) }

	return pagination.PaginateSorted(items, less, marker, clampMaxRecords(formInt(maxRecords)))
}

// writeInvalidMarker reports an unreadable pagination Marker the way real
// Redshift does: an InvalidParameterValue query-protocol error.
func writeInvalidMarker(w http.ResponseWriter) {
	awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterValue", "invalid Marker")
}

// clampMaxRecords applies the Redshift MaxRecords contract: unset (<= 0)
// defaults to 100, and values outside [20, 100] are clamped into the range.
func clampMaxRecords(n int) int {
	switch {
	case n <= 0:
		return defaultMaxRecords
	case n < minMaxRecords:
		return minMaxRecords
	case n > maxMaxRecords:
		return maxMaxRecords
	default:
		return n
	}
}
