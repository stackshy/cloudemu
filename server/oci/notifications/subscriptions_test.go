package notifications_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const missingSubscription = "ocid1.onssubscription.oc1..missing"

func TestSubscriptionMethodNotAllowed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alpha", compartment)
	id, _ := f.newSubscription(topicID, "ops@example.com")

	cases := map[string]struct{ method, target string }{
		"collection":     {http.MethodPatch, "/20181201/subscriptions?compartmentId=" + compartment},
		"single":         {http.MethodPatch, "/20181201/subscriptions/" + id},
		"confirmation":   {http.MethodPost, "/20181201/subscriptions/" + id + "/confirmation"},
		"unsubscription": {http.MethodPost, "/20181201/subscriptions/" + id + "/unsubscription"},
		"move":           {http.MethodGet, "/20181201/subscriptions/" + id + "/actions/changeCompartment"},
		"resend":         {http.MethodGet, "/20181201/subscriptions/" + id + "/actions/resendConfirmation"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := f.do(tc.method, tc.target, nil)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code, w.Body.String())
		})
	}
}

func TestSubscriptionUnknownPaths(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alpha", compartment)
	id, _ := f.newSubscription(topicID, "ops@example.com")

	for _, target := range []string{
		"/20181201/subscriptions/" + id + "/deliveries",
		"/20181201/subscriptions/" + id + "/actions/explode",
	} {
		w := f.do(http.MethodGet, target, nil)
		assert.Equal(t, http.StatusNotFound, w.Code, target)
	}
}

func TestCreateSubscriptionRefusesDefinedTags(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alpha", compartment)

	w := f.do(http.MethodPost, "/20181201/subscriptions", map[string]any{
		"topicId":       topicID,
		"compartmentId": compartment,
		"protocol":      "EMAIL",
		"endpoint":      "ops@example.com",
		"definedTags":   map[string]any{"ops": map[string]any{"tier": "gold"}},
	})
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestUpdateSubscriptionErrors(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alpha", compartment)
	id, _ := f.newSubscription(topicID, "ops@example.com")

	defined := f.do(http.MethodPut, "/20181201/subscriptions/"+id, map[string]any{
		"definedTags": map[string]any{"ops": map[string]any{"tier": "gold"}},
	})
	assert.Equal(t, http.StatusBadRequest, defined.Code, defined.Body.String())

	missing := f.do(http.MethodPut, "/20181201/subscriptions/"+missingSubscription, map[string]any{
		"freeformTags": map[string]string{"team": "ops"},
	})
	assert.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
}

// An empty deliveryPolicy round-trips as an empty policy rather than being
// read as "no policy given".
func TestUpdateSubscriptionWithAnEmptyDeliveryPolicy(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alpha", compartment)
	id, _ := f.newSubscription(topicID, "ops@example.com")

	w := f.do(http.MethodPut, "/20181201/subscriptions/"+id, map[string]any{
		"deliveryPolicy": map[string]any{},
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "{}", decode(t, w)["deliverPolicy"])
}

func TestTokenEndpointsRequireAToken(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alpha", compartment)
	id, _ := f.newSubscription(topicID, "ops@example.com")

	for _, sub := range []string{"confirmation", "unsubscription"} {
		w := f.do(http.MethodGet, "/20181201/subscriptions/"+id+"/"+sub, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code, sub)
	}
}

func TestUnsubscribeWithTheWrongToken(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alpha", compartment)
	id, _ := f.newSubscription(topicID, "ops@example.com")

	w := f.do(http.MethodGet, "/20181201/subscriptions/"+id+"/unsubscription?token=wrong&protocol=EMAIL", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestSubscriptionActionsOnAnUnknownSubscription(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	resend := f.do(http.MethodPost,
		"/20181201/subscriptions/"+missingSubscription+"/actions/resendConfirmation", nil)
	assert.Equal(t, http.StatusNotFound, resend.Code, resend.Body.String())

	move := f.do(http.MethodPost,
		"/20181201/subscriptions/"+missingSubscription+"/actions/changeCompartment",
		map[string]any{"compartmentId": otherCompartment})
	assert.Equal(t, http.StatusNotFound, move.Code, move.Body.String())

	del := f.do(http.MethodDelete, "/20181201/subscriptions/"+missingSubscription, nil)
	assert.Equal(t, http.StatusNotFound, del.Code, del.Body.String())
}

func TestListSubscriptionsRequiresACompartment(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	w := f.do(http.MethodGet, "/20181201/subscriptions", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestListSubscriptionsPaginates(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alpha", compartment)

	for _, endpoint := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		f.newSubscription(topicID, endpoint)
	}

	base := "/20181201/subscriptions?compartmentId=" + compartment

	first := f.do(http.MethodGet, base+"&limit=2", nil)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.Len(t, decodeList(t, first), 2)
	assert.Equal(t, "2", first.Header().Get("opc-next-page"))

	second := f.do(http.MethodGet, base+"&limit=2&page=2", nil)
	assert.Len(t, decodeList(t, second), 1)
}

func TestSubscriptionIfMatch(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)
	id, _ := f.newSubscription(topicID, "ops@example.com")

	get := f.do(http.MethodGet, "/20181201/subscriptions/"+id, nil)
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	etag, _ := decode(t, get)["etag"].(string)
	require.NotEmpty(t, etag)

	ok := f.doIfMatch(http.MethodPut, "/20181201/subscriptions/"+id, etag,
		map[string]any{"freeformTags": map[string]string{"team": "ops"}})
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())

	stale := f.doIfMatch(http.MethodPut, "/20181201/subscriptions/"+id, etag,
		map[string]any{"freeformTags": map[string]string{"team": "ignored"}})
	require.Equal(t, http.StatusPreconditionFailed, stale.Code, stale.Body.String())

	staleDelete := f.doIfMatch(http.MethodDelete, "/20181201/subscriptions/"+id, etag, nil)
	assert.Equal(t, http.StatusPreconditionFailed, staleDelete.Code, staleDelete.Body.String())
}

// ONS rejects an endpoint the protocol cannot deliver to, naming what is wrong.
func TestCreateSubscriptionRejectsAMalformedEndpoint(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)

	cases := map[string]struct{ protocol, endpoint, want string }{
		"email without an at": {"EMAIL", "ops-example.com", "@"},
		"https over http":     {"CUSTOM_HTTPS", "http://hooks.example.com/x", "https"},
		"slack over http":     {"SLACK", "http://hooks.slack.com/x", "https"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := f.do(http.MethodPost, "/20181201/subscriptions", map[string]any{
				"topicId": topicID, "compartmentId": compartment,
				"protocol": tc.protocol, "endpoint": tc.endpoint,
			})
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), tc.want)
		})
	}
}

