package redisenterprise_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/redisenterprise"
	redisenterprisesrv "github.com/stackshy/cloudemu/v2/server/azure/redisenterprise"
)

const (
	apiVer      = "?api-version=2024-10-01"
	clusterBase = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Cache/redisEnterprise/"
)

type clusterWire struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
	Zones    []string          `json:"zones"`
	Sku      *struct {
		Name     string `json:"name"`
		Capacity *int   `json:"capacity"`
	} `json:"sku"`
	Properties struct {
		ProvisioningState string `json:"provisioningState"`
		ResourceState     string `json:"resourceState"`
		HostName          string `json:"hostName"`
		RedisVersion      string `json:"redisVersion"`
		MinimumTLSVersion string `json:"minimumTlsVersion"`
	} `json:"properties"`
}

type databaseWire struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Properties struct {
		ProvisioningState string `json:"provisioningState"`
		ResourceState     string `json:"resourceState"`
		ClientProtocol    string `json:"clientProtocol"`
		ClusteringPolicy  string `json:"clusteringPolicy"`
		EvictionPolicy    string `json:"evictionPolicy"`
		Port              *int   `json:"port"`
		Modules           []struct {
			Name    string `json:"name"`
			Args    string `json:"args"`
			Version string `json:"version"`
		} `json:"modules"`
	} `json:"properties"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := redisenterprise.New(config.NewOptions())
	h := redisenterprisesrv.New(mock)
	srv := httptest.NewServer(h)
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

const clusterBody = `{
	"location": "West US",
	"tags": {"env": "dev"},
	"sku": {"name": "Enterprise_E10", "capacity": 2},
	"zones": ["1", "2", "3"],
	"properties": {"minimumTlsVersion": "1.2"}
}`

const databaseBody = `{
	"properties": {
		"clientProtocol": "Encrypted",
		"clusteringPolicy": "EnterpriseCluster",
		"evictionPolicy": "AllKeysLRU",
		"port": 10000,
		"modules": [{"name": "RediSearch"}]
	}
}`

func createCluster(t *testing.T, srv *httptest.Server) {
	t.Helper()

	if code, raw := do(t, srv, http.MethodPut, clusterBase+"cache1"+apiVer, clusterBody); code != http.StatusCreated {
		t.Fatalf("create cluster: %d (%s)", code, raw)
	}
}

func TestWireClusterCreateComputedFields(t *testing.T) {
	srv := newServer(t)

	code, raw := do(t, srv, http.MethodPut, clusterBase+"cache1"+apiVer, clusterBody)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", code, raw)
	}

	var got clusterWire
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}

	if got.Type != "Microsoft.Cache/redisEnterprise" {
		t.Errorf("type = %q", got.Type)
	}

	if got.Properties.HostName != "cache1.westus.redisenterprise.cache.azure.net" {
		t.Errorf("hostName = %q", got.Properties.HostName)
	}

	if got.Properties.ProvisioningState != "Succeeded" || got.Properties.ResourceState != "Running" {
		t.Errorf("states = %q/%q", got.Properties.ProvisioningState, got.Properties.ResourceState)
	}

	if got.Sku == nil || got.Sku.Name != "Enterprise_E10" || got.Sku.Capacity == nil || *got.Sku.Capacity != 2 {
		t.Errorf("sku = %+v", got.Sku)
	}

	if len(got.Zones) != 3 {
		t.Errorf("zones = %v", got.Zones)
	}
}

func TestWireClusterGetByteStable(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	_, a := do(t, srv, http.MethodGet, clusterBase+"cache1"+apiVer, "")
	_, b := do(t, srv, http.MethodGet, clusterBase+"cache1"+apiVer, "")

	if !bytes.Equal(a, b) {
		t.Errorf("cluster GET not byte-stable:\n a=%s\n b=%s", a, b)
	}
}

func TestWireClusterPatchTagsPreservesComputed(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	_, before := do(t, srv, http.MethodGet, clusterBase+"cache1"+apiVer, "")

	var beforeC clusterWire
	_ = json.Unmarshal(before, &beforeC)

	code, raw := do(t, srv, http.MethodPatch, clusterBase+"cache1"+apiVer, `{"tags":{"env":"prod"}}`)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d (%s)", code, raw)
	}

	var got clusterWire
	_ = json.Unmarshal(raw, &got)

	if got.Tags["env"] != "prod" {
		t.Errorf("tags after patch = %v", got.Tags)
	}

	if got.Properties.HostName != beforeC.Properties.HostName {
		t.Errorf("hostName drifted on patch: %q vs %q", got.Properties.HostName, beforeC.Properties.HostName)
	}

	if got.Sku == nil || got.Sku.Name != "Enterprise_E10" {
		t.Errorf("sku not preserved on patch: %+v", got.Sku)
	}
}

func TestWireClusterPatchMissingIs404(t *testing.T) {
	srv := newServer(t)

	code, _ := do(t, srv, http.MethodPatch, clusterBase+"ghost"+apiVer, `{"tags":{"a":"b"}}`)
	if code != http.StatusNotFound {
		t.Errorf("patch missing = %d, want 404", code)
	}
}

func TestWireDatabaseLifecycleAndKeys(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	dbPath := clusterBase + "cache1/databases/default" + apiVer

	code, raw := do(t, srv, http.MethodPut, dbPath, databaseBody)
	if code != http.StatusCreated {
		t.Fatalf("create database = %d, want 201 (%s)", code, raw)
	}

	var db databaseWire
	if err := json.Unmarshal(raw, &db); err != nil {
		t.Fatalf("unmarshal db: %v (%s)", err, raw)
	}

	if db.Type != "Microsoft.Cache/redisEnterprise/databases" {
		t.Errorf("db type = %q", db.Type)
	}

	if db.Name != "cache1/default" {
		t.Errorf("db name = %q, want cache1/default", db.Name)
	}

	if db.Properties.Port == nil || *db.Properties.Port != 10000 {
		t.Errorf("port = %v", db.Properties.Port)
	}

	if len(db.Properties.Modules) != 1 || db.Properties.Modules[0].Version == "" {
		t.Errorf("modules = %+v, want RediSearch with version", db.Properties.Modules)
	}

	// GET is byte-stable.
	_, g1 := do(t, srv, http.MethodGet, dbPath, "")
	_, g2 := do(t, srv, http.MethodGet, dbPath, "")

	if !bytes.Equal(g1, g2) {
		t.Errorf("database GET not byte-stable")
	}

	// listKeys is byte-stable across calls.
	keysPath := clusterBase + "cache1/databases/default/listKeys" + apiVer

	_, k1 := do(t, srv, http.MethodPost, keysPath, "")
	_, k2 := do(t, srv, http.MethodPost, keysPath, "")

	if !bytes.Equal(k1, k2) {
		t.Errorf("listKeys not byte-stable:\n a=%s\n b=%s", k1, k2)
	}

	var keys struct {
		PrimaryKey   string `json:"primaryKey"`
		SecondaryKey string `json:"secondaryKey"`
	}
	if err := json.Unmarshal(k1, &keys); err != nil {
		t.Fatalf("unmarshal keys: %v (%s)", err, k1)
	}

	if keys.PrimaryKey == "" || keys.SecondaryKey == "" {
		t.Errorf("keys empty: %+v", keys)
	}
}

func TestWireDatabaseCreateMissingClusterIs404(t *testing.T) {
	srv := newServer(t)

	code, raw := do(t, srv, http.MethodPut, clusterBase+"ghost/databases/default"+apiVer, databaseBody)
	if code != http.StatusNotFound {
		t.Errorf("create db missing parent = %d, want 404 (%s)", code, raw)
	}
}

func TestWireDatabaseList(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	for _, name := range []string{"default", "db2"} {
		if code, raw := do(t, srv, http.MethodPut,
			clusterBase+"cache1/databases/"+name+apiVer, databaseBody); code != http.StatusCreated {
			t.Fatalf("create %s: %d (%s)", name, code, raw)
		}
	}

	code, raw := do(t, srv, http.MethodGet, clusterBase+"cache1/databases"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list = %d (%s)", code, raw)
	}

	var out struct {
		Value []databaseWire `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal list: %v (%s)", err, raw)
	}

	if len(out.Value) != 2 {
		t.Errorf("list len = %d, want 2", len(out.Value))
	}
}

