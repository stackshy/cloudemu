package notifications_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	notifprovider "github.com/stackshy/cloudemu/v2/providers/oci/notifications"
	ocinotif "github.com/stackshy/cloudemu/v2/server/oci/notifications"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	compartment      = "ocid1.compartment.oc1..aaaaaaaatest"
	otherCompartment = "ocid1.compartment.oc1..aaaaaaaaother"
)

// Compile-time check that the OCI Notifications mock carries the capabilities
// the handler discovers by type assertion.
var _ ocinotif.Extras = (*notifprovider.Mock)(nil)

type fixture struct {
	t       *testing.T
	handler *ocinotif.Handler
	mock    *notifprovider.Mock
	work    *workrequest.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	opts := config.NewOptions(config.WithRegion("us-ashburn-1"), config.WithCompartmentID(compartment))
	mock := notifprovider.New(opts)
	work := workrequest.New(opts)

	return &fixture{t: t, handler: ocinotif.New(mock, work), mock: mock, work: work}
}

// do sends a request through the handler and returns the recorder.
func (f *fixture) do(method, target string, body any) *httptest.ResponseRecorder {
	f.t.Helper()

	var reader *bytes.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(f.t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	r := httptest.NewRequest(method, target, reader)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)

	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	out := map[string]any{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	return out
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()

	var out []map[string]any

	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	return out
}

// newTopic creates a topic over the wire and returns its OCID.
func (f *fixture) newTopic(name, compartmentID string) string {
	f.t.Helper()

	w := f.do(http.MethodPost, "/20181201/topics", map[string]any{
		"name":          name,
		"compartmentId": compartmentID,
		"description":   "topic " + name,
	})
	require.Equal(f.t, http.StatusOK, w.Code, w.Body.String())

	id, _ := decode(f.t, w)["topicId"].(string)

	return id
}

// newSubscription creates a subscription over the wire and returns its OCID
// and confirmation token.
func (f *fixture) newSubscription(topicID, endpoint string) (id, token string) {
	f.t.Helper()

	w := f.do(http.MethodPost, "/20181201/subscriptions", map[string]any{
		"topicId":       topicID,
		"compartmentId": compartment,
		"protocol":      "EMAIL",
		"endpoint":      endpoint,
	})
	require.Equal(f.t, http.StatusOK, w.Code, w.Body.String())

	body := decode(f.t, w)
	id, _ = body["id"].(string)
	token, _ = body["confirmationToken"].(string)

	return id, token
}

func TestMatches(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		expect bool
	}{
		{name: "topic collection", path: "/20181201/topics", expect: true},
		{name: "single topic", path: "/20181201/topics/ocid1.onstopic.oc1.iad.abc", expect: true},
		{name: "publish endpoint", path: "/20181201/topics/ocid1.onstopic.oc1.iad.abc/messages", expect: true},
		{
			name:   "topic action",
			path:   "/20181201/topics/ocid1.onstopic.oc1.iad.abc/actions/changeCompartment",
			expect: true,
		},
		{name: "subscription collection", path: "/20181201/subscriptions", expect: true},
		{name: "single subscription", path: "/20181201/subscriptions/ocid1.onssubscription.oc1.iad.abc", expect: true},
		{
			name:   "confirmation",
			path:   "/20181201/subscriptions/ocid1.onssubscription.oc1.iad.abc/confirmation",
			expect: true,
		},
		{
			name:   "unsubscription",
			path:   "/20181201/subscriptions/ocid1.onssubscription.oc1.iad.abc/unsubscription",
			expect: true,
		},

		{name: "another service's version prefix", path: "/20160918/vcns", expect: false},
		{name: "monitoring alarms", path: "/20180401/alarms", expect: false},
		{name: "topics under the wrong version", path: "/20180401/topics", expect: false},
		{name: "unknown collection on this version", path: "/20181201/alarms", expect: false},
		{name: "version alone", path: "/20181201", expect: false},
		{name: "work request poll", path: "/20181201/workRequests/ocid1.workrequest.oc1.iad.abc", expect: false},
		{name: "root", path: "/", expect: false},
		{
			name:   "deeper than any ONS path",
			path:   "/20181201/topics/ocid1.onstopic.oc1.iad.abc/messages/extra/parts",
			expect: false,
		},
	}

	f := newFixture(t)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			assert.Equal(t, tc.expect, f.handler.Matches(r))
		})
	}
}

