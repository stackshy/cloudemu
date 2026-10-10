package metricmath_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

// resolveExpr evaluates expr as entry e1 over m1, m2 and m3 (metrics A, B
// and C at one minute) for the five minutes from base.
func resolveExpr(t *testing.T, f *fixedFetcher, expr string) metricmath.Series {
	t.Helper()

	queries := []driver.MetricDataQuery{
		{ID: "m1", MetricStat: stat("A", 60), ReturnData: boolPtr(false)},
		{ID: "m2", MetricStat: stat("B", 60), ReturnData: boolPtr(false)},
		{ID: "m3", MetricStat: stat("C", 60), ReturnData: boolPtr(false)},
		{ID: "e1", Expression: expr},
	}

	s, err := metricmath.New(queries, f.fetch).WithRange(base, base.Add(5*time.Minute)).Resolve("e1")
	require.NoError(t, err)

	return s
}

// TestIFExpressions checks the three IF tables of the CloudWatch user guide
// ("Using IF expressions"). metric1 is [1, 1, 0, 0, -], metric2 is
// [30, -, 0, 0, 30] and metric3 is [0, 0, 20, -, 20].
func TestIFExpressions(t *testing.T) {
	f := &fixedFetcher{series: map[string]metricmath.Series{
		"A": {Timestamps: at(0, 1, 2, 3), Values: []float64{1, 1, 0, 0}},
		"B": {Timestamps: at(0, 2, 3, 4), Values: []float64{30, 0, 0, 30}},
		"C": {Timestamps: at(0, 1, 2, 4), Values: []float64{0, 0, 20, 20}},
	}}

	tests := []struct {
		name  string
		expr  string
		want  []float64
		stamp []time.Time
	}{
		{"IF(metric1, metric2, metric3)", "IF(m1, m2, m3)", []float64{30, 0, 20, 0}, at(0, 1, 2, 3)},
		{"IF(metric1, scalar2, metric3)", "IF(m1, 5, m3)", []float64{5, 5, 20}, at(0, 1, 2)},
		{"IF(metric1, metric2, scalar3)", "IF(m1, m2, 5)", []float64{30, 0, 5, 5}, at(0, 1, 2, 3)},
		{"false point dropped without falseValue", "IF(m1, m2)", []float64{30, 0}, at(0, 1)},
		{"comparison condition", "IF(m3 > 10, 1, 0)", []float64{0, 0, 1, 1}, at(0, 1, 2, 4)},
		{"scalar condition true", "IF(1, m2, m3)", []float64{30, 0, 0, 30}, at(0, 2, 3, 4)},
		{"scalar condition false", "IF(0, m2, m3)", []float64{0, 0, 20, 20}, at(0, 1, 2, 4)},
		{"scalar condition false without falseValue", "IF(0, m2)", []float64{}, []time.Time{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := resolveExpr(t, f, tc.expr)
			assert.Equal(t, tc.want, s.Values)
			assert.Equal(t, tc.stamp, s.Timestamps)
		})
	}
}

// TestComparisonAndLogicalOperators checks the operator table of the user
// guide: metric1 is [30, 20, 0, 0] and metric2 is [20, -, 20, -], and a point
// only one series has reads as 0 in the other.
func TestComparisonAndLogicalOperators(t *testing.T) {
	f := &fixedFetcher{series: map[string]metricmath.Series{
		"A": {Timestamps: at(0, 1, 2, 3), Values: []float64{30, 20, 0, 0}},
		"B": {Timestamps: at(0, 2), Values: []float64{20, 20}},
	}}

	tests := []struct {
		expr string
		want []float64
	}{
		{"m1 < m2", []float64{0, 0, 1, 0}},
		{"m1 >= 30", []float64{1, 0, 0, 0}},
		{"m1 > 15 AND m2 > 15", []float64{1, 0, 0, 0}},
		{"m1 > 15 && m2 > 15", []float64{1, 0, 0, 0}},
		{"m1 > 25 OR m2 > 15", []float64{1, 0, 1, 0}},
		{"m1 > 25 || m2 > 15", []float64{1, 0, 1, 0}},
		{"m1 == 0", []float64{0, 0, 1, 1}},
		{"m1 != 0", []float64{1, 1, 0, 0}},
		{"m1 <= 20", []float64{0, 1, 1, 1}},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			s := resolveExpr(t, f, tc.expr)
			assert.Equal(t, tc.want, s.Values)
		})
	}
}

