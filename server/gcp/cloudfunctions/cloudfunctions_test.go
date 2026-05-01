package cloudfunctions_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

const (
	project  = "demo-project"
	location = "us-central1"
)

func functionsURL() string {
	return "/v1/projects/" + project + "/locations/" + location + "/functions"
}

func TestMatchesAcceptsLocationsFunctions(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + functionsURL())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestMatchesRejectsFirestorePath(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := httptest.NewServer(gcpserver.New(gcpserver.Drivers{
		CloudFunctions: cloud.CloudFunctions,
		Firestore:      cloud.Firestore,
	}))
	t.Cleanup(srv.Close)

	// Firestore path must NOT be claimed by cloudfunctions.
	body := strings.NewReader(`{"writes":[]}`)

	resp, err := http.Post(
		srv.URL+"/v1/projects/"+project+"/databases/(default)/documents:commit",
		"application/json", body,
	)
	if err != nil {
		t.Fatalf("POST firestore: %v", err)
	}

	defer resp.Body.Close()

	// Expect Firestore handler to claim it (200 OK with empty writeResults).
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("firestore commit status = %d, want 200", resp.StatusCode)
	}
}

func TestCreateGetDeleteRoundTrip(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions}))
	t.Cleanup(srv.Close)

	body := `{
        "name":"projects/demo-project/locations/us-central1/functions/hello",
        "runtime":"go121",
        "entryPoint":"Hello",
        "availableMemoryMb":256,
        "timeout":"60s",
        "labels":{"env":"test"}
    }`

	createResp, err := http.Post(srv.URL+functionsURL(), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d, want 200", createResp.StatusCode)
	}

	var op operationShape

	if err := json.NewDecoder(createResp.Body).Decode(&op); err != nil {
		t.Fatalf("decode op: %v", err)
	}

	if !op.Done {
		t.Fatal("op.Done = false, want true (LRO returns immediately)")
	}

	if !strings.Contains(op.Name, "operations/create-hello") {
		t.Fatalf("op name = %q, want contains operations/create-hello", op.Name)
	}

	getResp, err := http.Get(srv.URL + functionsURL() + "/hello")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", getResp.StatusCode)
	}

	var fn cloudFunctionShape

	if err := json.NewDecoder(getResp.Body).Decode(&fn); err != nil {
		t.Fatalf("decode get: %v", err)
	}

	if fn.Runtime != "go121" {
		t.Fatalf("runtime = %q, want go121", fn.Runtime)
	}

	if fn.EntryPoint != "Hello" {
		t.Fatalf("entryPoint = %q, want Hello", fn.EntryPoint)
	}

	if fn.AvailableMemory != 256 {
		t.Fatalf("availableMemoryMb = %d, want 256", fn.AvailableMemory)
	}

	if fn.Timeout != "60s" {
		t.Fatalf("timeout = %q, want 60s", fn.Timeout)
	}

	delReq, _ := http.NewRequestWithContext(context.Background(),
		http.MethodDelete, srv.URL+functionsURL()+"/hello", nil)

	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	defer delResp.Body.Close()

	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", delResp.StatusCode)
	}

	missing, err := http.Get(srv.URL + functionsURL() + "/hello")
	if err != nil {
		t.Fatalf("post-delete get: %v", err)
	}

	defer missing.Body.Close()

	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("post-delete get = %d, want 404", missing.StatusCode)
	}
}

func TestList(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions}))
	t.Cleanup(srv.Close)

	for _, n := range []string{"a", "b"} {
		body := `{"name":"projects/demo-project/locations/us-central1/functions/` + n + `","runtime":"go121"}`

		resp, err := http.Post(srv.URL+functionsURL(), "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("create %s: %v", n, err)
		}

		_ = resp.Body.Close()
	}

	resp, err := http.Get(srv.URL + functionsURL())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	defer resp.Body.Close()

	var got struct {
		Functions []cloudFunctionShape `json:"functions"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Functions) != 2 {
		t.Fatalf("len(functions) = %d, want 2", len(got.Functions))
	}
}

func TestCallInvokesHandler(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions}))
	t.Cleanup(srv.Close)

	body := `{"name":"projects/demo-project/locations/us-central1/functions/echo","runtime":"go121"}`
	if r, _ := http.Post(srv.URL+functionsURL(), "application/json", strings.NewReader(body)); r != nil {
		_ = r.Body.Close()
	}

	cloud.CloudFunctions.RegisterHandler("echo", func(_ context.Context, payload []byte) ([]byte, error) {
		return []byte("got:" + string(payload)), nil
	})

	resp, err := http.Post(
		srv.URL+functionsURL()+"/echo:call",
		"application/json",
		strings.NewReader(`{"data":"hello"}`),
	)
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	out, _ := io.ReadAll(resp.Body)

	var cr struct {
		Result string `json:"result"`
		Error  string `json:"error"`
	}

	if err := json.Unmarshal(out, &cr); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if cr.Error != "" {
		t.Fatalf("call error = %q, want empty", cr.Error)
	}

	if cr.Result != "got:hello" {
		t.Fatalf("result = %q, want got:hello", cr.Result)
	}
}

func TestOperationPoll(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/v1/operations/some-op-id")
	if err != nil {
		t.Fatalf("op poll: %v", err)
	}

	defer resp.Body.Close()

	var op operationShape

	if err := json.NewDecoder(resp.Body).Decode(&op); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !op.Done {
		t.Fatal("op.Done = false, want true")
	}
}

// helpers --------------------------------------------------------------------

type operationShape struct {
	Name string `json:"name"`
	Done bool   `json:"done"`
}

type cloudFunctionShape struct {
	Name            string            `json:"name"`
	Status          string            `json:"status"`
	Runtime         string            `json:"runtime"`
	EntryPoint      string            `json:"entryPoint"`
	AvailableMemory int               `json:"availableMemoryMb"`
	Timeout         string            `json:"timeout"`
	Labels          map[string]string `json:"labels"`
}
