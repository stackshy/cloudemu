package azurearm_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const cpBase = "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Sql/servers"

func TestParsePathDepthRestNestedType(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		depth  int
		rest   string
		nested string
		ext    bool
	}{
		{"collection", cpBase, 0, "", "servers", false},
		{"resource", cpBase + "/s1", 1, "", "servers", false},
		{"child collection", cpBase + "/s1/databases", 2, "", "servers/databases", false},
		{"child", cpBase + "/s1/databases/d1", 3, "", "servers/databases", false},
		{"grandchild collection", cpBase + "/s1/databases/d1/backupShortTermRetentionPolicies", 4, "",
			"servers/databases/backupShortTermRetentionPolicies", false},
		{"grandchild", cpBase + "/s1/databases/d1/backupShortTermRetentionPolicies/default", 5, "default",
			"servers/databases/backupShortTermRetentionPolicies", false},
		{"depth six", cpBase + "/s1/databases/d1/zz/x/y", 6, "x/y", "servers/databases/zz/y", false},
		{"depth seven", cpBase + "/s1/a/b/c/d/e/f", 7, "d/e/f", "servers/a/c/e", false},
		{"empty inner segment", cpBase + "/s1//x", 3, "", "servers/", false},
		{"extension at sub-resource", cpBase + "/s1/providers/Microsoft.Authorization/locks/l1", 5, "l1",
			"servers/providers/locks", true},
		{"extension below a child", cpBase + "/s1/databases/d1/providers/Microsoft.Security/zz", 6,
			"Microsoft.Security/zz", "servers/databases/providers/zz", true},
		{"providers at a name position", cpBase + "/providers", 1, "", "servers", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rp, ok := azurearm.ParsePath(tc.path)
			if !ok {
				t.Fatalf("ParsePath(%q) not ok", tc.path)
			}

			if rp.Depth != tc.depth || rp.Rest != tc.rest {
				t.Errorf("Depth=%d Rest=%q, want %d %q", rp.Depth, rp.Rest, tc.depth, tc.rest)
			}

			if got := rp.NestedType(); got != tc.nested {
				t.Errorf("NestedType=%q, want %q", got, tc.nested)
			}

			if got := rp.IsExtension(); got != tc.ext {
				t.Errorf("IsExtension=%v, want %v", got, tc.ext)
			}

			_ = rp == rp // ResourcePath must stay comparable
		})
	}
}

func TestNestedTypeOnHandBuiltLiteral(t *testing.T) {
	rp := azurearm.ResourcePath{
		Provider: "Microsoft.EventGrid", ResourceType: "topics", ResourceName: "t1",
		SubResource: "eventSubscriptions", SubResourceName: "s1",
	}

	if got := rp.NestedType(); got != "topics/eventSubscriptions" {
		t.Errorf("NestedType=%q", got)
	}

	if rp.IsExtension() {
		t.Error("literal reported as extension")
	}

	w := httptest.NewRecorder()
	if azurearm.TooDeep(w, httptest.NewRequest(http.MethodGet, "/", nil), &rp, 1) {
		t.Error("a Depth 0 literal must never be too deep")
	}
}

func TestTooDeepBoundaries(t *testing.T) {
	cases := []struct {
		path   string
		max    int
		status int // 0 means not rejected
	}{
		{cpBase + "/s1/databases/d1", 3, 0},
		{cpBase + "/s1/databases/d1/zz", 3, http.StatusNotFound},
		{cpBase + "/s1/databases/d1/zz", 4, 0},
		{cpBase + "/s1", 1, 0},
		{cpBase + "/s1/zz", 1, http.StatusNotFound},
		{cpBase + "/s1/providers/Microsoft.Security/zz/x", 9, http.StatusNotImplemented},
	}

	for _, tc := range cases {
		rp, _ := azurearm.ParsePath(tc.path)
		w := httptest.NewRecorder()

		got := azurearm.TooDeep(w, httptest.NewRequest(http.MethodGet, tc.path, nil), &rp, tc.max)
		if got != (tc.status != 0) {
			t.Errorf("TooDeep(%q, %d)=%v", tc.path, tc.max, got)
			continue
		}

		if got && w.Code != tc.status {
			t.Errorf("TooDeep(%q, %d) status=%d, want %d", tc.path, tc.max, w.Code, tc.status)
		}
	}
}

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) (code, msg string) {
	t.Helper()

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}

	return env.Error.Code, env.Error.Message
}

