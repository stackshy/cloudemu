package azure

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// These tests reproduce and lock down the pre-release Azure acceptance findings.
// They drive the same raw ARM JSON wire a real azure-sdk-for-go / az / Terraform
// azurerm client emits, through CloudEmu's in-process Azure wire server.

const (
	preRelSub = "sub-prerel"
	preRelRG  = "rg-prerel"
	apiVer    = "?api-version=2023-09-01"
)

// bootPreRel boots the Azure wire server with every driver the finding tests
// touch, sharing one server across subtests.
func bootPreRel(t *testing.T) *compat.AzureSession {
	t.Helper()

	p := cloudemu.NewAzure()

	return compat.BootAzure(t, azureserver.Drivers{
		VirtualMachines:    p.VirtualMachines,
		Disks:              p.VirtualMachines,
		Network:            p.VNet,
		DNS:                p.DNS,
		ServiceBus:         p.ServiceBus,
		AKS:                p.AKS,
		ContainerInstances: p.ContainerInstances,
		EventGrid:          p.EventGrid,
		SQL:                p.SQL,
		PostgresFlex:       p.PostgresFlex,
		MySQLFlex:          p.MySQLFlex,
		Monitor:            p.Monitor,
	})
}

// armReq issues one raw ARM request and returns the status code and parsed body.
func armReq(t *testing.T, sess *compat.AzureSession, method, path, body string) (int, map[string]any) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}

	req, err := http.NewRequest(method, sess.Endpoint()+path+apiVer, rdr)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := sess.Transport().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var parsed map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &parsed)
	}

	return resp.StatusCode, parsed
}

// rgScoped / subScoped build ARM collection/resource paths.
func rgScoped(provider, resType, name string) string {
	p := "/subscriptions/" + preRelSub + "/resourceGroups/" + preRelRG +
		"/providers/" + provider + "/" + resType
	if name != "" {
		p += "/" + name
	}

	return p
}

func subScoped(provider, resType string) string {
	return "/subscriptions/" + preRelSub + "/providers/" + provider + "/" + resType
}

// assertWellFormedID fails when an ARM id is missing its resource group segment
// (the `resourceGroups//` malformation the finding is about).
func assertWellFormedID(t *testing.T, id string) {
	t.Helper()

	if id == "" {
		t.Fatal("resource id is empty")
	}

	if strings.Contains(id, "resourceGroups//") {
		t.Errorf("malformed id (empty resource-group segment): %s", id)
	}

	if !strings.Contains(id, "/resourceGroups/"+preRelRG+"/") {
		t.Errorf("id does not carry its resource group %q: %s", preRelRG, id)
	}
}

// listIDs pulls the "id" of each item in an ARM {"value":[...]} response.
func listIDs(t *testing.T, body map[string]any) []string {
	t.Helper()

	// An empty ARM collection serializes as {"value":null}; treat that as zero
	// ids rather than an error.
	val, _ := body["value"].([]any)

	ids := make([]string, 0, len(val))

	for _, it := range val {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}

		if id, ok := m["id"].(string); ok {
			ids = append(ids, id)
		}
	}

	return ids
}

