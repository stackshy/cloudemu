package managedlustre_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/managedlustre"
	managedlustresrv "github.com/stackshy/cloudemu/v2/server/azure/managedlustre"
)

const (
	apiVer   = "?api-version=2024-03-01"
	basePath = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.StorageCache/amlFilesystems/"
)

type wireResp struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
	Zones    []string          `json:"zones"`
	Sku      *struct {
		Name string `json:"name"`
	} `json:"sku"`
	Identity *struct {
		Type         string `json:"type"`
		TenantID     string `json:"tenantId"`
		UserAssigned map[string]struct {
			PrincipalID string `json:"principalId"`
			ClientID    string `json:"clientId"`
		} `json:"userAssignedIdentities"`
	} `json:"identity"`
	Properties struct {
		StorageCapacityTiB float64 `json:"storageCapacityTiB"`
		FilesystemSubnet   string  `json:"filesystemSubnet"`
		MaintenanceWindow  *struct {
			DayOfWeek    string `json:"dayOfWeek"`
			TimeOfDayUTC string `json:"timeOfDayUTC"`
		} `json:"maintenanceWindow"`
		ClientInfo struct {
			MgsAddress    string `json:"mgsAddress"`
			LustreVersion string `json:"lustreVersion"`
			MountCommand  string `json:"mountCommand"`
		} `json:"clientInfo"`
		Health struct {
			State             string `json:"state"`
			StatusDescription string `json:"statusDescription"`
		} `json:"health"`
		ProvisioningState         string `json:"provisioningState"`
		ThroughputProvisionedMBps int    `json:"throughputProvisionedMBps"`
		Hsm                       *struct {
			Settings      *struct{ Container string } `json:"settings"`
			ArchiveStatus []struct {
				FilesystemPath string `json:"filesystemPath"`
				Status         struct {
					State string `json:"state"`
				} `json:"status"`
			} `json:"archiveStatus"`
		} `json:"hsm"`
	} `json:"properties"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := managedlustre.New(config.NewOptions())
	h := managedlustresrv.New(mock)
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

func decode(t *testing.T, raw []byte) wireResp {
	t.Helper()

	var out wireResp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}

	return out
}

const createBody = `{
  "location": "eastus",
  "sku": {"name": "AMLFS-Durable-Premium-125"},
  "zones": ["1"],
  "tags": {"env": "dev"},
  "properties": {
    "storageCapacityTiB": 16,
    "filesystemSubnet": "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Network/virtualNetworks/vnet/subnets/fs",
    "maintenanceWindow": {"dayOfWeek": "Friday", "timeOfDayUTC": "22:00"}
  }
}`

func TestPutCreatesWith201(t *testing.T) {
	srv := newServer(t)

	code, raw := do(t, srv, http.MethodPut, basePath+"fs1"+apiVer, createBody)
	if code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201 (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Type != "Microsoft.StorageCache/amlFilesystems" {
		t.Errorf("type = %q", got.Type)
	}

	if got.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", got.Properties.ProvisioningState)
	}

	if got.Properties.ThroughputProvisionedMBps != 2000 {
		t.Errorf("throughput = %d, want 2000", got.Properties.ThroughputProvisionedMBps)
	}

	if got.Properties.ClientInfo.MgsAddress == "" {
		t.Error("mgsAddress empty")
	}

	if got.Properties.Health.State != "Available" {
		t.Errorf("health.state = %q, want Available", got.Properties.Health.State)
	}

	if got.Sku == nil || got.Sku.Name != "AMLFS-Durable-Premium-125" {
		t.Errorf("sku = %+v", got.Sku)
	}
}

func TestGetIsByteStableAcrossReads(t *testing.T) {
	srv := newServer(t)

	if code, raw := do(t, srv, http.MethodPut, basePath+"fs1"+apiVer, createBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	_, raw1 := do(t, srv, http.MethodGet, basePath+"fs1"+apiVer, "")
	_, raw2 := do(t, srv, http.MethodGet, basePath+"fs1"+apiVer, "")

	if !bytes.Equal(raw1, raw2) {
		t.Errorf("GET drifted between reads:\n1: %s\n2: %s", raw1, raw2)
	}
}

func TestPutIsIdempotentReturns200(t *testing.T) {
	srv := newServer(t)

	do(t, srv, http.MethodPut, basePath+"fs1"+apiVer, createBody)

	code, _ := do(t, srv, http.MethodPut, basePath+"fs1"+apiVer, createBody)
	if code != http.StatusOK {
		t.Errorf("second PUT status = %d, want 200", code)
	}
}

func TestPatchMergesTagsPreservesComputed(t *testing.T) {
	srv := newServer(t)

	_, createRaw := do(t, srv, http.MethodPut, basePath+"fs1"+apiVer, createBody)
	created := decode(t, createRaw)

	code, raw := do(t, srv, http.MethodPatch, basePath+"fs1"+apiVer, `{"tags":{"env":"prod","team":"ml"}}`)
	if code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200 (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Tags["env"] != "prod" || got.Tags["team"] != "ml" {
		t.Errorf("tags = %v", got.Tags)
	}

	if got.Properties.ClientInfo.MgsAddress != created.Properties.ClientInfo.MgsAddress {
		t.Errorf("mgsAddress drifted on patch")
	}

	if got.Properties.StorageCapacityTiB != 16 {
		t.Errorf("capacity not preserved: %v", got.Properties.StorageCapacityTiB)
	}

	if got.Sku == nil || got.Sku.Name != "AMLFS-Durable-Premium-125" {
		t.Errorf("sku not preserved: %+v", got.Sku)
	}
}

func TestArchiveAndCancelActions(t *testing.T) {
	srv := newServer(t)

	hsmBody := `{
	  "location": "eastus",
	  "sku": {"name": "AMLFS-Durable-Premium-125"},
	  "properties": {
	    "storageCapacityTiB": 16,
	    "filesystemSubnet": "subnet",
	    "maintenanceWindow": {"dayOfWeek": "Friday", "timeOfDayUTC": "22:00"},
	    "hsm": {"settings": {"container": "c", "loggingContainer": "l"}}
	  }
	}`

	if code, raw := do(t, srv, http.MethodPut, basePath+"fs1"+apiVer, hsmBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	code, _ := do(t, srv, http.MethodPost, basePath+"fs1/archive"+apiVer, `{"filesystemPath":"/"}`)
	if code != http.StatusOK {
		t.Fatalf("archive status = %d, want 200", code)
	}

	_, raw := do(t, srv, http.MethodGet, basePath+"fs1"+apiVer, "")
	got := decode(t, raw)
	if got.Properties.Hsm == nil || len(got.Properties.Hsm.ArchiveStatus) != 1 {
		t.Fatalf("archive status not recorded: %s", raw)
	}

	if got.Properties.Hsm.ArchiveStatus[0].Status.State != "Completed" {
		t.Errorf("archive state = %q, want Completed", got.Properties.Hsm.ArchiveStatus[0].Status.State)
	}

	if code, _ := do(t, srv, http.MethodPost, basePath+"fs1/cancelArchive"+apiVer, ""); code != http.StatusOK {
		t.Errorf("cancelArchive status = %d, want 200", code)
	}
}

func TestListByResourceGroup(t *testing.T) {
	srv := newServer(t)

	do(t, srv, http.MethodPut, basePath+"fs1"+apiVer, createBody)
	do(t, srv, http.MethodPut, basePath+"fs2"+apiVer, createBody)

	code, raw := do(t, srv, http.MethodGet, basePath[:len(basePath)-1]+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}

	var out struct {
		Value []wireResp `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}

	if len(out.Value) != 2 {
		t.Errorf("list count = %d, want 2", len(out.Value))
	}
}

