package batch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/batch"
	batchsrv "github.com/stackshy/cloudemu/v2/server/azure/batch"
)

const (
	apiVer      = "?api-version=2024-07-01"
	accountBase = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Batch/batchAccounts/"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := batch.New(config.NewOptions())
	srv := httptest.NewServer(batchsrv.New(mock))
	t.Cleanup(srv.Close)

	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, raw
}

const accountBody = `{"location": "West US", "tags": {"env": "dev"}}`

const poolBody = `{
	"properties": {
		"vmSize": "STANDARD_D1_V2",
		"displayName": "My Pool",
		"deploymentConfiguration": {"virtualMachineConfiguration": {"nodeAgentSkuId": "batch.node.ubuntu 22.04"}},
		"scaleSettings": {"fixedScale": {"targetDedicatedNodes": 3, "targetLowPriorityNodes": 1}}
	}
}`

func createAccount(t *testing.T, srv *httptest.Server) {
	t.Helper()

	if code, raw := do(t, srv, http.MethodPut, accountBase+"acct1"+apiVer, accountBody); code != http.StatusCreated {
		t.Fatalf("create account: %d (%s)", code, raw)
	}
}

func TestWireAccountCreateComputedFieldsNoKeys(t *testing.T) {
	srv := newServer(t)

	code, raw := do(t, srv, http.MethodPut, accountBase+"acct1"+apiVer, accountBody)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", code, raw)
	}

	var got struct {
		Type       string `json:"type"`
		Properties struct {
			AccountEndpoint        string `json:"accountEndpoint"`
			NodeManagementEndpoint string `json:"nodeManagementEndpoint"`
			ProvisioningState      string `json:"provisioningState"`
			PoolAllocationMode     string `json:"poolAllocationMode"`
			DedicatedCoreQuota     int    `json:"dedicatedCoreQuota"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}

	if got.Type != "Microsoft.Batch/batchAccounts" {
		t.Errorf("type = %q", got.Type)
	}

	if got.Properties.AccountEndpoint != "acct1.westus.batch.azure.com" {
		t.Errorf("accountEndpoint = %q", got.Properties.AccountEndpoint)
	}

	if got.Properties.ProvisioningState != "Succeeded" || got.Properties.PoolAllocationMode != "BatchService" {
		t.Errorf("state/mode = %q/%q", got.Properties.ProvisioningState, got.Properties.PoolAllocationMode)
	}

	if got.Properties.DedicatedCoreQuota != 20 {
		t.Errorf("dedicatedCoreQuota = %d, want 20", got.Properties.DedicatedCoreQuota)
	}

	// Keys must NOT be echoed on the account body.
	if strings.Contains(string(raw), "primaryKey") || strings.Contains(string(raw), "\"primary\"") {
		t.Errorf("account body leaked keys: %s", raw)
	}
}

func TestWireAccountGetByteStableNoKeys(t *testing.T) {
	srv := newServer(t)
	createAccount(t, srv)

	_, a := do(t, srv, http.MethodGet, accountBase+"acct1"+apiVer, "")
	_, b := do(t, srv, http.MethodGet, accountBase+"acct1"+apiVer, "")

	if !bytes.Equal(a, b) {
		t.Errorf("account GET not byte-stable:\n a=%s\n b=%s", a, b)
	}

	if strings.Contains(string(a), "primary") || strings.Contains(string(a), "secondary") {
		t.Errorf("account GET leaked keys: %s", a)
	}
}

func TestWireListKeysByteStableAndRegenerate(t *testing.T) {
	srv := newServer(t)
	createAccount(t, srv)

	keysPath := accountBase + "acct1/listKeys" + apiVer

	_, k1 := do(t, srv, http.MethodPost, keysPath, "")
	_, k2 := do(t, srv, http.MethodPost, keysPath, "")

	if !bytes.Equal(k1, k2) {
		t.Errorf("listKeys not byte-stable:\n a=%s\n b=%s", k1, k2)
	}

	var keys struct {
		AccountName string `json:"accountName"`
		Primary     string `json:"primary"`
		Secondary   string `json:"secondary"`
	}
	if err := json.Unmarshal(k1, &keys); err != nil {
		t.Fatalf("unmarshal keys: %v (%s)", err, k1)
	}

	if keys.AccountName != "acct1" || keys.Primary == "" || keys.Secondary == "" {
		t.Errorf("keys body = %+v", keys)
	}

	// Regenerate Primary: Primary changes, Secondary byte-stable.
	code, raw := do(t, srv, http.MethodPost, accountBase+"acct1/regenerateKey"+apiVer, `{"keyName":"Primary"}`)
	if code != http.StatusOK {
		t.Fatalf("regenerate = %d (%s)", code, raw)
	}

	var reg struct {
		Primary   string `json:"primary"`
		Secondary string `json:"secondary"`
	}
	_ = json.Unmarshal(raw, &reg)

	if reg.Primary == keys.Primary {
		t.Errorf("Primary unchanged after regenerate")
	}

	if reg.Secondary != keys.Secondary {
		t.Errorf("Secondary changed on Primary regenerate: %q vs %q", reg.Secondary, keys.Secondary)
	}
}

func TestWireSyncAutoStorageKeys(t *testing.T) {
	srv := newServer(t)
	createAccount(t, srv)

	code, _ := do(t, srv, http.MethodPost, accountBase+"acct1/syncAutoStorageKeys"+apiVer, "")
	if code != http.StatusNoContent {
		t.Errorf("syncAutoStorageKeys = %d, want 204", code)
	}
}

func TestWirePoolLifecycleAndStateMachine(t *testing.T) {
	srv := newServer(t)
	createAccount(t, srv)

	poolPath := accountBase + "acct1/pools/pool1" + apiVer

	code, raw := do(t, srv, http.MethodPut, poolPath, poolBody)
	if code != http.StatusCreated {
		t.Fatalf("create pool = %d, want 201 (%s)", code, raw)
	}

	var pool struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		Properties struct {
			AllocationState       string `json:"allocationState"`
			ProvisioningState     string `json:"provisioningState"`
			CurrentDedicatedNodes int    `json:"currentDedicatedNodes"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &pool); err != nil {
		t.Fatalf("unmarshal pool: %v (%s)", err, raw)
	}

	if pool.Name != "acct1/pool1" || pool.Type != "Microsoft.Batch/batchAccounts/pools" {
		t.Errorf("pool name/type = %q/%q", pool.Name, pool.Type)
	}

	if pool.Properties.AllocationState != "Steady" || pool.Properties.CurrentDedicatedNodes != 3 {
		t.Errorf("pool state = %q current=%d", pool.Properties.AllocationState, pool.Properties.CurrentDedicatedNodes)
	}

	// GET byte-stable.
	_, g1 := do(t, srv, http.MethodGet, poolPath, "")
	_, g2 := do(t, srv, http.MethodGet, poolPath, "")

	if !bytes.Equal(g1, g2) {
		t.Errorf("pool GET not byte-stable")
	}

	// resize: Steady -> Resizing.
	code, raw = do(t, srv, http.MethodPost, accountBase+"acct1/pools/pool1/resize"+apiVer,
		`{"properties":{"targetDedicatedNodes":5,"targetLowPriorityNodes":2}}`)
	if code != http.StatusOK {
		t.Fatalf("resize = %d (%s)", code, raw)
	}

	if !strings.Contains(string(raw), `"allocationState":"Resizing"`) {
		t.Errorf("resize did not enter Resizing: %s", raw)
	}

	// stopResize: Resizing -> Steady.
	code, raw = do(t, srv, http.MethodPost, accountBase+"acct1/pools/pool1/stopResize"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("stopResize = %d (%s)", code, raw)
	}

	if !strings.Contains(string(raw), `"allocationState":"Steady"`) {
		t.Errorf("stopResize did not settle to Steady: %s", raw)
	}

	// stopResize again (illegal on a Steady pool) -> 409.
	code, _ = do(t, srv, http.MethodPost, accountBase+"acct1/pools/pool1/stopResize"+apiVer, "")
	if code != http.StatusConflict {
		t.Errorf("stopResize on Steady = %d, want 409", code)
	}
}

