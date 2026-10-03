package azure_test

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// seedInventory creates a VM (with its NIC and implicit OS disk), a vnet, a
// storage account, a managed disk and a container registry in rg.
func seedInventory(c *casClient, rg string) {
	c.t.Helper()

	c.mustPut(rgPath(rg), `{"location":"westeurope"}`)

	vnet := resPath(rg, "Microsoft.Network/virtualNetworks", "vnet-"+rg)
	c.mustPut(vnet, `{"location":"westeurope","tags":{"env":"`+rg+`"},"properties":{"addressSpace":{"addressPrefixes":["10.0.0.0/16"]},`+
		`"subnets":[{"name":"s1","properties":{"addressPrefix":"10.0.1.0/24"}}]}}`)

	nic := resPath(rg, "Microsoft.Network/networkInterfaces", "nic-"+rg)
	c.mustPut(nic, `{"location":"westeurope","properties":{"ipConfigurations":[{"name":"ip","properties":{"subnet":{"id":"`+
		vnet+`/subnets/s1"}}}]}}`)

	c.mustPut(resPath(rg, "Microsoft.Compute/virtualMachines", "vm-"+rg), `{"location":"westeurope","properties":{`+
		`"hardwareProfile":{"vmSize":"Standard_B1s"},"storageProfile":{"imageReference":{"publisher":"Canonical",`+
		`"offer":"ubuntu","sku":"22_04-lts","version":"latest"},"osDisk":{"createOption":"FromImage","name":"os-`+rg+`"}},`+
		`"osProfile":{"computerName":"vm","adminUsername":"u","adminPassword":"Passw0rd!1234"},`+
		`"networkProfile":{"networkInterfaces":[{"id":"`+nic+`"}]}}}`)

	c.mustPut(resPath(rg, "Microsoft.Storage/storageAccounts", "st"+rg), `{"location":"westeurope","kind":"StorageV2",`+
		`"sku":{"name":"Standard_LRS"},"tags":{"team":"`+rg+`"}}`)
	c.mustPut(resPath(rg, "Microsoft.Compute/disks", "disk-"+rg), `{"location":"westeurope","sku":{"name":"Standard_LRS"},`+
		`"properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":4}}`)
	c.mustPut(resPath(rg, "Microsoft.ContainerRegistry/registries", "acr"+rg), `{"location":"westeurope","sku":{"name":"Basic"}}`)
}

// rowsOf renders "name type location" per row, sorted, and fails if any row's
// id does not end in its name under the expected group.
func rowsOf(t *testing.T, rows []any) []string {
	t.Helper()

	out := make([]string, 0, len(rows))

	for _, r := range rows {
		m, _ := r.(map[string]any)
		name, _ := m["name"].(string)
		id, _ := m["id"].(string)

		if !strings.HasSuffix(id, "/"+name) || !strings.Contains(id, "/resourceGroups/") {
			t.Errorf("row id %q does not end in its name %q", id, name)
		}

		out = append(out, name+" "+strings.ToLower(m["type"].(string))+" "+m["location"].(string))
	}

	sort.Strings(out)

	return out
}

func wantRows(t *testing.T, label string, got, want []string) {
	t.Helper()

	sort.Strings(want)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got  %v\n want %v", label, got, want)
	}
}

// TestInventoryRowsCarryARMIdentity is the AZRM-04/AZRM-05 regression: the
// generic resources listing, Resource Graph and exportTemplate report each
// resource under its ARM name, group and location (not the driver's internal
// vm-00000033 ids), list NICs and registries, honor $filter, and Resource Graph
// filters on resourceGroup.
func TestInventoryRowsCarryARMIdentity(t *testing.T) {
	c := newCasClient(t)
	seedInventory(c, "rga")
	seedInventory(c, "rgb")

	rga := []string{
		"vm-rga microsoft.compute/virtualmachines westeurope",
		"os-rga microsoft.compute/disks westeurope",
		"disk-rga microsoft.compute/disks westeurope",
		"vnet-rga microsoft.network/virtualnetworks westeurope",
		"nic-rga microsoft.network/networkinterfaces westeurope",
		"strga microsoft.storage/storageaccounts westeurope",
		"acrrga microsoft.containerregistry/registries westeurope",
	}

	_, list := c.do(http.MethodGet, rgPath("rga")+"/resources?api-version=2021-04-01", "")
	wantRows(t, "rga resources", rowsOf(t, list["value"].([]any)), rga)

	filter := url.QueryEscape("resourceType eq 'Microsoft.Compute/disks'")
	_, list = c.do(http.MethodGet, rgPath("rgb")+"/resources?api-version=2021-04-01&$filter="+filter, "")
	wantRows(t, "rgb disks", rowsOf(t, list["value"].([]any)), []string{
		"os-rgb microsoft.compute/disks westeurope",
		"disk-rgb microsoft.compute/disks westeurope",
	})

	_, arg := c.do(http.MethodPost, "/providers/Microsoft.ResourceGraph/resources?api-version=2021-03-01",
		`{"subscriptions":["`+casSub+`"],"query":"Resources | where resourceGroup == 'rga' and type != 'microsoft.network/subnets'"}`)
	wantRows(t, "ARG rga", rowsOf(t, arg["data"].([]any)), rga)

	_, arg = c.do(http.MethodPost, "/providers/Microsoft.ResourceGraph/resources?api-version=2021-03-01",
		`{"subscriptions":["`+casSub+`"],"query":"Resources | where type in~ ('microsoft.containerregistry/registries') | count"}`)
	if data, _ := arg["data"].([]any); len(data) != 1 || data[0].(map[string]any)["Count"] != float64(2) {
		t.Errorf("ARG registry count = %v, want 2", arg["data"])
	}

	if code, _ := c.do(http.MethodPost, "/providers/Microsoft.ResourceGraph/resources?api-version=2021-03-01",
		`{"query":"Resources | where name = 'x'"}`); code != http.StatusBadRequest {
		t.Errorf("malformed KQL status = %d, want 400", code)
	}

	// The VM reports its OS disk by ARM id, so a client deleting the VM can
	// delete the disk too instead of leaving it in the group.
	_, vm := c.do(http.MethodGet, resPath("rga", "Microsoft.Compute/virtualMachines", "vm-rga")+"?api-version=2024-03-01", "")
	sp, _ := vm["properties"].(map[string]any)["storageProfile"].(map[string]any)
	od, _ := sp["osDisk"].(map[string]any)
	md, _ := od["managedDisk"].(map[string]any)

	if id, _ := md["id"].(string); id != resPath("rga", "Microsoft.Compute/disks", "os-rga") {
		t.Errorf("VM osDisk.managedDisk.id = %q", id)
	}

	_, exp := c.do(http.MethodPost, rgPath("rga")+"/exportTemplate?api-version=2021-04-01", `{"resources":["*"]}`)
	tmpl, _ := exp["template"].(map[string]any)

	var names []string
	for _, r := range tmpl["resources"].([]any) {
		names = append(names, r.(map[string]any)["name"].(string))
	}

	sort.Strings(names)

	if got := strings.Join(names, ","); got != "acrrga,disk-rga,nic-rga,os-rga,s1,strga,vm-rga,vnet-rga" {
		t.Errorf("exportTemplate names = %s", got)
	}
}
