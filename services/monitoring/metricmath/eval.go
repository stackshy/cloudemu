package metricmath

import (
	"sort"
	"time"
)

// Labeled is one series of an expression result together with the label of
// the metric it came from. A SEARCH returns one per metric it finds. Label is
// empty for a series that does not come from a single found metric.
type Labeled struct {
	Label string
	Series
}

// result is a scalar constant, a single time series or an array of time
// series (TS[]).
type result struct {
	isScalar bool
	scalar   float64
	series   Series
	isArray  bool
	array    []Labeled
}

func emptySeries() Series {
	return Series{Timestamps: []time.Time{}, Values: []float64{}}
}

// asSeries renders a result as a series. A scalar becomes a single point, so
// a constant expression still returns a value row. An array has a single
// series value only when it holds exactly one series.
func (r *result) asSeries() Series {
	switch {
	case r.isScalar:
		return Series{Timestamps: []time.Time{{}}, Values: []float64{r.scalar}}
	case r.isArray:
		if len(r.array) == 1 {
			return r.array[0].Series
		}

		return emptySeries()
	default:
		return r.series
	}
}

// labeled renders a result as the list of series it returns.
func (r *result) labeled() []Labeled {
	if r.isArray {
		return r.array
	}

	return []Labeled{{Series: r.asSeries()}}
}

// mapArray applies f to each series of an array, keeping the labels.
func mapArray(arr []Labeled, f func(result) result) result {
	out := make([]Labeled, 0, len(arr))

	for _, l := range arr {
		r := f(result{series: l.Series})
		out = append(out, Labeled{Label: l.Label, Series: r.asSeries()})
	}

	return result{isArray: true, array: out}
}

// pointFunc combines two values into one.
type pointFunc func(a, b float64) float64

// pointwise applies f to two operands. A scalar operand is used against every
// point of the other one, and an array operand is combined series by series.
// With union set, a timestamp only one series has counts as 0 in the other,
// as metric-math comparison and logical operators do. Without it the point is
// dropped.
func pointwise(f pointFunc, left, right *result, union bool) result {
	switch {
	case left.isArray && right.isArray:
		// TS[] op TS[] is not a documented form.
		return result{series: emptySeries()}
	case left.isArray:
		return mapArray(left.array, func(l result) result { return pointwise(f, &l, right, union) })
	case right.isArray:
		return mapArray(right.array, func(r result) result { return pointwise(f, left, &r, union) })
	case left.isScalar && right.isScalar:
		return result{isScalar: true, scalar: f(left.scalar, right.scalar)}
	case left.isScalar:
		return result{series: scalarSeries(f, left.scalar, right.series, true)}
	case right.isScalar:
		return result{series: scalarSeries(f, right.scalar, left.series, false)}
	case union:
		return result{series: unionSeries(f, left.series, right.series)}
	default:
		return result{series: intersectSeries(f, left.series, right.series)}
	}
}

func scalarSeries(f pointFunc, scalar float64, s Series, scalarLeft bool) Series {
	values := make([]float64, len(s.Values))

	for i, v := range s.Values {
		if scalarLeft {
			values[i] = f(scalar, v)
		} else {
			values[i] = f(v, scalar)
		}
	}

	return Series{Timestamps: s.Timestamps, Values: values}
}

// valuesAt indexes a series by timestamp.
func valuesAt(s Series) map[int64]float64 {
	at := make(map[int64]float64, len(s.Timestamps))
	for i, ts := range s.Timestamps {
		at[ts.UnixNano()] = s.Values[i]
	}

	return at
}

// intersectSeries combines the points two series share a timestamp at. A
// point with no partner is dropped, so a gap in one input never shifts the
// other.
func intersectSeries(f pointFunc, left, right Series) Series {
	rightAt := valuesAt(right)
	out := emptySeries()

	for i, ts := range left.Timestamps {
		rv, ok := rightAt[ts.UnixNano()]
		if !ok {
			continue
		}

		out.Timestamps = append(out.Timestamps, ts)
		out.Values = append(out.Values, f(left.Values[i], rv))
	}

	return out
}

// unionSeries combines two series at every timestamp either has, reading a
// missing point as 0, in time order.
func unionSeries(f pointFunc, left, right Series) Series {
	leftAt, rightAt := valuesAt(left), valuesAt(right)
	stamps := mergeTimestamps(left.Timestamps, right.Timestamps)
	out := Series{Timestamps: make([]time.Time, 0, len(stamps)), Values: make([]float64, 0, len(stamps))}

	for _, ts := range stamps {
		out.Timestamps = append(out.Timestamps, ts)
		out.Values = append(out.Values, f(leftAt[ts.UnixNano()], rightAt[ts.UnixNano()]))
	}

	return out
}

// mergeTimestamps returns the distinct timestamps of all lists, sorted.
func mergeTimestamps(lists ...[]time.Time) []time.Time {
	seen := map[int64]bool{}

	var out []time.Time

	for _, l := range lists {
		for _, ts := range l {
			if seen[ts.UnixNano()] {
				continue
			}

			seen[ts.UnixNano()] = true

			out = append(out, ts)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })

	return out
}

// arithmetic returns the point function of an arithmetic operator.
func arithmetic(op byte) pointFunc {
	return func(a, b float64) float64 { return applyOp(op, a, b) }
}

