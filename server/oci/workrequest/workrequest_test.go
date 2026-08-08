package workrequest_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

func newStore() *workrequest.Store {
	return workrequest.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC))),
		config.WithRegion("us-ashburn-1"),
	))
}

func TestAcceptRecordsSucceededRequest(t *testing.T) {
	s := newStore()

	id := s.Accept("CREATE_INSTANCE", "ocid1.compartment.oc1..abc", workrequest.Resource{
		EntityType: "instance",
		ActionType: workrequest.ActionCreated,
		Identifier: "ocid1.instance.oc1.iad.xyz",
	})

	require.True(t, strings.HasPrefix(id, "ocid1.workrequest.oc1.iad."), "got %q", id)

	wr, ok := s.Get(id)
	require.True(t, ok)

	// Mutations complete synchronously, so a waiter's first poll is terminal.
	assert.Equal(t, workrequest.StatusSucceeded, wr.Status)
	assert.InDelta(t, float32(100), wr.PercentComplete, 0.001)
	assert.Equal(t, "CREATE_INSTANCE", wr.OperationType)
	assert.Equal(t, "ocid1.compartment.oc1..abc", wr.CompartmentID)
	assert.NotEmpty(t, wr.TimeFinished)
	require.Len(t, wr.Resources, 1)
	assert.Equal(t, "ocid1.instance.oc1.iad.xyz", wr.Resources[0].Identifier)
}

func TestGetMissingRequest(t *testing.T) {
	s := newStore()

	_, ok := s.Get("ocid1.workrequest.oc1.iad.missing")
	assert.False(t, ok)
}

func TestListFiltersByCompartment(t *testing.T) {
	s := newStore()
	s.Accept("CREATE_INSTANCE", "compartment-a")
	s.Accept("CREATE_VCN", "compartment-b")
	s.Accept("DELETE_INSTANCE", "compartment-a")

	tests := []struct {
		name        string
		compartment string
		expectLen   int
	}{
		{name: "compartment a", compartment: "compartment-a", expectLen: 2},
		{name: "compartment b", compartment: "compartment-b", expectLen: 1},
		{name: "empty compartment returns all", compartment: "", expectLen: 3},
		{name: "unknown compartment returns none", compartment: "compartment-z", expectLen: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Len(t, s.List(tc.compartment), tc.expectLen)
		})
	}
}

func TestListPreservesCreationOrder(t *testing.T) {
	s := newStore()
	first := s.Accept("CREATE_INSTANCE", "c")
	second := s.Accept("CREATE_VCN", "c")

	got := s.List("c")

	require.Len(t, got, 2)
	assert.Equal(t, first, got[0].ID)
	assert.Equal(t, second, got[1].ID)
}

func TestHandlerMatches(t *testing.T) {
	h := workrequest.NewHandler(newStore())

	tests := []struct {
		name   string
		method string
		path   string
		expect bool
	}{
		{name: "core version prefix", method: http.MethodGet, path: "/20160918/workRequests/ocid1.workrequest.oc1.iad.a", expect: true},
		{name: "another service version prefix", method: http.MethodGet, path: "/20180222/workRequests/ocid1.workrequest.oc1.iad.a", expect: true},
		{name: "collection", method: http.MethodGet, path: "/20160918/workRequests", expect: true},
		{name: "errors sub-collection", method: http.MethodGet, path: "/20160918/workRequests/abc/errors", expect: true},
		{name: "non-GET is not claimed", method: http.MethodPost, path: "/20160918/workRequests", expect: false},
		{name: "unrelated path", method: http.MethodGet, path: "/20160918/instances", expect: false},
		{name: "too many trailing segments", method: http.MethodGet, path: "/20160918/workRequests/a/b/c", expect: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, tc.expect, h.Matches(req))
		})
	}
}

func TestHandlerServesSingleRequest(t *testing.T) {
	s := newStore()
	id := s.Accept("CREATE_INSTANCE", "ocid1.compartment.oc1..abc")
	h := workrequest.NewHandler(s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/20160918/workRequests/"+id, nil))

	require.Equal(t, http.StatusOK, rec.Code)

	var wr workrequest.WorkRequest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &wr))
	assert.Equal(t, id, wr.ID)
	assert.Equal(t, workrequest.StatusSucceeded, wr.Status)
}

func TestHandlerUnknownRequestIs404(t *testing.T) {
	h := workrequest.NewHandler(newStore())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/20160918/workRequests/nope", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body ocirest.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "NotAuthorizedOrNotFound", body.Code)
}

func TestHandlerSubCollectionsAreEmpty(t *testing.T) {
	s := newStore()
	id := s.Accept("CREATE_INSTANCE", "c")
	h := workrequest.NewHandler(s)

	for _, sub := range []string{"errors", "logs"} {
		t.Run(sub, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/20160918/workRequests/"+id+"/"+sub, nil))

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.JSONEq(t, `[]`, rec.Body.String())
		})
	}
}

func TestHandlerListsByCompartment(t *testing.T) {
	s := newStore()
	s.Accept("CREATE_INSTANCE", "compartment-a")
	s.Accept("CREATE_VCN", "compartment-b")
	h := workrequest.NewHandler(s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/20160918/workRequests?compartmentId=compartment-a", nil))

	require.Equal(t, http.StatusOK, rec.Code)

	var got []workrequest.WorkRequest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "CREATE_INSTANCE", got[0].OperationType)
}
