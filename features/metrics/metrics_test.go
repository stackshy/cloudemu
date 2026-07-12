package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollector_Counter(t *testing.T) {
	c := NewCollector()
	c.Counter("requests_total", 1, map[string]string{"service": "s3"})
	c.Counter("requests_total", 1, map[string]string{"service": "ec2"})

	all := c.All()
	require.Len(t, all, 2)
	assert.Equal(t, "requests_total", all[0].Name)
	assert.Equal(t, CounterType, all[0].Type)
	assert.Equal(t, float64(1), all[0].Value)
	assert.Equal(t, "s3", all[0].Labels["service"])
}

func TestCollector_Gauge(t *testing.T) {
	c := NewCollector()
	c.Gauge("cpu_usage", 75.5, map[string]string{"instance": "i-1"})

	all := c.All()
	require.Len(t, all, 1)
	assert.Equal(t, GaugeType, all[0].Type)
	assert.Equal(t, 75.5, all[0].Value)
}

func TestCollector_Histogram(t *testing.T) {
	c := NewCollector()
	c.Histogram("request_duration", 250*time.Millisecond, map[string]string{"op": "get"})

	all := c.All()
	require.Len(t, all, 1)
	assert.Equal(t, HistogramType, all[0].Type)
	assert.InDelta(t, 0.25, all[0].Value, 0.01)
	assert.Equal(t, "get", all[0].Labels["op"])
}

func TestCollector_All_ReturnsCopy(t *testing.T) {
	c := NewCollector()
	c.Counter("m1", 1, nil)

	all1 := c.All()
	all2 := c.All()

	assert.Len(t, all1, 1)
	assert.Len(t, all2, 1)

	// modifying one copy should not affect the other
	all1[0].Value = 999
	assert.NotEqual(t, all1[0].Value, all2[0].Value)
}

func TestCollector_Reset(t *testing.T) {
	c := NewCollector()
	c.Counter("m1", 1, nil)
	c.Gauge("m2", 2, nil)
	require.Len(t, c.All(), 2)

	c.Reset()
	assert.Empty(t, c.All())
}

func TestCollector_Empty(t *testing.T) {
	c := NewCollector()
	assert.Empty(t, c.All())
}

func TestCollector_NilLabels(t *testing.T) {
	c := NewCollector()
	c.Counter("m", 1, nil)

	all := c.All()
	require.Len(t, all, 1)
	assert.Nil(t, all[0].Labels)
}

func TestQuery_ByName(t *testing.T) {
	c := NewCollector()
	c.Counter("a", 1, nil)
	c.Counter("b", 2, nil)
	c.Counter("a", 3, nil)

	q := NewQuery(c).ByName("a")
	assert.Equal(t, 2, q.Count())
	assert.InDelta(t, 4.0, q.Sum(), 0.001)
}

func TestQuery_ByType(t *testing.T) {
	c := NewCollector()
	c.Counter("c1", 1, nil)
	c.Gauge("g1", 2, nil)
	c.Counter("c2", 3, nil)

	q := NewQuery(c).ByType(CounterType)
	assert.Equal(t, 2, q.Count())

	q2 := NewQuery(c).ByType(GaugeType)
	assert.Equal(t, 1, q2.Count())

	q3 := NewQuery(c).ByType(HistogramType)
	assert.Equal(t, 0, q3.Count())
}

func TestQuery_ByLabel(t *testing.T) {
	c := NewCollector()
	c.Counter("req", 1, map[string]string{"env": "prod", "service": "s3"})
	c.Counter("req", 1, map[string]string{"env": "staging", "service": "s3"})
	c.Counter("req", 1, map[string]string{"env": "prod", "service": "ec2"})

	q := NewQuery(c).ByLabel("env", "prod")
	assert.Equal(t, 2, q.Count())

	q2 := NewQuery(c).ByLabel("service", "s3")
	assert.Equal(t, 2, q2.Count())

	q3 := NewQuery(c).ByLabel("env", "dev")
	assert.Equal(t, 0, q3.Count())
}

func TestQuery_Chaining(t *testing.T) {
	c := NewCollector()
	c.Counter("req", 10, map[string]string{"env": "prod"})
	c.Gauge("cpu", 50, map[string]string{"env": "prod"})
	c.Counter("req", 20, map[string]string{"env": "staging"})

	q := NewQuery(c).ByName("req").ByLabel("env", "prod")
	assert.Equal(t, 1, q.Count())
	assert.InDelta(t, 10.0, q.Sum(), 0.001)
}

func TestQuery_Results(t *testing.T) {
	c := NewCollector()
	c.Counter("m1", 1, nil)
	c.Counter("m2", 2, nil)

	results := NewQuery(c).Results()
	assert.Len(t, results, 2)
}

func TestQuery_Sum_Empty(t *testing.T) {
	c := NewCollector()
	q := NewQuery(c)
	assert.Equal(t, float64(0), q.Sum())
}

func TestQuery_Count_Empty(t *testing.T) {
	c := NewCollector()
	q := NewQuery(c)
	assert.Equal(t, 0, q.Count())
}