func TestWriteUnknownTypeEnvelope(t *testing.T) {
	path := cpBase + "/s1/zzbogus/c1"
	rp, _ := azurearm.ParsePath(path)
	w := httptest.NewRecorder()

	azurearm.WriteUnknownType(w, httptest.NewRequest(http.MethodPut, path+"?api-version=2023-05-01", nil), &rp)

	code, msg := decodeErr(t, w)
	want := "The resource type 'servers/zzbogus' could not be found in the namespace 'Microsoft.Sql' " +
		"for api version '2023-05-01'."

	if w.Code != http.StatusNotFound || code != "InvalidResourceType" || msg != want {
		t.Errorf("got %d %s %q", w.Code, code, msg)
	}
}

func TestExtensionAndChildNotImplemented(t *testing.T) {
	rp, _ := azurearm.ParsePath(cpBase + "/s1/providers/Microsoft.Security/advancedThreatProtectionSettings/current")
	w := httptest.NewRecorder()

	azurearm.RejectChild(w, httptest.NewRequest(http.MethodGet, "/", nil), &rp)

	code, msg := decodeErr(t, w)
	if w.Code != http.StatusNotImplemented || code != "NotImplemented" ||
		msg != "cloudemu does not implement extension resource Microsoft.Security/advancedThreatProtectionSettings" {
		t.Errorf("got %d %s %q", w.Code, code, msg)
	}
}

func TestServeDeferredReadRule(t *testing.T) {
	cases := []struct {
		name   string
		method string
		kind   azurearm.DeferredKind
		status int
		body   string
	}{
		{"singleton get", http.MethodGet, azurearm.DeferredSingleton, http.StatusOK, `{"state":"Disabled"}`},
		{"collection get", http.MethodGet, azurearm.DeferredCollection, http.StatusOK, `{"value":[]}`},
		{"item get", http.MethodGet, azurearm.DeferredItem, http.StatusNotFound, ""},
		{"singleton put", http.MethodPut, azurearm.DeferredSingleton, http.StatusNotImplemented, ""},
		{"collection post", http.MethodPost, azurearm.DeferredCollection, http.StatusNotImplemented, ""},
		{"item delete", http.MethodDelete, azurearm.DeferredItem, http.StatusNotImplemented, ""},
	}

	rp, _ := azurearm.ParsePath(cpBase + "/s1/zz/x")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			azurearm.ServeDeferred(w, httptest.NewRequest(tc.method, "/", nil), &rp, tc.kind,
				func() any { return map[string]string{"state": "Disabled"} })

			if w.Code != tc.status {
				t.Fatalf("status=%d, want %d", w.Code, tc.status)
			}

			if tc.body != "" && w.Body.String() != tc.body+"\n" {
				t.Errorf("body=%q, want %q", w.Body.String(), tc.body)
			}
		})
	}
}

func TestGuardLeaf(t *testing.T) {
	cases := []struct {
		path   string
		method string
		served bool
		status int
	}{
		{cpBase + "/s1", http.MethodPut, false, 0},
		{cpBase + "/s1/secrets", http.MethodGet, true, http.StatusOK},
		{cpBase + "/s1/Secrets/x", http.MethodGet, true, http.StatusNotFound},
		{cpBase + "/s1/secrets/x", http.MethodPut, true, http.StatusNotImplemented},
		{cpBase + "/s1/zzbogus/x", http.MethodDelete, true, http.StatusNotFound},
		{cpBase + "/s1/secrets/x/y", http.MethodGet, true, http.StatusNotFound},
		{cpBase + "/s1/providers/Microsoft.Security/zz/x", http.MethodPut, true, http.StatusNotImplemented},
	}

	for _, tc := range cases {
		rp, _ := azurearm.ParsePath(tc.path)
		w := httptest.NewRecorder()

		got := azurearm.GuardLeaf(w, httptest.NewRequest(tc.method, tc.path, nil), &rp, "secrets")
		if got != tc.served {
			t.Errorf("%s %s: served=%v", tc.method, tc.path, got)
			continue
		}

		if got && w.Code != tc.status {
			t.Errorf("%s %s: status=%d, want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
}