func TestWireDeleteClusterCascadesAndIdempotent(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	if code, raw := do(t, srv, http.MethodPut,
		clusterBase+"cache1/databases/default"+apiVer, databaseBody); code != http.StatusCreated {
		t.Fatalf("create db: %d (%s)", code, raw)
	}

	if code, _ := do(t, srv, http.MethodDelete, clusterBase+"cache1"+apiVer, ""); code != http.StatusOK {
		t.Errorf("first delete = %d, want 200", code)
	}

	if code, _ := do(t, srv, http.MethodDelete, clusterBase+"cache1"+apiVer, ""); code != http.StatusNoContent {
		t.Errorf("second delete = %d, want 204", code)
	}

	// The database was cascaded away.
	if code, _ := do(t, srv, http.MethodGet, clusterBase+"cache1/databases/default"+apiVer, ""); code != http.StatusNotFound {
		t.Errorf("cascaded database GET = %d, want 404", code)
	}
}

func TestWireClusterList(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	if code, raw := do(t, srv, http.MethodPut, clusterBase+"cache2"+apiVer, clusterBody); code != http.StatusCreated {
		t.Fatalf("create cache2: %d (%s)", code, raw)
	}

	code, raw := do(t, srv, http.MethodGet, clusterBase+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list = %d (%s)", code, raw)
	}

	var out struct {
		Value []clusterWire `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal list: %v (%s)", err, raw)
	}

	if len(out.Value) != 2 {
		t.Errorf("cluster list len = %d, want 2", len(out.Value))
	}
}
