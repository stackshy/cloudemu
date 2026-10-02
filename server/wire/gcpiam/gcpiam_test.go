package gcpiam_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/gcp/resourceiam"
	"github.com/stackshy/cloudemu/v2/server/wire/gcpiam"
)

const res = "projects/p/managedZones/z"

func serve(t *testing.T, s gcpiam.Store, method, verb, query, body string) (int, string) {
	t.Helper()

	r := httptest.NewRequest(method, "/x:"+verb+query, strings.NewReader(body))
	w := httptest.NewRecorder()
	gcpiam.Serve(w, r, verb, res, s)

	return w.Code, w.Body.String()
}

func etagOf(t *testing.T, body string) string {
	t.Helper()

	var p resourceiam.Policy
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	return p.Etag
}

func TestServeVerbs(t *testing.T) {
	tests := []struct {
		name   string
		method string
		verb   string
		query  string
		body   string
		want   int
		marker string
	}{
		{"get by GET", http.MethodGet, gcpiam.VerbGet, "?optionsRequestedPolicyVersion=3", "", http.StatusOK, `"version":1`},
		{"get by POST", http.MethodPost, gcpiam.VerbGet, "", `{"options":{"requestedPolicyVersion":3}}`, http.StatusOK, `"etag":"CAE="`},
		{"set by GET", http.MethodGet, gcpiam.VerbSet, "", "", http.StatusMethodNotAllowed, ""},
		{"get by PUT", http.MethodPut, gcpiam.VerbGet, "", "", http.StatusMethodNotAllowed, ""},
		{"test echoes", http.MethodPost, gcpiam.VerbTest, "", `{"permissions":["dns.managedZones.get"]}`, http.StatusOK, "dns.managedZones.get"},
		{"conditional v1 set", http.MethodPost, gcpiam.VerbSet, "",
			`{"policy":{"bindings":[{"role":"r","members":["user:a"],"condition":{"expression":"true"}}]}}`,
			http.StatusBadRequest, "INVALID_ARGUMENT"},
		{"conditional v3 set", http.MethodPost, gcpiam.VerbSet, "",
			`{"policy":{"version":3,"bindings":[{"role":"r","members":["user:a"],"condition":{"expression":"true","title":"c"}}]}}`,
			http.StatusOK, `"title":"c"`},
		{"compute legacy set", http.MethodPost, gcpiam.VerbSet, "",
			`{"bindings":[{"role":"r","members":["user:legacy"]}]}`, http.StatusOK, "user:legacy"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, body := serve(t, resourceiam.New(), tc.method, tc.verb, tc.query, tc.body)
			if code != tc.want || !strings.Contains(body, tc.marker) {
				t.Fatalf("code=%d body=%s, want %d containing %q", code, body, tc.want, tc.marker)
			}
		})
	}
}

// TestServeStaleEtagAborted: a set carrying an etag older than the stored one
// gets 409 ABORTED, and every accepted set mints a new etag.
func TestServeStaleEtagAborted(t *testing.T) {
	s := resourceiam.New()

	_, body := serve(t, s, http.MethodPost, gcpiam.VerbGet, "", `{}`)
	e0 := etagOf(t, body)

	set := func(etag, member string) (int, string) {
		return serve(t, s, http.MethodPost, gcpiam.VerbSet, "",
			`{"policy":{"etag":"`+etag+`","bindings":[{"role":"roles/viewer","members":["`+member+`"]}]}}`)
	}

	code, body := set(e0, "user:a")
	if code != http.StatusOK {
		t.Fatalf("first set: %d %s", code, body)
	}

	e1 := etagOf(t, body)
	if e1 == e0 {
		t.Fatalf("set kept etag %q", e1)
	}

	code, body = set(e0, "user:b")
	if code != http.StatusConflict || !strings.Contains(body, `"status":"ABORTED"`) {
		t.Fatalf("stale set: %d %s, want 409 ABORTED", code, body)
	}

	code, body = set("", "user:c")
	if code != http.StatusOK || etagOf(t, body) == e1 {
		t.Fatalf("blind set: %d %s, want 200 with a new etag", code, body)
	}
}

func TestSplitVerb(t *testing.T) {
	tests := []struct {
		in, wantRes, wantVerb string
	}{
		{"t1:getIamPolicy", "t1", gcpiam.VerbGet},
		{"projects/p/instances/i:setIamPolicy", "projects/p/instances/i", gcpiam.VerbSet},
		{"projects/p/regions/r/subnetworks/s/testIamPermissions", "projects/p/regions/r/subnetworks/s", gcpiam.VerbTest},
		{"t1:insert", "t1:insert", ""},
		{"t1", "t1", ""},
	}

	for _, tc := range tests {
		if res, verb := gcpiam.SplitVerb(tc.in); res != tc.wantRes || verb != tc.wantVerb {
			t.Errorf("SplitVerb(%q) = (%q, %q), want (%q, %q)", tc.in, res, verb, tc.wantRes, tc.wantVerb)
		}
	}
}
