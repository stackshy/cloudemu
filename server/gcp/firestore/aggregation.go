package firestore

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// runAggregationQueryRequest mirrors google.firestore.v1.RunAggregationQueryRequest:
// an underlying structured query plus a set of aggregations to compute over its
// results.
type runAggregationQueryRequest struct {
	StructuredAggregationQuery structuredAggregationQuery `json:"structuredAggregationQuery"`
}

type structuredAggregationQuery struct {
	StructuredQuery structuredQuery `json:"structuredQuery"`
	Aggregations    []aggregation   `json:"aggregations"`
}

// aggregation is one aliased aggregate operator. Exactly one of Count/Sum/Avg
// is set per entry.
type aggregation struct {
	Alias string    `json:"alias"`
	Count *countAgg `json:"count"`
	Sum   *fieldAgg `json:"sum"`
	Avg   *fieldAgg `json:"avg"`
}

// countAgg is a COUNT operator; UpTo optionally caps how far the count runs
// (Firestore's count(up_to)).
type countAgg struct {
	UpTo *aggInt64 `json:"upTo"`
}

// fieldAgg is a SUM or AVG operator over a single document field.
type fieldAgg struct {
	Field fieldRef `json:"field"`
}

// aggInt64 decodes a google.protobuf.Int64Value sent either as a JSON string
// ("1000", the proto3 JSON mapping the REST client uses) or a bare number.
type aggInt64 int64

func (a *aggInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}

	*a = aggInt64(n)

	return nil
}

// aggregationResponseEntry mirrors google.firestore.v1.RunAggregationQueryResponse.
// Firestore aggregation has no GROUP BY, so exactly one entry is streamed.
type aggregationResponseEntry struct {
	Result   aggregationResultBody `json:"result"`
	ReadTime string                `json:"readTime"`
}

type aggregationResultBody struct {
	AggregateFields map[string]value `json:"aggregateFields"`
}

// runAggregationQuery handles POST .../documents:runAggregationQuery. It runs
// the underlying structured query, then computes COUNT/SUM/AVG over the matched
// documents and returns a single aggregation result row.
func (h *Handler) runAggregationQuery(w http.ResponseWriter, r *http.Request, base string) {
	var req runAggregationQueryRequest

	if !decodeJSON(w, r, &req) {
		return
	}

	saq := &req.StructuredAggregationQuery

	if len(saq.StructuredQuery.From) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "from clause required")
		return
	}

	if len(saq.Aggregations) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "at least one aggregation required")
		return
	}

	docs, err := h.aggregationMatches(r, base, &saq.StructuredQuery)
	if err != nil {
		writeErr(w, err)
		return
	}

	fields, err := computeAggregates(saq.Aggregations, docs)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, []aggregationResponseEntry{{
		Result:   aggregationResultBody{AggregateFields: fields},
		ReadTime: time.Now().UTC().Format(time.RFC3339Nano),
	}})
}

// aggregationMatches resolves the underlying structured query to the set of
// documents the aggregates run over: it applies the where clause plus the
// non-select shaping (order/cursor/offset/limit). Select is intentionally
// skipped so the aggregated fields survive. A never-written collection has no
// driver table; real Firestore aggregates to zero rows rather than erroring, so
// NotFound is treated as an empty match.
func (h *Handler) aggregationMatches(r *http.Request, base string, q *structuredQuery) ([]map[string]any, error) {
	p, perr := parseFirestorePath(base)
	if perr != nil {
		return nil, cerrors.New(cerrors.InvalidArgument, perr.Error())
	}

	p.collection = joinPath(p.parentPath(), q.From[0].CollectionID)
	p.documentID = ""

	node, ferr := buildFilterNode(q.Where)
	if ferr != nil {
		return nil, cerrors.New(cerrors.InvalidArgument, ferr.Error())
	}

	result, err := h.db.Scan(r.Context(), dbdriver.ScanInput{Table: p.tableKey(), Limit: allResults})
	if err != nil {
		if cerrors.IsNotFound(err) {
			return nil, nil
		}

		return nil, err
	}

	matched, merr := filterDocuments(result.Items, node)
	if merr != nil {
		return nil, merr
	}

	shape := *q
	shape.Select = nil

	return shapeResults(matched, &shape), nil
}

