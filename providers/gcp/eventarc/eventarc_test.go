package eventarc

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/eventbus/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/providers/gcp/cloudmonitoring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	return New(opts), fc
}

func newTestMockWithMonitoring() (*Mock, *cloudmonitoring.Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	mon := cloudmonitoring.New(opts)
	m := New(opts)
	m.SetMonitoring(mon)

	return m, mon, fc
}

func createTestChannel(t *testing.T, m *Mock, name string) {
	t.Helper()

	_, err := m.CreateEventBus(context.Background(), driver.EventBusConfig{Name: name})
	require.NoError(t, err)
}

func createTestTrigger(t *testing.T, m *Mock, channel, name string) {
	t.Helper()

	_, err := m.PutRule(context.Background(), &driver.RuleConfig{
		Name:     name,
		EventBus: channel,
	})
	require.NoError(t, err)
}

func TestCreateEventBus(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.EventBusConfig
		setup     func(*Mock)
		expectErr bool
		checkFn   func(*testing.T, *driver.EventBusInfo)
	}{
		{
			name: "success",
			cfg:  driver.EventBusConfig{Name: "my-channel"},
			checkFn: func(t *testing.T, info *driver.EventBusInfo) {
				t.Helper()
				assert.Equal(t, "my-channel", info.Name)
				assert.Equal(t, activeChannelState, info.State)
				assert.Contains(t, info.ARN, "projects/test-project/locations/us-central1/channels/my-channel")
				assert.NotEmpty(t, info.CreatedAt)
			},
		},
		{
			name: "success with tags",
			cfg:  driver.EventBusConfig{Name: "tagged-channel", Tags: map[string]string{"env": "dev"}},
			checkFn: func(t *testing.T, info *driver.EventBusInfo) {
				t.Helper()
				assert.Equal(t, "dev", info.Tags["env"])
			},
		},
		{
			name:      "empty name",
			cfg:       driver.EventBusConfig{},
			expectErr: true,
		},
		{
			name: "duplicate channel",
			cfg:  driver.EventBusConfig{Name: "dup"},
			setup: func(m *Mock) {
				createTestChannel(&testing.T{}, m, "dup")
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

			if tc.checkFn != nil {
				tc.checkFn(t, info)
			}
		})
	}
}