func TestCreateSubscriptionRequiresAnExistingCompartment(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.knownCompartments()
	topicID := f.newTopic("alerts", compartment)

	w := f.do(http.MethodPost, "/20181201/subscriptions", map[string]any{
		"topicId": topicID, "compartmentId": "ocid1.compartment.oc1..bogus",
		"protocol": "EMAIL", "endpoint": "ops@example.com",
	})
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "NotAuthorizedOrNotFound")
}

// ONS does not ask a function subscription to confirm: it is created ACTIVE,
// carries no token, and receives the next publish.
func TestOracleFunctionsSubscriptionIsActiveOverTheWire(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)

	w := f.do(http.MethodPost, "/20181201/subscriptions", map[string]any{
		"topicId": topicID, "compartmentId": compartment,
		"protocol": "ORACLE_FUNCTIONS", "endpoint": "ocid1.fnfunc.oc1..x",
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	body := decode(t, w)
	assert.Equal(t, "ACTIVE", body["lifecycleState"])
	assert.NotContains(t, body, "confirmationToken")

	id, _ := body["id"].(string)

	publish := f.do(http.MethodPost, "/20181201/topics/"+topicID+"/messages", map[string]any{"body": "hello"})
	require.Equal(t, http.StatusOK, publish.Code, publish.Body.String())
	assert.Len(t, f.mock.Deliveries(id), 1)
}

// The SDK maps Create, Get and UpdateSubscription's Etag from the etag header.
func TestSubscriptionEtagHeader(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)

	create := f.do(http.MethodPost, "/20181201/subscriptions", map[string]any{
		"topicId": topicID, "compartmentId": compartment,
		"protocol": "EMAIL", "endpoint": "ops@example.com",
	})
	require.Equal(t, http.StatusCreated, create.Code)

	body := decode(t, create)
	id, _ := body["id"].(string)
	assert.NotEmpty(t, create.Header().Get("etag"))
	assert.Equal(t, body["etag"], create.Header().Get("etag"))

	get := f.do(http.MethodGet, "/20181201/subscriptions/"+id, nil)
	assert.Equal(t, decode(t, get)["etag"], get.Header().Get("etag"))

	update := f.do(http.MethodPut, "/20181201/subscriptions/"+id,
		map[string]any{"freeformTags": map[string]string{"team": "ops"}})
	require.Equal(t, http.StatusOK, update.Code)
	assert.Equal(t, decode(t, update)["etag"], update.Header().Get("etag"))
}

// ONS marks protocol mandatory on both token endpoints.
func TestTokenEndpointsRequireAProtocol(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	topicID := f.newTopic("alerts", compartment)
	id, token := f.newSubscription(topicID, "ops@example.com")

	for _, sub := range []string{"confirmation", "unsubscription"} {
		w := f.do(http.MethodGet, "/20181201/subscriptions/"+id+"/"+sub+"?token="+token, nil)
		require.Equal(t, http.StatusBadRequest, w.Code, sub)
		assert.Contains(t, w.Body.String(), "protocol is required", sub)
	}

	// Neither refusal touched the subscription.
	got := decode(t, f.do(http.MethodGet, "/20181201/subscriptions/"+id, nil))
	assert.Equal(t, "PENDING", got["lifecycleState"])
}

func TestListSubscriptionsRejectsABadPageToken(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	w := f.do(http.MethodGet, "/20181201/subscriptions?compartmentId="+compartment+"&page=garbage", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}
