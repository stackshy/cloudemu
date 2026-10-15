package azure_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	isoAcctA  = "acctaa"
	isoAcctB  = "acctbb"
	queueUA   = "azsdk-go-azqueue/v1.0.0 (go1.25; darwin)"
	tableUA   = "azsdk-go-aztables/v1.3.0 (go1.25; darwin)"
	xmsBlobTp = "x-ms-blob-type"
)

// newIsolationServer starts the full Azure server with two storage accounts.
func newIsolationServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts, p := newFullAzureServerWithProvider(t)

	for _, name := range []string{isoAcctA, isoAcctB} {
		ref := storagedriver.StorageAccountRef{Name: name, ResourceGroup: "rg"}
		if _, err := p.BlobStorage.CreateStorageAccount(context.Background(), ref); err != nil {
			t.Fatalf("create account %s: %v", name, err)
		}
	}

	return ts
}

// storageDo sends one data-plane request with the given Host and User-Agent
// and returns the status and body.
func storageDo(t *testing.T, ts *httptest.Server, host, ua, method, path, body string,
	hdr map[string]string,
) (status int, respBody string, header http.Header) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if host != "" {
		req.Host = host
	}

	req.Header.Set("User-Agent", ua)

	for k, v := range hdr {
		req.Header.Set(k, v)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(data), resp.Header
}

func expectStatus(t *testing.T, what string, got, want int, body string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %d, want %d: %s", what, got, want, body)
	}
}

// TestQueueAccountIsolation is the regression for AZSTO-06 on Queue: two
// accounts each get their own "jobs" queue, a message in one is invisible to
// the other, and listings only show the account's own queues. The default
// account (bare host) is separate again, and the path-style /{account}/ form
// reaches the same queue as the account host.
func TestQueueAccountIsolation(t *testing.T) {
	ts := newIsolationServer(t)
	hostA := isoAcctA + ".queue.core.windows.net"
	hostB := isoAcctB + ".queue.core.windows.net"

	for _, h := range []string{hostA, hostB} {
		st, body, _ := storageDo(t, ts, h, queueUA, http.MethodPut, "/jobs", "", nil)
		expectStatus(t, "create jobs on "+h, st, http.StatusCreated, body)
	}

	msg := "<QueueMessage><MessageText>hello</MessageText></QueueMessage>"
	st, body, _ := storageDo(t, ts, hostA, queueUA, http.MethodPost, "/jobs/messages", msg, nil)
	expectStatus(t, "put message on A", st, http.StatusCreated, body)

	_, body, _ = storageDo(t, ts, hostB, queueUA, http.MethodGet, "/jobs/messages?peekonly=true", "", nil)
	if strings.Contains(body, "hello") {
		t.Fatalf("account B sees account A's message: %s", body)
	}

	_, body, _ = storageDo(t, ts, "", queueUA, http.MethodGet, "/"+isoAcctA+"/jobs/messages?peekonly=true", "", nil)
	if !strings.Contains(body, "hello") {
		t.Fatalf("path-style peek on account A = %s, want the message", body)
	}

	_, body, _ = storageDo(t, ts, "", queueUA, http.MethodGet, "/?comp=list", "", nil)
	if strings.Contains(body, "<Name>jobs</Name>") || strings.Contains(body, isoAcctA) {
		t.Fatalf("default account lists another account's queue: %s", body)
	}

	_, body, _ = storageDo(t, ts, hostB, queueUA, http.MethodGet, "/?comp=list", "", nil)
	if !strings.Contains(body, "<Name>jobs</Name>") {
		t.Fatalf("account B list = %s, want jobs", body)
	}

	st, body, _ = storageDo(t, ts, hostA, queueUA, http.MethodDelete, "/jobs", "", nil)
	expectStatus(t, "delete jobs on A", st, http.StatusNoContent, body)

	st, body, _ = storageDo(t, ts, hostB, queueUA, http.MethodGet, "/jobs?comp=metadata", "", nil)
	expectStatus(t, "account B jobs after A's delete", st, http.StatusOK, body)
}

