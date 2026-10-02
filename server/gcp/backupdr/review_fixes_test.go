package backupdr_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	backupdr "google.golang.org/api/backupdr/v1"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// rawEnv drives the assembled server with plain HTTP, to assert on the exact
// wire bytes (error envelopes, operation shapes) an SDK would hide.
type rawEnv struct {
	url    string
	clock  *config.FakeClock
	parent string
}

func newRawEnv(t *testing.T) *rawEnv {
	t.Helper()

	clk := config.NewFakeClock(fixedNow)
	ts := httptest.NewServer(gcpserver.NewFromProvider(cloudemu.NewGCP(config.WithClock(clk))))
	t.Cleanup(ts.Close)

	return &rawEnv{url: ts.URL, clock: clk, parent: "projects/" + sdkProject + "/locations/" + sdkLocation}
}

// do sends method to /v1/path with an optional JSON body and returns the
// status and decoded JSON body.
func (e *rawEnv) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, e.url+"/v1/"+path, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s %s: %v", method, path, err)
	}

	return resp.StatusCode, out
}

func (e *rawEnv) create(t *testing.T, id, body string) {
	t.Helper()

	if code, out := e.do(t, http.MethodPost, e.parent+"/backupVaults?backupVaultId="+id, body); code != http.StatusOK {
		t.Fatalf("create %s: %d %v", id, code, out)
	}
}

// errorOf returns the (message, status, errors[0].reason) of an error envelope.
func errorOf(t *testing.T, out map[string]any) (msg, status, reason string) {
	t.Helper()

	e, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error envelope: %v", out)
	}

	errs, _ := e["errors"].([]any)
	if len(errs) == 0 {
		t.Fatalf("no errors[]: %v", e)
	}

	first, _ := errs[0].(map[string]any)
	msg, _ = e["message"].(string)
	status, _ = e["status"].(string)
	reason, _ = first["reason"].(string)

	return msg, status, reason
}

const vaultBody = `{"backupMinimumEnforcedRetentionDuration":"86400s"}`

// TestVaultIDFormat: ids outside the documented format are 400, and a '/' in
// the id can no longer mint an unreachable vault.
func TestVaultIDFormat(t *testing.T) {
	e := newRawEnv(t)

	for _, id := range []string{"Bad_ID", url.QueryEscape("a/bc"), "abc-", "-abc"} {
		code, out := e.do(t, http.MethodPost, e.parent+"/backupVaults?backupVaultId="+id, vaultBody)
		if code != http.StatusBadRequest {
			t.Fatalf("create %q = %d %v, want 400", id, code, out)
		}
	}

	if _, out := e.do(t, http.MethodGet, e.parent+"/backupVaults", ""); out["backupVaults"] != nil &&
		len(out["backupVaults"].([]any)) != 0 {
		t.Fatalf("rejected ids were stored: %v", out)
	}
}

