package accesscontextmanager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// do drives one request through the handler and returns the recorder.
func do(t *testing.T, h *Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var r *http.Request
	if body == "" {
		r, _ = http.NewRequest(method, "http://x"+path, nil)
	} else {
		r, _ = http.NewRequest(method, "http://x"+path, strings.NewReader(body))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	return rec
}

// decodeJSON parses a recorder body as a generic JSON object.
func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}

	return m
}

// doCreatePolicy POSTs a new access policy and returns the recorder.
func doCreatePolicy(t *testing.T, h *Handler, parent, title string) *httptest.ResponseRecorder {
	t.Helper()

	rec := do(t, h, http.MethodPost, "/v1/accessPolicies",
		`{"parent":"`+parent+`","title":"`+title+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create policy status = %d, body %s", rec.Code, rec.Body.String())
	}

	return rec
}

// decodeOpName returns the operation name from a completed-operation recorder.
func decodeOpName(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	m := decodeJSON(t, rec)

	name, _ := m["name"].(string)
	if name == "" {
		t.Fatalf("operation carried no name: %s", rec.Body.String())
	}

	return name
}

// opResponse returns the embedded Any response of a completed operation.
func opResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	m := decodeJSON(t, rec)
	if done, _ := m["done"].(bool); !done {
		t.Fatalf("operation not done: %s", rec.Body.String())
	}

	resp, ok := m["response"].(map[string]any)
	if !ok {
		t.Fatalf("operation carried no response: %s", rec.Body.String())
	}

	return resp
}

func policyNumberFrom(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	resp := opResponse(t, rec)

	name, _ := resp["name"].(string)
	if !strings.HasPrefix(name, "accessPolicies/") {
		t.Fatalf("policy name malformed: %q", name)
	}

	return strings.TrimPrefix(name, "accessPolicies/")
}

func TestSDKRoundTripPolicyLifecycle(t *testing.T) {
	h := newHandler()

	createRec := doCreatePolicy(t, h, "organizations/123", "prod")

	resp := opResponse(t, createRec)
	if resp["@type"] != policyTypeURL {
		t.Fatalf("policy @type = %v, want %s", resp["@type"], policyTypeURL)
	}

	num := policyNumberFrom(t, createRec)

	// GET is byte-stable across reads (name, etag, createTime).
	get1 := do(t, h, http.MethodGet, "/v1/accessPolicies/"+num, "")
	get2 := do(t, h, http.MethodGet, "/v1/accessPolicies/"+num, "")

	if get1.Code != http.StatusOK || get1.Body.String() != get2.Body.String() {
		t.Fatalf("policy GET not byte-stable:\n%s\n%s", get1.Body.String(), get2.Body.String())
	}

	pj := decodeJSON(t, get1)
	for _, k := range []string{"name", "etag", "createTime", "updateTime", "title", "parent"} {
		if _, ok := pj[k]; !ok {
			t.Fatalf("policy missing %q: %s", k, get1.Body.String())
		}
	}

	// List scoped by parent.
	listRec := do(t, h, http.MethodGet, "/v1/accessPolicies?parent=organizations/123", "")
	lj := decodeJSON(t, listRec)

	if arr, _ := lj["accessPolicies"].([]any); len(arr) != 1 {
		t.Fatalf("list len = %d, want 1: %s", len(arr), listRec.Body.String())
	}

	// Patch the title -> etag changes.
	etag1 := pj["etag"]

	patchRec := do(t, h, http.MethodPatch, "/v1/accessPolicies/"+num+"?updateMask=title",
		`{"title":"prod-v2"}`)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d", patchRec.Code)
	}

	get3 := decodeJSON(t, do(t, h, http.MethodGet, "/v1/accessPolicies/"+num, ""))
	if get3["title"] != "prod-v2" {
		t.Fatalf("patch not applied: %v", get3["title"])
	}

	if get3["etag"] == etag1 {
		t.Fatalf("etag did not change on patch")
	}
}