func TestDeleteEventBus(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "to-delete")

	t.Run("success", func(t *testing.T) {
		err := m.DeleteEventBus(ctx, "to-delete")
		require.NoError(t, err)

		_, getErr := m.GetEventBus(ctx, "to-delete")
		require.Error(t, getErr)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteEventBus(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetEventBus(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "my-channel")

	t.Run("success", func(t *testing.T) {
		info, err := m.GetEventBus(ctx, "my-channel")
		require.NoError(t, err)
		assert.Equal(t, "my-channel", info.Name)
		assert.Equal(t, activeChannelState, info.State)
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

	createTestChannel(t, m, "channel-a")
	createTestChannel(t, m, "channel-b")

	t.Run("two channels", func(t *testing.T) {
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
		checkFn   func(*testing.T, *driver.Rule)
	}{
		{
			name: "success with defaults",
			cfg:  driver.RuleConfig{Name: "my-trigger", EventBus: "my-channel"},
			setup: func(m *Mock) {
				createTestChannel(&testing.T{}, m, "my-channel")
			},
			checkFn: func(t *testing.T, rule *driver.Rule) {
				t.Helper()
				assert.Equal(t, "my-trigger", rule.Name)
				assert.Equal(t, "my-channel", rule.EventBus)
				assert.Equal(t, defaultTriggerState, rule.State)
				assert.NotEmpty(t, rule.CreatedAt)
			},
		},
		{
			name: "success with event pattern",
			cfg: driver.RuleConfig{
				Name:         "pattern-trigger",
				EventBus:     "my-channel",
				EventPattern: `{"source":["my.app"]}`,
				Description:  "matches my app events",
			},
			setup: func(m *Mock) {
				createTestChannel(&testing.T{}, m, "my-channel")
			},
			checkFn: func(t *testing.T, rule *driver.Rule) {
				t.Helper()
				assert.Equal(t, `{"source":["my.app"]}`, rule.EventPattern)
				assert.Equal(t, "matches my app events", rule.Description)
			},
		},
		{
			name: "update existing trigger preserves targets",
			cfg:  driver.RuleConfig{Name: "existing-trigger", EventBus: "my-channel", Description: "updated"},
			setup: func(m *Mock) {
				createTestChannel(&testing.T{}, m, "my-channel")
				createTestTrigger(&testing.T{}, m, "my-channel", "existing-trigger")

				_ = m.PutTargets(context.Background(), "my-channel", "existing-trigger", []driver.Target{
					{ID: "t1", ARN: "arn:target:1"},
				})
			},
			checkFn: func(t *testing.T, rule *driver.Rule) {
				t.Helper()
				assert.Equal(t, "updated", rule.Description)
				assert.Equal(t, 1, len(rule.Targets))
			},
		},
		{
			name:      "empty name",
			cfg:       driver.RuleConfig{EventBus: "my-channel"},
			expectErr: true,
		},
		{
			name:      "empty channel name",
			cfg:       driver.RuleConfig{Name: "my-trigger"},
			expectErr: true,
		},
		{
			name:      "channel not found",
			cfg:       driver.RuleConfig{Name: "my-trigger", EventBus: "nonexistent"},
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

			if tc.checkFn != nil {
				tc.checkFn(t, rule)
			}
		})
	}
}

func TestDeleteRule(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "my-channel")
	createTestTrigger(t, m, "my-channel", "to-delete")

	t.Run("success", func(t *testing.T) {
		err := m.DeleteRule(ctx, "my-channel", "to-delete")
		require.NoError(t, err)

		_, getErr := m.GetRule(ctx, "my-channel", "to-delete")
		require.Error(t, getErr)
	})

	t.Run("trigger not found", func(t *testing.T) {
		err := m.DeleteRule(ctx, "my-channel", "nonexistent")
		require.Error(t, err)
	})

	t.Run("channel not found", func(t *testing.T) {
		err := m.DeleteRule(ctx, "nonexistent", "some-trigger")
		require.Error(t, err)
	})

	t.Run("empty channel name", func(t *testing.T) {
		err := m.DeleteRule(ctx, "", "some-trigger")
		require.Error(t, err)
	})
}

func TestGetRule(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "my-channel")
	createTestTrigger(t, m, "my-channel", "my-trigger")

	t.Run("success", func(t *testing.T) {
		rule, err := m.GetRule(ctx, "my-channel", "my-trigger")
		require.NoError(t, err)
		assert.Equal(t, "my-trigger", rule.Name)
		assert.Equal(t, "my-channel", rule.EventBus)
	})

	t.Run("trigger not found", func(t *testing.T) {
		_, err := m.GetRule(ctx, "my-channel", "nonexistent")
		require.Error(t, err)
	})

	t.Run("channel not found", func(t *testing.T) {
		_, err := m.GetRule(ctx, "nonexistent", "my-trigger")
		require.Error(t, err)
	})

	t.Run("empty channel name", func(t *testing.T) {
		_, err := m.GetRule(ctx, "", "my-trigger")
		require.Error(t, err)
	})
}

func TestListRules(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "my-channel")

	t.Run("empty list", func(t *testing.T) {
		rules, err := m.ListRules(ctx, "my-channel")
		require.NoError(t, err)
		assert.Equal(t, 0, len(rules))
	})

	createTestTrigger(t, m, "my-channel", "trigger-a")
	createTestTrigger(t, m, "my-channel", "trigger-b")

	t.Run("two triggers", func(t *testing.T) {
		rules, err := m.ListRules(ctx, "my-channel")
		require.NoError(t, err)
		assert.Equal(t, 2, len(rules))
	})

	t.Run("channel not found", func(t *testing.T) {
		_, err := m.ListRules(ctx, "nonexistent")
		require.Error(t, err)
	})

	t.Run("empty channel name", func(t *testing.T) {
		_, err := m.ListRules(ctx, "")
		require.Error(t, err)
	})
}

func TestEnableDisableRule(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "my-channel")
	createTestTrigger(t, m, "my-channel", "my-trigger")

	t.Run("disable trigger", func(t *testing.T) {
		err := m.DisableRule(ctx, "my-channel", "my-trigger")
		require.NoError(t, err)

		rule, getErr := m.GetRule(ctx, "my-channel", "my-trigger")
		require.NoError(t, getErr)
		assert.Equal(t, "DISABLED", rule.State)
	})

	t.Run("enable trigger", func(t *testing.T) {
		err := m.EnableRule(ctx, "my-channel", "my-trigger")
		require.NoError(t, err)

		rule, getErr := m.GetRule(ctx, "my-channel", "my-trigger")
		require.NoError(t, getErr)
		assert.Equal(t, defaultTriggerState, rule.State)
	})

	t.Run("trigger not found", func(t *testing.T) {
		err := m.EnableRule(ctx, "my-channel", "nonexistent")
		require.Error(t, err)

		err = m.DisableRule(ctx, "my-channel", "nonexistent")
		require.Error(t, err)
	})

	t.Run("channel not found", func(t *testing.T) {
		err := m.EnableRule(ctx, "nonexistent", "my-trigger")
		require.Error(t, err)
	})

	t.Run("empty channel name", func(t *testing.T) {
		err := m.EnableRule(ctx, "", "my-trigger")
		require.Error(t, err)
	})
}