// TestFILL checks each FILL filler over a five-minute range with m1 at
// minutes 1 and 3: [-, 10, -, 30, -].
func TestFILL(t *testing.T) {
	f := &fixedFetcher{series: map[string]metricmath.Series{
		"A": {Timestamps: at(1, 3), Values: []float64{10, 30}},
		"B": {Timestamps: at(0, 2), Values: []float64{7, 8}},
	}}

	tests := []struct {
		name  string
		expr  string
		want  []float64
		stamp []time.Time
	}{
		{"scalar", "FILL(m1, 0)", []float64{0, 10, 0, 30, 0}, at(0, 1, 2, 3, 4)},
		{"scalar expression", "FILL(m1, 2*3)", []float64{6, 10, 6, 30, 6}, at(0, 1, 2, 3, 4)},
		{"metric", "FILL(m1, m2)", []float64{7, 10, 8, 30}, at(0, 1, 2, 3)},
		{"REPEAT", "FILL(m1, REPEAT)", []float64{10, 10, 30, 30}, at(1, 2, 3, 4)},
		{"LINEAR", "FILL(m1, LINEAR)", []float64{10, 20, 30}, at(1, 2, 3)},
		{"inside arithmetic", "FILL(m1, 0) + 1", []float64{1, 11, 1, 31, 1}, at(0, 1, 2, 3, 4)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := resolveExpr(t, f, tc.expr)
			assert.Equal(t, tc.want, s.Values)
			assert.Equal(t, tc.stamp, s.Timestamps)
		})
	}
}

// TestFILLNoData fills every period of the range when the metric has none.
func TestFILLNoData(t *testing.T) {
	s := resolveExpr(t, &fixedFetcher{}, "FILL(m1, 5)")
	assert.Equal(t, []float64{5, 5, 5, 5, 5}, s.Values)
}

// TestFILLWithoutRange leaves the input as is when the range is unknown.
func TestFILLWithoutRange(t *testing.T) {
	f := &fixedFetcher{series: map[string]metricmath.Series{"A": {Timestamps: at(1), Values: []float64{10}}}}
	queries := []driver.MetricDataQuery{
		{ID: "m1", MetricStat: stat("A", 60), ReturnData: boolPtr(false)},
		{ID: "e1", Expression: "FILL(m1, 0)"},
	}

	s, err := metricmath.New(queries, f.fetch).Resolve("e1")
	require.NoError(t, err)
	assert.Equal(t, []float64{10}, s.Values)
}

