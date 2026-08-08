package ocirest_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

func TestWriteJSONStampsRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/20160918/instances", nil)

	ocirest.WriteJSON(rec, req, http.StatusOK, map[string]string{"id": "ocid1.instance.oc1.iad.abc"})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Header().Get(ocirest.HeaderRequestID))
	assert.JSONEq(t, `{"id":"ocid1.instance.oc1.iad.abc"}`, rec.Body.String())
}

func TestRequestIDIsEchoedWhenCallerSendsOne(t *testing.T) {
	// SDKs and logs correlate on the caller's opc-request-id, so it must come
	// back rather than being replaced by a freshly minted one.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/20160918/instances", nil)
	req.Header.Set(ocirest.HeaderRequestID, "caller-supplied-id")

	ocirest.WriteJSON(rec, req, http.StatusOK, nil)

	assert.Equal(t, "caller-supplied-id", rec.Header().Get(ocirest.HeaderRequestID))
}

func TestRequestIDIsMintedWhenCallerSendsNone(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/20160918/instances", nil)

	ocirest.WriteJSON(rec, req, http.StatusOK, nil)

	assert.NotEmpty(t, rec.Header().Get(ocirest.HeaderRequestID))
}

func TestRequestIDEchoedOnErrorsToo(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/20160918/instances", nil)
	req.Header.Set(ocirest.HeaderRequestID, "caller-supplied-id")

	ocirest.WriteDriverError(rec, req, cerrors.New(cerrors.NotFound, "gone"))

	assert.Equal(t, "caller-supplied-id", rec.Header().Get(ocirest.HeaderRequestID))
}

func TestWriteJSONNilBodyWritesNothing(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/20160918/instances", nil)

	ocirest.WriteJSON(rec, req, http.StatusNoContent, nil)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

func TestWriteDriverError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectStatus int
		expectCode   string
	}{
		{
			name:         "not found",
			err:          cerrors.New(cerrors.NotFound, "no such bucket"),
			expectStatus: http.StatusNotFound,
			expectCode:   "NotAuthorizedOrNotFound",
		},
		{
			name:         "permission denied is indistinguishable from not found",
			err:          cerrors.New(cerrors.PermissionDenied, "denied"),
			expectStatus: http.StatusNotFound,
			expectCode:   "NotAuthorizedOrNotFound",
		},
		{
			name:         "already exists",
			err:          cerrors.New(cerrors.AlreadyExists, "bucket exists"),
			expectStatus: http.StatusConflict,
			expectCode:   "Conflict",
		},
		{
			name:         "invalid argument",
			err:          cerrors.New(cerrors.InvalidArgument, "bad name"),
			expectStatus: http.StatusBadRequest,
			expectCode:   "InvalidParameter",
		},
		{
			name:         "failed precondition",
			err:          cerrors.New(cerrors.FailedPrecondition, "not terminated"),
			expectStatus: http.StatusConflict,
			expectCode:   "IncorrectState",
		},
		{
			name:         "throttled",
			err:          cerrors.New(cerrors.Throttled, "slow down"),
			expectStatus: http.StatusTooManyRequests,
			expectCode:   "TooManyRequests",
		},
		{
			name:         "unimplemented",
			err:          cerrors.New(cerrors.Unimplemented, "not yet"),
			expectStatus: http.StatusNotImplemented,
			expectCode:   "NotImplemented",
		},
		{
			name:         "unavailable",
			err:          cerrors.New(cerrors.Unavailable, "down"),
			expectStatus: http.StatusServiceUnavailable,
			expectCode:   "ServiceUnavailable",
		},
		{
			name:         "non-cloudemu error falls back to internal",
			err:          assert.AnError,
			expectStatus: http.StatusInternalServerError,
			expectCode:   "InternalServerError",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/20160918/instances", nil)

			ocirest.WriteDriverError(rec, req, tc.err)

			assert.Equal(t, tc.expectStatus, rec.Code)

			var body ocirest.ErrorBody
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tc.expectCode, body.Code)
			assert.NotEmpty(t, body.Message)
		})
	}
}

func TestDecodeJSON(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		expectOK  bool
		expectVal string
	}{
		{name: "valid JSON", body: `{"name":"bucket-a"}`, expectOK: true, expectVal: "bucket-a"},
		{name: "malformed JSON", body: `{`, expectOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/20160918/buckets", strings.NewReader(tc.body))

			var payload struct {
				Name string `json:"name"`
			}

			ok := ocirest.DecodeJSON(rec, req, &payload)

			assert.Equal(t, tc.expectOK, ok)

			if tc.expectOK {
				assert.Equal(t, tc.expectVal, payload.Name)
				return
			}

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestRequireCompartmentID(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		expectID  string
		expectOK  bool
		expectErr int
	}{
		{
			name:     "present",
			url:      "/20160918/instances?compartmentId=ocid1.compartment.oc1..abc",
			expectID: "ocid1.compartment.oc1..abc",
			expectOK: true,
		},
		{
			name:      "absent",
			url:       "/20160918/instances",
			expectOK:  false,
			expectErr: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)

			id, ok := ocirest.RequireCompartmentID(rec, req)

			assert.Equal(t, tc.expectOK, ok)
			assert.Equal(t, tc.expectID, id)

			if !tc.expectOK {
				assert.Equal(t, tc.expectErr, rec.Code)
			}
		})
	}
}

func TestLimit(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		expect int
	}{
		{name: "absent falls back to default", url: "/x", expect: ocirest.DefaultLimit},
		{name: "explicit limit", url: "/x?limit=25", expect: 25},
		{name: "unparseable falls back", url: "/x?limit=abc", expect: ocirest.DefaultLimit},
		{name: "zero falls back", url: "/x?limit=0", expect: ocirest.DefaultLimit},
		{name: "negative falls back", url: "/x?limit=-5", expect: ocirest.DefaultLimit},
		{name: "at the cap", url: "/x?limit=1000", expect: ocirest.MaxLimit},
		{name: "above the cap is clamped", url: "/x?limit=999999", expect: ocirest.MaxLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			assert.Equal(t, tc.expect, ocirest.Limit(req))
		})
	}
}

func TestSetNextPageOmitsHeaderOnLastPage(t *testing.T) {
	// OCI signals the last page by omitting opc-next-page, so an empty token
	// must not write the header.
	rec := httptest.NewRecorder()
	ocirest.SetNextPage(rec, "")
	assert.Empty(t, rec.Header().Get(ocirest.HeaderNextPage))

	rec = httptest.NewRecorder()
	ocirest.SetNextPage(rec, "cursor-2")
	assert.Equal(t, "cursor-2", rec.Header().Get(ocirest.HeaderNextPage))
}