func TestPutTargets(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "my-channel")
	createTestTrigger(t, m, "my-channel", "my-trigger")

	t.Run("add targets", func(t *testing.T) {
		err := m.PutTargets(ctx, "my-channel", "my-trigger", []driver.Target{
			{ID: "t1", ARN: "projects/test-project/topics/topic-1"},
			{ID: "t2", ARN: "projects/test-project/topics/topic-2"},
		})
		require.NoError(t, err)

		targets, listErr := m.ListTargets(ctx, "my-channel", "my-trigger")
		require.NoError(t, listErr)
		assert.Equal(t, 2, len(targets))
	})

	t.Run("update existing target", func(t *testing.T) {
		err := m.PutTargets(ctx, "my-channel", "my-trigger", []driver.Target{
			{ID: "t1", ARN: "projects/test-project/topics/updated-topic"},
		})
		require.NoError(t, err)

		targets, listErr := m.ListTargets(ctx, "my-channel", "my-trigger")
		require.NoError(t, listErr)
		assert.Equal(t, 2, len(targets))
	})

	t.Run("trigger not found", func(t *testing.T) {
		err := m.PutTargets(ctx, "my-channel", "nonexistent", []driver.Target{
			{ID: "t1", ARN: "arn"},
		})
		require.Error(t, err)
	})

	t.Run("channel not found", func(t *testing.T) {
		err := m.PutTargets(ctx, "nonexistent", "my-trigger", []driver.Target{
			{ID: "t1", ARN: "arn"},
		})
		require.Error(t, err)
	})
}

func TestRemoveTargets(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "my-channel")
	createTestTrigger(t, m, "my-channel", "my-trigger")

	err := m.PutTargets(ctx, "my-channel", "my-trigger", []driver.Target{
		{ID: "t1", ARN: "arn:1"},
		{ID: "t2", ARN: "arn:2"},
		{ID: "t3", ARN: "arn:3"},
	})
	require.NoError(t, err)

	t.Run("remove one target", func(t *testing.T) {
		rmErr := m.RemoveTargets(ctx, "my-channel", "my-trigger", []string{"t2"})
		require.NoError(t, rmErr)

		targets, listErr := m.ListTargets(ctx, "my-channel", "my-trigger")
		require.NoError(t, listErr)
		assert.Equal(t, 2, len(targets))
	})

	t.Run("remove multiple targets", func(t *testing.T) {
		rmErr := m.RemoveTargets(ctx, "my-channel", "my-trigger", []string{"t1", "t3"})
		require.NoError(t, rmErr)

		targets, listErr := m.ListTargets(ctx, "my-channel", "my-trigger")
		require.NoError(t, listErr)
		assert.Equal(t, 0, len(targets))
	})

	t.Run("trigger not found", func(t *testing.T) {
		rmErr := m.RemoveTargets(ctx, "my-channel", "nonexistent", []string{"t1"})
		require.Error(t, rmErr)
	})

	t.Run("channel not found", func(t *testing.T) {
		rmErr := m.RemoveTargets(ctx, "nonexistent", "my-trigger", []string{"t1"})
		require.Error(t, rmErr)
	})
}

