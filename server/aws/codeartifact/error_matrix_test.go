package codeartifact_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// probe is a request the matrix sends: a verb and path from the API model, a
// query string and a JSON body.
type probe struct {
	query url.Values
	body  string
	// header sets extra request headers.
	header map[string]string
}

const (
	policyDoc  = `{\"Version\":\"2012-10-17\",\"Statement\":[]}`
	xSHA256    = "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881" // sha256("x")
	invalidDom = "Bad Domain"
	ghostDom   = "ghost-domain"
)

// validationProbe returns a request for op that carries one invalid field.
func validationProbe(op string) probe {
	q := url.Values{"domain": {invalidDom}, "repository": {"ghost-repo"}, "format": {"npm"}, "package": {"p"}, "version": {"1"},
		"asset": {"a"}, "package-group": {"/npm/*"}, "external-connection": {"public:npmjs"}}

	switch op {
	case "ListDomains":
		return probe{body: `{"maxResults":5000}`}
	case "ListRepositories":
		return probe{query: url.Values{"max-results": {"5000"}}}
	case "TagResource", "UntagResource", "ListTagsForResource":
		return probe{query: url.Values{"resourceArn": {"arn:aws:codeartifact:us-east-1:123456789012:thing/x"}}, body: `{"tags":[],"tagKeys":[]}`}
	case "PutDomainPermissionsPolicy":
		return probe{body: `{"domain":"` + invalidDom + `","policyDocument":"` + policyDoc + `"}`}
	}

	return probe{query: q, body: bodyFor(op)}
}

// ghostProbe returns a request for op that names a domain that does not exist.
func ghostProbe(op string) probe {
	q := url.Values{
		"domain": {ghostDom}, "repository": {"ghost-repo"}, "format": {"generic"}, "namespace": {"ns"}, "package": {"p"}, "version": {"1"},
		"asset": {"a"}, "package-group": {"/npm/*"}, "external-connection": {"public:npmjs"}, "source-repository": {"ghost-repo"},
		"destination-repository": {"other-repo"}, "originRestrictionType": {"PUBLISH"}, "duration": {"900"},
	}

	switch op {
	case "TagResource", "UntagResource", "ListTagsForResource":
		return probe{query: url.Values{"resourceArn": {"arn:aws:codeartifact:us-east-1:123456789012:domain/" + ghostDom}}, body: `{"tags":[],"tagKeys":[]}`}
	case "PutDomainPermissionsPolicy":
		return probe{body: `{"domain":"` + ghostDom + `","policyDocument":"` + policyDoc + `"}`}
	case "PublishPackageVersion":
		return probe{query: q, body: "x", header: map[string]string{"x-amz-content-sha256": xSHA256}}
	}

	return probe{query: q, body: bodyFor(op)}
}

// bodyFor returns a request body that is well formed for op.
func bodyFor(op string) string {
	switch op {
	case "CopyPackageVersions", "DisposePackageVersions", "DeletePackageVersions":
		return `{"versions":["1"]}`
	case "UpdatePackageVersionsStatus":
		return `{"versions":["1"],"targetStatus":"Archived"}`
	case "PutRepositoryPermissionsPolicy":
		return `{"policyDocument":"` + policyDoc + `"}`
	case "PutPackageOriginConfiguration":
		return `{"restrictions":{"publish":"ALLOW","upstream":"ALLOW"}}`
	case "CreatePackageGroup", "UpdatePackageGroup":
		return `{"packageGroup":"/npm/*"}`
	case "PublishPackageVersion":
		return "x"
	default:
		return "{}"
	}
}

func send(h http.Handler, method, path string, p probe) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path+"?"+p.query.Encode(), strings.NewReader(p.body))
	for k, v := range p.header {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}

	return false
}

// TestErrorMatrixPerOperation drives every one of the 48 operations into a
// validation failure and, where the API models ResourceNotFoundException, into a
// missing-domain failure, and checks the exception the wire answers is one the
// operation's model lists, with the exact HTTP status and a JSON message.
func TestErrorMatrixPerOperation(t *testing.T) {
	ops := loadModel(t)
	h := fullHandler()
	names := make([]string, 0, len(ops))

	for name := range ops {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		op := ops[name]

		rec := send(h, op.Method, op.Path, validationProbe(name))
		checkError(t, name+"/validation", rec, "ValidationException", http.StatusBadRequest, op.Errors)

		if name == "CreateDomain" || !contains(op.Errors, "ResourceNotFoundException") {
			continue
		}

		rec = send(h, op.Method, op.Path, ghostProbe(name))
		checkError(t, name+"/not-found", rec, "ResourceNotFoundException", http.StatusNotFound, op.Errors)
	}
}

func checkError(t *testing.T, label string, rec *httptest.ResponseRecorder, want string, status int, modelled []string) {
	t.Helper()

	got := rec.Header().Get("X-Amzn-Errortype")

	if got != want || rec.Code != status {
		t.Errorf("%s: got %d %q, want %d %q (%s)", label, rec.Code, got, status, want, strings.TrimSpace(rec.Body.String()))

		return
	}

	if !contains(modelled, got) {
		t.Errorf("%s: %s is not modelled for this operation (%v)", label, got, modelled)
	}

	var body struct {
		Message string `json:"message"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Message == "" {
		t.Errorf("%s: body %q has no message", label, rec.Body.String())
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: content-type %q", label, ct)
	}
}