func TestFunctionReferences(t *testing.T) {
	tests := []struct {
		expr string
		want []string
		ok   bool
	}{
		{"IF(m1 > 5, m2, m3)", []string{"m1", "m2", "m3"}, true},
		{"FILL(m1, REPEAT)", []string{"m1"}, true},
		{"FILL(m1, LINEAR) * e2", []string{"m1", "e2"}, true},
		{"SEARCH('{App} MetricName=\"Latency\"', 'Average', 300)", nil, true},
		{"FILL(m1, BOGUS)", nil, false},
		{"m1 + REPEAT", nil, false},
		{"IF(m1)", nil, false},
		{"FILL(m1)", nil, false},
		{"NOPE(m1)", nil, false},
		{"SEARCH('{App}')", nil, false},
		{"SEARCH(' :aws.AccountId = 1 ', 'Sum')", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			got, ok := metricmath.References(tc.expr)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// searchMetrics is what the SEARCH tests can find.
func searchMetrics() ([]driver.MetricIdentifier, error) {
	return []driver.MetricIdentifier{
		{Namespace: "AWS/EC2", MetricName: "CPUUtilization", Dimensions: map[string]string{"InstanceId": "i-1"}},
		{Namespace: "AWS/EC2", MetricName: "CPUUtilization", Dimensions: map[string]string{"InstanceId": "i-2"}},
		{Namespace: "AWS/EC2", MetricName: "CPUUtilization", Dimensions: map[string]string{"InstanceType": "t2.micro"}},
		{Namespace: "AWS/EC2", MetricName: "NetworkIn", Dimensions: map[string]string{"InstanceId": "i-1"}},
		{Namespace: "MyApp", MetricName: "CustomCount1"},
		{Namespace: "MyApp", MetricName: "Network-Errors-2"},
		{Namespace: "MyApp", MetricName: "SDBFailure"},
	}, nil
}

// searchLabels runs expr as an entry with Period 300 and returns the labels
// of the series it finds and the metric reads it made.
func searchLabels(t *testing.T, expr string) ([]string, []driver.MetricStat) {
	t.Helper()

	var fetched []driver.MetricStat

	fetch := func(ms *driver.MetricStat, p int) (metricmath.Series, error) {
		read := *ms
		read.Period = p
		fetched = append(fetched, read)

		return metricmath.Series{Timestamps: at(0), Values: []float64{1}}, nil
	}

	queries := []driver.MetricDataQuery{{ID: "e1", Expression: expr, Period: 300}}

	got, err := metricmath.New(queries, fetch).WithSearch(searchMetrics).ResolveAll("e1")
	require.NoError(t, err)

	labels := make([]string, 0, len(got))
	for _, l := range got {
		labels = append(labels, l.Label)
	}

	return labels, fetched
}

// TestSEARCH checks the matching rules of the search expression syntax
// page: the metric schema, partial and exact matches, designators and the
// boolean operators.
func TestSEARCH(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want []string
	}{
		{"schema and designator", `SEARCH('{AWS/EC2,InstanceId} MetricName="CPUUtilization"', 'Average')`,
			[]string{"i-1 CPUUtilization", "i-2 CPUUtilization"}},
		{"schema is the exact dimension set", `SEARCH('{AWS/EC2,InstanceType} MetricName="CPUUtilization"', 'Average')`,
			[]string{"t2.micro CPUUtilization"}},
		{"schema only", `SEARCH('{MyApp}', 'Sum')`,
			[]string{"CustomCount1", "Network-Errors-2", "SDBFailure"}},
		{"schema with space separator", `SEARCH('{AWS/EC2 InstanceType}', 'Sum')`, []string{"t2.micro CPUUtilization"}},
		{"designator partial", `SEARCH('InstanceType=micro', 'Average')`, []string{"t2.micro CPUUtilization"}},
		{"designator exact", `SEARCH('InstanceType="t2.micro"', 'Average')`, []string{"t2.micro CPUUtilization"}},
		{"partial token any case", `SEARCH('count', 'Sum')`, []string{"CustomCount1"}},
		{"partial token upper", `SEARCH('COUNT', 'Sum')`, []string{"CustomCount1"}},
		{"mixed case is split", `SEARCH('couNT', 'Sum')`, []string{}},
		{"composite token is case sensitive", `SEARCH('CustomCount', 'Sum')`, []string{"CustomCount1"}},
		{"composite with digit", `SEARCH('Count1', 'Sum')`, []string{"CustomCount1"}},
		{"lowercase composite misses", `SEARCH('customcount', 'Sum')`, []string{}},
		{"lowercase composite with digit misses", `SEARCH('count1', 'Sum')`, []string{}},
		{"acronym token", `SEARCH('sdb', 'Sum')`, []string{"SDBFailure"}},
		{"whole token", `SEARCH('sdbfailure', 'Sum')`, []string{"SDBFailure"}},
		{"delimited words", `SEARCH('network/errors', 'Sum')`, []string{"Network-Errors-2"}},
		{"underscore words", `SEARCH('Network_Errors', 'Sum')`, []string{"Network-Errors-2"}},
		{"exact match", `SEARCH(' "CustomCount1" ', 'Sum')`, []string{"CustomCount1"}},
		{"exact match is case sensitive", `SEARCH(' "customcount1" ', 'Sum')`, []string{}},
		{"exact match is whole", `SEARCH(' "Custom" ', 'Sum')`, []string{}},
		{"NOT", `SEARCH('{AWS/EC2,InstanceId} MetricName="CPUUtilization" NOT i-2', 'Average')`,
			[]string{"i-1 CPUUtilization"}},
		{"NOT namespace", `SEARCH('NOT Namespace=AWS', 'Sum')`,
			[]string{"CustomCount1", "Network-Errors-2", "SDBFailure"}},
		{"OR with groups", `SEARCH('{AWS/EC2,InstanceId} MetricName="NetworkIn" OR (MetricName="CPUUtilization" AND i-2)', 'Sum')`,
			[]string{"i-2 CPUUtilization", "i-1 NetworkIn"}},
		{"implicit AND", `SEARCH('{AWS/EC2,InstanceId} i-1 CPUUtilization', 'Sum')`, []string{"i-1 CPUUtilization"}},
		{"comma after schema", `SEARCH('{AWS/EC2, InstanceId}, MetricName="NetworkIn"', 'Sum')`, []string{"i-1 NetworkIn"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			labels, _ := searchLabels(t, tc.expr)
			assert.Equal(t, tc.want, labels)
		})
	}
}

// TestSEARCHReadsEachMetric checks a found metric is read with the search's
// statistic and period, and with its exact dimensions.
func TestSEARCHReadsEachMetric(t *testing.T) {
	_, fetched := searchLabels(t, `SEARCH('{AWS/EC2,InstanceId} MetricName="CPUUtilization"', 'Maximum', 120)`)
	require.Len(t, fetched, 2)
	assert.Equal(t, "Maximum", fetched[0].Stat)
	assert.Equal(t, 120, fetched[0].Period)
	assert.Equal(t, map[string]string{"InstanceId": "i-1"}, fetched[0].Dimensions)

	// Without a period argument the search reads at the entry's Period.
	_, fetched = searchLabels(t, `SEARCH('{AWS/EC2,InstanceId} MetricName="CPUUtilization"', 'Sum')`)
	require.Len(t, fetched, 2)
	assert.Equal(t, 300, fetched[0].Period)
}

// TestFILLOverSEARCH fills each series a search returns and keeps its label.
func TestFILLOverSEARCH(t *testing.T) {
	queries := []driver.MetricDataQuery{{ID: "e1", Period: 60,
		Expression: `FILL(SEARCH('{App,Host} MetricName="Hits"', 'Sum'), 0)`}}
	fetch := func(ms *driver.MetricStat, _ int) (metricmath.Series, error) {
		if ms.Dimensions["Host"] == "a" {
			return metricmath.Series{Timestamps: at(1), Values: []float64{4}}, nil
		}

		return metricmath.Series{Timestamps: at(2), Values: []float64{9}}, nil
	}

	ev := metricmath.New(queries, fetch).WithRange(base, base.Add(3*time.Minute)).
		WithSearch(func() ([]driver.MetricIdentifier, error) {
			return []driver.MetricIdentifier{
				{Namespace: "App", MetricName: "Hits", Dimensions: map[string]string{"Host": "a"}},
				{Namespace: "App", MetricName: "Hits", Dimensions: map[string]string{"Host": "b"}},
			}, nil
		})

	got, err := ev.ResolveAll("e1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "a Hits", got[0].Label)
	assert.Equal(t, []float64{0, 4, 0}, got[0].Values)
	assert.Equal(t, "b Hits", got[1].Label)
	assert.Equal(t, []float64{0, 0, 9}, got[1].Values)

	// An expression that returns several series has no single-series value.
	s, err := ev.Resolve("e1")
	require.NoError(t, err)
	assert.Empty(t, s.Values)
}
