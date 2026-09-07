package dataform_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newServer stands up the full GCP wire server so requests route through the
// dispatcher's Matches guard exactly as the hashicorp/google-beta Terraform
// provider hits Dataform at its default /v1beta1/ base path.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewGCP()
	ts := httptest.NewServer(gcpserver.NewFromProvider(cloud))
	t.Cleanup(ts.Close)

	return ts
}

// do issues a request and returns the status and raw body.
func do(t *testing.T, ts *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()

	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}

		rdr = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, raw
}

func doOK(t *testing.T, ts *httptest.Server, method, path string, body any, out any) {
	t.Helper()

	status, raw := do(t, ts, method, path, body)
	if status != http.StatusOK {
		t.Fatalf("%s %s: status %d body %s", method, path, status, raw)
	}

	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s %s: %v (body %s)", method, path, err, raw)
		}
	}
}

const collPath = "/v1beta1/projects/p/locations/us-central1/repositories"

// TestRepositoryLifecycleOnV1Beta1Path drives the full CRUD lifecycle over the
// /v1beta1/ prefix — the base path the stable google provider uses since
// Dataform ships a v1beta1 API only — and asserts the rich nested blocks round
// trip verbatim and name/createTime are stable, so a Terraform refresh sees no
// drift.
func TestRepositoryLifecycleOnV1Beta1Path(t *testing.T) {
	ts := newServer(t)

	createBody := map[string]any{
		"displayName": "My Repo",
		"labels":      map[string]string{"env": "dev"},
		"gitRemoteSettings": map[string]any{
			"url":           "https://github.com/x/y.git",
			"defaultBranch": "main",
		},
		"workspaceCompilationOverrides": map[string]any{
			"defaultDatabase": "db",
			"schemaSuffix":    "_dev",
			"tablePrefix":     "tp_",
		},
	}

	var created map[string]any
	doOK(t, ts, http.MethodPost, collPath+"?repositoryId=repo1", createBody, &created)

	wantName := "projects/p/locations/us-central1/repositories/repo1"
	if created["name"] != wantName {
		t.Fatalf("create name = %v, want %v", created["name"], wantName)
	}

	if created["createTime"] == nil || created["createTime"] == "" {
		t.Fatal("createTime missing on create")
	}

	// Get must echo the raw blocks verbatim and the same stable name/createTime.
	var got map[string]any
	doOK(t, ts, http.MethodGet, collPath+"/repo1", nil, &got)

	if got["name"] != wantName {
		t.Fatalf("get name = %v, want %v", got["name"], wantName)
	}

	if got["createTime"] != created["createTime"] {
		t.Fatalf("createTime drifted: %v vs %v", got["createTime"], created["createTime"])
	}

	assertGitURL(t, got, "https://github.com/x/y.git")
	assertBranch(t, got, "main")

	// List returns the repository under the `repositories` array field.
	var list struct {
		Repositories []map[string]any `json:"repositories"`
	}
	doOK(t, ts, http.MethodGet, collPath, nil, &list)

	if len(list.Repositories) != 1 || list.Repositories[0]["name"] != wantName {
		t.Fatalf("list = %+v", list.Repositories)
	}

	// Patch only displayName+labels; git settings must survive untouched.
	patchBody := map[string]any{
		"displayName": "Renamed",
		"labels":      map[string]string{"env": "prod"},
	}

	var patched map[string]any
	doOK(t, ts, http.MethodPatch, collPath+"/repo1?updateMask=displayName,labels", patchBody, &patched)

	if patched["displayName"] != "Renamed" {
		t.Fatalf("displayName not updated: %v", patched["displayName"])
	}

	assertGitURL(t, patched, "https://github.com/x/y.git")

	// Delete returns 200 with an empty object; a subsequent get is 404.
	doOK(t, ts, http.MethodDelete, collPath+"/repo1", nil, nil)

	status, _ := do(t, ts, http.MethodGet, collPath+"/repo1", nil)
	if status != http.StatusNotFound {
		t.Fatalf("get after delete: status %d, want 404", status)
	}
}

// TestCreateDuplicateConflict asserts a re-create of the same id is 409.
func TestCreateDuplicateConflict(t *testing.T) {
	ts := newServer(t)

	body := map[string]any{"displayName": "x"}
	doOK(t, ts, http.MethodPost, collPath+"?repositoryId=dup", body, nil)

	status, _ := do(t, ts, http.MethodPost, collPath+"?repositoryId=dup", body)
	if status != http.StatusConflict {
		t.Fatalf("duplicate create: status %d, want 409", status)
	}
}

func assertGitURL(t *testing.T, repo map[string]any, want string) {
	t.Helper()

	grs, ok := repo["gitRemoteSettings"].(map[string]any)
	if !ok {
		t.Fatalf("gitRemoteSettings missing/wrong type: %v", repo["gitRemoteSettings"])
	}

	if grs["url"] != want {
		t.Fatalf("git url = %v, want %v", grs["url"], want)
	}
}

func assertBranch(t *testing.T, repo map[string]any, want string) {
	t.Helper()

	grs, _ := repo["gitRemoteSettings"].(map[string]any)
	if grs["defaultBranch"] != want {
		t.Fatalf("defaultBranch = %v, want %v", grs["defaultBranch"], want)
	}
}