// computeAggregates evaluates every aggregation against the matched documents,
// keyed by its alias (Firestore autogenerates "field_<n>" when none is given).
func computeAggregates(aggs []aggregation, docs []map[string]any) (map[string]value, error) {
	out := make(map[string]value, len(aggs))

	for i := range aggs {
		v, err := computeOne(aggs[i], docs)
		if err != nil {
			return nil, err
		}

		out[aggregateAlias(aggs[i], i)] = v
	}

	return out, nil
}

func aggregateAlias(a aggregation, i int) string {
	if a.Alias != "" {
		return a.Alias
	}

	return "field_" + strconv.Itoa(i+1)
}

func computeOne(a aggregation, docs []map[string]any) (value, error) {
	switch {
	case a.Count != nil:
		return countValue(a.Count, len(docs)), nil
	case a.Sum != nil:
		return sumValue(a.Sum.Field.FieldPath, docs), nil
	case a.Avg != nil:
		return avgValue(a.Avg.Field.FieldPath, docs), nil
	default:
		return value{}, cerrors.New(cerrors.InvalidArgument, "aggregation must set one of count/sum/avg")
	}
}

// countValue returns the matched-document count, capped at UpTo when the client
// supplied a non-negative count(up_to) bound.
func countValue(c *countAgg, n int) value {
	if c.UpTo != nil {
		if cap64 := int64(*c.UpTo); cap64 >= 0 && int64(n) > cap64 {
			n = int(cap64)
		}
	}

	return intValue(int64(n))
}

// sumValue sums the numeric values of a field across the documents, ignoring
// documents where the field is absent or non-numeric. It returns an integer
// when every summand is an integer and the total neither overflows int64 nor
// involves a floating value; otherwise a double (NaN if any value is NaN). An
// empty numeric set sums to integer 0, matching real Firestore.
func sumValue(path string, docs []map[string]any) value {
	intSum, floatSum, sawFloat, overflow, sawNaN := accumulateSum(path, docs)

	switch {
	case sawNaN:
		return doubleValue(math.NaN())
	case sawFloat || overflow:
		return doubleValue(floatSum)
	default:
		return intValue(intSum)
	}
}

func accumulateSum(path string, docs []map[string]any) (intSum int64, floatSum float64, sawFloat, overflow, sawNaN bool) {
	for _, d := range docs {
		f, isInt, ok := numericField(d, path)
		if !ok {
			continue
		}

		floatSum += f

		if math.IsNaN(f) {
			sawNaN = true
		}

		if !isInt {
			sawFloat = true
			continue
		}

		if s, ok := addInt64(intSum, int64(f)); ok {
			intSum = s
		} else {
			overflow = true
		}
	}

	return intSum, floatSum, sawFloat, overflow, sawNaN
}

// avgValue averages the numeric values of a field. It returns a double, or null
// when no document has a numeric value for the field (matching Firestore, whose
// average over an empty numeric set is NULL rather than 0).
func avgValue(path string, docs []map[string]any) value {
	var sum float64

	count := 0

	for _, d := range docs {
		f, _, ok := numericField(d, path)
		if !ok {
			continue
		}

		sum += f
		count++
	}

	if count == 0 {
		return nullValue()
	}

	return doubleValue(sum / float64(count))
}

// numericField resolves a document field to a float, reporting whether the
// stored value was an integer (int64) and whether it was numeric at all.
func numericField(item map[string]any, path string) (f float64, isInt, ok bool) {
	switch v := resolveField(item, path).(type) {
	case int64:
		return float64(v), true, true
	case float64:
		return v, false, true
	default:
		return 0, false, false
	}
}

// addInt64 adds two int64 values, reporting false on signed overflow.
func addInt64(a, b int64) (int64, bool) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return 0, false
	}

	return s, true
}

func intValue(n int64) value {
	s := strconv.FormatInt(n, 10)

	return value{IntegerValue: &s}
}

func doubleValue(f float64) value {
	return value{DoubleValue: &f}
}

// nullValueName is the sentinel Firestore uses for a NullValue field.
const nullValueName = "NULL_VALUE"

func nullValue() value {
	s := nullValueName

	return value{NullValue: &s}
}
