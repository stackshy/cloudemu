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
	assert.NotNil(t, decode(t, w)["deliveryPolicy"])
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
