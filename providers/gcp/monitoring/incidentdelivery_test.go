package monitoring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// breachAlarm creates an alert policy that fires when a "gcp"/"metric" datum
// exceeds 0 (the server-created driver alarm's shape) targeting the given
// channel refs, then pushes a breaching datum.
func breachAlarm(t *testing.T, m *Mock, name string, actions []string, value float64) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{
		Name: name, Namespace: "gcp", MetricName: "metric",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 0,
		Period: 60, EvaluationPeriods: 1, Stat: "Average",
		AlarmActions: actions,
	}))

	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: "gcp", MetricName: "metric", Value: value, Timestamp: m.opts.Clock.Now(),
	}}))
}

func createChannel(t *testing.T, m *Mock, chType, endpoint string) string {
	t.Helper()

	info, err := m.CreateNotificationChannel(context.Background(), driver.NotificationChannelConfig{
		Name: "ch", Type: chType, Endpoint: endpoint,
	})
	require.NoError(t, err)

	return info.ID
}

// TestBreachDeliversWebhookIncident covers (a): a breach POSTs the incident to a
// webhook channel's URL, and the payload carries the policy's incident.
func TestBreachDeliversWebhookIncident(t *testing.T) {
	m, _ := newTestMock()

	var (
		mu      sync.Mutex
		bodies  [][]byte
		gotType string
	)

	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		mu.Lock()
		bodies = append(bodies, body)
		gotType = r.Header.Get("Content-Type")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(recv.Close)

	id := createChannel(t, m, "webhook_tokenauth", recv.URL)
	breachAlarm(t, m, "cpu-hot", []string{id}, 1)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, bodies, 1, "webhook must receive exactly one incident POST")
	assert.Contains(t, gotType, "application/json")

	var env incidentEnvelope
	require.NoError(t, json.Unmarshal(bodies[0], &env))
	assert.Equal(t, "cpu-hot", env.Incident.PolicyName)
	assert.Equal(t, "metric", env.Incident.ConditionName)
	assert.Equal(t, "open", env.Incident.State)
	assert.NotEmpty(t, env.Incident.IncidentID)
}

// TestBreachSucceedsWhenWebhookUnreachable covers (a): an unreachable receiver
// does not fail the breach — delivery is best-effort.
func TestBreachSucceedsWhenWebhookUnreachable(t *testing.T) {
	m, _ := newTestMock()

	// A closed listener's URL is unreachable; the breach must still transition.
	recv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := recv.URL
	recv.Close()

	id := createChannel(t, m, "webhook_tokenauth", url)
	breachAlarm(t, m, "cpu-hot", []string{id}, 1)

	alarms, err := m.DescribeAlarms(context.Background(), []string{"cpu-hot"})
	require.NoError(t, err)
	require.Len(t, alarms, 1)
	assert.Equal(t, "ALARM", alarms[0].State, "breach must succeed even when the webhook is unreachable")
}

type fakePublisher struct {
	mu     sync.Mutex
	topics []string
	datas  [][]byte
}

func (f *fakePublisher) PublishIncident(_ context.Context, topic string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.topics = append(f.topics, topic)
	f.datas = append(f.datas, append([]byte(nil), data...))
}

// TestBreachPublishesPubSubIncident covers (b): a breach publishes the incident
// to a pubsub channel's topic via the wired publisher.
func TestBreachPublishesPubSubIncident(t *testing.T) {
	m, _ := newTestMock()

	pub := &fakePublisher{}
	m.SetPubSubPublisher(pub)

	topic := "projects/test-project/topics/alerts"
	id := createChannel(t, m, channelTypePubSub, topic)
	breachAlarm(t, m, "cpu-hot", []string{id}, 1)

	pub.mu.Lock()
	defer pub.mu.Unlock()
	require.Len(t, pub.topics, 1, "pubsub channel must receive exactly one incident publish")
	assert.Equal(t, topic, pub.topics[0])

	var env incidentEnvelope
	require.NoError(t, json.Unmarshal(pub.datas[0], &env))
	assert.Equal(t, "cpu-hot", env.Incident.PolicyName)
	assert.Equal(t, "open", env.Incident.State)
}

// TestBreachEmailChannelRecordOnly covers (c): an email (or other) channel is
// fired and recorded (state + history) but performs no external delivery and
// does not crash.
func TestBreachEmailChannelRecordOnly(t *testing.T) {
	m, _ := newTestMock()

	pub := &fakePublisher{}
	m.SetPubSubPublisher(pub)

	id := createChannel(t, m, "email", "ops@example.com")
	breachAlarm(t, m, "cpu-hot", []string{id}, 1)

	ctx := context.Background()
	alarms, err := m.DescribeAlarms(ctx, []string{"cpu-hot"})
	require.NoError(t, err)
	assert.Equal(t, "ALARM", alarms[0].State)

	hist, err := m.GetAlarmHistory(ctx, "cpu-hot", 0)
	require.NoError(t, err)
	assert.NotEmpty(t, hist, "firing must still be recorded for an email channel")

	pub.mu.Lock()
	defer pub.mu.Unlock()
	assert.Empty(t, pub.topics, "email channel must not publish to pubsub")
}

// TestRecoveryDeliversClosedIncident covers open+close delivery: a breach then a
// recovery each deliver, the second carrying a closed incident — and the state /
// history transitions are unchanged (no regression).
func TestRecoveryDeliversClosedIncident(t *testing.T) {
	m, clk := newTestMock()

	pub := &fakePublisher{}
	m.SetPubSubPublisher(pub)

	ctx := context.Background()
	topic := "projects/test-project/topics/alerts"
	id := createChannel(t, m, channelTypePubSub, topic)

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{
		Name: "cpu-hot", Namespace: "gcp", MetricName: "metric",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 0,
		Period: 60, EvaluationPeriods: 1, Stat: "Average",
		AlarmActions: []string{id},
	}))

	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: "gcp", MetricName: "metric", Value: 1, Timestamp: clk.Now(),
	}}))

	clk.Advance(60e9) // 60s
	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: "gcp", MetricName: "metric", Value: 0, Timestamp: clk.Now(),
	}}))

	pub.mu.Lock()
	states := make([]string, 0, len(pub.datas))
	for _, d := range pub.datas {
		var env incidentEnvelope
		require.NoError(t, json.Unmarshal(d, &env))
		states = append(states, env.Incident.State)
	}
	pub.mu.Unlock()

	require.Equal(t, []string{"open", "closed"}, states, "must deliver on both incident open and close")

	hist, err := m.GetAlarmHistory(ctx, "cpu-hot", 0)
	require.NoError(t, err)
	assert.Len(t, hist, 2, "history records both transitions, unchanged by delivery")
}
