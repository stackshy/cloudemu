package notifications_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	notifprovider "github.com/stackshy/cloudemu/v2/providers/oci/notifications"
	ocinotif "github.com/stackshy/cloudemu/v2/server/oci/notifications"
)

func TestListTopicsSorts(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		f.newTopic(name, compartment)
	}

	names := func(target string) []string {
		w := f.do(http.MethodGet, target, nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		out := make([]string, 0, 3)
		for _, topic := range decodeList(t, w) {
			name, _ := topic["name"].(string)
			out = append(out, name)
		}

		return out
	}

	base := "/20181201/topics?compartmentId=" + compartment

	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, names(base+"&sortBy=TIMECREATED&sortOrder=ASC"))
	assert.Equal(t, []string{"charlie", "bravo", "alpha"}, names(base+"&sortBy=TIMECREATED&sortOrder=DESC"))
	// LIFECYCLESTATE ties across an all-ACTIVE listing, so the sort is stable.
	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, names(base+"&sortBy=LIFECYCLESTATE"))
}

func TestListTopicsRejectsAnUnknownSort(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	base := "/20181201/topics?compartmentId=" + compartment

	for _, target := range []string{base + "&sortBy=NAME", base + "&sortOrder=SIDEWAYS"} {
		w := f.do(http.MethodGet, target, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code, target)
	}
}

func TestListTopicsFilters(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	first := f.newTopic("alpha", compartment)
	f.newTopic("bravo", compartment)

	base := "/20181201/topics?compartmentId=" + compartment

	byID := decodeList(t, f.do(http.MethodGet, base+"&id="+first, nil))
	require.Len(t, byID, 1)
	assert.Equal(t, "alpha", byID[0]["name"])

	byName := decodeList(t, f.do(http.MethodGet, base+"&name=bravo", nil))
	require.Len(t, byName, 1)
	assert.Equal(t, "bravo", byName[0]["name"])

	assert.Empty(t, decodeList(t, f.do(http.MethodGet, base+"&name=missing", nil)))
	assert.Empty(t, decodeList(t, f.do(http.MethodGet, base+"&lifecycleState=DELETING", nil)))
	assert.Len(t, decodeList(t, f.do(http.MethodGet, base+"&lifecycleState=ACTIVE", nil)), 2)
}

func TestListTopicsPaginates(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		f.newTopic(name, compartment)
	}

	base := "/20181201/topics?compartmentId=" + compartment

	first := f.do(http.MethodGet, base+"&limit=2", nil)
	require.Equal(t, http.StatusOK, first.Code)
	assert.Len(t, decodeList(t, first), 2)

	next := first.Header().Get("opc-next-page")
	require.Equal(t, "2", next)

	second := f.do(http.MethodGet, base+"&limit=2&page="+next, nil)
	assert.Len(t, decodeList(t, second), 1)
	assert.Empty(t, second.Header().Get("opc-next-page"))

	// A cursor past the end is an empty page, not an error.
	assert.Empty(t, decodeList(t, f.do(http.MethodGet, base+"&page=99", nil)))
}

func TestTopicMethodNotAllowed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := f.newTopic("alpha", compartment)

	cases := map[string]struct{ method, target string }{
		"collection": {http.MethodPatch, "/20181201/topics?compartmentId=" + compartment},
		"single":     {http.MethodPatch, "/20181201/topics/" + id},
		"messages":   {http.MethodGet, "/20181201/topics/" + id + "/messages"},
		"move":       {http.MethodGet, "/20181201/topics/" + id + "/actions/changeCompartment"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := f.do(tc.method, tc.target, nil)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code, w.Body.String())
		})
	}
}

func TestUpdateTopicErrors(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := f.newTopic("alpha", compartment)

	defined := f.do(http.MethodPut, "/20181201/topics/"+id, map[string]any{
		"definedTags": map[string]any{"ops": map[string]any{"tier": "gold"}},
	})
	assert.Equal(t, http.StatusBadRequest, defined.Code, defined.Body.String())

	missing := f.do(http.MethodPut, "/20181201/topics/ocid1.onstopic.oc1..missing", map[string]any{
		"description": "x",
	})
	assert.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
}

