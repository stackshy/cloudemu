package eventbridge

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/eventbus/driver"
	"github.com/stackshy/cloudemu/providers/aws/cloudwatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts), fc
}

func createTestBus(t *testing.T, m *Mock, name string) {
	t.Helper()

	_, err := m.CreateEventBus(context.Background(), driver.EventBusConfig{Name: name})
	require.NoError(t, err)
}

func createTestRule(t *testing.T, m *Mock, busName, ruleName string) {
	t.Helper()

	_, err := m.PutRule(context.Background(), &driver.RuleConfig{
		Name:     ruleName,
		EventBus: busName,
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
		{
			name: "success",
			cfg:  driver.EventBusConfig{Name: "my-bus"},
		},
		{
			name:      "empty name",
			cfg:       driver.EventBusConfig{},
			expectErr: true,
		},
		{
			name: "duplicate",
			cfg:  driver.EventBusConfig{Name: "dup-bus"},
			setup: func(m *Mock) {
				createTestBus(&testing.T{}, m, "dup-bus")
			},
			expectErr: true,
		},
		{
			name: "duplicate default bus",
			cfg:  driver.EventBusConfig{Name: "default"},
			expectErr: true,
		},
		{
			name: "with tags",
			cfg:  driver.EventBusConfig{Name: "tagged-bus", Tags: map[string]string{"env": "prod"}},
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
			assert.NotEmpty(t, info.ARN)
			assert.Equal(t, activeBusState, info.State)
			assert.NotEmpty(t, info.CreatedAt)
		})
	}
}

func TestDeleteEventBus(t *testing.T) {
	tests := []struct {
		name      string
		busName   string
		setup     func(*Mock)
		expectErr bool
	}{
		{
			name:    "success",
			busName: "my-bus",
			setup: func(m *Mock) {
				createTestBus(&testing.T{}, m, "my-bus")
			},
		},
		{
			name:      "not found",
			busName:   "nonexistent",
			expectErr: true,
		},
		{
			name:      "cannot delete default",
			busName:   "default",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			if tc.setup != nil {
				tc.setup(m)
			}

			err := m.DeleteEventBus(context.Background(), tc.busName)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			_, getErr := m.GetEventBus(context.Background(), tc.busName)
			require.Error(t, getErr)
		})
	}
}

func TestGetEventBus(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	t.Run("success default bus", func(t *testing.T) {
		info, err := m.GetEventBus(ctx, "default")
		require.NoError(t, err)
		assert.Equal(t, "default", info.Name)
		assert.Equal(t, activeBusState, info.State)
		assert.NotEmpty(t, info.ARN)
	})

	createTestBus(t, m, "custom-bus")

	t.Run("success custom bus", func(t *testing.T) {
		info, err := m.GetEventBus(ctx, "custom-bus")
		require.NoError(t, err)
		assert.Equal(t, "custom-bus", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetEventBus(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListEventBuses(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	t.Run("includes default bus", func(t *testing.T) {
		buses, err := m.ListEventBuses(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(buses), 1)

		found := false
		for _, b := range buses {
			if b.Name == "default" {
				found = true
			}
		}
		assert.True(t, found)
	})

	createTestBus(t, m, "bus-a")
	createTestBus(t, m, "bus-b")

	t.Run("default plus two custom", func(t *testing.T) {
		buses, err := m.ListEventBuses(ctx)
		require.NoError(t, err)
		assert.Equal(t, 3, len(buses))
	})
}

func TestPutRule(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.RuleConfig
		expectErr bool
	}{
		{
			name: "success on default bus",
			cfg:  driver.RuleConfig{Name: "my-rule"},
		},
		{
			name: "success with description and pattern",
			cfg: driver.RuleConfig{
				Name:         "detailed-rule",
				Description:  "catches EC2 events",
				EventPattern: `{"source":["aws.ec2"]}`,
			},
		},
		{
			name:      "empty name",
			cfg:       driver.RuleConfig{},
			expectErr: true,
		},
		{
			name: "disabled rule",
			cfg:  driver.RuleConfig{Name: "disabled-rule", State: "DISABLED"},
		},
		{
			name:      "bus not found",
			cfg:       driver.RuleConfig{Name: "orphan-rule", EventBus: "nonexistent"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			rule, err := m.PutRule(context.Background(), &tc.cfg)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.cfg.Name, rule.Name)
			assert.NotEmpty(t, rule.CreatedAt)

			if tc.cfg.State == "DISABLED" {
				assert.Equal(t, "DISABLED", rule.State)
			} else {
				assert.Equal(t, defaultRuleState, rule.State)
			}
		})
	}
}

func TestPutRuleUpdate(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:         "my-rule",
		EventPattern: `{"source":["aws.ec2"]}`,
	})
	require.NoError(t, err)

	err = m.PutTargets(ctx, "", "my-rule", []driver.Target{
		{ID: "t1", ARN: "arn:aws:lambda:us-east-1:123:function:my-fn"},
	})
	require.NoError(t, err)

	updated, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:         "my-rule",
		EventPattern: `{"source":["aws.s3"]}`,
	})
	require.NoError(t, err)
	assert.Equal(t, `{"source":["aws.s3"]}`, updated.EventPattern)
	assert.Equal(t, 1, len(updated.Targets))
}