// TestRetentionLockOverWire: once effectiveTime passes, lowering the retention
// or moving effectiveTime is a 400 FAILED_PRECONDITION; 1s retention is 400.
func TestRetentionLockOverWire(t *testing.T) {
	e := newRawEnv(t)
	name := e.parent + "/backupVaults/locked"

	e.create(t, "locked", `{"backupMinimumEnforcedRetentionDuration":"172800s","effectiveTime":"`+
		fixedNow.Add(time.Minute).Format(time.RFC3339)+`"}`)

	code, out := e.do(t, http.MethodPatch, name+"?updateMask=backupMinimumEnforcedRetentionDuration",
		`{"backupMinimumEnforcedRetentionDuration":"1s"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("1s retention = %d %v, want 400", code, out)
	}

	e.clock.Advance(time.Hour)

	for _, tc := range []struct{ mask, body string }{
		{"backupMinimumEnforcedRetentionDuration", `{"backupMinimumEnforcedRetentionDuration":"86400s"}`},
		{"effectiveTime", `{"effectiveTime":"2030-01-01T00:00:00Z"}`},
	} {
		code, out := e.do(t, http.MethodPatch, name+"?updateMask="+tc.mask, tc.body)
		if code != http.StatusBadRequest {
			t.Fatalf("locked patch %s = %d %v, want 400", tc.mask, code, out)
		}

		if _, status, reason := errorOf(t, out); status != "FAILED_PRECONDITION" || reason != "failedPrecondition" {
			t.Fatalf("locked patch %s status/reason = %q/%q", tc.mask, status, reason)
		}
	}

	code, out = e.do(t, http.MethodPatch, name+"?updateMask=backupMinimumEnforcedRetentionDuration",
		`{"backupMinimumEnforcedRetentionDuration":"259200s"}`)
	if code != http.StatusOK {
		t.Fatalf("locked increase = %d %v, want 200", code, out)
	}
}

// TestErrorReasonsAndEtagMessage: errors[].reason is camelCase like every other
// GCP handler, the status is canonical, and the etag error names the vault.
func TestErrorReasonsAndEtagMessage(t *testing.T) {
	e := newRawEnv(t)
	name := e.parent + "/backupVaults/vault-etag"

	e.create(t, "vault-etag", vaultBody)

	code, out := e.do(t, http.MethodPatch, name+"?updateMask=description", `{"description":"x","etag":"stale"}`)
	if code != http.StatusConflict {
		t.Fatalf("stale etag patch = %d %v, want 409", code, out)
	}

	msg, status, reason := errorOf(t, out)
	if reason != "aborted" || status != "ABORTED" {
		t.Fatalf("stale etag status/reason = %q/%q, want ABORTED/aborted", status, reason)
	}

	if !strings.Contains(msg, name) || !strings.Contains(msg, "etag does not match") || strings.Contains(msg, "FailedPrecondition") {
		t.Fatalf("stale etag message = %q, want the vault name and the etag text without the code prefix", msg)
	}

	code, out = e.do(t, http.MethodDelete, name+"?etag=stale", "")
	if code != http.StatusConflict {
		t.Fatalf("stale etag delete = %d %v, want 409", code, out)
	}

	if msg, _, _ := errorOf(t, out); !strings.Contains(msg, name) {
		t.Fatalf("stale etag delete message = %q, want the vault name", msg)
	}
}

// TestValidateOnlyOperationShape: a validateOnly create/delete returns a done
// operation with its response inline and no name (nothing to poll), and the
// vault is untouched.
func TestValidateOnlyOperationShape(t *testing.T) {
	e := newRawEnv(t)
	name := e.parent + "/backupVaults/keep"

	e.create(t, "keep", vaultBody)

	code, out := e.do(t, http.MethodPost, e.parent+"/backupVaults?backupVaultId=dry&validateOnly=true", vaultBody)
	if code != http.StatusOK || out["name"] != nil || out["done"] != true || out["response"] == nil {
		t.Fatalf("validateOnly create = %d %v", code, out)
	}

	code, out = e.do(t, http.MethodDelete, name+"?validateOnly=true", "")
	if code != http.StatusOK || out["name"] != nil || out["done"] != true {
		t.Fatalf("validateOnly delete = %d %v", code, out)
	}

	if code, _ := e.do(t, http.MethodGet, name, ""); code != http.StatusOK {
		t.Fatalf("validateOnly delete removed the vault: %d", code)
	}

	// A real delete names its operation and carries google.protobuf.Empty,
	// inline and when polled through the shared poller.
	code, out = e.do(t, http.MethodDelete, name, "")
	if code != http.StatusOK {
		t.Fatalf("delete = %d %v", code, out)
	}

	opName, _ := out["name"].(string)
	resp, _ := out["response"].(map[string]any)

	if opName == "" || resp["@type"] != "type.googleapis.com/google.protobuf.Empty" {
		t.Fatalf("delete op = %v, want a name and an Empty response", out)
	}

	code, polled := e.do(t, http.MethodGet, opName, "")
	if presp, _ := polled["response"].(map[string]any); code != http.StatusOK ||
		presp["@type"] != "type.googleapis.com/google.protobuf.Empty" {
		t.Fatalf("polled delete op = %d %v, want an Empty response", code, polled)
	}
}

// TestListFilterAndOrderBy: filter narrows the list, orderBy orders it, and an
// unsupported expression in either is 400 instead of silently ignored.
func TestListFilterAndOrderBy(t *testing.T) {
	e := newSDKEnv(t)
	vaults := e.svc.Projects.Locations.BackupVaults

	for i, id := range []string{"vault-a", "vault-b", "vault-c"} {
		e.create(t, id, &backupdr.BackupVault{
			BackupMinimumEnforcedRetentionDuration: retention,
			Labels:                                 map[string]string{"team": map[bool]string{true: "red", false: "blue"}[i != 1]},
		})
		e.clock.Advance(time.Minute)
	}

	names := func(resp *backupdr.ListBackupVaultsResponse) string {
		out := make([]string, 0, len(resp.BackupVaults))
		for _, v := range resp.BackupVaults {
			out = append(out, v.Name[strings.LastIndex(v.Name, "/")+1:])
		}

		return strings.Join(out, ",")
	}

	cases := []struct{ filter, orderBy, want string }{
		{`labels.team = "red"`, "", "vault-a,vault-c"},
		{`labels.team != "red"`, "", "vault-b"},
		{`name = "` + e.parent + `/backupVaults/vault-b"`, "", "vault-b"},
		{`state = "ACTIVE" AND labels.team = "red"`, "createTime desc", "vault-c,vault-a"},
		{"", "name desc", "vault-c,vault-b,vault-a"},
		{"", "updateTime", "vault-a,vault-b,vault-c"},
	}

	for _, tc := range cases {
		resp, err := vaults.List(e.parent).Filter(tc.filter).OrderBy(tc.orderBy).Do()
		if err != nil {
			t.Fatalf("list filter=%q orderBy=%q: %v", tc.filter, tc.orderBy, err)
		}

		if got := names(resp); got != tc.want {
			t.Fatalf("list filter=%q orderBy=%q = %s, want %s", tc.filter, tc.orderBy, got, tc.want)
		}
	}

	for _, bad := range []struct{ filter, orderBy string }{
		{"createTime > \"2020-01-01T00:00:00Z\"", ""},
		{"bogus = \"x\"", ""},
		{"labels.team = \"red\" OR labels.team = \"blue\"", ""},
		{"", "totalStoredBytes"},
		{"", "name sideways"},
	} {
		_, err := vaults.List(e.parent).Filter(bad.filter).OrderBy(bad.orderBy).Do()
		wantCode(t, "list filter="+bad.filter+" orderBy="+bad.orderBy, err, http.StatusBadRequest)
	}
}

// TestRequestEdgeCases covers the request-shape rejections and the
// body-name id fallback.
func TestRequestEdgeCases(t *testing.T) {
	e := newRawEnv(t)
	coll := e.parent + "/backupVaults"

	if code, out := e.do(t, http.MethodPost, coll,
		`{"name":"`+coll+`/from-body","backupMinimumEnforcedRetentionDuration":"86400s"}`); code != http.StatusOK {
		t.Fatalf("create with id from body name = %d %v", code, out)
	}

	cases := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPut, coll, "", http.StatusMethodNotAllowed},
		{http.MethodPut, coll + "/from-body", "", http.StatusMethodNotAllowed},
		{http.MethodPost, coll, vaultBody, http.StatusBadRequest},
		{http.MethodPost, coll + "?backupVaultId=x1y", "{not json", http.StatusBadRequest},
		{http.MethodPost, coll + "?backupVaultId=x1y&validateOnly=maybe", vaultBody, http.StatusBadRequest},
		{http.MethodPatch, coll + "/from-body?updateMask=description&validateOnly=maybe", "{}", http.StatusBadRequest},
		{http.MethodPatch, coll + "/from-body?updateMask=description", "{bad", http.StatusBadRequest},
		{http.MethodDelete, coll + "/from-body?force=maybe", "", http.StatusBadRequest},
		{http.MethodGet, coll + "?pageToken=garbage", "", http.StatusBadRequest},
		{http.MethodGet, coll + "?pageSize=100000", "", http.StatusOK},
	}

	for _, tc := range cases {
		if code, out := e.do(t, tc.method, tc.path, tc.body); code != tc.want {
			t.Fatalf("%s %s = %d %v, want %d", tc.method, tc.path, code, out, tc.want)
		}
	}
}