// TestPreReleaseListBySubscriptionIDs covers the systemic HIGH finding: every
// subscription-scoped list must render each resource's id with its true
// resourceGroups/{rg} segment (not the empty `resourceGroups//`).
func TestPreReleaseListBySubscriptionIDs(t *testing.T) {
	sess := bootPreRel(t)

	cases := []struct {
		name     string
		provider string
		resType  string
		resName  string
		body     string
	}{
		{
			name: "virtualMachines", provider: "Microsoft.Compute", resType: "virtualMachines", resName: "vm1",
			body: `{"location":"eastus","properties":{"hardwareProfile":{"vmSize":"Standard_D2s_v3"}}}`,
		},
		{
			name: "disks", provider: "Microsoft.Compute", resType: "disks", resName: "disk1",
			body: `{"location":"westus2","sku":{"name":"Premium_LRS"},"properties":{"diskSizeGB":32,"creationData":{"createOption":"Empty"}}}`,
		},
		{
			name: "sqlServers", provider: "Microsoft.Sql", resType: "servers", resName: "sqlsrv1",
			body: `{"location":"eastus","properties":{"administratorLogin":"adm","administratorLoginPassword":"P@ssw0rd!23"}}`,
		},
		{
			name: "mysqlFlex", provider: "Microsoft.DBforMySQL", resType: "flexibleServers", resName: "mysql1",
			body: `{"location":"eastus","sku":{"name":"Standard_B1ms","tier":"Burstable"},` +
				`"properties":{"administratorLogin":"adm","administratorLoginPassword":"P@ssw0rd!23","version":"8.0.21"}}`,
		},
		{
			name: "postgresFlex", provider: "Microsoft.DBforPostgreSQL", resType: "flexibleServers", resName: "pg1",
			body: `{"location":"eastus","sku":{"name":"Standard_B1ms","tier":"Burstable"},` +
				`"properties":{"administratorLogin":"adm","administratorLoginPassword":"P@ssw0rd!23","version":"14"}}`,
		},
		{
			name: "eventGridTopics", provider: "Microsoft.EventGrid", resType: "topics", resName: "topic1",
			body: `{"location":"eastus"}`,
		},
		{
			name: "dnsZones", provider: "Microsoft.Network", resType: "dnsZones", resName: "example-prerel.com",
			body: `{"location":"global"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := armReq(t, sess, http.MethodPut, rgScoped(tc.provider, tc.resType, tc.resName), tc.body)
			if status >= 300 && status != http.StatusAccepted {
				t.Fatalf("create %s: status %d", tc.name, status)
			}

			_, listBody := armReq(t, sess, http.MethodGet, subScoped(tc.provider, tc.resType), "")

			ids := listIDs(t, listBody)
			if len(ids) == 0 {
				t.Fatalf("subscription-scoped list of %s returned no items", tc.name)
			}

			for _, id := range ids {
				assertWellFormedID(t, id)
			}
		})
	}

	// EventGrid additionally derives properties.metricResourceId from the id, so
	// it must be well-formed too.
	t.Run("eventGridMetricResourceID", func(t *testing.T) {
		_, listBody := armReq(t, sess, http.MethodGet, subScoped("Microsoft.EventGrid", "topics"), "")

		val, _ := listBody["value"].([]any)
		if len(val) == 0 {
			t.Fatal("no eventgrid topics listed")
		}

		for _, it := range val {
			m, _ := it.(map[string]any)
			props, _ := m["properties"].(map[string]any)
			metricID, _ := props["metricResourceId"].(string)
			assertWellFormedID(t, metricID)
		}
	})
}

// TestPreReleaseNoPhantomVNet covers the HIGH finding: creating a standalone NSG
// (or route table) must not leak a fabricated virtualNetworks resource.
func TestPreReleaseNoPhantomVNet(t *testing.T) {
	sess := bootPreRel(t)

	t.Run("networkSecurityGroups", func(t *testing.T) {
		status, _ := armReq(t, sess, http.MethodPut,
			rgScoped("Microsoft.Network", "networkSecurityGroups", "nsg1"), `{"location":"eastus"}`)
		if status >= 300 && status != http.StatusAccepted {
			t.Fatalf("create NSG: status %d", status)
		}

		assertNoVNets(t, sess)
	})

	t.Run("routeTables", func(t *testing.T) {
		status, _ := armReq(t, sess, http.MethodPut,
			rgScoped("Microsoft.Network", "routeTables", "rt1"), `{"location":"eastus"}`)
		if status >= 300 && status != http.StatusAccepted {
			t.Fatalf("create route table: status %d", status)
		}

		assertNoVNets(t, sess)
	})

	// A real user-created VNet must still list correctly.
	t.Run("realVNetStillLists", func(t *testing.T) {
		status, _ := armReq(t, sess, http.MethodPut,
			rgScoped("Microsoft.Network", "virtualNetworks", "vnet-real"),
			`{"location":"eastus","properties":{"addressSpace":{"addressPrefixes":["10.1.0.0/16"]}}}`)
		if status >= 300 && status != http.StatusAccepted {
			t.Fatalf("create VNet: status %d", status)
		}

		_, listBody := armReq(t, sess, http.MethodGet, subScoped("Microsoft.Network", "virtualNetworks"), "")

		ids := listIDs(t, listBody)
		if len(ids) != 1 {
			t.Fatalf("expected exactly the one real vnet, got %d: %v", len(ids), ids)
		}

		if !strings.Contains(ids[0], "/virtualNetworks/vnet-real") {
			t.Errorf("listed vnet is not the user's: %s", ids[0])
		}

		assertWellFormedID(t, ids[0])
	})
}

func assertNoVNets(t *testing.T, sess *compat.AzureSession) {
	t.Helper()

	_, subBody := armReq(t, sess, http.MethodGet, subScoped("Microsoft.Network", "virtualNetworks"), "")
	if ids := listIDs(t, subBody); len(ids) != 0 {
		t.Errorf("subscription vnet list leaked a phantom network: %v", ids)
	}

	_, rgBody := armReq(t, sess, http.MethodGet, rgScoped("Microsoft.Network", "virtualNetworks", ""), "")
	if ids := listIDs(t, rgBody); len(ids) != 0 {
		t.Errorf("resource-group vnet list leaked a phantom network: %v", ids)
	}
}

// TestPreReleaseDiskLocationRoundTrips covers the MEDIUM finding: a disk created
// in westus2 must read back its region, not the eastus default.
func TestPreReleaseDiskLocationRoundTrips(t *testing.T) {
	sess := bootPreRel(t)

	status, _ := armReq(t, sess, http.MethodPut,
		rgScoped("Microsoft.Compute", "disks", "disk-loc"),
		`{"location":"westus2","sku":{"name":"Premium_LRS"},"properties":{"diskSizeGB":16,"creationData":{"createOption":"Empty"}}}`)
	if status >= 300 && status != http.StatusAccepted {
		t.Fatalf("create disk: status %d", status)
	}

	_, getBody := armReq(t, sess, http.MethodGet, rgScoped("Microsoft.Compute", "disks", "disk-loc"), "")
	if loc, _ := getBody["location"].(string); loc != "westus2" {
		t.Errorf("GET disk location = %q, want westus2", loc)
	}

	_, listBody := armReq(t, sess, http.MethodGet, subScoped("Microsoft.Compute", "disks"), "")

	val, _ := listBody["value"].([]any)
	found := false

	for _, it := range val {
		m, _ := it.(map[string]any)
		if name, _ := m["name"].(string); name != "disk-loc" {
			continue
		}

		found = true

		if loc, _ := m["location"].(string); loc != "westus2" {
			t.Errorf("LIST disk location = %q, want westus2", loc)
		}
	}

	if !found {
		t.Error("disk-loc not present in subscription list")
	}
}

// TestPreReleaseACIPatch covers the MEDIUM finding: PATCH on a container group
// (ARM "Container Groups - Update") must merge tags and return 200, not 405.
func TestPreReleaseACIPatch(t *testing.T) {
	sess := bootPreRel(t)

	create := `{"location":"eastus","tags":{"team":"a"},"properties":{"osType":"Linux",` +
		`"containers":[{"name":"c1","properties":{"image":"nginx",` +
		`"resources":{"requests":{"cpu":1,"memoryInGB":1}}}}]}}`

	status, _ := armReq(t, sess, http.MethodPut,
		rgScoped("Microsoft.ContainerInstance", "containerGroups", "cg1"), create)
	if status != http.StatusCreated {
		t.Fatalf("create container group: status %d, want 201", status)
	}

	status, patchBody := armReq(t, sess, http.MethodPatch,
		rgScoped("Microsoft.ContainerInstance", "containerGroups", "cg1"), `{"tags":{"env":"prod"}}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH container group: status %d, want 200", status)
	}

	tags, _ := patchBody["tags"].(map[string]any)
	if tags["env"] != "prod" {
		t.Errorf("PATCH did not apply tag env=prod: %v", tags)
	}

	if tags["team"] != "a" {
		t.Errorf("PATCH did not preserve existing tag team=a: %v", tags)
	}
}

// TestPreReleaseCreateReturns201 covers the LOW finding: an ARM PUT that creates
// a new resource returns 201, and an in-place update returns 200.
func TestPreReleaseCreateReturns201(t *testing.T) {
	sess := bootPreRel(t)

	cases := []struct {
		name     string
		provider string
		resType  string
		resName  string
		body     string
	}{
		{
			name: "mysqlFlex", provider: "Microsoft.DBforMySQL", resType: "flexibleServers", resName: "mysql201",
			body: `{"location":"eastus","sku":{"name":"Standard_B1ms","tier":"Burstable"},` +
				`"properties":{"administratorLogin":"adm","administratorLoginPassword":"P@ssw0rd!23","version":"8.0.21"}}`,
		},
		{
			name: "postgresFlex", provider: "Microsoft.DBforPostgreSQL", resType: "flexibleServers", resName: "pg201",
			body: `{"location":"eastus","sku":{"name":"Standard_B1ms","tier":"Burstable"},` +
				`"properties":{"administratorLogin":"adm","administratorLoginPassword":"P@ssw0rd!23","version":"14"}}`,
		},
		{
			name: "sqlServers", provider: "Microsoft.Sql", resType: "servers", resName: "sql201",
			body: `{"location":"eastus","properties":{"administratorLogin":"adm","administratorLoginPassword":"P@ssw0rd!23"}}`,
		},
		{
			name: "serviceBus", provider: "Microsoft.ServiceBus", resType: "namespaces", resName: "sb201",
			body: `{"location":"eastus","sku":{"name":"Standard"}}`,
		},
		{
			name: "aks", provider: "Microsoft.ContainerService", resType: "managedClusters", resName: "aks201",
			body: `{"location":"eastus","properties":{"dnsPrefix":"aks201"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := rgScoped(tc.provider, tc.resType, tc.resName)

			status, _ := armReq(t, sess, http.MethodPut, path, tc.body)
			if status != http.StatusCreated {
				t.Errorf("first create: status %d, want 201", status)
			}

			status, _ = armReq(t, sess, http.MethodPut, path, tc.body)
			if status != http.StatusOK {
				t.Errorf("re-PUT update: status %d, want 200", status)
			}
		})
	}
}
