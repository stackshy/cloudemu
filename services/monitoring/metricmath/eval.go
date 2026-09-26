package metricmath

import "time"

// result is either a scalar constant or a resolved time series.
type result struct {
	isScalar bool
	scalar   float64
	series   Series
}

// asSeries renders a result as a series. A scalar becomes a single point, so
// a constant expression still returns a value row.
func (r result) asSeries() Series {
	if !r.isScalar {
		return r.series
	}

	return Series{Timestamps: []time.Time{{}}, Values: []float64{r.scalar}}
}

// combine applies a binary operator point by point. A scalar operand is used
// against every point of the other operand.
func combine(op byte, left, right result) result {
	if left.isScalar && right.isScalar {
		return result{isScalar: true, scalar: applyOp(op, left.scalar, right.scalar)}
	}

	if left.isScalar {
		return scalarSeries(op, left.scalar, right.series, true)
	}

	if right.isScalar {
		return scalarSeries(op, right.scalar, left.series, false)
	}

	return seriesSeries(op, left.series, right.series)
}

func scalarSeries(op byte, scalar float64, s Series, scalarLeft bool) result {
	values := make([]float64, len(s.Values))

	for i, v := range s.Values {
		if scalarLeft {
			values[i] = applyOp(op, scalar, v)
		} else {
			values[i] = applyOp(op, v, scalar)
		}
	}

	return result{series: Series{Timestamps: s.Timestamps, Values: values}}
}

// seriesSeries combines the points two series share a timestamp at. A point
// with no partner is dropped, so a gap in one input never shifts the other.
func seriesSeries(op byte, left, right Series) result {
	rightAt := make(map[int64]float64, len(right.Timestamps))
	for i, ts := range right.Timestamps {
		rightAt[ts.UnixNano()] = right.Values[i]
	}

	out := Series{Timestamps: []time.Time{}, Values: []float64{}}

	for i, ts := range left.Timestamps {
		rv, ok := rightAt[ts.UnixNano()]
		if !ok {
			continue
		}

		out.Timestamps = append(out.Timestamps, ts)
		out.Values = append(out.Values, applyOp(op, left.Values[i], rv))
	}

	return result{series: out}
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

// scope is what a node evaluates against: the evaluator that resolves IDs
// and the period to read them at.
type scope struct {
	e      *Evaluator
	period int
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
	series, err := s.e.resolve(n.id, s.period)
	if err != nil {
		return result{}, err
	}

	return result{series: series}, nil
}

type negNode struct{ operand node }

func (n negNode) evaluate(s scope) (result, error) {
	v, err := n.operand.evaluate(s)
	if err != nil {
		return result{}, err
	}

	return combine('-', result{isScalar: true}, v), nil
}

type binaryNode struct {
	op          byte
	left, right node
}

func (n binaryNode) evaluate(s scope) (result, error) {
	left, err := n.left.evaluate(s)
	if err != nil {
		return result{}, err
	}

	right, err := n.right.evaluate(s)
	if err != nil {
		return result{}, err
	}

	return combine(n.op, left, right), nil
}

// bandNode is ANOMALY_DETECTION_BAND(input, k). The band is two series, so
// it has no single-series value and evaluates to no data. Evaluator.Band
// reads it instead.
type bandNode struct {
	input string
	k     float64
}

func (bandNode) evaluate(scope) (result, error) {
	return result{series: Series{Timestamps: []time.Time{}, Values: []float64{}}}, nil
}

// collectRefs appends every ID the node reads to ids.
func collectRefs(n node, ids *[]string) {
	switch v := n.(type) {
	case refNode:
		*ids = append(*ids, v.id)
	case bandNode:
		*ids = append(*ids, v.input)
	case negNode:
		collectRefs(v.operand, ids)
	case binaryNode:
		collectRefs(v.left, ids)
		collectRefs(v.right, ids)
	}
}
