package sagemaker

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

func TestIAMChecksFollowsDispatch(t *testing.T) {
	h := New(nil)

	target := func(r *http.Request, op string) *http.Request {
		r.Header.Set("X-Amz-Target", targetPrefix+op)
		return r
	}

	cases := []struct {
		name string
		req  *http.Request
		want string
	}{
		{"control plane", target(httptest.NewRequest(http.MethodPost, "/", nil), "ListModels"), "sagemaker:ListModels"},
		{"invoke", httptest.NewRequest(http.MethodPost, "/endpoints/e/invocations", nil), "sagemaker:InvokeEndpoint"},
		{"invoke async", httptest.NewRequest(http.MethodPost, "/endpoints/e/async-invocations", nil), "sagemaker:InvokeEndpointAsync"},
		// The runtime path wins over the target in dispatch, so it does here.
		{"target on a runtime path", target(httptest.NewRequest(http.MethodPost, "/endpoints/e/invocations", nil), "ListModels"),
			"sagemaker:InvokeEndpoint"},
		{"put record", httptest.NewRequest(http.MethodPut, "/FeatureGroup/g", nil), "sagemaker:PutRecord"},
		{"get record", httptest.NewRequest(http.MethodGet, "/FeatureGroup/g", nil), "sagemaker:GetRecord"},
		{"delete record", httptest.NewRequest(http.MethodDelete, "/FeatureGroup/g", nil), "sagemaker:DeleteRecord"},
	}

	for _, tc := range cases {
		checks, ok := h.IAMChecks(tc.req, awsauthz.Scope{})
		if !ok || len(checks) != 1 || checks[0].Action != tc.want {
			t.Errorf("%s: got %+v ok=%v, want %s", tc.name, checks, ok, tc.want)
		}
	}

	for name, req := range map[string]*http.Request{
		"runtime GET":        httptest.NewRequest(http.MethodGet, "/endpoints/e/invocations", nil),
		"feature store POST": httptest.NewRequest(http.MethodPost, "/FeatureGroup/g", nil),
		"no target":          httptest.NewRequest(http.MethodPost, "/", nil),
	} {
		if checks, ok := h.IAMChecks(req, awsauthz.Scope{}); ok {
			t.Errorf("%s: got %+v, want ok=false", name, checks)
		}
	}
}