func TestCreateTopicWire(t *testing.T) {
	f := newFixture(t)

	w := f.do(http.MethodPost, "/20181201/topics", map[string]any{
		"name":          "alerts",
		"compartmentId": compartment,
		"description":   "production alerts",
		"freeformTags":  map[string]string{"env": "prod"},
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	body := decode(t, w)
	assert.Contains(t, body["topicId"], "ocid1.onstopic.oc1.iad.")
	assert.Equal(t, "alerts", body["name"])
	assert.Equal(t, compartment, body["compartmentId"])
	assert.Equal(t, "production alerts", body["description"])
	assert.Equal(t, "ACTIVE", body["lifecycleState"])
	assert.NotEmpty(t, body["shortTopicId"])
	assert.NotEmpty(t, body["timeCreated"])
	assert.NotEmpty(t, body["etag"])
	assert.Equal(t, "http://example.com", body["apiEndpoint"])
	assert.Equal(t, map[string]any{"env": "prod"}, body["freeformTags"])
	assert.Equal(t, map[string]any{}, body["definedTags"])
	assert.NotEmpty(t, w.Header().Get(ocirest.HeaderRequestID))
}

func TestTopicRequestErrors(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		body       any
		expectCode int
		expectErr  string
	}{
		{
			name: "create without compartment", method: http.MethodPost, target: "/20181201/topics",
			body: map[string]any{"name": "alerts"}, expectCode: http.StatusBadRequest, expectErr: "InvalidParameter",
		},
		{
			name: "create with defined tags", method: http.MethodPost, target: "/20181201/topics",
			body: map[string]any{
				"name": "alerts", "compartmentId": compartment,
				"definedTags": map[string]any{"ns": map[string]any{"k": "v"}},
			},
			expectCode: http.StatusBadRequest, expectErr: "InvalidParameter",
		},
		{
			name: "create with an illegal name", method: http.MethodPost, target: "/20181201/topics",
			body:       map[string]any{"name": "bad name!", "compartmentId": compartment},
			expectCode: http.StatusBadRequest, expectErr: "InvalidParameter",
		},
		{
			name: "list without compartment", method: http.MethodGet, target: "/20181201/topics",
			expectCode: http.StatusBadRequest, expectErr: "InvalidParameter",
		},
		{
			name: "list with an unsupported sort key", method: http.MethodGet,
			target:     "/20181201/topics?compartmentId=" + compartment + "&sortBy=DISPLAYNAME",
			expectCode: http.StatusBadRequest, expectErr: "InvalidParameter",
		},
		{
			name: "get a missing topic", method: http.MethodGet,
			target:     "/20181201/topics/ocid1.onstopic.oc1.iad.missing",
			expectCode: http.StatusNotFound, expectErr: "NotAuthorizedOrNotFound",
		},
		{
			name: "unknown sub-resource", method: http.MethodGet,
			target:     "/20181201/topics/ocid1.onstopic.oc1.iad.abc/bananas",
			expectCode: http.StatusNotFound, expectErr: "NotAuthorizedOrNotFound",
		},
		{
			name: "verb the collection does not serve", method: http.MethodDelete, target: "/20181201/topics",
			expectCode: http.StatusMethodNotAllowed, expectErr: "MethodNotAllowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			w := f.do(tc.method, tc.target, tc.body)

			require.Equal(t, tc.expectCode, w.Code, w.Body.String())

			var body ocirest.ErrorBody

			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, tc.expectErr, body.Code)
			assert.NotEmpty(t, body.Message)
		})
	}
}

