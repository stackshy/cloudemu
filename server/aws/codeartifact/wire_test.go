package codeartifact_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/aws/codeartifact"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

func TestPublishBodyBounds(t *testing.T) {
	h := fullHandler()
	q := "?domain=dom&repository=repo&format=generic&namespace=ns&package=p&version=1&asset=a"

	req := httptest.NewRequest(http.MethodPost, "/v1/package/version/publish"+q, bytes.NewReader(make([]byte, driver.MaxAssetBytes+2)))
	req.Header.Set("x-amz-content-sha256", xSHA256)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusPaymentRequired || rec.Header().Get("X-Amzn-Errortype") != "ServiceQuotaExceededException" {
		t.Fatalf("oversize publish: %d %q", rec.Code, rec.Header().Get("X-Amzn-Errortype"))
	}
}

func TestMalformedBodiesAndQueries(t *testing.T) {
	h := fullHandler()

	cases := []struct {
		name, method, target, body string
	}{
		{"broken JSON create domain", http.MethodPost, "/v1/domain?domain=dom", `{"tags": [`},
		{"wrong type tags", http.MethodPost, "/v1/domain?domain=dom", `{"tags": "nope"}`},
		{"wrong type maxResults", http.MethodPost, "/v1/domains", `{"maxResults": "ten"}`},
		{"negative max-results", http.MethodPost, "/v1/repositories?max-results=-1", ""},
		{"non-numeric max-results", http.MethodPost, "/v1/repositories?max-results=abc", ""},
		{"non-numeric duration", http.MethodPost, "/v1/authorization-token?domain=dom&duration=soon", ""},
		{"broken JSON bulk", http.MethodPost, "/v1/package/versions/delete?domain=dom&repository=repo&format=npm&package=p", `{"versions": `},
		{"missing everything", http.MethodGet, "/v1/package/version/asset", ""},
		{"huge max-results", http.MethodPost, "/v1/repositories?max-results=99999999999999999999", ""},
	}

	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.target, strings.NewReader(c.body))
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest || rec.Header().Get("X-Amzn-Errortype") != "ValidationException" {
			t.Errorf("%s: %d %q %s", c.name, rec.Code, rec.Header().Get("X-Amzn-Errortype"), strings.TrimSpace(rec.Body.String()))
		}
	}
}

func TestErrorBodyCarriesIdentity(t *testing.T) {
	e := newEnv(t)
	e.seed(t, "repo")

	resp, err := http.Get(e.url + "/v1/repository?domain=dom&repository=ghost-repo")
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)

	if resp.StatusCode != http.StatusNotFound || resp.Header.Get("X-Amzn-Errortype") != "ResourceNotFoundException" {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get("X-Amzn-Errortype"))
	}

	body := buf.String()
	if !strings.Contains(body, `"resourceId":"ghost-repo"`) || !strings.Contains(body, `"resourceType":"repository"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestUntypedDriverErrorsMapToCodeArtifactExceptions(t *testing.T) {
	cases := []struct {
		err    error
		status int
		kind   string
	}{
		{cerrors.New(cerrors.NotFound, "missing"), http.StatusNotFound, "ResourceNotFoundException"},
		{cerrors.New(cerrors.AlreadyExists, "dup"), http.StatusConflict, "ConflictException"},
		{cerrors.New(cerrors.InvalidArgument, "bad"), http.StatusBadRequest, "ValidationException"},
		{errors.New("boom"), http.StatusInternalServerError, "InternalServerException"},
	}

	for _, c := range cases {
		h := codeartifact.New(failingDriver{err: c.err})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/domain?domain=dom", nil))

		if rec.Code != c.status || rec.Header().Get("X-Amzn-Errortype") != c.kind {
			t.Errorf("%v: got %d %q, want %d %q", c.err, rec.Code, rec.Header().Get("X-Amzn-Errortype"), c.status, c.kind)
		}
	}

	if got := codeartifact.New(nil).IAMService(); got != "codeartifact" {
		t.Errorf("IAMService = %q", got)
	}
}

// failingDriver answers every call with err, standing in for a third-party driver that returns untagged errors.
type failingDriver struct {
	driver.CodeArtifact
	err error
}

func (f failingDriver) DescribeDomain(context.Context, string, string) (*driver.Domain, error) {
	return nil, f.err
}

func TestUnmodelledNotFoundIsAValidationException(t *testing.T) {
	h := fullHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/domain?domain=ghost-domain", nil))

	if rec.Code != http.StatusBadRequest || rec.Header().Get("X-Amzn-Errortype") != "ValidationException" {
		t.Fatalf("DeleteDomain of a missing domain: %d %q, want 400 ValidationException (the API does not model ResourceNotFound)",
			rec.Code, rec.Header().Get("X-Amzn-Errortype"))
	}

	// An operation that does model it keeps the typed 404.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/domain?domain=ghost-domain", nil))

	if rec.Code != http.StatusNotFound || rec.Header().Get("X-Amzn-Errortype") != "ResourceNotFoundException" {
		t.Fatalf("DescribeDomain of a missing domain: %d %q", rec.Code, rec.Header().Get("X-Amzn-Errortype"))
	}
}

func TestZeroMaxResultsIsRejected(t *testing.T) {
	h := fullHandler()

	for _, c := range []struct{ method, target, body string }{
		{http.MethodPost, "/v1/repositories?max-results=0", ""},
		{http.MethodPost, "/v1/packages?domain=dom&repository=repo&max-results=0", ""},
		{http.MethodPost, "/v1/domains", `{"maxResults":0}`},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.target, strings.NewReader(c.body)))

		if rec.Code != http.StatusBadRequest || rec.Header().Get("X-Amzn-Errortype") != "ValidationException" {
			t.Errorf("%s: %d %q, want 400 ValidationException", c.target, rec.Code, rec.Header().Get("X-Amzn-Errortype"))
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/domains", strings.NewReader(`{}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("an absent maxResults must use the default page size: %d %s", rec.Code, rec.Body.String())
	}
}

// brokenBody fails the way a dropped client connection does.
type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("connection reset by peer") }

func TestPublishReadErrorIsNotTheSizeLimit(t *testing.T) {
	h := fullHandler()
	req := httptest.NewRequest(http.MethodPost, "/v1/package/version/publish?domain=dom&repository=repo&format=generic&namespace=ns&package=p&version=1&asset=a", brokenBody{})
	req.Header.Set("x-amz-content-sha256", xSHA256)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || rec.Header().Get("X-Amzn-Errortype") != "ValidationException" {
		t.Fatalf("dropped upload: %d %q, want 400 ValidationException (402 is only for the size limit)", rec.Code, rec.Header().Get("X-Amzn-Errortype"))
	}
}