func TestSDKRoundTripChildrenAndOperationPoll(t *testing.T) {
	h := newHandler()
	num := policyNumberFrom(t, doCreatePolicy(t, h, "organizations/1", "t"))

	// Access level with a basic condition, id via query param.
	lvlBody := `{"title":"corp","basic":{"conditions":[{"ipSubnetworks":["10.0.0.0/8"]}]}}`

	lvlRec := do(t, h, http.MethodPost,
		"/v1/accessPolicies/"+num+"/accessLevels?accessLevelId=corp", lvlBody)
	if lvlRec.Code != http.StatusOK {
		t.Fatalf("create level status = %d body %s", lvlRec.Code, lvlRec.Body.String())
	}

	lvlResp := opResponse(t, lvlRec)
	if lvlResp["@type"] != accessLevelTypeURL {
		t.Fatalf("level @type = %v, want %s", lvlResp["@type"], accessLevelTypeURL)
	}

	if lvlResp["name"] != "accessPolicies/"+num+"/accessLevels/corp" {
		t.Fatalf("level name = %v", lvlResp["name"])
	}

	// Access levels carry NO create/update timestamps.
	if _, has := lvlResp["createTime"]; has {
		t.Fatalf("access level should not surface createTime: %s", lvlRec.Body.String())
	}

	// Basic block round-trips verbatim.
	gotLvl := decodeJSON(t, do(t, h, http.MethodGet, "/v1/accessPolicies/"+num+"/accessLevels/corp", ""))
	if b, _ := json.Marshal(gotLvl["basic"]); !strings.Contains(string(b), "10.0.0.0/8") {
		t.Fatalf("basic block drift: %s", b)
	}

	// Operation poll for the level create resolves to done with the response.
	opName := decodeOpName(t, lvlRec)
	pollRec := do(t, h, http.MethodGet, "/v1/"+opName, "")

	pollResp := opResponse(t, pollRec)
	if pollResp["@type"] != accessLevelTypeURL {
		t.Fatalf("operation poll response @type = %v", pollResp["@type"])
	}

	// Service perimeter referencing the level, with restricted services.
	periBody := `{"title":"pp","status":{"resources":["projects/1"],` +
		`"restrictedServices":["storage.googleapis.com"],` +
		`"accessLevels":["accessPolicies/` + num + `/accessLevels/corp"]}}`

	periRec := do(t, h, http.MethodPost,
		"/v1/accessPolicies/"+num+"/servicePerimeters?servicePerimeterId=peri", periBody)

	periResp := opResponse(t, periRec)
	if periResp["@type"] != servicePerimeterTypeURL {
		t.Fatalf("perimeter @type = %v, want %s", periResp["@type"], servicePerimeterTypeURL)
	}

	// Perimeters DO surface create/update timestamps.
	if _, has := periResp["createTime"]; !has {
		t.Fatalf("service perimeter should surface createTime: %s", periRec.Body.String())
	}

	// Update perimeter restricted_services in place.
	up := do(t, h, http.MethodPatch,
		"/v1/accessPolicies/"+num+"/servicePerimeters/peri?updateMask=status",
		`{"status":{"restrictedServices":["bigquery.googleapis.com"]}}`)
	if up.Code != http.StatusOK {
		t.Fatalf("perimeter patch status = %d", up.Code)
	}

	gotPeri := decodeJSON(t, do(t, h, http.MethodGet, "/v1/accessPolicies/"+num+"/servicePerimeters/peri", ""))
	if b, _ := json.Marshal(gotPeri["status"]); !strings.Contains(string(b), "bigquery.googleapis.com") {
		t.Fatalf("perimeter update not applied: %s", b)
	}

	// Delete perimeter -> GET 404.
	if del := do(t, h, http.MethodDelete, "/v1/accessPolicies/"+num+"/servicePerimeters/peri", ""); del.Code != http.StatusOK {
		t.Fatalf("perimeter delete status = %d", del.Code)
	}

	if g := do(t, h, http.MethodGet, "/v1/accessPolicies/"+num+"/servicePerimeters/peri", ""); g.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", g.Code)
	}
}

func TestChildCreateUnderMissingPolicyNotFound(t *testing.T) {
	h := newHandler()

	rec := do(t, h, http.MethodPost,
		"/v1/accessPolicies/999999999999/accessLevels?accessLevelId=x", `{"title":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("create under missing policy = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestRootOperationYieldsForForeignOps confirms a root operation this backend
// never minted is not claimed, so the assembled server can route it to its
// owning sibling (Cloud Functions gen1) instead of masking it.
func TestRootOperationYieldsForForeignOps(t *testing.T) {
	h := newHandler()

	if h.Matches(request(http.MethodGet, "/v1/operations/foreign-123")) {
		t.Fatalf("must not claim a foreign root operation")
	}
}