func TestChangeTopicCompartmentErrors(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := f.newTopic("alpha", compartment)
	target := "/20181201/topics/" + id + "/actions/changeCompartment"

	blank := f.do(http.MethodPost, target, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, blank.Code, blank.Body.String())

	missing := f.do(http.MethodPost,
		"/20181201/topics/ocid1.onstopic.oc1..missing/actions/changeCompartment",
		map[string]any{"compartmentId": otherCompartment})
	assert.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
}

// Deleting and moving a topic both record a work request, so a handler wired
// without a store cannot serve them.
func TestTopicWorkRequestPathsNeedAStore(t *testing.T) {
	t.Parallel()

	opts := config.NewOptions(config.WithRegion("us-ashburn-1"), config.WithCompartmentID(compartment))
	mock := notifprovider.New(opts)
	handler := ocinotif.New(mock, nil)

	cases := map[string]struct{ method, target string }{
		"delete": {http.MethodDelete, "/20181201/topics/ocid1.onstopic.oc1..x"},
		"move":   {http.MethodPost, "/20181201/topics/ocid1.onstopic.oc1..x/actions/changeCompartment"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(tc.method, tc.target, strings.NewReader("{}"))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assert.Equal(t, http.StatusNotImplemented, w.Code, w.Body.String())
		})
	}
}

func TestDeleteUnknownTopic(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	w := f.do(http.MethodDelete, "/20181201/topics/ocid1.onstopic.oc1..missing", nil)
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

// A topic reports the origin the caller reached as its apiEndpoint, so a TLS
// request gets an https one.
func TestAPIEndpointFollowsTheScheme(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := f.newTopic("alpha", compartment)

	r := httptest.NewRequest(http.MethodGet, "/20181201/topics/"+id, nil)
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	endpoint, _ := decode(t, w)["apiEndpoint"].(string)
	assert.True(t, strings.HasPrefix(endpoint, "https://"), endpoint)
}

func TestMalformedNotificationsPath(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	w := f.do(http.MethodGet, "/20181201/topics/a/b/c/d", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

// Real ONS answers 201 Created, not 200, on both creates.
func TestCreateAnswers201(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	topic := f.do(http.MethodPost, "/20181201/topics", map[string]any{
		"name": "alerts", "compartmentId": compartment,
	})
	require.Equal(t, http.StatusCreated, topic.Code, topic.Body.String())

	topicID, _ := decode(t, topic)["topicId"].(string)

	sub := f.do(http.MethodPost, "/20181201/subscriptions", map[string]any{
		"topicId": topicID, "compartmentId": compartment,
		"protocol": "EMAIL", "endpoint": "ops@example.com",
	})
	assert.Equal(t, http.StatusCreated, sub.Code, sub.Body.String())
}

// An if-match carrying the current etag proceeds; a stale one is a 412 and
// leaves the topic alone.
func TestTopicIfMatch(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := f.newTopic("alerts", compartment)

	get := f.do(http.MethodGet, "/20181201/topics/"+id, nil)
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	etag, _ := decode(t, get)["etag"].(string)
	require.NotEmpty(t, etag)

	ok := f.doIfMatch(http.MethodPut, "/20181201/topics/"+id, etag, map[string]any{"description": "fresh"})
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
	assert.Equal(t, "fresh", decode(t, ok)["description"])

	// The update rotated the etag, so the one just used is now stale.
	stale := f.doIfMatch(http.MethodPut, "/20181201/topics/"+id, etag, map[string]any{"description": "ignored"})
	require.Equal(t, http.StatusPreconditionFailed, stale.Code, stale.Body.String())

	unchanged := f.do(http.MethodGet, "/20181201/topics/"+id, nil)
	assert.Equal(t, "fresh", decode(t, unchanged)["description"])

	staleDelete := f.doIfMatch(http.MethodDelete, "/20181201/topics/"+id, etag, nil)
	require.Equal(t, http.StatusPreconditionFailed, staleDelete.Code, staleDelete.Body.String())

	current, _ := decode(t, f.do(http.MethodGet, "/20181201/topics/"+id, nil))["etag"].(string)
	freshDelete := f.doIfMatch(http.MethodDelete, "/20181201/topics/"+id, current, nil)
	assert.Equal(t, http.StatusNoContent, freshDelete.Code, freshDelete.Body.String())
}
