package eventgrid

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/eventbus/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"))

	return New(opts), fc
}

func createTestTopic(t *testing.T, m *Mock, name string) {
	t.Helper()

	_, err := m.CreateEventBus(context.Background(), driver.EventBusConfig{Name: name})
	require.NoError(t, err)
}

func createTestRule(t *testing.T, m *Mock, topicName, ruleName string) {
	t.Helper()

	_, err := m.PutRule(context.Background(), &driver.RuleConfig{
		Name:     ruleName,
		EventBus: topicName,
	})
	require.NoError(t, err)
}

func TestCreateEventBus(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.EventBusConfig
		setup     func(*Mock)
		expectErr bool
	}{
		{name: "success", cfg: driver.EventBusConfig{Name: "my-topic"}},
		{
			name: "success with tags",
			cfg: driver.EventBusConfig{
				Name: "tagged-topic",
				Tags: map[string]string{"env": "prod"},
			},
		},
		{name: "empty name", cfg: driver.EventBusConfig{}, expectErr: true},
		{
			name: "duplicate topic",
			cfg:  driver.EventBusConfig{Name: "dup"},
			setup: func(m *Mock) {
				createTestTopic(&testing.T{}, m, "dup")
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			if tc.setup != nil {
				tc.setup(m)
			}

			info, err := m.CreateEventBus(context.Background(), tc.cfg)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.Equal(t, tc.cfg.Name, info.Name)
			assert.Equal(t, "ACTIVE", info.State)
			assert.NotEmpty(t, info.ARN)
			assert.NotEmpty(t, info.CreatedAt)
		})
	}
}

