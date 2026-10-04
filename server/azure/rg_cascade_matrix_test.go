package azure_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

const (
	casSub   = "sub-cas"
	casAPI   = "?api-version=2023-01-01"
	casSSHPK = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7 cas@example"
)

type casClient struct {
	t  *testing.T
	ts *httptest.Server
	p  *azureprovider.Provider
}

func newCasClient(t *testing.T) *casClient {
	t.Helper()

	p := cloudemu.NewAzure()
	ts := httptest.NewServer(azureserver.NewFromProvider(p))
	t.Cleanup(ts.Close)

	return &casClient{t: t, ts: ts, p: p}
}

func (c *casClient) do(method, path, body string) (int, map[string]any) {
	c.t.Helper()

	if !strings.Contains(path, "?") {
		path += casAPI
	}

	req, err := http.NewRequestWithContext(context.Background(), method, c.ts.URL+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.ts.Client().Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	var out map[string]any
	_ = json.Unmarshal(data, &out)

	return resp.StatusCode, out
}

func (c *casClient) mustPut(path, body string) {
	c.t.Helper()

	if code, out := c.do(http.MethodPut, path, body); code < 200 || code > 299 {
		c.t.Fatalf("PUT %s: status %d: %v", path, code, out)
	}
}

func (c *casClient) wantStatus(method, path string, want int) {
	c.t.Helper()

	if code, out := c.do(method, path, ""); code != want {
		c.t.Errorf("%s %s: status %d, want %d (%v)", method, path, code, want, out)
	}
}

func rgPath(rg string) string { return "/subscriptions/" + casSub + "/resourceGroups/" + rg }

func resPath(rg, typ, name string) string { return rgPath(rg) + "/providers/" + typ + "/" + name }

// seedAzureRMFamilies creates one resource of every family PR-A1 cascades, plus
// a child where the type has one, and returns the GET paths of everything made.
func seedAzureRMFamilies(c *casClient, rg string) []string {
	c.t.Helper()

	c.mustPut(rgPath(rg), `{"location":"eastus"}`)

	disk := resPath(rg, "Microsoft.Compute/disks", "d1")
	c.mustPut(disk, `{"location":"eastus","properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":4}}`)

	snap := resPath(rg, "Microsoft.Compute/snapshots", "s1")
	c.mustPut(snap, `{"location":"eastus","properties":{"creationData":{"createOption":"Copy","sourceResourceId":"`+disk+`"}}}`)

	img := resPath(rg, "Microsoft.Compute/images", "i1")
	c.mustPut(img, `{"location":"eastus","properties":{"storageProfile":{"osDisk":{"osType":"Linux",`+
		`"osState":"Generalized","managedDisk":{"id":"`+disk+`"}}}}}`)

	// SSH key names are still global (AZCMP-01), so each group gets its own.
	key := resPath(rg, "Microsoft.Compute/sshPublicKeys", "k"+strings.ToLower(rg))
	c.mustPut(key, `{"location":"eastus","properties":{"publicKey":"`+casSSHPK+`"}}`)

	vm := resPath(rg, "Microsoft.Compute/virtualMachines", "vm1")
	c.mustPut(vm, `{"location":"eastus","properties":{"hardwareProfile":{"vmSize":"Standard_B1s"}}}`)

	zone := resPath(rg, "Microsoft.Network/dnsZones", "cas.example.com")
	c.mustPut(zone, `{"location":"global"}`)

	record := zone + "/A/www"
	c.mustPut(record, `{"properties":{"TTL":300,"ARecords":[{"ipv4Address":"10.0.0.4"}]}}`)

	acr := resPath(rg, "Microsoft.ContainerRegistry/registries", "acr"+strings.ToLower(rg))
	c.mustPut(acr, `{"location":"eastus","sku":{"name":"Standard"}}`)

	hook := acr + "/webhooks/wh1"
	c.mustPut(hook, `{"location":"eastus","properties":{"serviceUri":"https://example.com/h","actions":["push"]}}`)

	aks := resPath(rg, "Microsoft.ContainerService/managedClusters", "aks1")
	c.mustPut(aks, `{"location":"eastus","properties":{"dnsPrefix":"cas","agentPoolProfiles":[`+
		`{"name":"sys","count":1,"vmSize":"Standard_D2s_v3","mode":"System"}]}}`)

	pool := aks + "/agentPools/user1"
	c.mustPut(pool, `{"properties":{"count":1,"vmSize":"Standard_D2s_v3","mode":"User"}}`)

	aci := resPath(rg, "Microsoft.ContainerInstance/containerGroups", "cg1")
	c.mustPut(aci, `{"location":"eastus","properties":{"osType":"Linux","containers":[{"name":"c1",`+
		`"properties":{"image":"nginx","resources":{"requests":{"cpu":1,"memoryInGB":1}}}}]}}`)

	return []string{disk, snap, img, key, vm, zone, record, acr, hook, aks, pool, aci}
}

// TestResourceGroupDeleteCascadesA1Families is the AZRM-01 regression: deleting
// a group removes its snapshots, images, SSH keys, DNS zones, registries, AKS
// clusters and container groups (and their children), leaves a group whose
// name merely starts the same untouched, and recreating the group resurrects
// nothing.
func TestResourceGroupDeleteCascadesA1Families(t *testing.T) {
	c := newCasClient(t)

	gone := seedAzureRMFamilies(c, "Cas1")
	kept := seedAzureRMFamilies(c, "cas10")

	c.wantStatus(http.MethodDelete, rgPath("cas1"), http.StatusAccepted)

	for _, p := range gone {
		c.wantStatus(http.MethodGet, p, http.StatusNotFound)
	}

	for _, p := range kept {
		c.wantStatus(http.MethodGet, p, http.StatusOK)
	}

	c.mustPut(rgPath("cas1"), `{"location":"eastus"}`)

	for _, p := range gone {
		c.wantStatus(http.MethodGet, p, http.StatusNotFound)
	}

	if _, out := c.do(http.MethodGet, rgPath("cas1")+"/resources", ""); len(asList(out["value"])) != 0 {
		t.Errorf("recreated group lists resources: %v", out["value"])
	}

	// The where clause is not relied on (resourceGroup filtering is tracker
	// AZRM-05); the rows are checked here instead.
	argQuery := `{"subscriptions":["` + casSub + `"],"query":"resources | where resourceGroup =~ 'cas1'"}`

	_, out := c.do(http.MethodPost, "/providers/Microsoft.ResourceGraph/resources?api-version=2021-03-01", argQuery)
	for _, row := range asList(out["data"]) {
		m, _ := row.(map[string]any)
		if group, _ := m["resourceGroup"].(string); strings.EqualFold(group, "cas1") {
			t.Errorf("Resource Graph still returns a row for the deleted group: %v", m["id"])
		}
	}

	_, out = c.do(http.MethodPost, rgPath("cas1")+"/exportTemplate?api-version=2021-04-01", `{"resources":["*"]}`)
	if tmpl, _ := out["template"].(map[string]any); len(asList(tmpl["resources"])) != 0 {
		t.Errorf("exportTemplate of the recreated group lists resources: %v", tmpl["resources"])
	}
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// TestVMDeleteRemovesInventoryRow is the AZRM-03 regression: a deleted VM is
// gone from the driver and every inventory, not left behind as a terminated
// ghost row.
func TestVMDeleteRemovesInventoryRow(t *testing.T) {
	c := newCasClient(t)

	c.mustPut(rgPath("v1"), `{"location":"eastus"}`)

	vm := resPath("v1", "Microsoft.Compute/virtualMachines", "ghost")
	c.mustPut(vm, `{"location":"eastus","properties":{"hardwareProfile":{"vmSize":"Standard_B1s"}}}`)
	c.wantStatus(http.MethodDelete, vm, http.StatusAccepted)
	c.wantStatus(http.MethodGet, vm, http.StatusNotFound)

	if _, out := c.do(http.MethodGet, rgPath("v1")+"/resources", ""); len(asList(out["value"])) != 0 {
		t.Errorf("deleted VM still listed in the group's resources: %v", out["value"])
	}

	insts, err := c.p.VirtualMachines.DescribeInstances(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(insts) != 0 {
		t.Errorf("DescribeInstances still returns %d instance(s) after the ARM delete", len(insts))
	}
}

// TestResourceGroupDeleteBlockedByChildLock pins that a lock anywhere in the
// group stops the cascade before it runs, and a lock in a group whose name only
// shares a prefix does not.
func TestResourceGroupDeleteBlockedByChildLock(t *testing.T) {
	c := newCasClient(t)

	for _, rg := range []string{"l1", "l10"} {
		c.mustPut(rgPath(rg), `{"location":"eastus"}`)
		c.mustPut(resPath(rg, "Microsoft.Compute/disks", "d1"),
			`{"location":"eastus","properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":4}}`)
		c.mustPut(resPath(rg, "Microsoft.Compute/snapshots", "s1"), `{"location":"eastus","properties":{"creationData":`+
			`{"createOption":"Copy","sourceResourceId":"`+resPath(rg, "Microsoft.Compute/disks", "d1")+`"}}}`)
	}

	snap := resPath("l1", "Microsoft.Compute/snapshots", "s1")
	lock := snap + "/providers/Microsoft.Authorization/locks/keep?api-version=2016-09-01"
	c.mustPut(lock, `{"properties":{"level":"CanNotDelete"}}`)
	c.mustPut(resPath("l10", "Microsoft.Compute/snapshots", "s1")+"/providers/Microsoft.Authorization/locks/keep?api-version=2016-09-01",
		`{"properties":{"level":"CanNotDelete"}}`)

	c.wantStatus(http.MethodDelete, rgPath("l1"), http.StatusConflict)
	c.wantStatus(http.MethodGet, snap, http.StatusOK)
	c.wantStatus(http.MethodGet, rgPath("l1"), http.StatusOK)

	c.wantStatus(http.MethodDelete, lock, http.StatusOK)
	c.wantStatus(http.MethodDelete, rgPath("l1"), http.StatusAccepted)
	c.wantStatus(http.MethodGet, snap, http.StatusNotFound)
}

// TestResourceGroupDeleteEvictsOverlay is the overlay half of the cascade: a
// resource recreated under a recreated group must not inherit the deleted
// resource's unmodeled properties, whichever spelling the DELETE used. A
// managed identity is used because its purge predates this change, so only the
// overlay eviction is under test; the recreate sends no properties, which is
// when the overlay replays what it stored.
func TestResourceGroupDeleteEvictsOverlay(t *testing.T) {
	for _, seg := range []string{"resourceGroups", "resourcegroups"} {
		t.Run(seg, func(t *testing.T) {
			c := newCasClient(t)
			mi := resPath("Ov1", "Microsoft.ManagedIdentity/userAssignedIdentities", "id1")

			c.mustPut(rgPath("Ov1"), `{"location":"eastus"}`)
			c.mustPut(mi, `{"location":"eastus","properties":{"cloudemuProbe":"x"}}`)

			c.wantStatus(http.MethodDelete, "/subscriptions/"+casSub+"/"+seg+"/ov1", http.StatusAccepted)

			c.mustPut(rgPath("Ov1"), `{"location":"eastus"}`)
			c.mustPut(mi, `{"location":"eastus"}`)

			_, out := c.do(http.MethodGet, mi, "")
			if p, _ := out["properties"].(map[string]any); p["cloudemuProbe"] != nil {
				t.Errorf("recreated identity inherited the deleted one's unmodeled property: %v", p["cloudemuProbe"])
			}
		})
	}
}

// TestResourceGroupDeleteNetworkOrder covers the network phases: an
// application gateway and a load balancer holding public IPs and a subnet are
// purged before the virtual network, so everything in the group is gone.
func TestResourceGroupDeleteNetworkOrder(t *testing.T) {
	c := newCasClient(t)
	rg := "net1"
	net := "Microsoft.Network"

	c.mustPut(rgPath(rg), `{"location":"eastus"}`)

	vnet := resPath(rg, net+"/virtualNetworks", "vnet")
	c.mustPut(vnet, `{"location":"eastus","properties":{"addressSpace":{"addressPrefixes":["10.0.0.0/16"]}}}`)

	subnet := vnet + "/subnets/appgw"
	c.mustPut(subnet, `{"properties":{"addressPrefix":"10.0.1.0/24"}}`)

	pip := resPath(rg, net+"/publicIPAddresses", "pip")
	lbPIP := resPath(rg, net+"/publicIPAddresses", "lbpip")

	for _, p := range []string{pip, lbPIP} {
		c.mustPut(p, `{"location":"eastus","sku":{"name":"Standard"},"properties":{"publicIPAllocationMethod":"Static"}}`)
	}

	lb := resPath(rg, net+"/loadBalancers", "lb")
	c.mustPut(lb, `{"location":"eastus","sku":{"name":"Standard"},"properties":{"frontendIPConfigurations":`+
		`[{"name":"fe","properties":{"publicIPAddress":{"id":"`+lbPIP+`"}}}]}}`)

	gw := resPath(rg, net+"/applicationGateways", "gw")
	c.mustPut(gw, appGatewayBody(gw, subnet, pip))

	c.wantStatus(http.MethodDelete, rgPath(rg), http.StatusAccepted)

	for _, p := range []string{gw, lb, pip, lbPIP, subnet, vnet} {
		c.wantStatus(http.MethodGet, p, http.StatusNotFound)
	}
}

func appGatewayBody(gw, subnet, pip string) string {
	return `{"location":"eastus","properties":{"sku":{"name":"Standard_v2","tier":"Standard_v2","capacity":1},` +
		`"gatewayIPConfigurations":[{"name":"gwip","properties":{"subnet":{"id":"` + subnet + `"}}}],` +
		`"frontendIPConfigurations":[{"name":"feip","properties":{"publicIPAddress":{"id":"` + pip + `"}}}],` +
		`"frontendPorts":[{"name":"p80","properties":{"port":80}}],` +
		`"backendAddressPools":[{"name":"pool1","properties":{}}],` +
		`"backendHttpSettingsCollection":[{"name":"bhs","properties":{"port":80,"protocol":"Http",` +
		`"cookieBasedAffinity":"Disabled"}}],` +
		`"httpListeners":[{"name":"l1","properties":{"frontendIPConfiguration":{"id":"` + gw +
		`/frontendIPConfigurations/feip"},"frontendPort":{"id":"` + gw + `/frontendPorts/p80"},"protocol":"Http"}}],` +
		`"requestRoutingRules":[{"name":"r1","properties":{"ruleType":"Basic","priority":100,` +
		`"httpListener":{"id":"` + gw + `/httpListeners/l1"},"backendAddressPool":{"id":"` + gw +
		`/backendAddressPools/pool1"},"backendHttpSettings":{"id":"` + gw + `/backendHttpSettingsCollection/bhs"}}}]}}`
}

// dnsWithoutPurge hides the Azure DNS mock's purge method, standing in for a
// library caller that wires a driver without the capability.
type dnsWithoutPurge struct{ dnsdriver.DNS }

// TestResourceGroupDeleteSurvivesDriverWithoutPurge pins that a driver that
// cannot purge does not fail the group delete or stop the other purgers.
func TestResourceGroupDeleteSurvivesDriverWithoutPurge(t *testing.T) {
	p := cloudemu.NewAzure()
	ts := httptest.NewServer(azureserver.New(azureserver.Drivers{
		DNS:                dnsWithoutPurge{p.DNS},
		ContainerInstances: p.ContainerInstances,
	}))
	t.Cleanup(ts.Close)

	c := &casClient{t: t, ts: ts, p: p}
	c.mustPut(rgPath("np1"), `{"location":"eastus"}`)
	c.mustPut(resPath("np1", "Microsoft.Network/dnsZones", "np.example.com"), `{"location":"global"}`)

	aci := resPath("np1", "Microsoft.ContainerInstance/containerGroups", "cg1")
	c.mustPut(aci, `{"location":"eastus","properties":{"osType":"Linux","containers":[{"name":"c1",`+
		`"properties":{"image":"nginx","resources":{"requests":{"cpu":1,"memoryInGB":1}}}}]}}`)

	c.wantStatus(http.MethodDelete, rgPath("np1"), http.StatusAccepted)
	c.wantStatus(http.MethodGet, aci, http.StatusNotFound)
}
