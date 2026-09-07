package mongocluster_test

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
	"github.com/stackshy/cloudemu/v2/providers/azure/mongocluster"
	mongoclustersrv "github.com/stackshy/cloudemu/v2/server/azure/mongocluster"
)

const (
	apiVer      = "?api-version=2024-07-01"
	clusterBase = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DocumentDB/mongoClusters/"
)

type clusterWire struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags"`
	Properties struct {
		ProvisioningState string `json:"provisioningState"`
		ClusterStatus     string `json:"clusterStatus"`
		Administrator     *struct {
			UserName string `json:"userName"`
			Password string `json:"password"`
		} `json:"administrator"`
		ServerVersion string `json:"serverVersion"`
		CreateMode    string `json:"createMode"`
		Compute       *struct {
			Tier string `json:"tier"`
		} `json:"compute"`
		Storage *struct {
			SizeGb *int64 `json:"sizeGb"`
		} `json:"storage"`
		Sharding *struct {
			ShardCount *int32 `json:"shardCount"`
		} `json:"sharding"`
		HighAvailability *struct {
			TargetMode string `json:"targetMode"`
		} `json:"highAvailability"`
		ConnectionString string `json:"connectionString"`
	} `json:"properties"`
}

type connStringsWire struct {
	ConnectionStrings []struct {
		ConnectionString string `json:"connectionString"`
		Description      string `json:"description"`
		Name             string `json:"name"`
	} `json:"connectionStrings"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := mongocluster.New(config.NewOptions())
	h := mongoclustersrv.New(mock)
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
	"location": "West US 2",
	"tags": {"env": "dev"},
	"properties": {
		"administrator": {"userName": "mongoAdmin", "password": "Sup3rSecret!"},
		"serverVersion": "7.0",
		"compute": {"tier": "M30"},
		"storage": {"sizeGb": 128},
		"sharding": {"shardCount": 1},
		"highAvailability": {"targetMode": "SameZone"}
	}
}`

func createCluster(t *testing.T, srv *httptest.Server) clusterWire {
	t.Helper()

	code, raw := do(t, srv, http.MethodPut, clusterBase+"mongo1"+apiVer, clusterBody)
	if code != http.StatusCreated {
		t.Fatalf("create cluster: %d (%s)", code, raw)
	}

	return unmarshalCluster(t, raw)
}

func unmarshalCluster(t *testing.T, raw []byte) clusterWire {
	t.Helper()

	var c clusterWire
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal cluster: %v (%s)", err, raw)
	}

	return c
}

func TestCreateAndGet(t *testing.T) {
	srv := newServer(t)
	created := createCluster(t, srv)

	if created.Type != "Microsoft.DocumentDB/mongoClusters" {
		t.Errorf("type = %q", created.Type)
	}

	if created.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q", created.Properties.ProvisioningState)
	}

	if created.Properties.ClusterStatus != "Ready" {
		t.Errorf("clusterStatus = %q", created.Properties.ClusterStatus)
	}

	code, raw := do(t, srv, http.MethodGet, clusterBase+"mongo1"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("get: %d (%s)", code, raw)
	}

	got := unmarshalCluster(t, raw)

	if got.Properties.Administrator == nil || got.Properties.Administrator.UserName != "mongoAdmin" {
		t.Errorf("administrator userName = %+v", got.Properties.Administrator)
	}

	if got.Properties.Compute == nil || got.Properties.Compute.Tier != "M30" {
		t.Errorf("compute = %+v", got.Properties.Compute)
	}

	if got.Properties.ConnectionString == "" ||
		!strings.HasPrefix(got.Properties.ConnectionString, "mongodb+srv://<user>:<password>@mongo1.mongocluster.cosmos.azure.com") {
		t.Errorf("connectionString = %q", got.Properties.ConnectionString)
	}
}

func TestPasswordNeverEchoed(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	code, raw := do(t, srv, http.MethodGet, clusterBase+"mongo1"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}

	// The literal "<password>" placeholder in the connection string is expected;
	// only the real secret must never appear.
	if strings.Contains(string(raw), "Sup3rSecret") {
		t.Errorf("GET body leaked the administrator password: %s", raw)
	}

	got := unmarshalCluster(t, raw)
	if got.Properties.Administrator != nil && got.Properties.Administrator.Password != "" {
		t.Errorf("administrator.password echoed: %q", got.Properties.Administrator.Password)
	}
}

func TestGetByteIdenticalAcrossReads(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	_, first := do(t, srv, http.MethodGet, clusterBase+"mongo1"+apiVer, "")
	_, second := do(t, srv, http.MethodGet, clusterBase+"mongo1"+apiVer, "")

	if !bytes.Equal(first, second) {
		t.Errorf("GET not byte-identical:\n%s\n%s", first, second)
	}
}