// TestTableAccountIsolation is the regression for AZSTO-06 on Table: the same
// table name in two accounts holds separate entities and lists separately.
func TestTableAccountIsolation(t *testing.T) {
	ts := newIsolationServer(t)
	hostA := isoAcctA + ".table.core.windows.net"
	hostB := isoAcctB + ".table.core.windows.net"
	jsonHdr := map[string]string{"Content-Type": "application/json", "Accept": "application/json;odata=nometadata"}

	for _, h := range []string{hostA, hostB} {
		st, body, _ := storageDo(t, ts, h, tableUA, http.MethodPost, "/Tables", `{"TableName":"people"}`, jsonHdr)
		expectStatus(t, "create people on "+h, st, http.StatusCreated, body)
	}

	st, body, _ := storageDo(t, ts, hostA, tableUA, http.MethodPost, "/people",
		`{"PartitionKey":"org","RowKey":"alice"}`, jsonHdr)
	expectStatus(t, "insert on A", st, http.StatusCreated, body)

	st, body, _ = storageDo(t, ts, hostB, tableUA, http.MethodGet,
		"/people(PartitionKey='org',RowKey='alice')", "", jsonHdr)
	expectStatus(t, "get on B", st, http.StatusNotFound, body)

	st, body, _ = storageDo(t, ts, "", tableUA, http.MethodGet,
		"/"+isoAcctA+"/people(PartitionKey='org',RowKey='alice')", "", jsonHdr)
	expectStatus(t, "path-style get on A", st, http.StatusOK, body)

	_, body, _ = storageDo(t, ts, "", tableUA, http.MethodGet, "/Tables", "", jsonHdr)
	if strings.Contains(body, "people") {
		t.Fatalf("default account lists another account's table: %s", body)
	}

	_, body, _ = storageDo(t, ts, "", tableUA, http.MethodGet, "/"+isoAcctB+"/Tables", "", jsonHdr)
	if !strings.Contains(body, `"TableName":"people"`) {
		t.Fatalf("path-style list on B = %s, want people", body)
	}
}

// TestStorageRootListAndServiceOps is the regression for AZSTO-07 and
// AZSTO-08: a bare-host "GET /?comp=list" is List Containers unless a Queue
// client sends it, and the account-level service calls reach Blob, Queue or
// Table by host instead of the Cosmos account probe.
func TestStorageRootListAndServiceOps(t *testing.T) {
	ts := newIsolationServer(t)
	blobA := isoAcctA + ".blob.core.windows.net"

	st, body, _ := storageDo(t, ts, "", "", http.MethodPut, "/ctr1?restype=container", "", nil)
	expectStatus(t, "create container", st, http.StatusCreated, body)

	st, body, _ = storageDo(t, ts, "", "", http.MethodPut, "/q1", "", nil)
	expectStatus(t, "create queue", st, http.StatusCreated, body)

	_, body, _ = storageDo(t, ts, "", "azsdk-go-azblob/v1.6.0", http.MethodGet, "/?comp=list", "", nil)
	if !strings.Contains(body, "<Name>ctr1</Name>") {
		t.Fatalf("bare-host blob list = %s, want ctr1", body)
	}

	_, body, _ = storageDo(t, ts, "", queueUA, http.MethodGet, "/?comp=list", "", nil)
	if !strings.Contains(body, "<Name>q1</Name>") {
		t.Fatalf("bare-host queue list = %s, want q1", body)
	}

	const props = "/?restype=service&comp=properties"

	for _, tc := range []struct{ host, ua string }{
		{"", ""},
		{blobA, ""},
		{isoAcctA + ".queue.core.windows.net", ""},
		{isoAcctA + ".table.core.windows.net", ""},
		{"", queueUA},
		{"", tableUA},
	} {
		st, body, _ := storageDo(t, ts, tc.host, tc.ua, http.MethodGet, props, "", nil)
		if st != http.StatusOK || !strings.Contains(body, "<StorageServiceProperties>") {
			t.Fatalf("GET service properties on %q/%q = %d %s", tc.host, tc.ua, st, body)
		}
	}

	set := `<?xml version="1.0" encoding="utf-8"?><StorageServiceProperties>` +
		`<DeleteRetentionPolicy><Enabled>true</Enabled><Days>7</Days></DeleteRetentionPolicy>` +
		`<Cors><CorsRule><AllowedOrigins>https://a.example</AllowedOrigins><AllowedMethods>GET,PUT</AllowedMethods>` +
		`<AllowedHeaders>*</AllowedHeaders><ExposedHeaders>*</ExposedHeaders><MaxAgeInSeconds>60</MaxAgeInSeconds>` +
		`</CorsRule></Cors></StorageServiceProperties>`

	st, body, _ = storageDo(t, ts, blobA, "", http.MethodPut, props, set, nil)
	expectStatus(t, "set blob service properties", st, http.StatusAccepted, body)

	_, body, _ = storageDo(t, ts, blobA, "", http.MethodGet, props, "", nil)
	if !strings.Contains(body, "<Days>7</Days>") || !strings.Contains(body, "https://a.example") {
		t.Fatalf("blob service properties after set = %s", body)
	}

	_, body, _ = storageDo(t, ts, isoAcctB+".blob.core.windows.net", "", http.MethodGet, props, "", nil)
	if strings.Contains(body, "https://a.example") {
		t.Fatalf("account B sees account A's blob service properties: %s", body)
	}

	st, body, hdr := storageDo(t, ts, "", "", http.MethodGet, "/?restype=account&comp=properties", "", nil)
	if st != http.StatusOK || hdr.Get("x-ms-account-kind") != "StorageV2" {
		t.Fatalf("get account info = %d kind %q: %s", st, hdr.Get("x-ms-account-kind"), body)
	}
}

