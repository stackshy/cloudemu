package azure_test

import (
	"net/http"
	"strings"
	"testing"
)

// seedTailFamilies creates one resource of every family the last cascade slice
// covers (monitor, Event Grid, Notification Hubs, Databricks, Kusto, Cognitive
// Services, Machine Learning, role assignments, SQL managed instances), plus a
// child where the type has one, and returns the GET paths of everything made
// and the tag-set path attached inside the group. Globally keyed names carry
// the group.
func seedTailFamilies(c *casClient, rg, guid string) (paths []string, tagScope string) {
	c.t.Helper()

	c.mustPut(rgPath(rg), `{"location":"eastus"}`)

	sfx := strings.ToLower(rg)
	loc := `{"location":"eastus"}`

	ag := resPath(rg, "Microsoft.Insights/actionGroups", "ag1")
	c.mustPut(ag, `{"location":"global","properties":{"groupShortName":"ag","enabled":true}}`)

	ala := resPath(rg, "Microsoft.Insights/activityLogAlerts", "ala1")
	c.mustPut(ala, `{"location":"global","properties":{"scopes":["`+rgPath(rg)+`"],`+
		`"condition":{"allOf":[{"field":"category","equals":"Administrative"}]},"actions":{"actionGroups":[]}}}`)

	diag := ag + "/providers/microsoft.insights/diagnosticSettings/ds1"
	c.mustPut(diag, `{"properties":{"logs":[]}}`)

	topic := resPath(rg, "Microsoft.EventGrid/topics", "egt"+sfx)
	c.mustPut(topic, loc)

	domain := resPath(rg, "Microsoft.EventGrid/domains", "egd1")
	c.mustPut(domain, loc)

	sysTopic := resPath(rg, "Microsoft.EventGrid/systemTopics", "egs1")
	c.mustPut(sysTopic, `{"location":"global","properties":{"source":"`+
		resPath(rg, "Microsoft.Storage/storageAccounts", "st"+sfx)+`","topicType":"Microsoft.Storage.StorageAccounts"}}`)

	scopedSub := rgPath(rg) + "/providers/Microsoft.EventGrid/eventSubscriptions/es1"
	c.mustPut(scopedSub, `{"properties":{"destination":{"endpointType":"WebHook",`+
		`"properties":{"endpointUrl":"https://example.com/hook"}}}}`)

	nh := resPath(rg, "Microsoft.NotificationHubs/namespaces", "nh"+sfx)
	c.mustPut(nh, `{"location":"eastus","sku":{"name":"Free"}}`)

	hub := nh + "/notificationHubs/h1"
	c.mustPut(hub, loc)

	dbx := resPath(rg, "Microsoft.Databricks/workspaces", "dbx1")
	c.mustPut(dbx, `{"location":"eastus","sku":{"name":"standard"},"properties":{"managedResourceGroupId":"`+
		rgPath("mrg-"+sfx)+`"}}`)

	ac := resPath(rg, "Microsoft.Databricks/accessConnectors", "ac1")
	c.mustPut(ac, loc)

	kusto := resPath(rg, "Microsoft.Kusto/clusters", "kc"+sfx)
	c.mustPut(kusto, `{"location":"eastus","sku":{"name":"Dev(No SLA)_Standard_E2a_v4","tier":"Basic","capacity":1}}`)

	kdb := kusto + "/databases/db1"
	c.mustPut(kdb, `{"location":"eastus","kind":"ReadWrite"}`)

	cog := resPath(rg, "Microsoft.CognitiveServices/accounts", "cog1")
	c.mustPut(cog, `{"location":"eastus","kind":"OpenAI","sku":{"name":"S0"}}`)

	ml := resPath(rg, "Microsoft.MachineLearningServices/workspaces", "ml1")
	c.mustPut(ml, loc)

	ra := rgPath(rg) + "/providers/Microsoft.Authorization/roleAssignments/" + guid
	c.mustPut(ra, `{"properties":{"roleDefinitionId":"/subscriptions/`+casSub+
		`/providers/Microsoft.Authorization/roleDefinitions/acdd72a7-3385-48ef-bd42-f606fba81ae7",`+
		`"principalId":"00000000-0000-0000-0000-0000000000aa"}}`)

	mi := resPath(rg, "Microsoft.Sql/managedInstances", "mi"+sfx)
	c.mustPut(mi, `{"location":"eastus","properties":{"administratorLogin":"miadmin","subnetId":"`+
		resPath(rg, "Microsoft.Network/virtualNetworks", "vn")+`/subnets/mi"}}`)

	tagScope = ag + "/providers/Microsoft.Resources/tags/default"
	c.mustPut(tagScope, `{"properties":{"tags":{"team":"`+sfx+`"}}}`)

	return []string{
		ag, ala, diag, topic, domain, sysTopic, scopedSub, nh, hub, dbx, ac, kusto, kdb, cog, ml, ra, mi,
	}, tagScope
}

// TestResourceGroupDeleteCascadesTailFamilies is the AZRM-02b regression:
// deleting a group removes its monitor, Event Grid, Notification Hubs,
// Databricks, Kusto, AI, role-assignment, tag-set and SQL managed instance
// state, and leaves a group whose name only starts the same untouched.
func TestResourceGroupDeleteCascadesTailFamilies(t *testing.T) {
	c := newCasClient(t)

	gone, goneTags := seedTailFamilies(c, "Tail1", "11111111-1111-1111-1111-111111111111")
	kept, keptTags := seedTailFamilies(c, "tail10", "22222222-2222-2222-2222-222222222222")

	c.wantStatus(http.MethodDelete, rgPath("tail1"), http.StatusAccepted)

	// A deleted group answers 404 for everything under it whatever the stores
	// hold, so recreate it: only a real purge keeps the old resources gone.
	c.mustPut(rgPath("tail1"), `{"location":"eastus"}`)

	for _, p := range gone {
		c.wantStatus(http.MethodGet, p, http.StatusNotFound)
	}

	for _, p := range kept {
		c.wantStatus(http.MethodGet, p, http.StatusOK)
	}

	if n := len(tagSet(c, goneTags)); n != 0 {
		t.Errorf("tag set inside the deleted group survived with %d tags", n)
	}

	if n := len(tagSet(c, keptTags)); n != 1 {
		t.Errorf("tag set in the kept group has %d tags, want 1", n)
	}
}

func tagSet(c *casClient, path string) map[string]any {
	c.t.Helper()

	_, out := c.do(http.MethodGet, path, "")
	props, _ := out["properties"].(map[string]any)
	tags, _ := props["tags"].(map[string]any)

	return tags
}