func TestUpdatePatchMerges(t *testing.T) {
	srv := newServer(t)
	before := createCluster(t, srv)

	patch := `{"properties": {"storage": {"sizeGb": 256}}}`

	code, raw := do(t, srv, http.MethodPatch, clusterBase+"mongo1"+apiVer, patch)
	if code != http.StatusOK {
		t.Fatalf("patch: %d (%s)", code, raw)
	}

	got := unmarshalCluster(t, raw)

	if got.Properties.Storage == nil || got.Properties.Storage.SizeGb == nil || *got.Properties.Storage.SizeGb != 256 {
		t.Errorf("storage not patched: %+v", got.Properties.Storage)
	}

	// Untouched fields survive the merge.
	if got.Properties.ServerVersion != "7.0" || got.Properties.Compute == nil || got.Properties.Compute.Tier != "M30" {
		t.Errorf("patch did not merge: %+v", got.Properties)
	}

	// Connection string does not drift on patch.
	if got.Properties.ConnectionString != before.Properties.ConnectionString {
		t.Errorf("connectionString drifted on patch: %q vs %q",
			got.Properties.ConnectionString, before.Properties.ConnectionString)
	}
}

func TestUpdateTagsReplace(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	patch := `{"tags": {"team": "db"}}`
	code, raw := do(t, srv, http.MethodPatch, clusterBase+"mongo1"+apiVer, patch)
	if code != http.StatusOK {
		t.Fatalf("patch tags: %d (%s)", code, raw)
	}

	got := unmarshalCluster(t, raw)
	if _, hasOld := got.Tags["env"]; hasOld {
		t.Errorf("UpdateTags should REPLACE, env survived: %+v", got.Tags)
	}

	if got.Tags["team"] != "db" {
		t.Errorf("tags = %+v, want team=db", got.Tags)
	}
}

func TestListConnectionStrings(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	code, raw := do(t, srv, http.MethodPost, clusterBase+"mongo1/listConnectionStrings"+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("listConnectionStrings: %d (%s)", code, raw)
	}

	var first connStringsWire
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(first.ConnectionStrings) != 1 {
		t.Fatalf("want 1 connection string, got %d", len(first.ConnectionStrings))
	}

	if !strings.Contains(first.ConnectionStrings[0].ConnectionString, "mongo1.mongocluster.cosmos.azure.com") {
		t.Errorf("connectionString = %q", first.ConnectionStrings[0].ConnectionString)
	}

	// Stable across calls.
	_, raw2 := do(t, srv, http.MethodPost, clusterBase+"mongo1/listConnectionStrings"+apiVer, "")
	if !bytes.Equal(raw, raw2) {
		t.Errorf("listConnectionStrings not byte-identical:\n%s\n%s", raw, raw2)
	}
}

func TestListConnectionStringsMissing(t *testing.T) {
	srv := newServer(t)

	code, _ := do(t, srv, http.MethodPost, clusterBase+"nope/listConnectionStrings"+apiVer, "")
	if code != http.StatusNotFound {
		t.Errorf("want 404, got %d", code)
	}
}

func TestListClusters(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	code, raw := do(t, srv, http.MethodGet, clusterBase+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list: %d (%s)", code, raw)
	}

	var out struct {
		Value []clusterWire `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}

	if len(out.Value) != 1 || out.Value[0].Name != "mongo1" {
		t.Errorf("list = %+v", out.Value)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	srv := newServer(t)
	createCluster(t, srv)

	if code, _ := do(t, srv, http.MethodDelete, clusterBase+"mongo1"+apiVer, ""); code != http.StatusOK {
		t.Errorf("first delete = %d, want 200", code)
	}

	if code, _ := do(t, srv, http.MethodDelete, clusterBase+"mongo1"+apiVer, ""); code != http.StatusNoContent {
		t.Errorf("second delete = %d, want 204", code)
	}

	if code, _ := do(t, srv, http.MethodGet, clusterBase+"mongo1"+apiVer, ""); code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", code)
	}
}

func TestGetMissing(t *testing.T) {
	srv := newServer(t)

	if code, _ := do(t, srv, http.MethodGet, clusterBase+"nope"+apiVer, ""); code != http.StatusNotFound {
		t.Errorf("want 404, got %d", code)
	}
}

func TestMatchesDistinctFromCosmosDB(t *testing.T) {
	h := mongoclustersrv.New(mongocluster.New(config.NewOptions()))

	mongo, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		clusterBase+"mongo1"+apiVer, nil)
	if !h.Matches(mongo) {
		t.Error("handler should match a mongoClusters path")
	}

	// The Cosmos DB core resource type must NOT be captured by this handler.
	accounts, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DocumentDB/databaseAccounts/acct1"+apiVer, nil)
	if h.Matches(accounts) {
		t.Error("handler must NOT match Microsoft.DocumentDB/databaseAccounts")
	}
}