// TestBareHostRootListByUserAgent pins the bare-host rule for the shared
// "GET /?comp=list" shape: only an Azure SDK Queue product token picks Queue.
// An application id that merely contains "queue" stays with Blob, as does a
// request with no User-Agent.
func TestBareHostRootListByUserAgent(t *testing.T) {
	ts := newIsolationServer(t)

	st, body, _ := storageDo(t, ts, "", "", http.MethodPut, "/ctr1?restype=container", "", nil)
	expectStatus(t, "create container", st, http.StatusCreated, body)

	st, body, _ = storageDo(t, ts, "", "", http.MethodPut, "/q1", "", nil)
	expectStatus(t, "create queue", st, http.StatusCreated, body)

	tests := []struct {
		name, ua, want string
	}{
		{"blob client with queue-like app id", "myqueue-worker azsdk-go-azblob/v1.6.0 (go1.25; darwin)", "ctr1"},
		{"azqueue", queueUA, "q1"},
		{"python queue sdk", "azsdk-python-storage-queue/12.10.0 Python/3.12", "q1"},
		{"net queue sdk", "azsdk-net-Storage.Queues/12.19.0 (.NET 8.0)", "q1"},
		{"no user agent", "", "ctr1"},
		{"table client with queue-like app id", "queue-sync azsdk-go-aztables/v1.3.0", "ctr1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, body, _ := storageDo(t, ts, "", tt.ua, http.MethodGet, "/?comp=list", "", nil)
			if st != http.StatusOK || !strings.Contains(body, "<Name>"+tt.want+"</Name>") {
				t.Fatalf("GET /?comp=list with UA %q = %d %s, want %s", tt.ua, st, body, tt.want)
			}
		})
	}
}

// TestQueuePathStyleLeavesBlobPuts guards the path-style peel: a Put Blob into
// a container of the default account whose name matches a storage account is
// still a blob write, not a queue create.
func TestQueuePathStyleLeavesBlobPuts(t *testing.T) {
	ts := newIsolationServer(t)

	st, body, _ := storageDo(t, ts, "", "", http.MethodPut, "/"+isoAcctA+"/ctr9?restype=container", "", nil)
	expectStatus(t, "path-style create container", st, http.StatusCreated, body)

	st, body, _ = storageDo(t, ts, "", "", http.MethodPut, "/"+isoAcctA+"/ctr9/b1", "data",
		map[string]string{xmsBlobTp: "BlockBlob"})
	expectStatus(t, "path-style put blob", st, http.StatusCreated, body)

	st, body, _ = storageDo(t, ts, "", "", http.MethodGet, "/"+isoAcctA+"/ctr9/b1", "", nil)
	if st != http.StatusOK || body != "data" {
		t.Fatalf("path-style get blob = %d %q", st, body)
	}
}