func TestDeleteThen404(t *testing.T) {
	srv := newServer(t)

	do(t, srv, http.MethodPut, basePath+"fs1"+apiVer, createBody)

	if code, _ := do(t, srv, http.MethodDelete, basePath+"fs1"+apiVer, ""); code != http.StatusOK {
		t.Errorf("first DELETE = %d, want 200", code)
	}

	if code, _ := do(t, srv, http.MethodDelete, basePath+"fs1"+apiVer, ""); code != http.StatusNoContent {
		t.Errorf("second DELETE = %d, want 204", code)
	}

	if code, _ := do(t, srv, http.MethodGet, basePath+"fs1"+apiVer, ""); code != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", code)
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	srv := newServer(t)

	body := `{
	  "location": "eastus",
	  "identity": {
	    "type": "UserAssigned",
	    "userAssignedIdentities": {
	      "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1": {}
	    }
	  },
	  "sku": {"name": "AMLFS-Durable-Premium-40"},
	  "properties": {
	    "storageCapacityTiB": 48,
	    "filesystemSubnet": "subnet",
	    "maintenanceWindow": {"dayOfWeek": "Monday", "timeOfDayUTC": "01:00"}
	  }
	}`

	code, raw := do(t, srv, http.MethodPut, basePath+"fs1"+apiVer, body)
	if code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Identity == nil || got.Identity.Type != "UserAssigned" {
		t.Fatalf("identity = %+v", got.Identity)
	}

	for _, v := range got.Identity.UserAssigned {
		if v.PrincipalID == "" || v.ClientID == "" {
			t.Errorf("user-assigned ids not minted: %+v", v)
		}
	}

	// 48 TiB * 40 = 1920.
	if got.Properties.ThroughputProvisionedMBps != 1920 {
		t.Errorf("throughput = %d, want 1920", got.Properties.ThroughputProvisionedMBps)
	}
}