func applyOp(op byte, a, b float64) float64 {
	switch op {
	case '+':
		return a + b
	case '-':
		return a - b
	case '*':
		return a * b
	case '/':
		if b == 0 {
			return 0
		}

		return a / b
	default:
		return 0
	}
}

// truth renders a boolean as the 1 or 0 of a metric-math comparison.
func truth(b bool) float64 {
	if b {
		return 1
	}

	return 0
}

// comparison returns the point function of a comparison operator.
func comparison(op string) pointFunc {
	return func(a, b float64) float64 {
		switch op {
		case opEq:
			return truth(a == b)
		case opNe:
			return truth(a != b)
		case opLt:
			return truth(a < b)
		case opLe:
			return truth(a <= b)
		case opGt:
			return truth(a > b)
		case opGe:
			return truth(a >= b)
		default:
			return 0
		}
	}
}

// scope is what a node evaluates against: the evaluator that resolves IDs,
// the period to read them at, and the period of the expression's own points,
// which FILL fills and SEARCH reads at.
type scope struct {
	e          *Evaluator
	period     int
	gridPeriod int
}

// node is a parsed expression node.
type node interface {
	evaluate(s scope) (result, error)
}

type numberNode struct{ val float64 }

func (n numberNode) evaluate(scope) (result, error) {
	return result{isScalar: true, scalar: n.val}, nil
}

type refNode struct{ id string }

func (n refNode) evaluate(s scope) (result, error) {
	return s.e.resolve(n.id, s.period)
}

// stringNode is a string literal. Only SEARCH reads one, so on its own it
// has no value.
type stringNode struct{ val string }

func (stringNode) evaluate(scope) (result, error) {
	return result{series: emptySeries()}, nil
}

// keywordNode is a bare uppercase word such as REPEAT. Only FILL reads one.
type keywordNode struct{ word string }

func (keywordNode) evaluate(scope) (result, error) {
	return result{series: emptySeries()}, nil
}

type negNode struct{ operand node }

func (n negNode) evaluate(s scope) (result, error) {
	v, err := n.operand.evaluate(s)
	if err != nil {
		return result{}, err
	}

	return pointwise(arithmetic('-'), &result{isScalar: true}, &v, false), nil
}

type binaryNode struct {
	op          byte
	left, right node
}

func (n binaryNode) evaluate(s scope) (result, error) {
	left, right, err := evalPair(s, n.left, n.right)
	if err != nil {
		return result{}, err
	}

	return pointwise(arithmetic(n.op), &left, &right, false), nil
}

// compareNode is a comparison. Each point is 1 when it holds and 0 when not.
type compareNode struct {
	op          string
	left, right node
}

func (n compareNode) evaluate(s scope) (result, error) {
	left, right, err := evalPair(s, n.left, n.right)
	if err != nil {
		return result{}, err
	}

	return pointwise(comparison(n.op), &left, &right, true), nil
}

// logicNode is AND or OR. A non-zero value is true.
type logicNode struct {
	and         bool
	left, right node
}

func (n logicNode) evaluate(s scope) (result, error) {
	left, right, err := evalPair(s, n.left, n.right)
	if err != nil {
		return result{}, err
	}

	f := func(a, b float64) float64 {
		if n.and {
			return truth(a != 0 && b != 0)
		}

		return truth(a != 0 || b != 0)
	}

	return pointwise(f, &left, &right, true), nil
}

func evalPair(s scope, a, b node) (left, right result, err error) {
	left, err = a.evaluate(s)
	if err != nil {
		return result{}, result{}, err
	}

	right, err = b.evaluate(s)
	if err != nil {
		return result{}, result{}, err
	}

	return left, right, nil
}

// bandNode is ANOMALY_DETECTION_BAND(input, k). The band is two series, so
// it has no single-series value and evaluates to no data. Evaluator.Band
// reads it instead.
type bandNode struct {
	input string
	k     float64
}

func (bandNode) evaluate(scope) (result, error) {
	return result{series: emptySeries()}, nil
}

// children returns the sub-expressions of a node.
func children(n node) []node {
	switch v := n.(type) {
	case negNode:
		return []node{v.operand}
	case binaryNode:
		return []node{v.left, v.right}
	case compareNode:
		return []node{v.left, v.right}
	case logicNode:
		return []node{v.left, v.right}
	case ifNode:
		if v.otherwise != nil {
			return []node{v.cond, v.then, v.otherwise}
		}

		return []node{v.cond, v.then}
	case fillNode:
		return []node{v.input, v.with}
	default:
		return nil
	}
}

// collectRefs appends every ID the node reads to ids.
func collectRefs(n node, ids *[]string) {
	switch v := n.(type) {
	case refNode:
		*ids = append(*ids, v.id)
	case bandNode:
		*ids = append(*ids, v.input)
	default:
		for _, c := range children(n) {
			collectRefs(c, ids)
		}
	}
}

// keywordsPlaced reports whether every keyword is the filler of a FILL.
func keywordsPlaced(n node) bool {
	switch v := n.(type) {
	case keywordNode:
		return false
	case fillNode:
		if _, ok := v.with.(keywordNode); ok {
			return keywordsPlaced(v.input)
		}
	}

	for _, c := range children(n) {
		if !keywordsPlaced(c) {
			return false
		}
	}

	return true
}
