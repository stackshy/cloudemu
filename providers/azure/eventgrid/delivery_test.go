package eventgrid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// webhookReceiver is a tiny live HTTP server standing in for a subscriber's
// WebHook endpoint, recording every delivered batch.
type webhookReceiver struct {
	*httptest.Server

	mu    sync.Mutex
	calls [][]deliveryEvent
}

func newWebhookReceiver(t *testing.T) *webhookReceiver {
	t.Helper()

	wh := &webhookReceiver{}
	wh.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []deliveryEvent
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		wh.mu.Lock()
		wh.calls = append(wh.calls, batch)
		wh.mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(wh.Close)

	return wh
}

func (wh *webhookReceiver) events() []deliveryEvent {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	var out []deliveryEvent
	for _, batch := range wh.calls {
		out = append(out, batch...)
	}

	return out
}

func destinationJSON(endpointURL string) string {
	b, _ := json.Marshal(map[string]any{
		"destination": map[string]any{
			"endpointType": endpointTypeWebHook,
			"properties":   map[string]any{"endpointUrl": endpointURL},
		},
	})

	return string(b)
}

// TestPutEventsDeliversToWebHook is the BLOCKER regression: PutEvents must
// actually reach a subscription's WebHook destination, not just record the
// event in history.
func TestPutEventsDeliversToWebHook(t *testing.T) {
	receiver := newWebhookReceiver(t)

	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "orders")

	_, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:        "sub1",
		EventBus:    "orders",
		Description: destinationJSON(receiver.URL),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []driver.Event{{
		EventBus:   "orders",
		DetailType: "Order.Created",
		Detail:     `{"total":42}`,
		Subject:    "orders/1",
	}})
	require.NoError(t, err)

	got := receiver.events()
	require.Len(t, got, 1)
	assert.Equal(t, "Order.Created", got[0].EventType)
	assert.Equal(t, "orders/1", got[0].Subject)
	assert.JSONEq(t, `{"total":42}`, string(got[0].Data))
}

// TestPutEventsDeliveryHonorsFilter locks the MEDIUM fix: a subscription
// filter that doesn't match the event must suppress delivery, not just
// suppress the MatchedRules count.
func TestPutEventsDeliveryHonorsFilter(t *testing.T) {
	receiver := newWebhookReceiver(t)

	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "orders")

	props, _ := json.Marshal(map[string]any{
		"destination": map[string]any{
			"endpointType": endpointTypeWebHook,
			"properties":   map[string]any{"endpointUrl": receiver.URL},
		},
		"filter": map[string]any{"subjectBeginsWith": "orders/"},
	})

	_, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:        "sub1",
		EventBus:    "orders",
		Description: string(props),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []driver.Event{{
		EventBus:   "orders",
		DetailType: "Invoice.Created",
		Subject:    "invoices/1",
	}})
	require.NoError(t, err)

	assert.Empty(t, receiver.events(), "non-matching subject must not be delivered")
}

// TestPutEventsWebHookRecursionBounded locks the HIGH fix: a WebHook
// subscription whose endpoint re-publishes to the same topic is a
// self-referential loop. Delivery is synchronous, so without a cap each level
// ties up a blocked goroutine forever. The re-entrant depth guard (carried
// across the webhook round-trip via recursionguard.DepthHeader) must bound the
// chain to at most MaxDepth deliveries and let the process unwind.
func TestPutEventsWebHookRecursionBounded(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "loop")

	var deliveries atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deliveries.Add(1)

		// Seed the depth from the header exactly as the real PublishHandler
		// does, then re-publish to the same topic to close the loop.
		reCtx := context.Background()
		if d, err := strconv.Atoi(r.Header.Get(recursionguard.DepthHeader)); err == nil && d > 0 {
			reCtx = recursionguard.WithDepth(reCtx, d)
		}

		_, _ = m.PutEvents(reCtx, []driver.Event{{
			EventBus:   "loop",
			DetailType: "Loop.Tick",
			Subject:    "loop/1",
		}})

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	_, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:        "sub-loop",
		EventBus:    "loop",
		Description: destinationJSON(srv.URL),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []driver.Event{{
		EventBus:   "loop",
		DetailType: "Loop.Start",
		Subject:    "loop/0",
	}})
	require.NoError(t, err)

	got := deliveries.Load()
	assert.Positive(t, got, "the webhook must be delivered to at least once")
	assert.LessOrEqual(t, got, int32(recursionguard.MaxDepth),
		"the self-referential webhook chain must be bounded by the depth cap")
}

// TestMatchedRulesScopedToOwnTopic locks the isolation fix: a subscription on
// one topic must never match an event published to a different topic.
func TestMatchedRulesScopedToOwnTopic(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "topic-a")
	createTestTopic(t, m, "topic-b")

	_, err := m.PutRule(ctx, &driver.RuleConfig{Name: "rule-a", EventBus: "topic-a"})
	require.NoError(t, err)

	event := &driver.Event{
		Source:     "svc",
		DetailType: "Thing",
		EventBus:   "topic-b",
	}

	matched := m.MatchedRules(event)
	assert.Empty(t, matched, "a rule on topic-a must not match an event published to topic-b")
}
