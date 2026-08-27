package eventarc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	crdriver "github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFunctionInvoker records the invocations a dispatch drove.
type fakeFunctionInvoker struct {
	mu    sync.Mutex
	calls []sdrv.InvokeInput
}

func (f *fakeFunctionInvoker) Invoke(_ context.Context, input sdrv.InvokeInput) (*sdrv.InvokeOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, input)

	return &sdrv.InvokeOutput{StatusCode: 200}, nil
}

func (f *fakeFunctionInvoker) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

// fakeCloudRunResolver returns a fixed service (or a not-found error).
type fakeCloudRunResolver struct {
	svc *crdriver.Service
}

func (f *fakeCloudRunResolver) GetService(_ context.Context, name string) (*crdriver.Service, error) {
	if f.svc == nil || lastSegment(name) != f.svc.Name {
		return nil, assert.AnError
	}

	return f.svc, nil
}

const pubsubEventType = "google.cloud.pubsub.topic.v1.messagePublished"

func typeFilterPattern(t *testing.T, eventType string) string {
	t.Helper()

	b, err := json.Marshal([]eventFilter{{Attribute: "type", Value: eventType}})
	require.NoError(t, err)

	return string(b)
}

func setTrigger(t *testing.T, m *Mock, channel, name, pattern string, dest *triggerDestination) {
	t.Helper()

	_, err := m.PutRule(context.Background(), &driver.RuleConfig{
		Name:         name,
		EventBus:     channel,
		EventPattern: pattern,
	})
	require.NoError(t, err)

	if dest != nil {
		input, err := json.Marshal(dest)
		require.NoError(t, err)

		err = m.PutTargets(context.Background(), channel, name, []driver.Target{
			{ID: destinationTargetID, Input: string(input)},
		})
		require.NoError(t, err)
	}
}

func TestDispatchToCloudFunctionDestination(t *testing.T) {
	m, _ := newTestMock()
	fn := &fakeFunctionInvoker{}
	m.SetFunctionInvoker(fn)

	createTestChannel(t, m, "eventarc-us-central1")
	setTrigger(t, m, "eventarc-us-central1", "trg", typeFilterPattern(t, pubsubEventType),
		&triggerDestination{CloudFunction: "projects/p/locations/l/functions/my-fn"})

	_, err := m.PutEvents(context.Background(), []driver.Event{{
		EventBus:   "eventarc-us-central1",
		Source:     "//pubsub.googleapis.com/projects/p/topics/t",
		DetailType: pubsubEventType,
		Detail:     `{"message":{"data":"aGVsbG8="}}`,
	}})
	require.NoError(t, err)

	require.Equal(t, 1, fn.count())
	call := fn.calls[0]
	assert.Equal(t, "my-fn", call.FunctionName)
	assert.Equal(t, "Event", call.InvokeType)

	var env cloudEventEnvelope
	require.NoError(t, json.Unmarshal(call.Payload, &env))
	assert.Equal(t, cloudEventSpecVersion, env.SpecVersion)
	assert.Equal(t, pubsubEventType, env.Type)
	assert.NotEmpty(t, env.ID)
	assert.JSONEq(t, `{"message":{"data":"aGVsbG8="}}`, string(env.Data))
}

func TestDispatchSkipsNonMatchingEvent(t *testing.T) {
	m, _ := newTestMock()
	fn := &fakeFunctionInvoker{}
	m.SetFunctionInvoker(fn)

	createTestChannel(t, m, "eventarc-us-central1")
	setTrigger(t, m, "eventarc-us-central1", "trg", typeFilterPattern(t, pubsubEventType),
		&triggerDestination{CloudFunction: "projects/p/locations/l/functions/my-fn"})

	_, err := m.PutEvents(context.Background(), []driver.Event{{
		EventBus:   "eventarc-us-central1",
		DetailType: "google.cloud.storage.object.v1.finalized",
	}})
	require.NoError(t, err)

	assert.Equal(t, 0, fn.count())
}

func TestDispatchDisabledTriggerNotInvoked(t *testing.T) {
	m, _ := newTestMock()
	fn := &fakeFunctionInvoker{}
	m.SetFunctionInvoker(fn)

	createTestChannel(t, m, "eventarc-us-central1")
	setTrigger(t, m, "eventarc-us-central1", "trg", typeFilterPattern(t, pubsubEventType),
		&triggerDestination{CloudFunction: "projects/p/locations/l/functions/my-fn"})
	require.NoError(t, m.DisableRule(context.Background(), "eventarc-us-central1", "trg"))

	_, err := m.PutEvents(context.Background(), []driver.Event{{
		EventBus:   "eventarc-us-central1",
		DetailType: pubsubEventType,
	}})
	require.NoError(t, err)

	assert.Equal(t, 0, fn.count())
}

func TestDispatchToCloudRunDestination(t *testing.T) {
	var (
		mu       sync.Mutex
		gotBody  []byte
		gotPath  string
		received bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()
		received = true
		gotBody = body
		gotPath = r.URL.Path
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m, _ := newTestMock()
	m.SetCloudRunInvoker(&fakeCloudRunResolver{svc: &crdriver.Service{Name: "my-svc", URI: srv.URL}})

	createTestChannel(t, m, "eventarc-us-central1")
	setTrigger(t, m, "eventarc-us-central1", "trg", typeFilterPattern(t, pubsubEventType),
		&triggerDestination{CloudRun: &cloudRunDestination{Service: "my-svc", Path: "/events"}})

	_, err := m.PutEvents(context.Background(), []driver.Event{{
		EventBus:   "eventarc-us-central1",
		DetailType: pubsubEventType,
		Detail:     `{"k":"v"}`,
	}})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.True(t, received, "cloud run endpoint should have received the event")
	assert.Equal(t, "/events", gotPath)

	var env cloudEventEnvelope
	require.NoError(t, json.Unmarshal(gotBody, &env))
	assert.Equal(t, pubsubEventType, env.Type)
}

func TestDispatchNilPeersNoPanicStillStores(t *testing.T) {
	m, _ := newTestMock() // no SetFunctionInvoker / SetCloudRunInvoker

	createTestChannel(t, m, "eventarc-us-central1")
	setTrigger(t, m, "eventarc-us-central1", "trg", typeFilterPattern(t, pubsubEventType),
		&triggerDestination{CloudFunction: "projects/p/locations/l/functions/my-fn"})

	res, err := m.PutEvents(context.Background(), []driver.Event{{
		EventBus:   "eventarc-us-central1",
		DetailType: pubsubEventType,
	}})
	require.NoError(t, err)
	assert.Equal(t, 1, res.SuccessCount)

	history, err := m.GetEventHistory(context.Background(), "eventarc-us-central1", 0)
	require.NoError(t, err)
	assert.Len(t, history, 1)
}

func TestDispatchTriggerWithoutDestinationStores(t *testing.T) {
	m, _ := newTestMock()
	fn := &fakeFunctionInvoker{}
	m.SetFunctionInvoker(fn)

	createTestChannel(t, m, "eventarc-us-central1")
	// Trigger with a matching filter but no destination target.
	setTrigger(t, m, "eventarc-us-central1", "trg", typeFilterPattern(t, pubsubEventType), nil)

	res, err := m.PutEvents(context.Background(), []driver.Event{{
		EventBus:   "eventarc-us-central1",
		DetailType: pubsubEventType,
	}})
	require.NoError(t, err)

	assert.Equal(t, 1, res.SuccessCount)
	assert.Equal(t, 0, fn.count())
}
