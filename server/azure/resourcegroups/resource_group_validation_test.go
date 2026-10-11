package resourcegroups_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/azure/rgstore"
	"github.com/stackshy/cloudemu/v2/server/azure/resourcegroups"
)

const rgBase = "/subscriptions/sub1/resourcegroups/"

func rgDo(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	return rec.Code, out
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)

	return c
}

func newRGHandler() http.Handler {
	return resourcegroups.NewWithStore(rgstore.New(nil), nil)
}

func TestResourceGroupPutValidation(t *testing.T) {
	tests := []struct {
		name, rg, body string
		status         int
		code           string
	}{
		{"ok", "my-rg_1.(x)", `{"location":"eastus"}`, 201, ""},
		{"max length", strings.Repeat("a", 90), `{"location":"eastus"}`, 201, ""},
		{"too long", strings.Repeat("a", 91), `{"location":"eastus"}`, 400, "InvalidResourceGroupName"},
		{"trailing period", "rg.", `{"location":"eastus"}`, 400, "InvalidResourceGroupName"},
		{"bad char", "rg%20x", `{"location":"eastus"}`, 400, "InvalidResourceGroupName"},
		{"spaced location", "rg2", `{"location":"West US 2"}`, 201, ""},
		{"unicode letters", "gruppe-%C3%BC", `{"location":"eastus"}`, 201, ""},
		{"unknown location", "rg3", `{"location":"mars"}`, 400, "LocationNotAvailableForResourceGroup"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, out := rgDo(t, newRGHandler(), http.MethodPut, rgBase+tt.rg+"?api-version=2021-04-01", tt.body)
			if status != tt.status || errCode(out) != tt.code {
				t.Fatalf("got %d %q, want %d %q", status, errCode(out), tt.status, tt.code)
			}
		})
	}
}

func TestResourceGroupRePutLocation(t *testing.T) {
	h := newRGHandler()
	p := rgBase + "rg?api-version=2021-04-01"

	if s, _ := rgDo(t, h, http.MethodPut, p, `{"location":"westus2"}`); s != 201 {
		t.Fatalf("create: %d", s)
	}

	s, out := rgDo(t, h, http.MethodPut, p, `{"location":"eastus"}`)
	if s != 409 || errCode(out) != "InvalidResourceGroupLocation" {
		t.Fatalf("different location: %d %q", s, errCode(out))
	}

	s, out = rgDo(t, h, http.MethodPut, p, `{"location":"West US 2","tags":{"a":"b"}}`)
	if s != 200 {
		t.Fatalf("same location: %d", s)
	}

	if out["location"] != "westus2" {
		t.Fatalf("location changed: %v", out["location"])
	}

	_, got := rgDo(t, h, http.MethodGet, p, "")
	if tags, _ := got["tags"].(map[string]any); tags["a"] != "b" || got["location"] != "westus2" {
		t.Fatalf("stored group: %v", got)
	}
}

func TestResourceGroupListTopAndFilter(t *testing.T) {
	h := newRGHandler()

	for _, g := range []struct{ name, tags string }{
		{"a", `{"env":"dev"}`}, {"b", `{"env":"prod"}`}, {"c", `{"env":"dev","x":"y"}`}, {"d", `{}`},
	} {
		body := `{"location":"eastus","tags":` + g.tags + `}`
		if s, _ := rgDo(t, h, http.MethodPut, rgBase+g.name+"?api-version=2021-04-01", body); s != 201 {
			t.Fatalf("create %s: %d", g.name, s)
		}
	}

	count := func(q string) (int, string) {
		_, out := rgDo(t, h, http.MethodGet, "/subscriptions/sub1/resourcegroups?api-version=2021-04-01"+q, "")
		v, _ := out["value"].([]any)
		next, _ := out["nextLink"].(string)

		return len(v), next
	}

	if n, _ := count(""); n != 4 {
		t.Fatalf("all: %d", n)
	}

	n, next := count("&$top=3")
	if n != 3 || next == "" {
		t.Fatalf("top: %d next=%q", n, next)
	}

	if n, _ := count("&$filter=tagName%20eq%20'env'"); n != 3 {
		t.Fatalf("tagName: %d", n)
	}

	if n, _ := count("&$filter=tagName%20eq%20'env'%20and%20tagValue%20eq%20'dev'"); n != 2 {
		t.Fatalf("tagName+tagValue: %d", n)
	}

	if n, _ := count("&$filter=tagName%20eq%20'nope'"); n != 0 {
		t.Fatalf("no match: %d", n)
	}
}

func TestResourceGroupLocationIsCanonical(t *testing.T) {
	h := newRGHandler()

	status, out := rgDo(t, h, http.MethodPut, rgBase+"rg1?api-version=2021-04-01", `{"location":"West US 2"}`)
	if status != http.StatusCreated || out["location"] != "westus2" {
		t.Fatalf("got %d location %v, want 201 westus2", status, out["location"])
	}
}