func TestListTopicsFiltersByCompartment(t *testing.T) {
	f := newFixture(t)
	f.newTopic("mine", compartment)
	f.newTopic("theirs", otherCompartment)

	w := f.do(http.MethodGet, "/20181201/topics?compartmentId="+compartment, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	list := decodeList(t, w)
	require.Len(t, list, 1)
	assert.Equal(t, "mine", list[0]["name"])

	w = f.do(http.MethodGet, "/20181201/topics?compartmentId="+compartment+"&name=nothing", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeList(t, w))
}

func TestUpdateTopic(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)

	w := f.do(http.MethodPut, "/20181201/topics/"+topicID, map[string]any{
		"description":  "new description",
		"freeformTags": map[string]string{"team": "sre"},
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	body := decode(t, w)
	assert.Equal(t, "new description", body["description"])
	assert.Equal(t, "alerts", body["name"])
	assert.Equal(t, map[string]any{"team": "sre"}, body["freeformTags"])
}

// TestDeleteTopicIsAsynchronous covers the one ONS mutation that returns a
// work request.
func TestDeleteTopicIsAsynchronous(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)

	w := f.do(http.MethodDelete, "/20181201/topics/"+topicID, nil)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	wrID := w.Header().Get(ocirest.HeaderWorkRequestID)
	require.NotEmpty(t, wrID)

	wr, ok := f.work.Get(wrID)
	require.True(t, ok)
	assert.Equal(t, "DELETE_TOPIC", wr.OperationType)
	assert.Equal(t, compartment, wr.CompartmentID)
	require.Len(t, wr.Resources, 1)
	assert.Equal(t, topicID, wr.Resources[0].Identifier)
	assert.Equal(t, workrequest.ActionDeleted, wr.Resources[0].ActionType)

	w = f.do(http.MethodGet, "/20181201/topics/"+topicID, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)

	w = f.do(http.MethodDelete, "/20181201/topics/"+topicID, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestChangeTopicCompartment(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)

	w := f.do(http.MethodPost, "/20181201/topics/"+topicID+"/actions/changeCompartment",
		map[string]any{"compartmentId": otherCompartment})
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	assert.NotEmpty(t, w.Header().Get(ocirest.HeaderWorkRequestID))

	w = f.do(http.MethodGet, "/20181201/topics?compartmentId="+otherCompartment, nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, decodeList(t, w), 1)

	w = f.do(http.MethodPost, "/20181201/topics/"+topicID+"/actions/changeCompartment", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateSubscriptionWire(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)

	w := f.do(http.MethodPost, "/20181201/subscriptions", map[string]any{
		"topicId":       topicID,
		"compartmentId": compartment,
		"protocol":      "EMAIL",
		"endpoint":      "ops@example.com",
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	body := decode(t, w)
	assert.Contains(t, body["id"], "ocid1.onssubscription.oc1.iad.")
	assert.Equal(t, topicID, body["topicId"])
	assert.Equal(t, "PENDING", body["lifecycleState"])
	assert.Equal(t, "EMAIL", body["protocol"])
	assert.Equal(t, "ops@example.com", body["endpoint"])
	assert.NotEmpty(t, body["confirmationToken"])
	assert.NotZero(t, body["createdTime"])
}

func TestSubscriptionRequestErrors(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)

	tests := []struct {
		name       string
		method     string
		target     string
		body       any
		expectCode int
	}{
		{
			name: "create without compartment", method: http.MethodPost, target: "/20181201/subscriptions",
			body:       map[string]any{"topicId": topicID, "protocol": "EMAIL", "endpoint": "a@b.c"},
			expectCode: http.StatusBadRequest,
		},
		{
			name: "create without topic", method: http.MethodPost, target: "/20181201/subscriptions",
			body:       map[string]any{"compartmentId": compartment, "protocol": "EMAIL", "endpoint": "a@b.c"},
			expectCode: http.StatusBadRequest,
		},
		{
			name: "create with an unsupported protocol", method: http.MethodPost, target: "/20181201/subscriptions",
			body: map[string]any{
				"topicId": topicID, "compartmentId": compartment, "protocol": "SQS", "endpoint": "q",
			},
			expectCode: http.StatusBadRequest,
		},
		{
			name: "list without compartment", method: http.MethodGet, target: "/20181201/subscriptions",
			expectCode: http.StatusBadRequest,
		},
		{
			name: "get a missing subscription", method: http.MethodGet,
			target:     "/20181201/subscriptions/ocid1.onssubscription.oc1.iad.missing",
			expectCode: http.StatusNotFound,
		},
		{
			name: "confirm without a token", method: http.MethodGet,
			target:     "/20181201/subscriptions/ocid1.onssubscription.oc1.iad.abc/confirmation",
			expectCode: http.StatusBadRequest,
		},
		{
			name: "unknown action", method: http.MethodPost,
			target:     "/20181201/subscriptions/ocid1.onssubscription.oc1.iad.abc/actions/explode",
			expectCode: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := f.do(tc.method, tc.target, tc.body)
			assert.Equal(t, tc.expectCode, w.Code, w.Body.String())
		})
	}
}

// TestConfirmationFlowOverTheWire walks the whole ONS lifecycle: a PENDING
// subscription receives nothing, confirmation makes it ACTIVE, and only then
// does a publish reach it.
func TestConfirmationFlowOverTheWire(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)
	subID, token := f.newSubscription(topicID, "ops@example.com")

	w := f.do(http.MethodPost, "/20181201/topics/"+topicID+"/messages",
		map[string]any{"title": "early", "body": "before confirmation"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Empty(t, f.mock.Deliveries(subID), "a PENDING subscription must receive nothing")

	w = f.do(http.MethodGet, "/20181201/subscriptions/"+subID+"/confirmation?token="+token+"&protocol=EMAIL", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	body := decode(t, w)
	assert.Equal(t, subID, body["subscriptionId"])
	assert.Equal(t, "alerts", body["topicName"])
	assert.Equal(t, "ops@example.com", body["endpoint"])
	assert.Contains(t, body["unsubscribeUrl"], "/20181201/subscriptions/"+subID+"/unsubscription?")

	w = f.do(http.MethodGet, "/20181201/subscriptions/"+subID, nil)
	require.Equal(t, http.StatusOK, w.Code)

	body = decode(t, w)
	assert.Equal(t, "ACTIVE", body["lifecycleState"])
	assert.Empty(t, body["confirmationToken"], "the token is dropped once it is spent")

	w = f.do(http.MethodPost, "/20181201/topics/"+topicID+"/messages",
		map[string]any{"title": "disk", "body": "after confirmation"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	published := decode(t, w)
	assert.NotEmpty(t, published["messageId"])
	assert.NotEmpty(t, published["timeStamp"])

	delivered := f.mock.Deliveries(subID)
	require.Len(t, delivered, 1)
	assert.Equal(t, "after confirmation", delivered[0].Body)
}

func TestConfirmWithTheWrongToken(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)
	subID, _ := f.newSubscription(topicID, "ops@example.com")

	w := f.do(http.MethodGet, "/20181201/subscriptions/"+subID+"/confirmation?token=wrong", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestResendConfirmation(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)
	subID, token := f.newSubscription(topicID, "ops@example.com")

	w := f.do(http.MethodPost, "/20181201/subscriptions/"+subID+"/actions/resendConfirmation", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	fresh, _ := decode(t, w)["confirmationToken"].(string)
	require.NotEmpty(t, fresh)
	assert.NotEqual(t, token, fresh)
}

func TestUnsubscribeEndpoints(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)

	subID, token := f.newSubscription(topicID, "link@example.com")
	w := f.do(http.MethodGet, "/20181201/subscriptions/"+subID+"/unsubscription?token="+token, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	w = f.do(http.MethodGet, "/20181201/subscriptions/"+subID, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)

	subID, _ = f.newSubscription(topicID, "api@example.com")
	w = f.do(http.MethodDelete, "/20181201/subscriptions/"+subID, nil)
	require.Equal(t, http.StatusNoContent, w.Code)

	w = f.do(http.MethodDelete, "/20181201/subscriptions/"+subID, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListSubscriptions(t *testing.T) {
	f := newFixture(t)
	mine := f.newTopic("mine", compartment)
	theirs := f.newTopic("theirs", otherCompartment)

	f.newSubscription(mine, "a@example.com")

	w := f.do(http.MethodPost, "/20181201/subscriptions", map[string]any{
		"topicId": theirs, "compartmentId": otherCompartment, "protocol": "EMAIL", "endpoint": "b@example.com",
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	w = f.do(http.MethodGet, "/20181201/subscriptions?compartmentId="+compartment, nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, decodeList(t, w), 1)

	w = f.do(http.MethodGet, "/20181201/subscriptions?compartmentId="+compartment+"&topicId="+theirs, nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeList(t, w))
}

func TestUpdateAndMoveSubscription(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)
	subID, _ := f.newSubscription(topicID, "ops@example.com")

	w := f.do(http.MethodPut, "/20181201/subscriptions/"+subID, map[string]any{
		"deliveryPolicy": map[string]any{
			"backoffRetryPolicy": map[string]any{"maxRetryDuration": 7200, "policyType": "EXPONENTIAL"},
		},
		"freeformTags": map[string]string{"team": "sre"},
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	policy, ok := decode(t, w)["deliveryPolicy"].(map[string]any)
	require.True(t, ok)
	backoff, ok := policy["backoffRetryPolicy"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 7200, backoff["maxRetryDuration"], 0)

	w = f.do(http.MethodPost, "/20181201/subscriptions/"+subID+"/actions/changeCompartment",
		map[string]any{"compartmentId": otherCompartment})
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	w = f.do(http.MethodGet, "/20181201/subscriptions?compartmentId="+otherCompartment, nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, decodeList(t, w), 1)
}

func TestPublishRejectsAnUnknownMessageType(t *testing.T) {
	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)

	w := f.do(http.MethodPost, "/20181201/topics/"+topicID+"/messages?messageType=PROTOBUF",
		map[string]any{"body": "hello"})
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

	w = f.do(http.MethodGet, "/20181201/topics/"+topicID+"/messages", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// portableOnly implements the portable driver and nothing else, standing in
// for a non-OCI notification driver wired into the OCI server.
type portableOnly struct{}

func (portableOnly) CreateTopic(context.Context, notifdriver.TopicConfig) (*notifdriver.TopicInfo, error) {
	return nil, nil //nolint:nilnil // never reached; the handler answers 501 first
}

func (portableOnly) UpdateTopic(context.Context, notifdriver.TopicConfig) (*notifdriver.TopicInfo, error) {
	return nil, nil //nolint:nilnil // never reached
}
func (portableOnly) DeleteTopic(context.Context, string) error { return nil }

func (portableOnly) GetTopic(context.Context, string) (*notifdriver.TopicInfo, error) {
	return nil, nil //nolint:nilnil // never reached
}

func (portableOnly) ListTopics(context.Context, scope.Scope) ([]notifdriver.TopicInfo, error) {
	return nil, nil
}

func (portableOnly) Subscribe(
	context.Context, notifdriver.SubscriptionConfig,
) (*notifdriver.SubscriptionInfo, error) {
	return nil, nil //nolint:nilnil // never reached
}
func (portableOnly) Unsubscribe(context.Context, string) error { return nil }

func (portableOnly) ListSubscriptions(context.Context, string) ([]notifdriver.SubscriptionInfo, error) {
	return nil, nil
}

func (portableOnly) Publish(context.Context, notifdriver.PublishInput) (*notifdriver.PublishOutput, error) {
	return nil, nil //nolint:nilnil // never reached
}

func TestDriverWithoutExtrasAnswers501(t *testing.T) {
	h := ocinotif.New(portableOnly{}, workrequest.New(config.NewOptions()))

	r := httptest.NewRequest(http.MethodGet, "/20181201/topics?compartmentId="+compartment, nil)
	w := httptest.NewRecorder()

	require.True(t, h.Matches(r))
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusNotImplemented, w.Code)

	var body ocirest.ErrorBody

	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "NotImplemented", body.Code)
}