func TestDeleteEventBus(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "to-delete")

	t.Run("success", func(t *testing.T) {
		err := m.DeleteEventBus(ctx, "to-delete")
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteEventBus(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetEventBus(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "my-topic")

	t.Run("success", func(t *testing.T) {
		info, err := m.GetEventBus(ctx, "my-topic")
		require.NoError(t, err)
		assert.Equal(t, "my-topic", info.Name)
		assert.Equal(t, "ACTIVE", info.State)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetEventBus(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListEventBuses(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	t.Run("empty list", func(t *testing.T) {
		buses, err := m.ListEventBuses(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, len(buses))
	})

	createTestTopic(t, m, "topic-a")
	createTestTopic(t, m, "topic-b")

	t.Run("two topics", func(t *testing.T) {
		buses, err := m.ListEventBuses(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, len(buses))
	})
}

func TestPutRule(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.RuleConfig
		setup     func(*Mock)
		expectErr bool
	}{
		{
			name: "success",
			cfg:  driver.RuleConfig{Name: "my-rule", EventBus: "my-topic"},
			setup: func(m *Mock) {
				createTestTopic(&testing.T{}, m, "my-topic")
			},
		},
		{
			name: "with event pattern",
			cfg: driver.RuleConfig{
				Name:         "pattern-rule",
				EventBus:     "my-topic",
				EventPattern: `{"source":["my.app"]}`,
			},
			setup: func(m *Mock) {
				createTestTopic(&testing.T{}, m, "my-topic")
			},
		},
		{
			name:      "empty name",
			cfg:       driver.RuleConfig{EventBus: "my-topic"},
			expectErr: true,
		},
		{
			name:      "empty topic",
			cfg:       driver.RuleConfig{Name: "my-rule"},
			expectErr: true,
		},
		{
			name:      "topic not found",
			cfg:       driver.RuleConfig{Name: "my-rule", EventBus: "missing"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			if tc.setup != nil {
				tc.setup(m)
			}

			rule, err := m.PutRule(context.Background(), &tc.cfg)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.Equal(t, tc.cfg.Name, rule.Name)
			assert.Equal(t, tc.cfg.EventBus, rule.EventBus)
			assert.Equal(t, "ENABLED", rule.State)
			assert.NotEmpty(t, rule.CreatedAt)
		})
	}
}

func TestDeleteRule(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "my-topic")
	createTestRule(t, m, "my-topic", "my-rule")

	t.Run("success", func(t *testing.T) {
		err := m.DeleteRule(ctx, "my-topic", "my-rule")
		require.NoError(t, err)
	})

	t.Run("rule not found", func(t *testing.T) {
		err := m.DeleteRule(ctx, "my-topic", "nonexistent")
		require.Error(t, err)
	})

	t.Run("topic not found", func(t *testing.T) {
		err := m.DeleteRule(ctx, "missing", "my-rule")
		require.Error(t, err)
	})

	t.Run("empty topic name", func(t *testing.T) {
		err := m.DeleteRule(ctx, "", "my-rule")
		require.Error(t, err)
	})
}

func TestGetRule(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "my-topic")
	createTestRule(t, m, "my-topic", "my-rule")

	t.Run("success", func(t *testing.T) {
		rule, err := m.GetRule(ctx, "my-topic", "my-rule")
		require.NoError(t, err)
		assert.Equal(t, "my-rule", rule.Name)
		assert.Equal(t, "my-topic", rule.EventBus)
	})

	t.Run("rule not found", func(t *testing.T) {
		_, err := m.GetRule(ctx, "my-topic", "nonexistent")
		require.Error(t, err)
	})

	t.Run("topic not found", func(t *testing.T) {
		_, err := m.GetRule(ctx, "missing", "my-rule")
		require.Error(t, err)
	})

	t.Run("empty topic name", func(t *testing.T) {
		_, err := m.GetRule(ctx, "", "my-rule")
		require.Error(t, err)
	})
}

func TestListRules(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "my-topic")

	t.Run("empty list", func(t *testing.T) {
		rules, err := m.ListRules(ctx, "my-topic")
		require.NoError(t, err)
		assert.Equal(t, 0, len(rules))
	})

	createTestRule(t, m, "my-topic", "rule-a")
	createTestRule(t, m, "my-topic", "rule-b")

	t.Run("two rules", func(t *testing.T) {
		rules, err := m.ListRules(ctx, "my-topic")
		require.NoError(t, err)
		assert.Equal(t, 2, len(rules))
	})

	t.Run("topic not found", func(t *testing.T) {
		_, err := m.ListRules(ctx, "missing")
		require.Error(t, err)
	})

	t.Run("empty topic name", func(t *testing.T) {
		_, err := m.ListRules(ctx, "")
		require.Error(t, err)
	})
}

func TestEnableDisableRule(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "my-topic")
	createTestRule(t, m, "my-topic", "my-rule")

	t.Run("disable rule", func(t *testing.T) {
		err := m.DisableRule(ctx, "my-topic", "my-rule")
		require.NoError(t, err)

		rule, err := m.GetRule(ctx, "my-topic", "my-rule")
		require.NoError(t, err)
		assert.Equal(t, "DISABLED", rule.State)
	})

	t.Run("enable rule", func(t *testing.T) {
		err := m.EnableRule(ctx, "my-topic", "my-rule")
		require.NoError(t, err)

		rule, err := m.GetRule(ctx, "my-topic", "my-rule")
		require.NoError(t, err)
		assert.Equal(t, "ENABLED", rule.State)
	})

	t.Run("enable nonexistent rule", func(t *testing.T) {
		err := m.EnableRule(ctx, "my-topic", "nonexistent")
		require.Error(t, err)
	})

	t.Run("disable nonexistent topic", func(t *testing.T) {
		err := m.DisableRule(ctx, "missing", "my-rule")
		require.Error(t, err)
	})

	t.Run("enable empty topic", func(t *testing.T) {
		err := m.EnableRule(ctx, "", "my-rule")
		require.Error(t, err)
	})
}

func TestPutTargets(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "my-topic")
	createTestRule(t, m, "my-topic", "my-rule")

	t.Run("add targets", func(t *testing.T) {
		targets := []driver.Target{
			{ID: "t1", ARN: "/subscriptions/123/functions/func1"},
			{ID: "t2", ARN: "/subscriptions/123/functions/func2"},
		}

		err := m.PutTargets(ctx, "my-topic", "my-rule", targets)
		require.NoError(t, err)

		listed, err := m.ListTargets(ctx, "my-topic", "my-rule")
		require.NoError(t, err)
		assert.Equal(t, 2, len(listed))
	})

	t.Run("update existing target", func(t *testing.T) {
		targets := []driver.Target{
			{ID: "t1", ARN: "/subscriptions/123/functions/updated-func"},
		}

		err := m.PutTargets(ctx, "my-topic", "my-rule", targets)
		require.NoError(t, err)

		listed, err := m.ListTargets(ctx, "my-topic", "my-rule")
		require.NoError(t, err)
		assert.Equal(t, 2, len(listed))
	})

	t.Run("rule not found", func(t *testing.T) {
		err := m.PutTargets(ctx, "my-topic", "missing", []driver.Target{{ID: "t1"}})
		require.Error(t, err)
	})

	t.Run("topic not found", func(t *testing.T) {
		err := m.PutTargets(ctx, "missing", "my-rule", []driver.Target{{ID: "t1"}})
		require.Error(t, err)
	})
}

