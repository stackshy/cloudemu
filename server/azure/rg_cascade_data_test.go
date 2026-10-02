package azure_test

import (
	"net/http"
	"strings"
	"testing"
)

// seedDataFamilies creates one resource of every data, messaging and web
// family the resource-group cascade covers, plus a child where the type has
// one, and returns the GET paths of everything made. Names carry the group so
// globally unique names (vaults, servers, caches) never collide.
func seedDataFamilies(c *casClient, rg string) []string {
	c.t.Helper()

	c.mustPut(rgPath(rg), `{"location":"eastus"}`)

	sfx := strings.ToLower(rg)
	loc := `{"location":"eastus"}`

	kv := resPath(rg, "Microsoft.KeyVault/vaults", "kv"+sfx)
	c.mustPut(kv, `{"location":"eastus","properties":{"tenantId":"00000000-0000-0000-0000-000000000001",`+
		`"sku":{"family":"A","name":"standard"},"accessPolicies":[{"tenantId":"00000000-0000-0000-0000-000000000001",`+
		`"objectId":"00000000-0000-0000-0000-000000000002","permissions":{"secrets":["get"]}}]}}`)

	mi := resPath(rg, "Microsoft.ManagedIdentity/userAssignedIdentities", "id"+sfx)
	c.mustPut(mi, loc)

	fic := mi + "/federatedIdentityCredentials/fic1"
	c.mustPut(fic, `{"properties":{"issuer":"https://issuer.example.com","subject":"system:sa:`+sfx+`",`+
		`"audiences":["api://AzureADTokenExchange"]}}`)

	cosmos := resPath(rg, "Microsoft.DocumentDB/databaseAccounts", "cos"+sfx)
	c.mustPut(cosmos, `{"location":"eastus","kind":"GlobalDocumentDB","properties":{"databaseAccountOfferType":"Standard",`+
		`"locations":[{"locationName":"eastus","failoverPriority":0}]}}`)

	cosmosDB := cosmos + "/sqlDatabases/db1"
	c.mustPut(cosmosDB, `{"properties":{"resource":{"id":"db1"}}}`)

	sb := resPath(rg, "Microsoft.ServiceBus/namespaces", "sb"+sfx)
	c.mustPut(sb, `{"location":"eastus","sku":{"name":"Standard"}}`)

	queue := sb + "/queues/q1"
	c.mustPut(queue, `{"properties":{}}`)

	eh := resPath(rg, "Microsoft.EventHub/namespaces", "eh"+sfx)
	c.mustPut(eh, `{"location":"eastus","sku":{"name":"Standard"}}`)

	hub := eh + "/eventhubs/h1"
	c.mustPut(hub, `{"properties":{"partitionCount":2}}`)

	sqlSrv := resPath(rg, "Microsoft.Sql/servers", "sql"+sfx)
	c.mustPut(sqlSrv, `{"location":"eastus","properties":{"administratorLogin":"sqladmin",`+
		`"administratorLoginPassword":"P@ssw0rd1234!","version":"12.0"}}`)

	sqlDB := sqlSrv + "/databases/db1"
	c.mustPut(sqlDB, loc)

	str := sqlDB + "/backupShortTermRetentionPolicies/default"
	c.mustPut(str, `{"properties":{"retentionDays":14}}`)

	redis := resPath(rg, "Microsoft.Cache/redis", "redis"+sfx)
	c.mustPut(redis, `{"location":"eastus","properties":{"sku":{"name":"Basic","family":"C","capacity":0}}}`)

	la := resPath(rg, "Microsoft.OperationalInsights/workspaces", "la"+sfx)
	c.mustPut(la, loc)

	flexBody := `{"location":"eastus","sku":{"name":"Standard_B1ms","tier":"Burstable"},` +
		`"properties":{"administratorLogin":"flexadmin","administratorLoginPassword":"P@ssw0rd1234!"}}`

	my := resPath(rg, "Microsoft.DBforMySQL/flexibleServers", "my"+sfx)
	c.mustPut(my, flexBody)

	pg := resPath(rg, "Microsoft.DBforPostgreSQL/flexibleServers", "pg"+sfx)
	c.mustPut(pg, flexBody)

	cpg := resPath(rg, "Microsoft.DBforPostgreSQL/serverGroupsv2", "cpg"+sfx)
	c.mustPut(cpg, `{"location":"eastus","properties":{"administratorLoginPassword":"P@ssw0rd1234!"}}`)

	mc := resPath(rg, "Microsoft.DocumentDB/cassandraClusters", "mc"+sfx)
	c.mustPut(mc, `{"location":"eastus","properties":{"initialCassandraAdminPassword":"P@ssw0rd1234!"}}`)

	search := resPath(rg, "Microsoft.Search/searchServices", "srch"+sfx)
	c.mustPut(search, `{"location":"eastus","sku":{"name":"basic"}}`)

	plan := resPath(rg, "Microsoft.Web/serverfarms", "plan1")
	c.mustPut(plan, `{"location":"eastus","kind":"linux","sku":{"name":"B1"},"properties":{"reserved":true}}`)

	site := resPath(rg, "Microsoft.Web/sites", "site"+sfx)
	c.mustPut(site, `{"location":"eastus","kind":"app,linux","properties":{"serverFarmId":"`+plan+`"}}`)

	return []string{
		kv, mi, fic, cosmos, cosmosDB, sb, queue, eh, hub, sqlSrv, sqlDB, str, redis, la,
		my, pg, cpg, mc, search, plan, site,
	}
}

// TestResourceGroupDeleteCascadesDataFamilies is the AZRM-02 regression for
// the data, messaging and web families: deleting a group removes every vault,
// Cosmos account, Service Bus and Event Hubs namespace, SQL server, cache,
// workspace, flexible server, cluster, search service, site and plan in it,
// with their children, and leaves a group whose name only starts the same
// untouched. A recreated group resurrects nothing.
func TestResourceGroupDeleteCascadesDataFamilies(t *testing.T) {
	c := newCasClient(t)

	gone := seedDataFamilies(c, "Dat1")
	kept := seedDataFamilies(c, "dat10")

	c.wantStatus(http.MethodDelete, rgPath("dat1"), http.StatusAccepted)

	for _, p := range gone {
		c.wantStatus(http.MethodGet, p, http.StatusNotFound)
	}

	for _, p := range kept {
		c.wantStatus(http.MethodGet, p, http.StatusOK)
	}

	c.mustPut(rgPath("dat1"), `{"location":"eastus"}`)

	for _, p := range gone {
		c.wantStatus(http.MethodGet, p, http.StatusNotFound)
	}

	if _, out := c.do(http.MethodGet, rgPath("dat1")+"/resources", ""); len(asList(out["value"])) != 0 {
		t.Errorf("recreated group lists resources: %v", out["value"])
	}
}
