package metrics

import (
	"sync"
	"time"
)

// Collector collects in-memory metrics.
type Collector struct {
	mu      sync.RWMutex
	metrics []Metric
}

// NewCollector creates a new Collector.
func NewCollector() *Collector {
	return &Collector{}
}

// Counter increments a counter metric.
func (c *Collector) Counter(name string, value float64, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.metrics = append(c.metrics, Metric{
		Name:      name,
		Type:      CounterType,
		Value:     value,
		Labels:    labels,
		Timestamp: time.Now(),
	})
}

// Gauge records a gauge metric.
func (c *Collector) Gauge(name string, value float64, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.metrics = append(c.metrics, Metric{
		Name:      name,
		Type:      GaugeType,
		Value:     value,
		Labels:    labels,
		Timestamp: time.Now(),
	})
}

// Histogram records a duration metric.
func (c *Collector) Histogram(name string, duration time.Duration, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.metrics = append(c.metrics, Metric{
		Name:      name,
		Type:      HistogramType,
		Value:     duration.Seconds(),
		Labels:    labels,
		Timestamp: time.Now(),
	})
}

// All returns a copy of all metrics.
func (c *Collector) All() []Metric {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]Metric, len(c.metrics))
	copy(result, c.metrics)

	return result
}

// Reset clears all collected metrics.
func (c *Collector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.metrics = nil
}
