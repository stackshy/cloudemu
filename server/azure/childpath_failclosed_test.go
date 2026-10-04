package azure_test

// Child-path fail-closed probes. Every ARM handler that parses a path with
// azurearm.ParsePath must answer a nested type it does not route with 404
// InvalidResourceType (501 for an extension resource), never by applying the
// request to the parent. childpath_scan_test.go checks that every such package
// has a row here or a reason in childPathNoChildren.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	cpSub = "sub-cp"
	cpRG  = "rg1"
	cpAPI = "?api-version=2023-01-01"
	// cpChildBody would move the parent to another region if a child write
	// ever reached the parent's createOrUpdate.
	cpChildBody = `{"location":"centralus","tags":{"moved":"yes"},"properties":{}}`
)

type cpClient struct {
	t  *testing.T
	ts *httptest.Server
}

func newCPClient(t *testing.T) *cpClient {
	t.Helper()

	ts := httptest.NewServer(azureserver.NewFromProvider(cloudemu.NewAzure()))
	t.Cleanup(ts.Close)

	c := &cpClient{t: t, ts: ts}
	c.mustPut("/subscriptions/"+cpSub+"/resourceGroups/"+cpRG, `{"location":"westus"}`)

	return c
}

func (c *cpClient) do(method, path, body string) (int, []byte) {
	c.t.Helper()

	if !strings.Contains(path, "?") {
		path += cpAPI
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

	return resp.StatusCode, data
}

func (c *cpClient) mustPut(path, body string) {
	c.t.Helper()

	if code, out := c.do(http.MethodPut, path, body); code < 200 || code > 299 {
		c.t.Fatalf("PUT %s: status %d: %s", path, code, out)
	}
}

func (c *cpClient) get(path string) (int, []byte) {
	c.t.Helper()

	return c.do(http.MethodGet, path, "")
}

func cpRes(typ, name string) string {
	return "/subscriptions/" + cpSub + "/resourceGroups/" + cpRG + "/providers/" + typ + "/" + name
}

// childPathRow is one guarded route. parent is the resource the guard
// protects: a top-level resource for a parent-collapse (P) row, or a child for
// a depth-collapse (D) row. With setup nil the row runs the ghost and
// extension probes only, because its parent needs dependencies the probe
// table does not build.
type childPathRow struct {
	pkg    string
	parent string
	setup  func(c *cpClient)
	child  bool // D row: parent is itself a child
}

const (
	cpDiskBody = `{"location":"westus","properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":4}}`
	cpVNetBody = `{"location":"westus","tags":{"k":"v"},` +
		`"properties":{"addressSpace":{"addressPrefixes":["10.0.0.0/16"]}}}`
	cpTagged = `{"location":"westus","tags":{"k":"v"},"properties":{}}`
)

func put(path, body string) func(c *cpClient) {
	return func(c *cpClient) { c.mustPut(path, body) }
}

func chain(steps ...func(c *cpClient)) func(c *cpClient) {
	return func(c *cpClient) {
		for _, s := range steps {
			s(c)
		}
	}
}

//nolint:funlen // one row per guarded route keeps the table reviewable
func childPathRows() []childPathRow {
	disk := cpRes("Microsoft.Compute/disks", "d1")
	vnet := cpRes("Microsoft.Network/virtualNetworks", "vn1")
	subnet := vnet + "/subnets/s1"
	nsg := cpRes("Microsoft.Network/networkSecurityGroups", "nsg1")
	rt := cpRes("Microsoft.Network/routeTables", "rt1")
	sqlSrv := cpRes("Microsoft.Sql/servers", "sql1")
	pg := cpRes("Microsoft.DBforPostgreSQL/flexibleServers", "pg1")
	my := cpRes("Microsoft.DBforMySQL/flexibleServers", "my1")
	law := cpRes("Microsoft.OperationalInsights/workspaces", "law1")
	aks := cpRes("Microsoft.ContainerService/managedClusters", "aks1")
	cog := cpRes("Microsoft.CognitiveServices/accounts", "cog1")
	rsv := cpRes("Microsoft.RecoveryServices/vaults", "rsv1")
	cpg := cpRes("Microsoft.DBforPostgreSQL/serverGroupsv2", "cpg1")
	syn := cpRes("Microsoft.Synapse/workspaces", "syn1")
	acr := cpRes("Microsoft.ContainerRegistry/registries", "acrcp1")
	lb := cpRes("Microsoft.Network/loadBalancers", "lb1")
	env := cpRes("Microsoft.App/managedEnvironments", "env1")
	egDomain := cpRes("Microsoft.EventGrid/domains", "egd1")
	cass := cpRes("Microsoft.DocumentDB/cassandraClusters", "cas1")
	pdns := cpRes("Microsoft.Network/privateDnsZones", "cp.internal")
	dnsZone := cpRes("Microsoft.Network/dnsZones", "cp.example.com")

	withVNet := put(vnet, cpVNetBody)
	withSubnet := chain(withVNet, put(subnet, `{"properties":{"addressPrefix":"10.0.1.0/24"}}`))
	withDisk := put(disk, cpDiskBody)
	withSQL := put(sqlSrv, `{"location":"westus","tags":{"k":"v"},"properties":{"administratorLogin":"a",`+
		`"administratorLoginPassword":"P@ssw0rd1234!","version":"12.0"}}`)
	withPG := put(pg, `{"location":"westus","sku":{"name":"Standard_B1ms","tier":"Burstable"},`+
		`"properties":{"administratorLogin":"a","administratorLoginPassword":"P@ssw0rd1234!","version":"16",`+
		`"storage":{"storageSizeGB":32}}}`)
	withMy := put(my, `{"location":"westus","sku":{"name":"Standard_B1ms","tier":"Burstable"},`+
		`"properties":{"administratorLogin":"a","administratorLoginPassword":"P@ssw0rd1234!","version":"8.0.21"}}`)
	withAKS := put(aks, `{"location":"westus","properties":{"dnsPrefix":"cp","agentPoolProfiles":[`+
		`{"name":"sys","count":1,"vmSize":"Standard_D2s_v3","mode":"System"}]}}`)
	withCog := put(cog, `{"location":"westus","kind":"OpenAI","sku":{"name":"S0"},"properties":{}}`)
	withCPG := put(cpg, `{"location":"westus","properties":{"administratorLoginPassword":"P@ssw0rd1234!",`+
		`"coordinatorVCores":2,"nodeCount":0,"postgresqlVersion":"16"}}`)
	withSyn := put(syn, `{"location":"westus","identity":{"type":"SystemAssigned"},"properties":{`+
		`"defaultDataLakeStorage":{"accountUrl":"https://a.dfs.core.windows.net","filesystem":"f"},`+
		`"sqlAdministratorLogin":"a","sqlAdministratorLoginPassword":"P@ssw0rd1234!"}}`)

	return []childPathRow{
		// P rows: the guard protects a top-level resource.
		{pkg: "keyvault", parent: cpRes("Microsoft.KeyVault/vaults", "kv1"), setup: put(
			cpRes("Microsoft.KeyVault/vaults", "kv1"), `{"location":"westus","tags":{"k":"v"},"properties":{`+
				`"tenantId":"00000000-0000-0000-0000-000000000001","sku":{"family":"A","name":"premium"},`+
				`"accessPolicies":[]}}`)},
		{pkg: "managedidentity", parent: cpRes("Microsoft.ManagedIdentity/userAssignedIdentities", "id1"),
			setup: put(cpRes("Microsoft.ManagedIdentity/userAssignedIdentities", "id1"), cpTagged)},
		{pkg: "appinsights", parent: cpRes("Microsoft.Insights/components", "ai1"), setup: put(
			cpRes("Microsoft.Insights/components", "ai1"),
			`{"location":"westus","kind":"web","tags":{"k":"v"},"properties":{"Application_Type":"web"}}`)},
		{pkg: "containerapps", parent: env, setup: put(env, cpTagged)},
		{pkg: "containerapps", parent: cpRes("Microsoft.App/containerApps", "app1")},
		{pkg: "monitor", parent: cpRes("Microsoft.Insights/actionGroups", "ag1"), setup: put(
			cpRes("Microsoft.Insights/actionGroups", "ag1"),
			`{"location":"global","tags":{"k":"v"},"properties":{"groupShortName":"ag","enabled":true}}`)},
		{pkg: "monitor", parent: cpRes("Microsoft.Insights/metricAlerts", "ma1")},
		{pkg: "monitor", parent: cpRes("Microsoft.Insights/activityLogAlerts", "ala1")},
		{pkg: "monitor", parent: cpRes("Microsoft.Insights/autoscaleSettings", "as1")},
		{pkg: "chaosstudio", parent: cpRes("Microsoft.Chaos/experiments", "ex1"),
			setup: put(cpRes("Microsoft.Chaos/experiments", "ex1"), cpTagged)},
		{pkg: "devcenter", parent: cpRes("Microsoft.DevCenter/devcenters", "dc1"),
			setup: put(cpRes("Microsoft.DevCenter/devcenters", "dc1"), cpTagged)},
		{pkg: "digitaltwins", parent: cpRes("Microsoft.DigitalTwins/digitalTwinsInstances", "dt1"),
			setup: put(cpRes("Microsoft.DigitalTwins/digitalTwinsInstances", "dt1"), cpTagged)},
		{pkg: "loadtesting", parent: cpRes("Microsoft.LoadTestService/loadTests", "lt1"),
			setup: put(cpRes("Microsoft.LoadTestService/loadTests", "lt1"), cpTagged)},
		{pkg: "managedgrafana", parent: cpRes("Microsoft.Dashboard/grafana", "gf1"),
			setup: put(cpRes("Microsoft.Dashboard/grafana", "gf1"), cpTagged)},
		{pkg: "snapshots", parent: cpRes("Microsoft.Compute/snapshots", "sn1"), setup: chain(withDisk, put(
			cpRes("Microsoft.Compute/snapshots", "sn1"), `{"location":"westus","tags":{"k":"v"},"properties":`+
				`{"creationData":{"createOption":"Copy","sourceResourceId":"`+disk+`"}}}`))},
		{pkg: "images", parent: cpRes("Microsoft.Compute/images", "im1"), setup: chain(withDisk, put(
			cpRes("Microsoft.Compute/images", "im1"), `{"location":"westus","tags":{"k":"v"},"properties":`+
				`{"storageProfile":{"osDisk":{"osType":"Linux","osState":"Generalized","managedDisk":{"id":"`+
				disk+`"}}}}}`))},
		{pkg: "sqlvirtualmachine", parent: cpRes("Microsoft.SqlVirtualMachine/sqlVirtualMachines", "sv1"),
			setup: put(cpRes("Microsoft.SqlVirtualMachine/sqlVirtualMachines", "sv1"),
				`{"location":"westus","tags":{"k":"v"},"properties":{"virtualMachineResourceId":"`+
					cpRes("Microsoft.Compute/virtualMachines", "vm1")+`"}}`)},
		{pkg: "sshpublickeys", parent: cpRes("Microsoft.Compute/sshPublicKeys", "kcp1"),
			setup: put(cpRes("Microsoft.Compute/sshPublicKeys", "kcp1"), `{"location":"westus","tags":{"k":"v"},`+
				`"properties":{"publicKey":"`+casSSHPK+`"}}`)},
		{pkg: "functions", parent: cpRes("Microsoft.Web/serverfarms", "plan1"), setup: put(
			cpRes("Microsoft.Web/serverfarms", "plan1"),
			`{"location":"westus","tags":{"k":"v"},"kind":"linux","sku":{"name":"B1"},"properties":{"reserved":true}}`)},
		{pkg: "vnet", parent: vnet, setup: withVNet},
		{pkg: "vnet", parent: nsg, setup: put(nsg, cpTagged)},
		{pkg: "vnet", parent: rt, setup: put(rt, cpTagged)},
		{pkg: "vnet", parent: cpRes("Microsoft.Network/publicIPAddresses", "pip1"),
			setup: put(cpRes("Microsoft.Network/publicIPAddresses", "pip1"), cpTagged)},
		{pkg: "vnet", parent: cpRes("Microsoft.Network/publicIPPrefixes", "pfx1"),
			setup: put(cpRes("Microsoft.Network/publicIPPrefixes", "pfx1"),
				`{"location":"westus","tags":{"k":"v"},"sku":{"name":"Standard"},"properties":{"prefixLength":28}}`)},
		{pkg: "vnet", parent: cpRes("Microsoft.Network/natGateways", "nat1"),
			setup: put(cpRes("Microsoft.Network/natGateways", "nat1"),
				`{"location":"westus","tags":{"k":"v"},"sku":{"name":"Standard"},"properties":{}}`)},
		{pkg: "vnet", parent: cpRes("Microsoft.Network/applicationSecurityGroups", "asg1"),
			setup: put(cpRes("Microsoft.Network/applicationSecurityGroups", "asg1"), cpTagged)},
		{pkg: "vnet", parent: cpRes("Microsoft.Network/networkInterfaces", "nic1"), setup: chain(withSubnet, put(
			cpRes("Microsoft.Network/networkInterfaces", "nic1"), `{"location":"westus","tags":{"k":"v"},`+
				`"properties":{"ipConfigurations":[{"name":"ipc","properties":{"subnet":{"id":"`+subnet+
				`"},"privateIPAllocationMethod":"Dynamic"}}]}}`))},
		{pkg: "vnet", parent: cpRes("Microsoft.Network/localNetworkGateways", "lng1"),
			setup: put(cpRes("Microsoft.Network/localNetworkGateways", "lng1"), `{"location":"westus",`+
				`"tags":{"k":"v"},"properties":{"gatewayIpAddress":"1.2.3.4",`+
				`"localNetworkAddressSpace":{"addressPrefixes":["10.50.0.0/16"]}}}`)},
		{pkg: "vnet", parent: cpRes("Microsoft.Network/virtualNetworkGateways", "vng1")},
		{pkg: "vnet", parent: cpRes("Microsoft.Network/connections", "conn1")},
		{pkg: "vnet", parent: cpRes("Microsoft.Network/privateEndpoints", "pe1")},
		{pkg: "vnet", parent: cpRes("Microsoft.Network/privateLinkServices", "pls1")},
		{pkg: "ai", parent: cpRes("Microsoft.MachineLearningServices/registries", "mlr1")},
		{pkg: "storageaccount", parent: cpRes("Microsoft.Storage/storageAccounts", "stcp1"), setup: put(
			cpRes("Microsoft.Storage/storageAccounts", "stcp1"),
			`{"location":"westus","kind":"StorageV2","sku":{"name":"Standard_GRS"},"tags":{"k":"v"},"properties":{}}`)},

		// D rows: the guard protects a child below its parent.
		{pkg: "sql", child: true, parent: sqlSrv + "/databases/db1",
			setup: chain(withSQL, put(sqlSrv+"/databases/db1", `{"location":"westus","properties":{}}`))},
		{pkg: "sql", child: true, parent: sqlSrv + "/firewallRules/fw1", setup: chain(withSQL, put(
			sqlSrv+"/firewallRules/fw1", `{"properties":{"startIpAddress":"1.1.1.1","endIpAddress":"1.1.1.2"}}`))},
		{pkg: "sql", child: true, parent: sqlSrv + "/elasticPools/ep1",
			setup: chain(withSQL, put(sqlSrv+"/elasticPools/ep1", `{"location":"westus","properties":{}}`))},
		{pkg: "sql", child: true, parent: sqlSrv + "/failoverGroups/fg1"},
		{pkg: "vnet", child: true, parent: subnet, setup: withSubnet},
		{pkg: "vnet", child: true, parent: nsg + "/securityRules/r1", setup: chain(put(nsg, cpTagged),
			put(nsg+"/securityRules/r1", `{"properties":{"priority":100,"direction":"Inbound","access":"Allow",`+
				`"protocol":"Tcp","sourcePortRange":"*","destinationPortRange":"22","sourceAddressPrefix":"*",`+
				`"destinationAddressPrefix":"*"}}`))},
		{pkg: "vnet", child: true, parent: rt + "/routes/r1", setup: chain(put(rt, cpTagged),
			put(rt+"/routes/r1", `{"properties":{"addressPrefix":"10.1.0.0/16","nextHopType":"Internet"}}`))},
		{pkg: "vnet", child: true, parent: vnet + "/virtualNetworkPeerings/p1"},
		{pkg: "postgresflex", child: true, parent: pg + "/databases/db1",
			setup: chain(withPG, put(pg+"/databases/db1", `{"properties":{"charset":"UTF8"}}`))},
		{pkg: "postgresflex", child: true, parent: pg + "/firewallRules/fw1", setup: chain(withPG, put(
			pg+"/firewallRules/fw1", `{"properties":{"startIpAddress":"1.1.1.1","endIpAddress":"1.1.1.2"}}`))},
		{pkg: "mysqlflex", child: true, parent: my + "/databases/db1",
			setup: chain(withMy, put(my+"/databases/db1", `{"properties":{"charset":"utf8"}}`))},
		{pkg: "mysqlflex", child: true, parent: my + "/firewallRules/fw1", setup: chain(withMy, put(
			my+"/firewallRules/fw1", `{"properties":{"startIpAddress":"1.1.1.1","endIpAddress":"1.1.1.2"}}`))},
		{pkg: "privatedns", child: true, parent: pdns + "/A/www", setup: chain(put(pdns, `{"location":"global"}`),
			put(pdns+"/A/www", `{"properties":{"ttl":300,"aRecords":[{"ipv4Address":"10.0.0.4"}]}}`))},
		{pkg: "dns", child: true, parent: dnsZone + "/A/www", setup: chain(put(dnsZone, `{"location":"global"}`),
			put(dnsZone+"/A/www", `{"properties":{"TTL":300,"ARecords":[{"ipv4Address":"10.0.0.4"}]}}`))},
		{pkg: "loganalytics", child: true, parent: law + "/savedSearches/ss1", setup: chain(
			put(law, `{"location":"westus","properties":{}}`),
			put(law+"/savedSearches/ss1", `{"properties":{"category":"c","displayName":"d","query":"q"}}`))},
		{pkg: "aks", child: true, parent: aks + "/agentPools/user1", setup: chain(withAKS,
			put(aks+"/agentPools/user1", `{"properties":{"count":1,"vmSize":"Standard_D2s_v3","mode":"User"}}`))},
		{pkg: "search", child: true,
			parent: cpRes("Microsoft.Search/searchServices", "srch1") + "/privateEndpointConnections/pec1"},
		{pkg: "ai", child: true, parent: cog + "/deployments/dep1", setup: chain(withCog,
			put(cog+"/deployments/dep1", `{"sku":{"name":"Standard","capacity":1},"properties":{"model":`+
				`{"format":"OpenAI","name":"gpt-4o","version":"2024-05-13"}}}`))},
		{pkg: "managedcassandra", child: true, parent: cass + "/dataCenters/dc1"},
		{pkg: "eventgrid", child: true, parent: egDomain + "/topics/t1",
			setup: chain(put(egDomain, `{"location":"westus","properties":{}}`), put(egDomain+"/topics/t1", `{}`))},
		{pkg: "eventgrid", child: true,
			parent: cpRes("Microsoft.EventGrid/systemTopics", "st1") + "/eventSubscriptions/es1"},
		{pkg: "recoveryservices", child: true, parent: rsv + "/backupPolicies/bp1"},
		{pkg: "cosmospostgresql", child: true, parent: cpg + "/firewallRules/fw1", setup: chain(withCPG, put(
			cpg+"/firewallRules/fw1", `{"properties":{"startIpAddress":"1.1.1.1","endIpAddress":"1.1.1.2"}}`))},
		{pkg: "synapse", child: true, parent: syn + "/bigDataPools/bp1", setup: chain(withSyn,
			put(syn+"/bigDataPools/bp1", `{"location":"westus","properties":{"nodeCount":3,"nodeSize":"Small",`+
				`"nodeSizeFamily":"MemoryOptimized","sparkVersion":"3.4"}}`))},
		{pkg: "acr", child: true, parent: acr + "/replications/westus2", setup: chain(
			put(acr, `{"location":"westus","sku":{"name":"Premium"}}`),
			put(acr+"/replications/westus2", `{"location":"westus2"}`))},
		{pkg: "loadbalancer", child: true, parent: lb + "/backendAddressPools/bp1"},
		{pkg: "containerapps", child: true, parent: cpRes("Microsoft.App/containerApps", "app1") + "/revisions/r1"},
	}
}

// cpBogusSuffixes are child paths no handler routes, at several depths.
func cpBogusSuffixes(child bool) []string {
	if child {
		return []string{"/zz", "/zz/x", "/zz/x/y"}
	}

	return []string{"/zzunknown/c1", "/zzunknown", "/a/b/c/d/e", "/zz/c/zz/d"}
}

func cpFailClosed(code int) bool {
	return code == http.StatusNotFound || code == http.StatusMethodNotAllowed || code == http.StatusNotImplemented
}

// TestChildPathParentIntact is the parent-collapse and depth-collapse
// regression: a write or delete on a nested type the handler does not route
// must fail closed and leave the protected resource byte-identical.
func TestChildPathParentIntact(t *testing.T) {
	for _, row := range childPathRows() {
		if row.setup == nil {
			continue
		}

		t.Run(row.pkg+row.parent[strings.LastIndex(row.parent, "/providers/"):], func(t *testing.T) {
			c := newCPClient(t)
			row.setup(c)

			code, before := c.get(row.parent)
			if code != http.StatusOK {
				t.Fatalf("GET %s: %d %s", row.parent, code, before)
			}

			for _, suffix := range cpBogusSuffixes(row.child) {
				for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
					if code, out := c.do(m, row.parent+suffix, cpChildBody); !cpFailClosed(code) {
						t.Errorf("%s %s: status %d, want 404/405/501: %s", m, suffix, code, out)
					}
				}
			}

			if code, after := c.get(row.parent); code != http.StatusOK || !bytes.Equal(before, after) {
				t.Errorf("protected resource changed: %d\nbefore %s\nafter  %s", code, before, after)
			}
		})
	}
}