func TestDeleteRule(t *testing.T) {
	tests := []struct {
		name      string
		bus       string
		ruleName  string
		setup     func(*Mock)
		expectErr bool
	}{
		{
			name:     "success",
			bus:      "",
			ruleName: "my-rule",
			setup: func(m *Mock) {
				createTestRule(&testing.T{}, m, "", "my-rule")
			},
		},
		{
			name:      "not found",
			bus:       "",
			ruleName:  "nonexistent",
			expectErr: true,
		},
		{
			name:      "bus not found",
			bus:       "nonexistent",
			ruleName:  "some-rule",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			if tc.setup != nil {
				tc.setup(m)
			}

			err := m.DeleteRule(context.Background(), tc.bus, tc.ruleName)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			_, getErr := m.GetRule(context.Background(), tc.bus, tc.ruleName)
			require.Error(t, getErr)
		})
	}
}

func TestGetRule(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRule(t, m, "", "my-rule")

	t.Run("success", func(t *testing.T) {
		rule, err := m.GetRule(ctx, "", "my-rule")
		require.NoError(t, err)
		assert.Equal(t, "my-rule", rule.Name)
		assert.Equal(t, "default", rule.EventBus)
		assert.Equal(t, defaultRuleState, rule.State)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetRule(ctx, "", "nonexistent")
		require.Error(t, err)
	})

	t.Run("bus not found", func(t *testing.T) {
		_, err := m.GetRule(ctx, "nonexistent", "my-rule")
		require.Error(t, err)
	})
}