func TestRemoveTargets(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "my-topic")
	createTestRule(t, m, "my-topic", "my-rule")

	targets := []driver.Target{
		{ID: "t1", ARN: "/subscriptions/123/functions/func1"},
		{ID: "t2", ARN: "/subscriptions/123/functions/func2"},
		{ID: "t3", ARN: "/subscriptions/123/functions/func3"},
	}

	err := m.PutTargets(ctx, "my-topic", "my-rule", targets)
	require.NoError(t, err)

	t.Run("remove one target", func(t *testing.T) {
		err := m.RemoveTargets(ctx, "my-topic", "my-rule", []string{"t2"})
		require.NoError(t, err)

		listed, err := m.ListTargets(ctx, "my-topic", "my-rule")
		require.NoError(t, err)
		assert.Equal(t, 2, len(listed))
	})

	t.Run("remove nonexistent target is idempotent", func(t *testing.T) {
		err := m.RemoveTargets(ctx, "my-topic", "my-rule", []string{"nonexistent"})
		require.NoError(t, err)
	})

	t.Run("rule not found", func(t *testing.T) {
		err := m.RemoveTargets(ctx, "my-topic", "missing", []string{"t1"})
		require.Error(t, err)
	})

	t.Run("topic not found", func(t *testing.T) {
		err := m.RemoveTargets(ctx, "missing", "my-rule", []string{"t1"})
		require.Error(t, err)
	})
}

func TestPutEvents(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m, _ := newTestMock()
		createTestTopic(t, m, "my-topic")

		events := []driver.Event{
			{
				Source:     "my.app",
				DetailType: "OrderPlaced",
				Detail:     `{"orderId":"123"}`,
				EventBus:   "my-topic",
			},
		}

		result, err := m.PutEvents(ctx, events)
		require.NoError(t, err)
		assert.Equal(t, 1, result.SuccessCount)
		assert.Equal(t, 0, result.FailCount)
		assert.Equal(t, 1, len(result.EventIDs))
	})

	t.Run("multiple events", func(t *testing.T) {
		m, _ := newTestMock()
		createTestTopic(t, m, "my-topic")

		events := []driver.Event{
			{Source: "app1", DetailType: "Event1", Detail: "{}", EventBus: "my-topic"},
			{Source: "app2", DetailType: "Event2", Detail: "{}", EventBus: "my-topic"},
		}

		result, err := m.PutEvents(ctx, events)
		require.NoError(t, err)
		assert.Equal(t, 2, result.SuccessCount)
		assert.Equal(t, 0, result.FailCount)
	})

	t.Run("topic not found increments fail count", func(t *testing.T) {
		m, _ := newTestMock()

		events := []driver.Event{
			{Source: "app", DetailType: "Evt", Detail: "{}", EventBus: "missing"},
		}

		result, err := m.PutEvents(ctx, events)
		require.NoError(t, err)
		assert.Equal(t, 0, result.SuccessCount)
		assert.Equal(t, 1, result.FailCount)
	})

	t.Run("empty event bus fails", func(t *testing.T) {
		m, _ := newTestMock()

		events := []driver.Event{
			{Source: "app", DetailType: "Evt", Detail: "{}"},
		}

		result, err := m.PutEvents(ctx, events)
		require.NoError(t, err)
		assert.Equal(t, 0, result.SuccessCount)
		assert.Equal(t, 1, result.FailCount)
	})

	t.Run("mixed success and failure", func(t *testing.T) {
		m, _ := newTestMock()
		createTestTopic(t, m, "my-topic")

		events := []driver.Event{
			{Source: "app", DetailType: "Good", Detail: "{}", EventBus: "my-topic"},
			{Source: "app", DetailType: "Bad", Detail: "{}", EventBus: "missing"},
		}

		result, err := m.PutEvents(ctx, events)
		require.NoError(t, err)
		assert.Equal(t, 1, result.SuccessCount)
		assert.Equal(t, 1, result.FailCount)
	})
}

