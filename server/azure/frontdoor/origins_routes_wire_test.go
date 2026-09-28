package frontdoor_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Wire-level checks for origins and routes: status codes, ARM error codes,
// stored casing and ETag rotation as a client sees them.

const (
	wireEP = "ep-1"
	wireOG = "og-1"
)

func ogURL(base, og string) string { return profileURL(base, profile) + "/originGroups/" + og }

func originURL(base, name string) string { return ogURL(base, wireOG) + "/origins/" + name }

func routeURL(base, name string) string {
	return profileURL(base, profile) + "/afdEndpoints/" + wireEP + "/routes/" + name
}

func ogRef(sub, rg, prof, og string) map[string]any {
	return map[string]any{"id": "/subscriptions/" + sub + "/resourceGroups/" + rg +
		"/providers/Microsoft.Cdn/profiles/" + prof + "/originGroups/" + og}
}

// seedWireChain creates profile -> endpoint + origin group -> origin, plus a
// route forwarding to the group on the default domain for "/*".
func seedWireChain(t *testing.T) *httptest.Server {
	t.Helper()

	ts := newServer(t)
	createProfile(t, ts, profile)

	mustStatus(t, ts, http.MethodPut, profileURL(ts.URL, profile)+"/afdEndpoints/"+wireEP,
		map[string]any{"location": "global"}, http.StatusCreated)
	mustStatus(t, ts, http.MethodPut, ogURL(ts.URL, wireOG),
		map[string]any{"properties": map[string]any{}}, http.StatusCreated)
	mustStatus(t, ts, http.MethodPut, originURL(ts.URL, "o-1"),
		map[string]any{"properties": map[string]any{"hostName": "app.example.net"}}, http.StatusCreated)
	mustStatus(t, ts, http.MethodPut, routeURL(ts.URL, "r-1"), routeBody("/*"), http.StatusCreated)

	return ts
}

func routeBody(patterns ...any) map[string]any {
	return map[string]any{"properties": map[string]any{
		"originGroup":         ogRef(testSub, testRG, profile, wireOG),
		"linkToDefaultDomain": "Enabled",
		"patternsToMatch":     patterns,
	}}
}

func mustStatus(t *testing.T, ts *httptest.Server, method, url string, body any, want int) map[string]any {
	t.Helper()

	status, out := do(t, ts, method, url, body)
	if status != want {
		t.Fatalf("%s %s = %d %v, want %d", method, url, status, out, want)
	}

	return out
}

// wantARMError asserts status and the ARM error code, and that the message
// contains msg.
func wantARMError(t *testing.T, ts *httptest.Server, method, url string, body any, status int, code, msg string) {
	t.Helper()

	out := mustStatus(t, ts, method, url, body, status)

	e, _ := out["error"].(map[string]any)
	if e["code"] != code || !strings.Contains(e["message"].(string), msg) {
		t.Errorf("%s %s error = %v, want code %s containing %q", method, url, e, code, msg)
	}
}

func TestWireOriginDefaultsAndValidation(t *testing.T) {
	ts := seedWireChain(t)

	p := props(t, mustStatus(t, ts, http.MethodGet, originURL(ts.URL, "o-1"), nil, http.StatusOK))
	if p["httpPort"] != float64(80) || p["httpsPort"] != float64(443) ||
		p["enforceCertificateNameCheck"] != true || p["enabledState"] != "Enabled" {
		t.Errorf("origin defaults = %v", p)
	}

	wantARMError(t, ts, http.MethodPut, originURL(ts.URL, "o-2"),
		map[string]any{"properties": map[string]any{"hostName": "h", "weight": 0}},
		http.StatusBadRequest, "InvalidParameter", "weight")

	r := props(t, mustStatus(t, ts, http.MethodGet, routeURL(ts.URL, "r-1"), nil, http.StatusOK))
	if r["forwardingProtocol"] != "MatchRequest" || r["httpsRedirect"] != "Disabled" {
		t.Errorf("route defaults = %v", r)
	}

	body := routeBody("/api/*")
	body["properties"].(map[string]any)["forwardingProtocol"] = "bogus"
	wantARMError(t, ts, http.MethodPut, routeURL(ts.URL, "r-2"), body,
		http.StatusBadRequest, "InvalidParameter", "forwardingProtocol")
}