// TestChildPathGhostParent checks that a child write under a missing parent
// is InvalidResourceType and never creates the parent.
func TestChildPathGhostParent(t *testing.T) {
	c := newCPClient(t)

	for _, row := range childPathRows() {
		if row.child {
			continue
		}

		ghost := row.parent[:strings.LastIndex(row.parent, "/")] + "/ghost"

		for _, m := range []string{http.MethodPut, http.MethodGet, http.MethodDelete} {
			code, out := c.do(m, ghost+"/zzunknown/c1", cpChildBody)
			if code != http.StatusNotFound || !bytes.Contains(out, []byte("InvalidResourceType")) {
				t.Errorf("%s %s/zzunknown/c1: %d %s, want 404 InvalidResourceType", m, ghost, code, out)
			}
		}

		if code, _ := c.get(ghost); code != http.StatusNotFound {
			t.Errorf("GET %s after child probes: %d, want 404 (ghost parent created)", ghost, code)
		}
	}
}

// TestChildPathExtensionNotImplemented checks that an extension resource no
// extension handler claims is 501, and never reaches the parent.
func TestChildPathExtensionNotImplemented(t *testing.T) {
	c := newCPClient(t)

	for _, row := range childPathRows() {
		if row.child {
			continue
		}

		if row.setup != nil {
			row.setup(c)
		}

		_, before := c.get(row.parent)
		ext := row.parent + "/providers/Microsoft.Security/zz/x"

		for _, m := range []string{http.MethodPut, http.MethodGet, http.MethodDelete} {
			if code, out := c.do(m, ext, cpChildBody); code != http.StatusNotImplemented {
				t.Errorf("%s %s: %d %s, want 501", m, ext, code, out)
			}
		}

		if _, after := c.get(row.parent); !bytes.Equal(before, after) {
			t.Errorf("%s changed by extension probes:\nbefore %s\nafter  %s", row.parent, before, after)
		}
	}
}
