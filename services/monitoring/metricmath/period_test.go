package metricmath_test

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

// evalTimeout bounds one evaluation, so a FILL grid that never ends fails the
// test instead of hanging the run.
const evalTimeout = 5 * time.Second

// resolveWithin resolves id on ev in a goroutine and fails the test when it
// does not return within evalTimeout.
func resolveWithin(t *testing.T, ev *metricmath.Evaluator, id string) []metricmath.Labeled {
	t.Helper()

	type outcome struct {
		got []metricmath.Labeled
		err error
	}

	done := make(chan outcome, 1)

	go func() {
		got, err := ev.ResolveAll(id)
		done <- outcome{got: got, err: err}
	}()

	select {
	case o := <-done:
		require.NoError(t, o.err)

		return o.got
	case <-time.After(evalTimeout):
		t.Fatalf("resolving %s did not finish within %s", id, evalTimeout)

		return nil
	}
}

func searchOneHost() ([]driver.MetricIdentifier, error) {
	return []driver.MetricIdentifier{
		{Namespace: "Rev/App", MetricName: "Hits", Dimensions: map[string]string{"InstanceId": "i-1"}},
	}, nil
}

func TestValidPeriod(t *testing.T) {
	for _, p := range []int{1, 5, 10, 20, 30, 60, 120, 300, 3600, 86400, 2147483640} {
		assert.True(t, metricmath.ValidPeriod(p), "period %d", p)
	}

	for _, p := range []int{0, -1, -60, 2, 15, 45, 61, 90, 2147483700, 9223372037, 1 << 55, math.MaxInt64} {
		assert.False(t, metricmath.ValidPeriod(p), "period %d", p)
	}
}

// TestInvalidSearchPeriod checks the SEARCH period argument against the
// MetricStat period rule, including values past the int64 range that used to
// wrap or saturate on the float to int conversion.
func TestInvalidSearchPeriod(t *testing.T) {
	expr := func(p string) string { return `FILL(SEARCH('{Rev/App,InstanceId}','Sum',` + p + `),0)` }

	for _, p := range []string{
		"9223372037", "36028797018963968", "9223372036854775807", "9223372036854775808",
		"18446744073709551616", "2147483700", "0", "0.5", "61", "15", "60.5", "-60", "m1",
	} {
		assert.True(t, metricmath.InvalidSearchPeriod(expr(p)), "period %s", p)
	}

	for _, p := range []string{"1", "5", "10", "20", "30", "60", "300", "86400", "2147483640"} {
		assert.False(t, metricmath.InvalidSearchPeriod(expr(p)), "period %s", p)
	}

	for _, e := range []string{
		`SEARCH('{Rev/App,InstanceId}', 'Sum')`, "m1 * 2", "FILL(m1, 0)", "", "SEARCH(", "NOPE(m1)",
	} {
		assert.False(t, metricmath.InvalidSearchPeriod(e), "expression %q", e)
	}
}

// TestFILLOverSEARCHHugePeriod is the hang from the review: a SEARCH period
// whose step overflowed a Duration made the FILL loop run forever (negative
// step) or divide by zero (a step of exactly 0). Such a SEARCH now reads
// nothing and returns at once.
func TestFILLOverSEARCHHugePeriod(t *testing.T) {
	for _, p := range []string{"9223372037", "36028797018963968", "9223372036854775808"} {
		t.Run(p, func(t *testing.T) {
			fetched := 0
			fetch := func(_ *driver.MetricStat, _ int) (metricmath.Series, error) {
				fetched++

				return metricmath.Series{Timestamps: at(0), Values: []float64{1}}, nil
			}

			queries := []driver.MetricDataQuery{
				{ID: "e1", Expression: `FILL(SEARCH('{Rev/App,InstanceId}','Sum',` + p + `),0)`},
			}
			ev := metricmath.New(queries, fetch).WithRange(base, base.Add(20*time.Minute)).WithSearch(searchOneHost)

			got := resolveWithin(t, ev, "e1")
			for _, l := range got {
				assert.Empty(t, l.Values)
			}

			assert.Zero(t, fetched)
		})
	}
}

// TestFILLHugeEntryPeriod drives the FILL grid with a period that does not
// come from SEARCH: the expression's own Period, which a library caller can
// set to anything. The grid counts in seconds, so it ends for any period and
// holds just the start of the range when the period outlasts it.
func TestFILLHugeEntryPeriod(t *testing.T) {
	for _, p := range []int{9223372037, 1 << 55, 1 << 62, math.MaxInt64} {
		t.Run(strconv.Itoa(p), func(t *testing.T) {
			fetch := func(_ *driver.MetricStat, _ int) (metricmath.Series, error) {
				return metricmath.Series{Timestamps: at(3), Values: []float64{7}}, nil
			}

			queries := []driver.MetricDataQuery{
				{ID: "m1", MetricStat: &driver.MetricStat{Namespace: "App", MetricName: "A", Period: 60, Stat: "Sum"}},
				{ID: "e1", Expression: "FILL(m1, 0)", Period: p},
			}
			ev := metricmath.New(queries, fetch).WithRange(base, base.Add(20*time.Minute))

			got := resolveWithin(t, ev, "e1")
			require.Len(t, got, 1)
			assert.Equal(t, at(0, 3), got[0].Timestamps)
			assert.Equal(t, []float64{0, 7}, got[0].Values)
		})
	}
}

// TestFILLOverSEARCHArithmeticKeepsPeriod checks that arithmetic over a
// SEARCH keeps the period SEARCH read at, so a FILL around it still fills
// every period when the query sets no Period.
func TestFILLOverSEARCHArithmeticKeepsPeriod(t *testing.T) {
	fetch := func(_ *driver.MetricStat, _ int) (metricmath.Series, error) {
		return metricmath.Series{Timestamps: at(0), Values: []float64{4}}, nil
	}

	for _, expr := range []string{
		`FILL(SEARCH('{Rev/App,InstanceId}','Sum',300) * 2, 0)`,
		`FILL(2 * SEARCH('{Rev/App,InstanceId}','Sum',300), 0)`,
	} {
		t.Run(expr, func(t *testing.T) {
			queries := []driver.MetricDataQuery{{ID: "e1", Expression: expr}}
			ev := metricmath.New(queries, fetch).WithRange(base, base.Add(20*time.Minute)).WithSearch(searchOneHost)

			got := resolveWithin(t, ev, "e1")
			require.Len(t, got, 1)
			assert.Equal(t, at(0, 5, 10, 15), got[0].Timestamps)
			assert.Equal(t, []float64{8, 0, 0, 0}, got[0].Values)
		})
	}
}