func TestEventPatternMatching(t *testing.T) {
	tests := []struct {
		name         string
		pattern      string
		event        driver.Event
		expectMatch  bool
	}{
		{
			name:    "empty pattern matches all",
			pattern: "",
			event: driver.Event{
				Source:     "any.source",
				DetailType: "AnyType",
			},
			expectMatch: true,
		},
		{
			name:    "source match",
			pattern: `{"source":["my.app"]}`,
			event: driver.Event{
				Source:     "my.app",
				DetailType: "SomeType",
			},
			expectMatch: true,
		},
		{
			name:    "source no match",
			pattern: `{"source":["my.app"]}`,
			event: driver.Event{
				Source:     "other.app",
				DetailType: "SomeType",
			},
			expectMatch: false,
		},
		{
			name:    "detail-type match",
			pattern: `{"detail-type":["OrderPlaced"]}`,
			event: driver.Event{
				Source:     "any",
				DetailType: "OrderPlaced",
			},
			expectMatch: true,
		},
		{
			name:    "detail-type no match",
			pattern: `{"detail-type":["OrderPlaced"]}`,
			event: driver.Event{
				Source:     "any",
				DetailType: "OrderCancelled",
			},
			expectMatch: false,
		},
		{
			name:    "both source and detail-type match",
			pattern: `{"source":["my.app"],"detail-type":["OrderPlaced"]}`,
			event: driver.Event{
				Source:     "my.app",
				DetailType: "OrderPlaced",
			},
			expectMatch: true,
		},
		{
			name:    "source matches but detail-type does not",
			pattern: `{"source":["my.app"],"detail-type":["OrderPlaced"]}`,
			event: driver.Event{
				Source:     "my.app",
				DetailType: "OrderCancelled",
			},
			expectMatch: false,
		},
		{
			name:    "multiple allowed sources",
			pattern: `{"source":["app1","app2"]}`,
			event: driver.Event{
				Source:     "app2",
				DetailType: "Evt",
			},
			expectMatch: true,
		},
		{
			name:    "invalid json pattern no match",
			pattern: `{invalid`,
			event: driver.Event{
				Source: "any",
			},
			expectMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesPattern(&tc.event, tc.pattern)
			assert.Equal(t, tc.expectMatch, got)
		})
	}
}

func TestMatchedRulesIntegration(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "my-topic")

	_, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:         "order-rule",
		EventBus:     "my-topic",
		EventPattern: `{"source":["order.service"]}`,
	})
	require.NoError(t, err)

	_, err = m.PutRule(ctx, &driver.RuleConfig{
		Name:         "catch-all",
		EventBus:     "my-topic",
		EventPattern: "",
	})
	require.NoError(t, err)

	t.Run("event matches specific rule and catch-all", func(t *testing.T) {
		event := &driver.Event{
			Source:     "order.service",
			DetailType: "OrderCreated",
			EventBus:   "my-topic",
		}
		matched := m.MatchedRules(event)
		assert.Equal(t, 2, len(matched))
	})

	t.Run("event matches only catch-all", func(t *testing.T) {
		event := &driver.Event{
			Source:     "other.service",
			DetailType: "SomeEvent",
			EventBus:   "my-topic",
		}
		matched := m.MatchedRules(event)
		assert.Equal(t, 1, len(matched))
	})

	t.Run("disabled rule not matched", func(t *testing.T) {
		err := m.DisableRule(ctx, "my-topic", "catch-all")
		require.NoError(t, err)

		event := &driver.Event{
			Source:     "other.service",
			DetailType: "SomeEvent",
			EventBus:   "my-topic",
		}
		matched := m.MatchedRules(event)
		assert.Equal(t, 0, len(matched))
	})
}

