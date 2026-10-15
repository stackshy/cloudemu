package azure_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestStorageHostContainerNamesNotStolen proves a blob container whose name is
// a Cosmos ("dbs") or Search ("indexes") data-plane root stays with the Blob
// handler when the request targets a storage account host: create the
// container, put a blob, read it back and list the container.
func TestStorageHostContainerNamesNotStolen(t *testing.T) {
	ts := httptest.NewTLSServer(azureserver.NewFromProvider(cloudemu.NewAzure()))
	t.Cleanup(ts.Close)

	do := func(t *testing.T, method, path, body string, headers map[string]string) (status int, respBody string) {
		t.Helper()

		req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}

		req.Host = "acct1.blob.core.windows.net"
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()

		raw, _ := io.ReadAll(resp.Body)

		return resp.StatusCode, string(raw)
	}

	for _, container := range []string{"dbs", "indexes"} {
		t.Run(container, func(t *testing.T) {
			if status, body := do(t, http.MethodPut, "/"+container+"?restype=container", "", nil); status != http.StatusCreated {
				t.Fatalf("create container: status %d, body %s", status, body)
			}

			blob := "/" + container + "/obj1"
			blockBlob := map[string]string{"x-ms-blob-type": "BlockBlob"}

			if status, body := do(t, http.MethodPut, blob, "hello", blockBlob); status != http.StatusCreated {
				t.Fatalf("put blob: status %d, body %s", status, body)
			}

			if status, body := do(t, http.MethodGet, blob, "", nil); status != http.StatusOK || body != "hello" {
				t.Fatalf("get blob: status %d, body %q, want 200 hello", status, body)
			}

			status, body := do(t, http.MethodGet, "/"+container+"?restype=container&comp=list", "", nil)
			if status != http.StatusOK || !strings.Contains(body, "<Name>obj1</Name>") {
				t.Fatalf("list blobs: status %d, body %s", status, body)
			}
		})
	}
}
