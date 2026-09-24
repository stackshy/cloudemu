package notifications_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The data plane is reached through the topic's own apiEndpoint, which
// CloudEmu points back at this listener.
func TestPublishThroughTheAdvertisedAPIEndpoint(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := f.newTopic("alpha", compartment)

	get := f.do(http.MethodGet, "/20181201/topics/"+id, nil)
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())

	endpoint, _ := decode(t, get)["apiEndpoint"].(string)
	require.NotEmpty(t, endpoint)

	w := f.do(http.MethodPost, endpoint+"/20181201/topics/"+id+"/messages", map[string]any{
		"title": "deploy",
		"body":  "shipped",
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	body := decode(t, w)
	assert.NotEmpty(t, body["messageId"])
	assert.NotEmpty(t, body["timeStamp"])
}

func TestPublishToAnUnknownTopic(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	w := f.do(http.MethodPost, "/20181201/topics/ocid1.onstopic.oc1..missing/messages", map[string]any{
		"body": "shipped",
	})
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

func TestPublishRequiresABody(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := f.newTopic("alpha", compartment)

	w := f.do(http.MethodPost, "/20181201/topics/"+id+"/messages", map[string]any{"title": "deploy"})
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

// A PENDING subscription receives nothing; confirming it opens delivery.
func TestPublishDeliversOnlyToConfirmedSubscriptions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := f.newTopic("alpha", compartment)
	subID, token := f.newSubscription(id, "ops@example.com")

	publish := func(body string) {
		t.Helper()

		w := f.do(http.MethodPost, "/20181201/topics/"+id+"/messages", map[string]any{"body": body})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}

	publish("while pending")
	assert.Empty(t, f.mock.Deliveries(subID))

	confirm := f.do(http.MethodGet,
		"/20181201/subscriptions/"+subID+"/confirmation?token="+token+"&protocol=EMAIL", nil)
	require.Equal(t, http.StatusOK, confirm.Code, confirm.Body.String())

	publish("after confirming")

	delivered := f.mock.Deliveries(subID)
	require.Len(t, delivered, 1)
	assert.Equal(t, "after confirming", delivered[0].Body)
}

// ONS caps a published message at 64 KB.
func TestPublishRejectsAnOversizedBody(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := f.newTopic("alerts", compartment)

	w := f.do(http.MethodPost, "/20181201/topics/"+id+"/messages", map[string]any{
		"body": strings.Repeat("x", 64*1024+1),
	})
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "65536")

	atLimit := f.do(http.MethodPost, "/20181201/topics/"+id+"/messages", map[string]any{
		"body": strings.Repeat("x", 64*1024),
	})
	assert.Equal(t, http.StatusOK, atLimit.Code, atLimit.Body.String())
}
