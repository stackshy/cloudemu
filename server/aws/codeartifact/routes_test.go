package codeartifact_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/server/aws/codeartifact"
)

// modelOperation is one operation of the CodeArtifact API model (aws-sdk-go-v2
// service/codeartifact v1.45.0, from the botocore service-2.json): its route and
// the exceptions the API says it can return.
type modelOperation struct {
	Method string   `json:"method"`
	Path   string   `json:"path"`
	Errors []string `json:"errors"`
}

func loadModel(t *testing.T) map[string]modelOperation {
	t.Helper()

	raw, err := os.ReadFile("testdata/operations.json")
	if err != nil {
		t.Fatalf("read model: %v", err)
	}

	var ops map[string]modelOperation
	if err = json.Unmarshal(raw, &ops); err != nil {
		t.Fatalf("parse model: %v", err)
	}

	return ops
}

func fullHandler() *codeartifact.Handler {
	return codeartifact.New(cloudemu.NewAWS().CodeArtifact)
}

// TestAllFortyEightOperationsRouted proves the route table is exactly the API
// model: every operation has its verb and path, none answers "unsupported path",
// and the CloudTrail name of each route is the operation name.
func TestAllFortyEightOperationsRouted(t *testing.T) {
	ops := loadModel(t)

	const operations = 48
	if len(ops) != operations {
		t.Fatalf("model has %d operations, want %d", len(ops), operations)
	}

	h := fullHandler()
	names := make([]string, 0, len(ops))

	for name := range ops {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		op := ops[name]

		if got := codeartifact.OperationName(op.Method, op.Path); got != name {
			t.Errorf("OperationName(%s %s) = %q, want %q", op.Method, op.Path, got, name)
		}

		target := op.Path + "?resourceArn=arn:aws:codeartifact:us-east-1:123456789012:domain/dom"
		req := httptest.NewRequest(op.Method, target, strings.NewReader("{}"))

		if !h.Matches(req) {
			t.Errorf("%s: Matches(%s %s) = false", name, op.Method, op.Path)

			continue
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if msg := rec.Body.String(); strings.Contains(msg, "unsupported path") {
			t.Errorf("%s: answered %q", name, msg)
		}
	}
}

func TestMatchesAllRootsAndSiblings(t *testing.T) {
	h := fullHandler()
	ca := "?resourceArn=arn:aws:codeartifact:us-east-1:123456789012:repository/d/r"

	positive := []struct{ method, path string }{
		{http.MethodPost, "/v1/authorization-token"}, {http.MethodGet, "/v1/repository/endpoint"},
		{http.MethodPut, "/v1/domain/permissions/policy"}, {http.MethodDelete, "/v1/repository/permissions/policies"},
		{http.MethodPost, "/v1/packages"}, {http.MethodGet, "/v1/package"}, {http.MethodPost, "/v1/package/versions/copy"},
		{http.MethodPost, "/v1/package/version/publish"}, {http.MethodGet, "/v1/package/version/asset"},
		{http.MethodPost, "/v1/package-group"}, {http.MethodPost, "/v1/package-groups/sub-groups"},
		{http.MethodGet, "/v1/get-associated-package-group"}, {http.MethodGet, "/v1/list-associated-packages"},
		{http.MethodGet, "/v1/package-group-allowed-repositories"}, {http.MethodPut, "/v1/package-group-origin-configuration"},
	}

	for _, c := range positive {
		if !h.Matches(httptest.NewRequest(c.method, c.path, nil)) {
			t.Errorf("Matches(%s %s) = false", c.method, c.path)
		}
	}

	for _, p := range []string{"/v1/tag", "/v1/tags", "/v1/untag"} {
		if !h.Matches(httptest.NewRequest(http.MethodPost, p+ca, nil)) {
			t.Errorf("Matches(POST %s with a CodeArtifact ARN) = false", p)
		}
	}

	negative := []struct{ method, path string }{
		{http.MethodPost, "/v1/tags?resourceArn=arn:aws:mq:us-east-1:1:broker:b-1"},
		{http.MethodPost, "/v1/tag"},
		{http.MethodGet, "/v1/tags/arn%3Aaws%3Amq%3Aus-east-1%3A1%3Abroker%3Ab-1"},
		{http.MethodPost, "/v1/brokers"}, {http.MethodGet, "/v1/apis"}, {http.MethodGet, "/v1/clusters"},
		{http.MethodGet, "/v1/domain/unknown"}, {http.MethodGet, "/v1/package/unknown"}, {http.MethodGet, "/v1/package-group/unknown"},
		{http.MethodGet, "/v1"}, {http.MethodGet, "/v1/"}, {http.MethodGet, "/bucket/v1/domain"}, {http.MethodGet, "/some-bucket/key"},
		{http.MethodGet, "/2015-03-31/functions"},
	}

	for _, c := range negative {
		if h.Matches(httptest.NewRequest(c.method, c.path, nil)) {
			t.Errorf("Matches(%s %s) = true, want false", c.method, c.path)
		}
	}
}

func TestWrongVerbAndUnknownPath(t *testing.T) {
	h := fullHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/domain", nil))

	if rec.Code != http.StatusBadRequest || rec.Header().Get("X-Amzn-Errortype") != "ValidationException" {
		t.Fatalf("wrong verb: %d %s", rec.Code, rec.Header().Get("X-Amzn-Errortype"))
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/nothing", nil))

	if rec.Code != http.StatusNotFound || rec.Header().Get("X-Amzn-Errortype") != "ResourceNotFoundException" {
		t.Fatalf("unknown path: %d %s", rec.Code, rec.Header().Get("X-Amzn-Errortype"))
	}
}

func TestOperationNameUnknown(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/nothing"}, {http.MethodPatch, "/v1/domain"}, {http.MethodGet, ""},
	} {
		if got := codeartifact.OperationName(c.method, c.path); got != "" {
			t.Errorf("OperationName(%s %s) = %q, want empty", c.method, c.path, got)
		}
	}
}
