package apimanagement_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestWireRoutesAndErrors walks the handler's routing and error mapping over
// raw ARM requests: unsupported paths and methods, missing parents, malformed
// bodies, and the deleted-services and checkNameAvailability endpoints.
func TestWireRoutesAndErrors(t *testing.T) {
	f := newFixture(t)
	f.create(t, rgName, svcName, developerService())

	svc := f.serviceURL(rgName, svcName)
	missing := f.serviceURL(rgName, "missing-svc")
	subProv := f.ts.URL + "/subscriptions/" + subID + "/providers/Microsoft.ApiManagement"

	cases := []struct {
		name, method, url, body string
		hdr                     map[string]string
		status                  int
		contains                string
	}{
		{"service bad method", http.MethodPost, svc, "", nil, http.StatusMethodNotAllowed, "MethodNotAllowed"},
		{"list bad method", http.MethodPost, f.ts.URL + "/subscriptions/" + subID + "/resourceGroups/" + rgName +
			"/providers/Microsoft.ApiManagement/service", "", nil, http.StatusMethodNotAllowed, "MethodNotAllowed"},
		{"subscription list", http.MethodGet, subProv + "/service", "", nil, http.StatusOK, svcName},
		{"malformed body", http.MethodPut, svc, "{", nil, http.StatusBadRequest, "InvalidRequestContent"},
		{"patch malformed body", http.MethodPatch, svc, "{", nil, http.StatusBadRequest, "InvalidRequestContent"},
		{"patch missing", http.MethodPatch, missing, "{}", nil, http.StatusNotFound, "ResourceNotFound"},
		{"delete missing", http.MethodDelete, missing, "", nil, http.StatusNoContent, ""},
		{"unsupported child", http.MethodGet, svc + "/backends", "", nil, http.StatusNotFound, "InvalidResourceType"},
		{"api operations", http.MethodGet, svc + "/apis/echo-api/operations", "", nil, http.StatusNotFound, "InvalidResourceType"},
		{"api bad method", http.MethodPut, svc + "/apis/echo-api", "{}", nil, http.StatusMethodNotAllowed, "MethodNotAllowed"},
		{"api head", http.MethodHead, svc + "/apis/echo-api", "", nil, http.StatusOK, ""},
		{"api missing", http.MethodGet, svc + "/apis/nope", "", nil, http.StatusNotFound, "ResourceNotFound"},
		{"apis of missing svc", http.MethodGet, missing + "/apis", "", nil, http.StatusNotFound, "ResourceNotFound"},
		{"api delete of missing svc", http.MethodDelete, missing + "/apis/x", "", nil, http.StatusNotFound, "ResourceNotFound"},
		{"product stale delete", http.MethodDelete, svc + "/products/starter", "",
			map[string]string{"If-Match": `"old"`}, http.StatusPreconditionFailed, "PreconditionFailed"},
		{"product get", http.MethodGet, svc + "/products/unlimited", "", nil, http.StatusOK, `"approvalRequired":true`},
		{"policies list empty", http.MethodGet, svc + "/policies", "", nil, http.StatusOK, `"count":0`},
		{"policies list missing svc", http.MethodGet, missing + "/policies", "", nil, http.StatusNotFound, "ResourceNotFound"},
		{"policy bad name", http.MethodGet, svc + "/policies/other", "", nil, http.StatusNotFound, "InvalidResourceType"},
		{"policy bad method", http.MethodPatch, svc + "/policies/policy", "{}", nil, http.StatusMethodNotAllowed, "MethodNotAllowed"},
		{"policy malformed", http.MethodPut, svc + "/policies/policy", "{", nil, http.StatusBadRequest, "InvalidRequestContent"},
		{"policy put missing svc", http.MethodPut, missing + "/policies/policy",
			`{"properties":{"value":"<policies/>"}}`, nil, http.StatusNotFound, "ResourceNotFound"},
		{"policy delete missing svc", http.MethodDelete, missing + "/policies/policy", "", nil, http.StatusNotFound, "ResourceNotFound"},
		{"policy delete none", http.MethodDelete, svc + "/policies/policy", "", nil, http.StatusNoContent, ""},
		{"portal deep path", http.MethodGet, svc + "/portalsettings/signin/x/y", "", nil, http.StatusNotFound, "InvalidResourceType"},
		{"portal bad method", http.MethodDelete, svc + "/portalsettings/signin", "", nil, http.StatusMethodNotAllowed, "MethodNotAllowed"},
		{"portal malformed", http.MethodPut, svc + "/portalsettings/signin", "{", nil, http.StatusBadRequest, "InvalidRequestContent"},
		{"portal put missing svc", http.MethodPut, missing + "/portalsettings/signin", `{"properties":{}}`, nil,
			http.StatusNotFound, "ResourceNotFound"},
		{"portal get missing", http.MethodGet, svc + "/portalsettings/nope", "", nil, http.StatusNotFound, "ResourceNotFound"},
		{"portal head", http.MethodHead, svc + "/portalsettings/signup", "", nil, http.StatusOK, ""},
		{"delegation secrets missing svc", http.MethodPost, missing + "/portalsettings/delegation/listSecrets", "", nil,
			http.StatusNotFound, "ResourceNotFound"},
		{"tenant bad method", http.MethodPut, svc + "/tenant/access", "{}", nil, http.StatusMethodNotAllowed, "MethodNotAllowed"},
		{"tenant deep path", http.MethodGet, svc + "/tenant/access/x/y", "", nil, http.StatusNotFound, "InvalidResourceType"},
		{"tenant get missing", http.MethodGet, svc + "/tenant/nope", "", nil, http.StatusNotFound, "ResourceNotFound"},
		{"tenant secrets missing", http.MethodPost, svc + "/tenant/nope/listSecrets", "", nil, http.StatusNotFound, "ResourceNotFound"},
		{"tenant patch malformed", http.MethodPatch, svc + "/tenant/access", "{", nil, http.StatusBadRequest, "InvalidRequestContent"},
		{"tenant patch stale", http.MethodPatch, svc + "/tenant/access", `{"properties":{"enabled":true}}`,
			map[string]string{"If-Match": `"old"`}, http.StatusPreconditionFailed, "PreconditionFailed"},
		{"deleted list", http.MethodGet, subProv + "/deletedservices", "", nil, http.StatusOK, `"value":[]`},
		{"deleted list bad method", http.MethodPost, subProv + "/deletedservices", "", nil, http.StatusMethodNotAllowed, "MethodNotAllowed"},
		{"deleted no name", http.MethodGet, subProv + "/locations/eastus/deletedservices", "", nil, http.StatusNotFound, "InvalidResourceType"},
		{"deleted bad method", http.MethodPut, subProv + "/locations/eastus/deletedservices/x", "{}", nil,
			http.StatusMethodNotAllowed, "MethodNotAllowed"},
		{"deleted purge missing", http.MethodDelete, subProv + "/locations/eastus/deletedservices/x", "", nil,
			http.StatusNotFound, "ResourceNotFound"},
		{"check name bad method", http.MethodGet, subProv + "/checkNameAvailability", "", nil, http.StatusMethodNotAllowed, "MethodNotAllowed"},
		{"check name malformed", http.MethodPost, subProv + "/checkNameAvailability", "{", nil, http.StatusBadRequest, "InvalidRequestContent"},
		{"other locations type", http.MethodGet, subProv + "/locations/eastus/operationResults/x", "", nil, http.StatusNotImplemented, ""},
		{"no rg on PUT", http.MethodPut, subProv + "/service/x", "{}", nil, http.StatusBadRequest, "InvalidPath"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body, _ := f.do(t, tc.method, tc.url+apiVersion, tc.body, tc.hdr)
			if code != tc.status || !strings.Contains(body, tc.contains) {
				t.Fatalf("%s %s = %d %s, want %d containing %q", tc.method, tc.url, code, body, tc.status, tc.contains)
			}
		})
	}
}

// TestWireUserAssignedIdentity round-trips a user-assigned identity.
func TestWireUserAssignedIdentity(t *testing.T) {
	f := newFixture(t)

	uai := "/subscriptions/" + subID + "/resourceGroups/" + rgName +
		"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1"
	body := `{"location":"eastus","sku":{"name":"Developer","capacity":1},` +
		`"identity":{"type":"UserAssigned","userAssignedIdentities":{"` + uai + `":{}}},` +
		`"properties":{"publisherEmail":"a@b.test","publisherName":"n"}}`

	code, resp, _ := f.do(t, http.MethodPut, f.serviceURL(rgName, "uai-svc")+apiVersion, body, nil)
	if code != http.StatusCreated || !strings.Contains(resp, "clientId") {
		t.Fatalf("PUT = %d %s", code, resp)
	}
}
