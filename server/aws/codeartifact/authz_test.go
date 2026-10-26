package codeartifact_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

var testScope = awsauthz.Scope{AccountID: "123456789012", Region: "us-east-1", Partition: "aws"}

func TestIAMChecksCoverEveryOperation(t *testing.T) {
	ops := loadModel(t)
	h := fullHandler()

	for name, op := range ops {
		req := httptest.NewRequest(op.Method, op.Path+"?"+validationProbeQuery(name), strings.NewReader(validationProbe(name).body))

		checks, ok := h.IAMChecks(req, testScope)
		if !ok || len(checks) == 0 {
			t.Errorf("%s: no IAM checks", name)

			continue
		}

		if checks[0].Action != "codeartifact:"+name {
			t.Errorf("%s: action = %q", name, checks[0].Action)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/nothing", nil)
	if _, ok := h.IAMChecks(req, testScope); ok {
		t.Error("an unknown path must not resolve")
	}
}

func validationProbeQuery(op string) string {
	return validationProbe(op).query.Encode()
}

func TestIAMChecksNameTheResource(t *testing.T) {
	h := fullHandler()
	prefix := "arn:aws:codeartifact:us-east-1:123456789012:"

	cases := []struct {
		name, method, path string
		query              url.Values
		body               string
		resource           string
	}{
		{"domain", http.MethodGet, "/v1/domain", url.Values{"domain": {"dom"}}, "", prefix + "domain/dom"},
		{"repository", http.MethodGet, "/v1/repository", url.Values{"domain": {"dom"}, "repository": {"repo"}}, "", prefix + "repository/dom/repo"},
		{"package", http.MethodGet, "/v1/package", url.Values{"domain": {"dom"}, "repository": {"repo"}, "format": {"generic"}, "namespace": {"ns"}, "package": {"p"}}, "", prefix + "package/dom/repo/generic/ns/p"},
		{"npm package without namespace", http.MethodGet, "/v1/package", url.Values{"domain": {"dom"}, "repository": {"repo"}, "format": {"npm"}, "package": {"p"}}, "", prefix + "package/dom/repo/npm//p"},
		{"copy targets the destination", http.MethodPost, "/v1/package/versions/copy", url.Values{"domain": {"dom"}, "destination-repository": {"dst"}, "source-repository": {"src"}, "format": {"npm"}, "package": {"p"}}, "", prefix + "package/dom/dst/npm//p"},
		{"group", http.MethodGet, "/v1/package-group", url.Values{"domain": {"dom"}, "package-group": {"/npm/*"}}, "", prefix + "package-group/dom/npm/*"},
		{"group update reads the body", http.MethodPut, "/v1/package-group", url.Values{"domain": {"dom"}}, `{"packageGroup":"/npm/space/*"}`, prefix + "package-group/dom/npm/space/*"},
		{"domain policy put reads the body", http.MethodPut, "/v1/domain/permissions/policy", nil, `{"domain":"dom","policyDocument":"{}"}`, prefix + "domain/dom"},
		{"tags use the ARN", http.MethodPost, "/v1/tags", url.Values{"resourceArn": {prefix + "domain/dom"}}, "", prefix + "domain/dom"},
		{"list domains has no resource", http.MethodPost, "/v1/domains", nil, "{}", "*"},
		{"missing names are unresolved", http.MethodGet, "/v1/repository", url.Values{"domain": {"dom"}}, "", ""},
	}

	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path+"?"+c.query.Encode(), strings.NewReader(c.body))

		checks, ok := h.IAMChecks(req, testScope)
		if !ok || checks[0].Resource != c.resource {
			t.Errorf("%s: resource = %q (ok=%v), want %q", c.name, firstResource(checks), ok, c.resource)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/authorization-token?domain=dom", nil)

	checks, _ := h.IAMChecks(req, testScope)
	if len(checks) != 2 || checks[1].Action != "sts:GetServiceBearerToken" {
		t.Errorf("GetAuthorizationToken checks = %+v, want codeartifact + sts:GetServiceBearerToken", checks)
	}
}

func firstResource(checks []awsauthz.Check) string {
	if len(checks) == 0 {
		return ""
	}

	return checks[0].Resource
}

func TestIAMChecksPutPolicyBodyStillReadableByHandler(t *testing.T) {
	h := fullHandler()
	body := `{"domain":"dom","policyDocument":"{}"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/domain/permissions/policy", strings.NewReader(body))

	if _, ok := h.IAMChecks(req, testScope); !ok {
		t.Fatal("resolver refused")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// The body survived the resolver: the handler saw a domain, not an empty request.
	if strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("handler could not read the body after the resolver: %s", rec.Body.String())
	}
}

func TestCopyNeedsReadOnTheSourceRepository(t *testing.T) {
	h := fullHandler()
	prefix := "arn:aws:codeartifact:us-east-1:123456789012:"

	req := httptest.NewRequest(http.MethodPost, "/v1/package/versions/copy?"+url.Values{
		"domain": {"dom"}, "source-repository": {"src"}, "destination-repository": {"dst"}, "format": {"npm"}, "package": {"p"},
	}.Encode(), strings.NewReader("{}"))

	checks, ok := h.IAMChecks(req, testScope)
	if !ok || len(checks) != 2 {
		t.Fatalf("checks = %+v, want the copy action and a read on the source", checks)
	}

	if checks[0].Action != "codeartifact:CopyPackageVersions" || checks[0].Resource != prefix+"package/dom/dst/npm//p" {
		t.Errorf("copy check = %+v", checks[0])
	}

	if checks[1].Action != "codeartifact:ReadFromRepository" || checks[1].Resource != prefix+"repository/dom/src" {
		t.Errorf("source read check = %+v", checks[1])
	}

	noSource := httptest.NewRequest(http.MethodPost, "/v1/package/versions/copy?domain=dom&destination-repository=dst&format=npm&package=p", strings.NewReader("{}"))

	checks, _ = h.IAMChecks(noSource, testScope)
	if len(checks) != 2 || checks[1].Resource != "" {
		t.Errorf("a copy that names no source must leave the read unresolved (fail closed): %+v", checks)
	}
}