func TestWireParentResourceNotFound(t *testing.T) {
	ts := seedWireChain(t)

	wantARMError(t, ts, http.MethodPut, ogURL(ts.URL, "no-such-og")+"/origins/o",
		map[string]any{"properties": map[string]any{"hostName": "h"}},
		http.StatusNotFound, "ParentResourceNotFound", "no-such-og")

	wantARMError(t, ts, http.MethodPut, profileURL(ts.URL, profile)+"/afdEndpoints/no-such-ep/routes/r",
		routeBody("/*"), http.StatusNotFound, "ParentResourceNotFound", "no-such-ep")

	// A missing grandchild itself is still ResourceNotFound.
	wantARMError(t, ts, http.MethodGet, originURL(ts.URL, "nope"), nil,
		http.StatusNotFound, "ResourceNotFound", "nope")
}

func TestWireDeleteMissingIs204(t *testing.T) {
	ts := seedWireChain(t)

	mustStatus(t, ts, http.MethodDelete, originURL(ts.URL, "nope"), nil, http.StatusNoContent)
	mustStatus(t, ts, http.MethodDelete, routeURL(ts.URL, "nope"), nil, http.StatusNoContent)

	// An existing one answers 200, and a repeat 204.
	mustStatus(t, ts, http.MethodDelete, routeURL(ts.URL, "r-1"), nil, http.StatusOK)
	mustStatus(t, ts, http.MethodDelete, routeURL(ts.URL, "r-1"), nil, http.StatusNoContent)
}

func TestWireLastOriginAndInUseGroupAre400(t *testing.T) {
	ts := seedWireChain(t)

	const lastOrigin = "Cannot disable or delete the last origin"

	wantARMError(t, ts, http.MethodDelete, originURL(ts.URL, "o-1"), nil,
		http.StatusBadRequest, "BadRequest", lastOrigin)
	wantARMError(t, ts, http.MethodPatch, originURL(ts.URL, "o-1"),
		map[string]any{"properties": map[string]any{"enabledState": "Disabled"}},
		http.StatusBadRequest, "BadRequest", lastOrigin)
	wantARMError(t, ts, http.MethodDelete, ogURL(ts.URL, wireOG), nil,
		http.StatusBadRequest, "BadRequest", "in use by route")

	// The origin is untouched by the refused PATCH.
	p := props(t, mustStatus(t, ts, http.MethodGet, originURL(ts.URL, "o-1"), nil, http.StatusOK))
	if p["enabledState"] != "Enabled" {
		t.Errorf("refused PATCH changed enabledState to %v", p["enabledState"])
	}
}

func TestWireRouteConflict(t *testing.T) {
	ts := seedWireChain(t)

	const conflict = "The route domains, paths and protocols configuration has a conflict"

	wantARMError(t, ts, http.MethodPut, routeURL(ts.URL, "r-2"), routeBody("/*"),
		http.StatusBadRequest, "BadRequest", conflict)

	mustStatus(t, ts, http.MethodPut, routeURL(ts.URL, "r-2"), routeBody("/api/*"), http.StatusCreated)

	wantARMError(t, ts, http.MethodPatch, routeURL(ts.URL, "r-2"),
		map[string]any{"properties": map[string]any{"patternsToMatch": []any{"/*"}}},
		http.StatusBadRequest, "BadRequest", conflict)
}

