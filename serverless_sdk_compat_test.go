package cloudemu_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
	azureserver "github.com/stackshy/cloudemu/server/azure"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
	sdrv "github.com/stackshy/cloudemu/serverless/driver"
)

// TestServerlessSDKCompat_CrossProvider verifies that all 3 provider-specific
// SDK-compat servers (AWS Lambda, Azure Functions, GCP Cloud Functions) are
// properly registered through their respective server factories and serve
// the same lifecycle (create → invoke → delete) when driven over HTTP.
//
// The test uses raw HTTP rather than each cloud's official SDK so it
// verifies the wire-format paths in the same place that
// SetMonitoring-style cross-service wiring is asserted.
func TestServerlessSDKCompat_CrossProvider(t *testing.T) {
	t.Run("aws", func(t *testing.T) {
		cloud := cloudemu.NewAWS()
		ts := httptest.NewServer(awsserver.New(awsserver.Drivers{Lambda: cloud.Lambda}))
		t.Cleanup(ts.Close)

		registerEcho(cloud.Lambda, "echo")

		// Create.
		body := `{"FunctionName":"echo","Runtime":"go1.x","Handler":"main"}`
		mustStatus(t, postJSON(t, ts.URL+"/2015-03-31/functions", body), http.StatusCreated)

		// Invoke.
		invoke := postJSON(t, ts.URL+"/2015-03-31/functions/echo/invocations", `"hi"`)
		mustStatus(t, invoke, http.StatusOK)

		got := readBody(t, invoke)
		if got != `{"echoed":"hi"}` {
			t.Fatalf("invoke body = %q, want {\"echoed\":\"hi\"}", got)
		}

		// Delete.
		mustStatus(t, mustDelete(t, ts.URL+"/2015-03-31/functions/echo"), http.StatusNoContent)
	})

	t.Run("azure", func(t *testing.T) {
		cloud := cloudemu.NewAzure()
		ts := httptest.NewServer(azureserver.New(azureserver.Drivers{Functions: cloud.Functions}))
		t.Cleanup(ts.Close)

		registerEcho(cloud.Functions, "echo")

		const (
			subID  = "00000000-0000-0000-0000-000000000000"
			rgName = "rg-test"
			apiVer = "?api-version=2022-03-01"
		)
		baseURL := ts.URL + "/subscriptions/" + subID +
			"/resourceGroups/" + rgName +
			"/providers/Microsoft.Web/sites/echo"

		body := `{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{"linuxFxVersion":"Python|3.10"}}}`
		mustStatus(t, putJSON(t, baseURL+apiVer, body), http.StatusOK)

		// Invoke via the non-ARM /api/{name} surface.
		invoke := postJSON(t, ts.URL+"/api/echo", `"hi"`)
		mustStatus(t, invoke, http.StatusOK)

		got := readBody(t, invoke)
		if got != `{"echoed":"hi"}` {
			t.Fatalf("invoke body = %q, want {\"echoed\":\"hi\"}", got)
		}

		mustStatus(t, mustDelete(t, baseURL+apiVer), http.StatusOK)
	})

	t.Run("gcp", func(t *testing.T) {
		cloud := cloudemu.NewGCP()
		ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{
			CloudFunctions: cloud.CloudFunctions,
			Firestore:      cloud.Firestore, // exercise registration ordering
		}))
		t.Cleanup(ts.Close)

		registerEcho(cloud.CloudFunctions, "echo")

		base := ts.URL + "/v1/projects/demo/locations/us-central1/functions"

		body := `{"name":"projects/demo/locations/us-central1/functions/echo","runtime":"go121","entryPoint":"H"}`
		mustStatus(t, postJSON(t, base, body), http.StatusOK)

		// :call invoke. The `data` field carries the raw payload as a string,
		// so we send the JSON literal "hi" (with embedded quotes) to match
		// the AWS/Azure shape the echo handler produces.
		invoke := postJSON(t, base+"/echo:call", `{"data":"\"hi\""}`)
		mustStatus(t, invoke, http.StatusOK)

		var cr struct {
			Result string `json:"result"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal([]byte(readBody(t, invoke)), &cr); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if cr.Result != `{"echoed":"hi"}` {
			t.Fatalf("call result = %q, want {\"echoed\":\"hi\"}", cr.Result)
		}

		mustStatus(t, mustDelete(t, base+"/echo"), http.StatusOK)
	})
}

// registerEcho wires a deterministic handler so all 3 provider tests can
// assert the same expected output.
func registerEcho(fn sdrv.Serverless, name string) {
	fn.RegisterHandler(name, func(_ context.Context, payload []byte) ([]byte, error) {
		out := append([]byte(`{"echoed":`), payload...)
		out = append(out, '}')
		return out, nil
	})
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()

	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}

	return resp
}

func putJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPut, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}

	return resp
}

func mustDelete(t *testing.T, url string) *http.Response {
	t.Helper()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete, url, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}

	return resp
}

func mustStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()

	if resp.StatusCode != want {
		body := readBody(t, resp)
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, want, body)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return strings.TrimSpace(string(b))
}
