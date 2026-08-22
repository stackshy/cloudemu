package azurefunctions_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/azurefunctions"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/internal/dtest"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// functionAppPy is a real Python v2 (function_app.py) HTTP function. The route
// name matches the site/function name the engine invokes by, so the modeled
// invoke path (POST /api/doubler) reaches this handler. It doubles the "n" field
// of the JSON body — proving the REAL azure-functions host executed the code.
const functionAppPy = `import azure.functions as func

app = func.FunctionApp()

@app.route(route="doubler", auth_level=func.AuthLevel.ANONYMOUS)
def doubler(req: func.HttpRequest) -> func.HttpResponse:
    n = req.get_json()["n"]
    return func.HttpResponse(str(n * 2))
`

// hostJSON is a minimal Functions host config. HTTP triggers are a built-in
// binding, so no extension bundle (which would need a download inside the
// container) is required.
const hostJSON = `{"version":"2.0"}`

// TestAzureFunctionsE2E runs the exact flow a real user runs against Azure
// Functions, end to end through CloudEmu's modeled wire path, backed by a REAL
// azure-functions host container (no cloud account):
//
//	create the Function App (ARM site PUT) -> deploy code (Kudu zipdeploy PUT of a
//	real Python v2 app) -> invoke it (POST /api/doubler with {"n":21}) -> assert
//	the response is "42".
//
// A "42" response can only come from the real Python handler running inside the
// official Azure Functions host image — the in-memory emulator would echo the
// request payload back instead.
func TestAzureFunctionsE2E(t *testing.T) {
	if !dtest.DockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := azurefunctions.New()
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewAzure(config.WithFunctionEngine(eng))
	ts := httptest.NewTLSServer(azureserver.New(azureserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	// The zipdeploy PUT blocks until the engine has pulled the image, started the
	// host, and the app is READY — a cold image pull can take minutes.
	client := ts.Client()
	client.Timeout = 6 * time.Minute

	const (
		fn      = "doubler"
		apiVer  = "?api-version=2022-03-01"
		siteURL = "/subscriptions/00000000-0000-0000-0000-000000000000" +
			"/resourceGroups/rg-e2e/providers/Microsoft.Web/sites/" + fn
	)

	siteBody := `{
        "kind":"functionapp,linux",
        "location":"eastus",
        "properties":{
            "reserved":true,
            "siteConfig":{"linuxFxVersion":"Python|3.11"}
        }
    }`

	// 1. Create the Function App (ARM site PUT) — like `az functionapp create`.
	doAzureReq(t, client, ts.URL, http.MethodPut, siteURL+apiVer, strings.NewReader(siteBody), http.StatusOK)

	// 2. Deploy the code (Kudu zipdeploy PUT of the real app zip). This drives the
	//    engine's Deploy: extract the zip, start the host container, wait READY.
	doAzureReq(t, client, ts.URL, http.MethodPut, siteURL+"/extensions/zipdeploy",
		bytes.NewReader(functionAppZip(t)), http.StatusOK)

	// 3. Invoke the function through the modeled invoke path with {"n":21}.
	body := doAzureReq(t, client, ts.URL, http.MethodPost, "/api/"+fn,
		strings.NewReader(`{"n":21}`), http.StatusOK)

	if got := strings.TrimSpace(string(body)); got != "42" {
		t.Fatalf("invoke body = %q, want \"42\" (the real Python handler doubling 21)", got)
	}

	// 4. Delete the site — the real container is torn down and no leak remains.
	doAzureReq(t, client, ts.URL, http.MethodDelete, siteURL+apiVer, nil, http.StatusOK)
}

// TestAzureFunctionsEngineDirectE2E drives the engine directly (Deploy -> Invoke
// -> Remove), the smallest real proof that the azure-functions host executes the
// uploaded code. It complements the wire-path e2e above.
func TestAzureFunctionsEngineDirectE2E(t *testing.T) {
	if !dtest.DockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := azurefunctions.New()
	t.Cleanup(func() { _ = eng.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	if err := eng.Deploy(ctx, config.FunctionDeployment{
		Name:    "doubler",
		Runtime: "python3.11",
		Code:    functionAppZip(t),
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	res, err := eng.Invoke(ctx, "doubler", []byte(`{"n":21}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if res.FunctionError != "" {
		t.Fatalf("handler raised: %s", res.FunctionError)
	}

	if got := strings.TrimSpace(string(res.Payload)); got != "42" {
		t.Fatalf("payload = %q, want \"42\"", got)
	}

	if err := eng.Remove(ctx, "doubler"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

// functionAppZip builds the deployment zip (host.json + function_app.py) in
// memory — the exact shape a `func azure functionapp publish` / zipdeploy sends.
func functionAppZip(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for name, content := range map[string]string{
		"host.json":       hostJSON,
		"function_app.py": functionAppPy,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}

		if _, err := io.WriteString(w, content); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	return buf.Bytes()
}

// doAzureReq issues one HTTP request against the ARM test server and asserts the
// status, returning the body.
func doAzureReq(t *testing.T, client *http.Client, base, method, path string, body io.Reader, want int) []byte {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, base+path, body)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	out, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != want {
		t.Fatalf("%s %s status = %d, want %d (body: %s)", method, path, resp.StatusCode, want, out)
	}

	return out
}
