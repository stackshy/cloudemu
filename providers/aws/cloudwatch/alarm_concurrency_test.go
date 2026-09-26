package cloudwatch

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Writers and readers of alarm state run at once. Run with -race.
func TestAlarmEvaluationConcurrency(t *testing.T) {
	ctx := context.Background()
	m, fc, _ := newClockMock()

	for i := range 3 {
		requireNoError(t, m.CreateAlarm(ctx, lazyAlarm(fmt.Sprintf("race-%d", i), 10, "")))
	}

	names := []string{"race-0", "race-1", "race-2"}

	var wg sync.WaitGroup

	for g := range 20 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range 20 {
				_ = m.PutMetricData(ctx, []driver.MetricDatum{
					{Namespace: lazyNS, MetricName: lazyMetric, Value: float64(i), Timestamp: fc.Now()},
				})
				_, _ = m.DescribeAlarms(ctx, nil)
				_ = m.SetAlarmActionsEnabled(ctx, names, i%2 == 0)
				_, _ = m.GetAlarmHistory(ctx, names[g%3], 0)
				_ = m.AddAlarmTags(ctx, names[g%3], map[string]string{"k": "v"})
				_, _ = m.AlarmTags(ctx, names[g%3])
				_ = m.SetAlarmState(ctx, names[i%3], stateAlarm, "race")
				_, _ = m.Snapshot(ctx, false)

				fc.Advance(time.Second)
			}
		}()
	}

	wg.Wait()
}

// reentrantPublisher calls back into the mock from inside a publish, as an
// SNS-subscribed Lambda that reads alarms and writes metrics would.
type reentrantPublisher struct {
	m     *Mock
	clock *config.FakeClock
	calls int
}

func (p *reentrantPublisher) PublishExternal(ctx context.Context, _, _ string) error {
	p.calls++
	if p.calls > 5 {
		return nil
	}

	_, _ = p.m.DescribeAlarms(ctx, nil)
	_, _ = p.m.GetAlarmHistory(ctx, "loop", 0)

	return p.m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: lazyNS, MetricName: lazyMetric, Value: 1, Timestamp: p.clock.Now()},
	})
}

// An alarm action that calls back into the same mock must not deadlock.
func TestAlarmActionReentrancy(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc)))
	pub := &reentrantPublisher{m: m, clock: fc}
	m.SetSNSPublisher(pub)

	requireNoError(t, m.CreateAlarm(ctx, lazyAlarm("loop", 60, "")))

	done := make(chan error, 1)

	go func() {
		if err := m.PutMetricData(ctx, []driver.MetricDatum{
			{Namespace: lazyNS, MetricName: lazyMetric, Value: 10, Timestamp: fc.Now()},
		}); err != nil {
			done <- err

			return
		}

		done <- m.SetAlarmState(ctx, "loop", stateOK, "forced")
	}()

	select {
	case err := <-done:
		requireNoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("alarm action calling back into the mock deadlocked")
	}

	if pub.calls == 0 {
		t.Fatal("alarm action was never published")
	}
}
