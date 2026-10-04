package ec2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

func queryRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", formContentType)

	return r
}

func TestIAMChecksSplitsAutoScaling(t *testing.T) {
	h := New(nil, nil, "123456789012")

	cases := map[string]string{
		"Action=RunInstances":           "ec2:RunInstances",
		"Action=DescribeVpcs":           "ec2:DescribeVpcs",
		"Action=NoSuchAction":           "ec2:NoSuchAction",
		"Action=CreateUser":             "ec2:CreateUser",
		"Action=CreateOrUpdateTags":     "ec2:CreateOrUpdateTags",
		"Action=CreateAutoScalingGroup": "autoscaling:CreateAutoScalingGroup",
	}

	for action := range autoScalingRoutes {
		cases["Action="+action] = "autoscaling:" + action
	}

	for body, want := range cases {
		checks, ok := h.IAMChecks(queryRequest(body), awsauthz.Scope{})
		if !ok || len(checks) != 1 || checks[0].Action != want || checks[0].Resource != "" {
			t.Errorf("%s: got %+v ok=%v, want %s on an unknown resource", body, checks, ok, want)
		}
	}

	for _, body := range []string{"Version=2016-11-15", "Action=RunInstances&x=%zz"} {
		if checks, ok := h.IAMChecks(queryRequest(body), awsauthz.Scope{}); ok {
			t.Errorf("%s: got %+v, want ok=false", body, checks)
		}
	}
}

func TestWriteAccessDeniedShape(t *testing.T) {
	h := New(nil, nil, "123456789012")

	for body, code := range map[string]string{
		"Action=RunInstances":           "<Code>UnauthorizedOperation</Code>",
		"Action=CreateAutoScalingGroup": "<Code>AccessDenied</Code>",
	} {
		// The gate passes the request IAMChecks already parsed.
		req := queryRequest(body)
		if err := req.ParseForm(); err != nil {
			t.Fatalf("parse: %v", err)
		}

		rec := httptest.NewRecorder()
		h.WriteAccessDenied(rec, req, "User: u is not authorized")

		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), code) ||
			!strings.Contains(rec.Body.String(), "<Response><Errors><Error>") {
			t.Errorf("%s: %d %s, want 403 %s in the EC2 envelope", body, rec.Code, rec.Body, code)
		}
	}
}