func TestWirePoolCreateMissingAccountIs404(t *testing.T) {
	srv := newServer(t)

	code, _ := do(t, srv, http.MethodPut, accountBase+"ghost/pools/pool1"+apiVer, poolBody)
	if code != http.StatusNotFound {
		t.Errorf("create pool missing parent = %d, want 404", code)
	}
}

func TestWirePoolPatchTagsReplacePreservesComputed(t *testing.T) {
	srv := newServer(t)
	createAccount(t, srv)

	poolPath := accountBase + "acct1/pools/pool1" + apiVer

	if code, raw := do(t, srv, http.MethodPut, poolPath,
		`{"tags":{"a":"1","b":"2"},"properties":{"vmSize":"STANDARD_D1_V2","scaleSettings":{"fixedScale":{"targetDedicatedNodes":3}}}}`,
	); code != http.StatusCreated {
		t.Fatalf("create pool: %d (%s)", code, raw)
	}

	code, raw := do(t, srv, http.MethodPatch, poolPath, `{"tags":{"a":"9"}}`)
	if code != http.StatusOK {
		t.Fatalf("patch = %d (%s)", code, raw)
	}

	var got struct {
		Tags       map[string]string `json:"tags"`
		Properties struct {
			VMSize string `json:"vmSize"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(raw, &got)

	if len(got.Tags) != 1 || got.Tags["a"] != "9" {
		t.Errorf("tags after patch = %v, want {a:9} (REPLACE)", got.Tags)
	}

	if got.Properties.VMSize != "STANDARD_D1_V2" {
		t.Errorf("vmSize dropped on patch: %q", got.Properties.VMSize)
	}
}

func TestWireAccountDeleteCascadesAndIdempotent(t *testing.T) {
	srv := newServer(t)
	createAccount(t, srv)

	if code, raw := do(t, srv, http.MethodPut, accountBase+"acct1/pools/pool1"+apiVer, poolBody); code != http.StatusCreated {
		t.Fatalf("create pool: %d (%s)", code, raw)
	}

	if code, _ := do(t, srv, http.MethodDelete, accountBase+"acct1"+apiVer, ""); code != http.StatusOK {
		t.Errorf("first delete = %d, want 200", code)
	}

	if code, _ := do(t, srv, http.MethodDelete, accountBase+"acct1"+apiVer, ""); code != http.StatusNoContent {
		t.Errorf("second delete = %d, want 204", code)
	}

	if code, _ := do(t, srv, http.MethodGet, accountBase+"acct1/pools/pool1"+apiVer, ""); code != http.StatusNotFound {
		t.Errorf("cascaded pool GET = %d, want 404", code)
	}
}

func TestWireAccountAndPoolList(t *testing.T) {
	srv := newServer(t)
	createAccount(t, srv)

	if code, raw := do(t, srv, http.MethodPut, accountBase+"acct2"+apiVer, accountBody); code != http.StatusCreated {
		t.Fatalf("create acct2: %d (%s)", code, raw)
	}

	for _, name := range []string{"pool1", "pool2"} {
		if code, raw := do(t, srv, http.MethodPut, accountBase+"acct1/pools/"+name+apiVer, poolBody); code != http.StatusCreated {
			t.Fatalf("create %s: %d (%s)", name, code, raw)
		}
	}

	code, raw := do(t, srv, http.MethodGet, accountBase+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list accounts = %d (%s)", code, raw)
	}

	var accts struct {
		Value []json.RawMessage `json:"value"`
	}
	_ = json.Unmarshal(raw, &accts)

	if len(accts.Value) != 2 {
		t.Errorf("account list len = %d, want 2", len(accts.Value))
	}

	code, raw = do(t, srv, http.MethodGet, accountBase+"acct1/pools"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list pools = %d (%s)", code, raw)
	}

	var pools struct {
		Value []json.RawMessage `json:"value"`
	}
	_ = json.Unmarshal(raw, &pools)

	if len(pools.Value) != 2 {
		t.Errorf("pool list len = %d, want 2", len(pools.Value))
	}
}

func TestWirePatchMissingAccountIs404(t *testing.T) {
	srv := newServer(t)

	code, _ := do(t, srv, http.MethodPatch, accountBase+"ghost"+apiVer, `{"tags":{"a":"b"}}`)
	if code != http.StatusNotFound {
		t.Errorf("patch missing account = %d, want 404", code)
	}
}