func TestGetEventHistory(t *testing.T) {
	ctx := context.Background()

	t.Run("retrieves published events", func(t *testing.T) {
		m, _ := newTestMock()
		createTestTopic(t, m, "my-topic")

		events := []driver.Event{
			{Source: "app", DetailType: "Evt1", Detail: `{"k":"v1"}`, EventBus: "my-topic"},
			{Source: "app", DetailType: "Evt2", Detail: `{"k":"v2"}`, EventBus: "my-topic"},
			{Source: "app", DetailType: "Evt3", Detail: `{"k":"v3"}`, EventBus: "my-topic"},
		}

		_, err := m.PutEvents(ctx, events)
		require.NoError(t, err)

		history, err := m.GetEventHistory(ctx, "my-topic", 0)
		require.NoError(t, err)
		assert.Equal(t, 3, len(history))
	})

	t.Run("limit returns last N events", func(t *testing.T) {
		m, _ := newTestMock()
		createTestTopic(t, m, "my-topic")

		events := []driver.Event{
			{Source: "app", DetailType: "Evt1", Detail: "{}", EventBus: "my-topic"},
			{Source: "app", DetailType: "Evt2", Detail: "{}", EventBus: "my-topic"},
			{Source: "app", DetailType: "Evt3", Detail: "{}", EventBus: "my-topic"},
		}

		_, err := m.PutEvents(ctx, events)
		require.NoError(t, err)

		history, err := m.GetEventHistory(ctx, "my-topic", 2)
		require.NoError(t, err)
		assert.Equal(t, 2, len(history))
	})

	t.Run("empty history", func(t *testing.T) {
		m, _ := newTestMock()
		createTestTopic(t, m, "my-topic")

		history, err := m.GetEventHistory(ctx, "my-topic", 0)
		require.NoError(t, err)
		assert.Equal(t, 0, len(history))
	})

	t.Run("topic not found", func(t *testing.T) {
		m, _ := newTestMock()

		_, err := m.GetEventHistory(ctx, "missing", 0)
		require.Error(t, err)
	})

	t.Run("empty topic name", func(t *testing.T) {
		m, _ := newTestMock()

		_, err := m.GetEventHistory(ctx, "", 0)
		require.Error(t, err)
	})
}

// fakeMonitoring is a minimal monitoring mock for testing metric emission.
type fakeMonitoring struct {
	data []mondriver.MetricDatum
}

func (f *fakeMonitoring) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	f.data = append(f.data, data...)
	return nil
}

func (f *fakeMonitoring) GetMetricData(
	_ context.Context, _ mondriver.GetMetricInput,
) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (f *fakeMonitoring) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (f *fakeMonitoring) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (f *fakeMonitoring) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (f *fakeMonitoring) DescribeAlarms(
	_ context.Context, _ []string,
) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (f *fakeMonitoring) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func TestMetricsEmission(t *testing.T) {
	ctx := context.Background()

	t.Run("publish emits PublishedEvents and MatchedEvents", func(t *testing.T) {
		m, _ := newTestMock()
		mon := &fakeMonitoring{}
		m.SetMonitoring(mon)
		createTestTopic(t, m, "my-topic")

		events := []driver.Event{
			{Source: "app", DetailType: "Evt", Detail: "{}", EventBus: "my-topic"},
		}

		_, err := m.PutEvents(ctx, events)
		require.NoError(t, err)

		require.NotEmpty(t, mon.data)

		metricNames := make(map[string]bool)
		for _, d := range mon.data {
			metricNames[d.MetricName] = true
			assert.Equal(t, "Microsoft.EventGrid/topics", d.Namespace)
			assert.Equal(t, "my-topic", d.Dimensions["topicName"])
		}

		assert.True(t, metricNames["PublishedEvents"], "expected PublishedEvents metric")
		assert.True(t, metricNames["MatchedEvents"], "expected MatchedEvents metric")
	})

	t.Run("failed events do not emit metrics", func(t *testing.T) {
		m, _ := newTestMock()
		mon := &fakeMonitoring{}
		m.SetMonitoring(mon)

		events := []driver.Event{
			{Source: "app", DetailType: "Evt", Detail: "{}", EventBus: "missing"},
		}

		_, err := m.PutEvents(ctx, events)
		require.NoError(t, err)

		assert.Equal(t, 0, len(mon.data))
	})

	t.Run("no monitoring does not panic", func(t *testing.T) {
		m, _ := newTestMock()
		createTestTopic(t, m, "my-topic")

		events := []driver.Event{
			{Source: "app", DetailType: "Evt", Detail: "{}", EventBus: "my-topic"},
		}

		_, err := m.PutEvents(ctx, events)
		require.NoError(t, err)
	})
}