func TestWireRouteReferences(t *testing.T) {
	ts := seedWireChain(t)

	// A second profile with a same-named origin group: its id is not usable here.
	createProfile(t, ts, "profile-2")
	mustStatus(t, ts, http.MethodPut, profileURL(ts.URL, "profile-2")+"/originGroups/"+wireOG,
		map[string]any{"properties": map[string]any{}}, http.StatusCreated)

	crossProfile := map[string]any{"originGroup": ogRef(testSub, testRG, "profile-2", wireOG)}

	wantARMError(t, ts, http.MethodPut, routeURL(ts.URL, "r-x"),
		map[string]any{"properties": crossProfile}, http.StatusBadRequest, "InvalidParameter", "must belong to profile")
	wantARMError(t, ts, http.MethodPatch, routeURL(ts.URL, "r-1"),
		map[string]any{"properties": crossProfile}, http.StatusBadRequest, "InvalidParameter", "must belong to profile")

	// PATCH repointing at a missing group in the same profile.
	wantARMError(t, ts, http.MethodPatch, routeURL(ts.URL, "r-1"),
		map[string]any{"properties": map[string]any{"originGroup": ogRef(testSub, testRG, profile, "missing")}},
		http.StatusBadRequest, "InvalidParameter", "does not exist")

	// customDomains / ruleSets cannot resolve: neither resource is modeled.
	for _, key := range []string{"customDomains", "ruleSets"} {
		body := routeBody("/other/*")
		body["properties"].(map[string]any)[key] = []any{map[string]any{"id": "/subscriptions/x/whatever"}}
		wantARMError(t, ts, http.MethodPut, routeURL(ts.URL, "r-"+key), body,
			http.StatusBadRequest, "InvalidParameter", key)
	}

	// The refused PATCHes left the route on its original group.
	p := props(t, mustStatus(t, ts, http.MethodGet, routeURL(ts.URL, "r-1"), nil, http.StatusOK))

	og, _ := p["originGroup"].(map[string]any)
	if !strings.HasSuffix(og["id"].(string), "/profiles/"+profile+"/originGroups/"+wireOG) {
		t.Errorf("route originGroup after refused PATCHes = %v", og)
	}
}

func TestWireReadBackUsesStoredCasingAndRotatesETag(t *testing.T) {
	ts := seedWireChain(t)

	lower := mustStatus(t, ts, http.MethodGet, routeURL(ts.URL, "r-1"), nil, http.StatusOK)

	upperURL := ts.URL + "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Cdn/profiles/" + strings.ToUpper(profile) + "/AFDENDPOINTS/" +
		strings.ToUpper(wireEP) + "/ROUTES/R-1"
	upper := mustStatus(t, ts, http.MethodGet, upperURL, nil, http.StatusOK)

	if upper["id"] != lower["id"] || upper["name"] != "r-1" || upper["etag"] != lower["etag"] ||
		props(t, upper)["endpointName"] != wireEP {
		t.Errorf("upper-case GET = id %v name %v etag %v endpoint %v; want %v r-1 %v %s",
			upper["id"], upper["name"], upper["etag"], props(t, upper)["endpointName"],
			lower["id"], lower["etag"], wireEP)
	}

	o1 := mustStatus(t, ts, http.MethodGet, originURL(ts.URL, "o-1"), nil, http.StatusOK)
	o2 := mustStatus(t, ts, http.MethodPut, originURL(ts.URL, "o-1"),
		map[string]any{"properties": map[string]any{"hostName": "other.example.net"}}, http.StatusOK)
	o3 := mustStatus(t, ts, http.MethodPatch, originURL(ts.URL, "o-1"),
		map[string]any{"properties": map[string]any{"weight": 10}}, http.StatusOK)

	if o1["etag"] == o2["etag"] || o2["etag"] == o3["etag"] {
		t.Errorf("origin etag did not rotate: %v, %v, %v", o1["etag"], o2["etag"], o3["etag"])
	}

	if props(t, o3)["hostName"] != "other.example.net" {
		t.Errorf("PATCH lost the replaced hostName: %v", props(t, o3))
	}
}