func TestListRules(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		rules, err := m.ListRules(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, 0, len(rules))
	})

	createTestRule(t, m, "", "rule-a")
	createTestRule(t, m, "", "rule-b")

	t.Run("two rules", func(t *testing.T) {
		rules, err := m.ListRules(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, 2, len(rules))
	})

	t.Run("bus not found", func(t *testing.T) {
		_, err := m.ListRules(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestEnableDisableRule(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.PutRule(ctx, &driver.RuleConfig{Name: "toggle-rule"})
	require.NoError(t, err)

	t.Run("disable rule", func(t *testing.T) {
		disableErr := m.DisableRule(ctx, "", "toggle-rule")
		require.NoError(t, disableErr)

		rule, getErr := m.GetRule(ctx, "", "toggle-rule")
		require.NoError(t, getErr)
		assert.Equal(t, "DISABLED", rule.State)
	})

	t.Run("enable rule", func(t *testing.T) {
		enableErr := m.EnableRule(ctx, "", "toggle-rule")
		require.NoError(t, enableErr)

		rule, getErr := m.GetRule(ctx, "", "toggle-rule")
		require.NoError(t, getErr)
		assert.Equal(t, defaultRuleState, rule.State)
	})

	t.Run("disable nonexistent rule", func(t *testing.T) {
		disableErr := m.DisableRule(ctx, "", "nonexistent")
		require.Error(t, disableErr)
	})

	t.Run("enable nonexistent rule", func(t *testing.T) {
		enableErr := m.EnableRule(ctx, "", "nonexistent")
		require.Error(t, enableErr)
	})

	t.Run("disable on nonexistent bus", func(t *testing.T) {
		disableErr := m.DisableRule(ctx, "nonexistent", "toggle-rule")
		require.Error(t, disableErr)
	})
}

func TestPutTargets(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRule(t, m, "", "my-rule")

	t.Run("add targets", func(t *testing.T) {
		err := m.PutTargets(ctx, "", "my-rule", []driver.Target{
			{ID: "t1", ARN: "arn:aws:lambda:us-east-1:123:function:fn1"},
			{ID: "t2", ARN: "arn:aws:sqs:us-east-1:123:my-queue"},
		})
		require.NoError(t, err)

		targets, listErr := m.ListTargets(ctx, "", "my-rule")
		require.NoError(t, listErr)
		assert.Equal(t, 2, len(targets))
	})

	t.Run("add more targets", func(t *testing.T) {
		err := m.PutTargets(ctx, "", "my-rule", []driver.Target{
			{ID: "t3", ARN: "arn:aws:sns:us-east-1:123:my-topic"},
		})
		require.NoError(t, err)

		targets, listErr := m.ListTargets(ctx, "", "my-rule")
		require.NoError(t, listErr)
		assert.Equal(t, 3, len(targets))
	})

	t.Run("update existing target", func(t *testing.T) {
		err := m.PutTargets(ctx, "", "my-rule", []driver.Target{
			{ID: "t1", ARN: "arn:aws:lambda:us-east-1:123:function:fn-updated", Input: `{"key":"value"}`},
		})
		require.NoError(t, err)

		targets, listErr := m.ListTargets(ctx, "", "my-rule")
		require.NoError(t, listErr)
		assert.Equal(t, 3, len(targets))
	})

	t.Run("rule not found", func(t *testing.T) {
		err := m.PutTargets(ctx, "", "nonexistent", []driver.Target{
			{ID: "t1", ARN: "arn"},
		})
		require.Error(t, err)
	})

	t.Run("bus not found", func(t *testing.T) {
		err := m.PutTargets(ctx, "nonexistent", "my-rule", []driver.Target{
			{ID: "t1", ARN: "arn"},
		})
		require.Error(t, err)
	})
}

func TestRemoveTargets(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRule(t, m, "", "my-rule")

	err := m.PutTargets(ctx, "", "my-rule", []driver.Target{
		{ID: "t1", ARN: "arn:aws:lambda:us-east-1:123:function:fn1"},
		{ID: "t2", ARN: "arn:aws:sqs:us-east-1:123:my-queue"},
		{ID: "t3", ARN: "arn:aws:sns:us-east-1:123:my-topic"},
	})
	require.NoError(t, err)

	t.Run("remove one target", func(t *testing.T) {
		removeErr := m.RemoveTargets(ctx, "", "my-rule", []string{"t2"})
		require.NoError(t, removeErr)

		targets, listErr := m.ListTargets(ctx, "", "my-rule")
		require.NoError(t, listErr)
		assert.Equal(t, 2, len(targets))
	})

	t.Run("remove multiple targets", func(t *testing.T) {
		removeErr := m.RemoveTargets(ctx, "", "my-rule", []string{"t1", "t3"})
		require.NoError(t, removeErr)

		targets, listErr := m.ListTargets(ctx, "", "my-rule")
		require.NoError(t, listErr)
		assert.Equal(t, 0, len(targets))
	})

	t.Run("rule not found", func(t *testing.T) {
		removeErr := m.RemoveTargets(ctx, "", "nonexistent", []string{"t1"})
		require.Error(t, removeErr)
	})

	t.Run("bus not found", func(t *testing.T) {
		removeErr := m.RemoveTargets(ctx, "nonexistent", "my-rule", []string{"t1"})
		require.Error(t, removeErr)
	})
}

func TestPutEvents(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	t.Run("events published to default bus", func(t *testing.T) {
		result, err := m.PutEvents(ctx, []driver.Event{
			{Source: "my.app", DetailType: "OrderCreated", Detail: `{"orderId":"123"}`},
			{Source: "my.app", DetailType: "OrderShipped", Detail: `{"orderId":"456"}`},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, result.SuccessCount)
		assert.Equal(t, 0, result.FailCount)
		assert.Equal(t, 2, len(result.EventIDs))
	})

	t.Run("events to nonexistent bus counted as failures", func(t *testing.T) {
		result, err := m.PutEvents(ctx, []driver.Event{
			{Source: "my.app", DetailType: "Test", Detail: "{}", EventBus: "nonexistent"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, result.SuccessCount)
		assert.Equal(t, 1, result.FailCount)
	})

	t.Run("history stores events", func(t *testing.T) {
		history, err := m.GetEventHistory(ctx, "", 0)
		require.NoError(t, err)
		assert.Equal(t, 2, len(history))
	})

	t.Run("mixed success and failure", func(t *testing.T) {
		createTestBus(t, m, "custom-bus")

		result, err := m.PutEvents(ctx, []driver.Event{
			{Source: "my.app", DetailType: "OK", Detail: "{}", EventBus: "custom-bus"},
			{Source: "my.app", DetailType: "Fail", Detail: "{}", EventBus: "ghost-bus"},
		})
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
		shouldMatch  bool
	}{
		{
			name:    "match by source",
			pattern: `{"source":["aws.ec2"]}`,
			event:   driver.Event{Source: "aws.ec2", DetailType: "EC2 Instance State-change"},
			shouldMatch: true,
		},
		{
			name:    "no match by source",
			pattern: `{"source":["aws.ec2"]}`,
			event:   driver.Event{Source: "aws.s3", DetailType: "Object Created"},
			shouldMatch: false,
		},
		{
			name:    "match by detail-type",
			pattern: `{"detail-type":["OrderCreated"]}`,
			event:   driver.Event{Source: "my.app", DetailType: "OrderCreated"},
			shouldMatch: true,
		},
		{
			name:    "no match by detail-type",
			pattern: `{"detail-type":["OrderCreated"]}`,
			event:   driver.Event{Source: "my.app", DetailType: "OrderShipped"},
			shouldMatch: false,
		},
		{
			name:    "match by source and detail-type",
			pattern: `{"source":["aws.ec2"],"detail-type":["EC2 Instance State-change"]}`,
			event:   driver.Event{Source: "aws.ec2", DetailType: "EC2 Instance State-change"},
			shouldMatch: true,
		},
		{
			name:    "source matches but detail-type does not",
			pattern: `{"source":["aws.ec2"],"detail-type":["EC2 Instance State-change"]}`,
			event:   driver.Event{Source: "aws.ec2", DetailType: "Other Event"},
			shouldMatch: false,
		},
		{
			name:    "empty pattern matches all",
			pattern: "",
			event:   driver.Event{Source: "anything", DetailType: "anything"},
			shouldMatch: true,
		},
		{
			name:    "multiple sources",
			pattern: `{"source":["aws.ec2","aws.s3"]}`,
			event:   driver.Event{Source: "aws.s3", DetailType: "Object Created"},
			shouldMatch: true,
		},
		{
			name:    "invalid json pattern",
			pattern: "not json",
			event:   driver.Event{Source: "anything"},
			shouldMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesPattern(&tc.event, tc.pattern)
			assert.Equal(t, tc.shouldMatch, got)
		})
	}
}

func TestGetEventHistory(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	t.Run("empty history", func(t *testing.T) {
		history, err := m.GetEventHistory(ctx, "", 0)
		require.NoError(t, err)
		assert.Equal(t, 0, len(history))
	})

	_, err := m.PutEvents(ctx, []driver.Event{
		{Source: "app", DetailType: "Event1", Detail: "{}"},
		{Source: "app", DetailType: "Event2", Detail: "{}"},
		{Source: "app", DetailType: "Event3", Detail: "{}"},
	})
	require.NoError(t, err)

	t.Run("all events", func(t *testing.T) {
		history, err := m.GetEventHistory(ctx, "", 0)
		require.NoError(t, err)
		assert.Equal(t, 3, len(history))
	})

	t.Run("limited events", func(t *testing.T) {
		history, err := m.GetEventHistory(ctx, "", 2)
		require.NoError(t, err)
		assert.Equal(t, 2, len(history))
	})

	t.Run("limit greater than total", func(t *testing.T) {
		history, err := m.GetEventHistory(ctx, "", 100)
		require.NoError(t, err)
		assert.Equal(t, 3, len(history))
	})

	t.Run("bus not found", func(t *testing.T) {
		_, err := m.GetEventHistory(ctx, "nonexistent", 0)
		require.Error(t, err)
	})
}

func TestMatchedRules(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:         "ec2-rule",
		EventPattern: `{"source":["aws.ec2"]}`,
	})
	require.NoError(t, err)

	_, err = m.PutRule(ctx, &driver.RuleConfig{
		Name:         "s3-rule",
		EventPattern: `{"source":["aws.s3"]}`,
	})
	require.NoError(t, err)

	_, err = m.PutRule(ctx, &driver.RuleConfig{
		Name:         "catch-all-rule",
		EventPattern: "",
	})
	require.NoError(t, err)

	t.Run("ec2 event matches ec2-rule and catch-all", func(t *testing.T) {
		event := &driver.Event{Source: "aws.ec2", DetailType: "State Change"}
		matched := m.MatchedRules(event)
		assert.GreaterOrEqual(t, len(matched), 2)

		names := make([]string, 0, len(matched))
		for _, r := range matched {
			names = append(names, r.Name)
		}
		assert.Contains(t, names, "ec2-rule")
		assert.Contains(t, names, "catch-all-rule")
	})

	t.Run("s3 event matches s3-rule and catch-all", func(t *testing.T) {
		event := &driver.Event{Source: "aws.s3", DetailType: "Object Created"}
		matched := m.MatchedRules(event)
		assert.GreaterOrEqual(t, len(matched), 2)

		names := make([]string, 0, len(matched))
		for _, r := range matched {
			names = append(names, r.Name)
		}
		assert.Contains(t, names, "s3-rule")
		assert.Contains(t, names, "catch-all-rule")
	})

	t.Run("disabled rule not matched", func(t *testing.T) {
		disableErr := m.DisableRule(ctx, "", "ec2-rule")
		require.NoError(t, disableErr)

		event := &driver.Event{Source: "aws.ec2", DetailType: "State Change"}
		matched := m.MatchedRules(event)

		names := make([]string, 0, len(matched))
		for _, r := range matched {
			names = append(names, r.Name)
		}
		assert.NotContains(t, names, "ec2-rule")
	})

	t.Run("unmatched source", func(t *testing.T) {
		event := &driver.Event{Source: "custom.source", DetailType: "Custom Event"}
		matched := m.MatchedRules(event)

		names := make([]string, 0, len(matched))
		for _, r := range matched {
			names = append(names, r.Name)
		}
		assert.NotContains(t, names, "ec2-rule")
		assert.NotContains(t, names, "s3-rule")
		assert.Contains(t, names, "catch-all-rule")
	})
}

func TestMetricsEmission(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	cw := cloudwatch.New(opts)
	m := New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()

	t.Run("PutEvents emits metrics", func(t *testing.T) {
		_, err := m.PutEvents(ctx, []driver.Event{
			{Source: "my.app", DetailType: "TestEvent", Detail: "{}"},
		})
		require.NoError(t, err)

		metrics, listErr := cw.ListMetrics(ctx, "AWS/Events")
		require.NoError(t, listErr)
		assert.Contains(t, metrics, "PutEventsRequestCount")
		assert.Contains(t, metrics, "MatchedEvents")
	})
}