func TestPutEvents(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "my-channel")

	t.Run("success", func(t *testing.T) {
		result, err := m.PutEvents(ctx, []driver.Event{
			{
				Source:     "my.app",
				DetailType: "OrderCreated",
				Detail:     `{"orderId":"123"}`,
				EventBus:   "my-channel",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, result.SuccessCount)
		assert.Equal(t, 0, result.FailCount)
		assert.Equal(t, 1, len(result.EventIDs))
		assert.NotEmpty(t, result.EventIDs[0])
	})

	t.Run("multiple events", func(t *testing.T) {
		result, err := m.PutEvents(ctx, []driver.Event{
			{Source: "app1", DetailType: "Event1", Detail: "{}", EventBus: "my-channel"},
			{Source: "app2", DetailType: "Event2", Detail: "{}", EventBus: "my-channel"},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, result.SuccessCount)
		assert.Equal(t, 2, len(result.EventIDs))
	})

	t.Run("missing channel name fails", func(t *testing.T) {
		result, err := m.PutEvents(ctx, []driver.Event{
			{Source: "app", DetailType: "Event", Detail: "{}"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, result.SuccessCount)
		assert.Equal(t, 1, result.FailCount)
	})

	t.Run("nonexistent channel fails", func(t *testing.T) {
		result, err := m.PutEvents(ctx, []driver.Event{
			{Source: "app", DetailType: "Event", Detail: "{}", EventBus: "nonexistent"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, result.SuccessCount)
		assert.Equal(t, 1, result.FailCount)
	})

	t.Run("mixed success and failure", func(t *testing.T) {
		result, err := m.PutEvents(ctx, []driver.Event{
			{Source: "app", DetailType: "Event", Detail: "{}", EventBus: "my-channel"},
			{Source: "app", DetailType: "Event", Detail: "{}", EventBus: "nonexistent"},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, result.SuccessCount)
		assert.Equal(t, 1, result.FailCount)
	})
}

func TestEventPatternMatching(t *testing.T) {
	tests := []struct {
		name        string
		pattern     string
		event       driver.Event
		shouldMatch bool
	}{
		{
			name:    "empty pattern matches all",
			pattern: "",
			event: driver.Event{
				Source:     "any.source",
				DetailType: "AnyType",
				EventBus:   "ch",
			},
			shouldMatch: true,
		},
		{
			name:    "source match",
			pattern: `{"source":["my.app"]}`,
			event: driver.Event{
				Source:     "my.app",
				DetailType: "OrderCreated",
				EventBus:   "ch",
			},
			shouldMatch: true,
		},
		{
			name:    "source no match",
			pattern: `{"source":["my.app"]}`,
			event: driver.Event{
				Source:     "other.app",
				DetailType: "OrderCreated",
				EventBus:   "ch",
			},
			shouldMatch: false,
		},
		{
			name:    "detail-type match",
			pattern: `{"detail-type":["OrderCreated","OrderUpdated"]}`,
			event: driver.Event{
				Source:     "any",
				DetailType: "OrderCreated",
				EventBus:   "ch",
			},
			shouldMatch: true,
		},
		{
			name:    "detail-type no match",
			pattern: `{"detail-type":["OrderCreated"]}`,
			event: driver.Event{
				Source:     "any",
				DetailType: "OrderDeleted",
				EventBus:   "ch",
			},
			shouldMatch: false,
		},
		{
			name:    "source and detail-type combined match",
			pattern: `{"source":["my.app"],"detail-type":["OrderCreated"]}`,
			event: driver.Event{
				Source:     "my.app",
				DetailType: "OrderCreated",
				EventBus:   "ch",
			},
			shouldMatch: true,
		},
		{
			name:    "source matches but detail-type does not",
			pattern: `{"source":["my.app"],"detail-type":["OrderCreated"]}`,
			event: driver.Event{
				Source:     "my.app",
				DetailType: "OrderDeleted",
				EventBus:   "ch",
			},
			shouldMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()
			ctx := context.Background()

			createTestChannel(t, m, "ch")

			_, err := m.PutRule(ctx, &driver.RuleConfig{
				Name:         "test-trigger",
				EventBus:     "ch",
				EventPattern: tc.pattern,
			})
			require.NoError(t, err)

			matched := m.MatchedRules(&tc.event)

			if tc.shouldMatch {
				assert.Greater(t, len(matched), 0)
			} else {
				assert.Equal(t, 0, len(matched))
			}
		})
	}
}

func TestEventPatternMatchingDisabledRule(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "ch")

	_, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:         "disabled-trigger",
		EventBus:     "ch",
		EventPattern: `{"source":["my.app"]}`,
	})
	require.NoError(t, err)

	err = m.DisableRule(ctx, "ch", "disabled-trigger")
	require.NoError(t, err)

	event := driver.Event{Source: "my.app", DetailType: "Test", EventBus: "ch"}
	matched := m.MatchedRules(&event)
	assert.Equal(t, 0, len(matched))
}

func TestGetEventHistory(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestChannel(t, m, "my-channel")

	t.Run("empty history", func(t *testing.T) {
		history, err := m.GetEventHistory(ctx, "my-channel", 10)
		require.NoError(t, err)
		assert.Equal(t, 0, len(history))
	})

	t.Run("returns published events", func(t *testing.T) {
		_, err := m.PutEvents(ctx, []driver.Event{
			{Source: "app", DetailType: "Event1", Detail: `{"k":"v1"}`, EventBus: "my-channel"},
			{Source: "app", DetailType: "Event2", Detail: `{"k":"v2"}`, EventBus: "my-channel"},
			{Source: "app", DetailType: "Event3", Detail: `{"k":"v3"}`, EventBus: "my-channel"},
		})
		require.NoError(t, err)

		history, getErr := m.GetEventHistory(ctx, "my-channel", 0)
		require.NoError(t, getErr)
		assert.Equal(t, 3, len(history))
	})

	t.Run("limit returns most recent", func(t *testing.T) {
		history, err := m.GetEventHistory(ctx, "my-channel", 2)
		require.NoError(t, err)
		assert.Equal(t, 2, len(history))
	})

	t.Run("channel not found", func(t *testing.T) {
		_, err := m.GetEventHistory(ctx, "nonexistent", 10)
		require.Error(t, err)
	})

	t.Run("empty channel name", func(t *testing.T) {
		_, err := m.GetEventHistory(ctx, "", 10)
		require.Error(t, err)
	})
}

func TestMetricsEmission(t *testing.T) {
	m, mon, _ := newTestMockWithMonitoring()
	ctx := context.Background()

	createTestChannel(t, m, "my-channel")

	t.Run("put events emits event_count metric", func(t *testing.T) {
		_, err := m.PutEvents(ctx, []driver.Event{
			{Source: "app", DetailType: "Test", Detail: "{}", EventBus: "my-channel"},
			{Source: "app", DetailType: "Test2", Detail: "{}", EventBus: "my-channel"},
		})
		require.NoError(t, err)

		metrics, getErr := mon.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  "eventarc.googleapis.com",
			MetricName: "event_count",
			Dimensions: map[string]string{"channel_name": "my-channel"},
			StartTime:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Period:     60,
			Stat:       "Sum",
		})
		require.NoError(t, getErr)
		assert.Greater(t, len(metrics.Values), 0)
	})

	t.Run("matched events emit matched_event_count metric", func(t *testing.T) {
		_, err := m.PutRule(ctx, &driver.RuleConfig{
			Name:         "match-trigger",
			EventBus:     "my-channel",
			EventPattern: `{"source":["matched.app"]}`,
		})
		require.NoError(t, err)

		_, err = m.PutEvents(ctx, []driver.Event{
			{Source: "matched.app", DetailType: "Test", Detail: "{}", EventBus: "my-channel"},
		})
		require.NoError(t, err)

		metrics, getErr := mon.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace:  "eventarc.googleapis.com",
			MetricName: "matched_event_count",
			Dimensions: map[string]string{"channel_name": "my-channel"},
			StartTime:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Period:     60,
			Stat:       "Sum",
		})
		require.NoError(t, getErr)
		assert.Greater(t, len(metrics.Values), 0)
	})
}
