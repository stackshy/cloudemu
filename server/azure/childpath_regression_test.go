package azure_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

const cpContributor = "/subscriptions/" + cpSub +
	"/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"

// TestRoleAssignmentAtResourceScopeReachesIAM is the extension-path blocker: a
// role assignment scoped to a resource used to be claimed by that resource's
// own handler and overwrite it.
func TestRoleAssignmentAtResourceScopeReachesIAM(t *testing.T) {
	c := newCPClient(t)

	vnet := cpRes("Microsoft.Network/virtualNetworks", "vn1")
	identity := cpRes("Microsoft.ManagedIdentity/userAssignedIdentities", "id1")
	zone := cpRes("Microsoft.Network/dnsZones", "cp.example.com")

	c.mustPut(vnet, cpVNetBody)
	c.mustPut(vnet+"/subnets/s1", `{"properties":{"addressPrefix":"10.0.1.0/24"}}`)
	c.mustPut(identity, cpTagged)
	c.mustPut(zone, `{"location":"global","tags":{"k":"v"}}`)

	scopes := []struct {
		scope, guid string
	}{
		{vnet, "00000000-0000-0000-0000-0000000000a1"},
		{vnet + "/subnets/s1", "00000000-0000-0000-0000-0000000000a2"},
		{identity, "00000000-0000-0000-0000-0000000000a3"},
		{zone, "00000000-0000-0000-0000-0000000000a4"},
	}

	for _, s := range scopes {
		_, before := c.get(s.scope)
		ra := s.scope + "/providers/Microsoft.Authorization/roleAssignments/" + s.guid

		code, out := c.do(http.MethodPut, ra, `{"properties":{"roleDefinitionId":"`+cpContributor+
			`","principalId":"11111111-1111-1111-1111-111111111111"}}`)
		if code != http.StatusCreated || !bytes.Contains(out, []byte(`"Microsoft.Authorization/roleAssignments"`)) {
			t.Errorf("PUT %s: %d %s, want 201 roleAssignment", ra, code, out)
		}

		if code, _ := c.get(ra); code != http.StatusOK {
			t.Errorf("GET %s: %d, want 200", ra, code)
		}

		if _, after := c.get(s.scope); !bytes.Equal(before, after) {
			t.Errorf("%s changed by its role assignment:\nbefore %s\nafter  %s", s.scope, before, after)
		}
	}

	// Locks at resource scope still reach the locks handler.
	code, out := c.get(vnet + "/providers/Microsoft.Authorization/locks")
	if code != http.StatusOK || !bytes.Contains(out, []byte(`"value":[]`)) {
		t.Errorf("GET vnet locks: %d %s, want 200 empty list", code, out)
	}
}

type cpExpect struct {
	method, suffix string
	status         int
	body           string // substring the response must contain
}

// TestDeferredChildrenNeverReachTheParent covers the named rows: each child
// type real Azure has but cloudemu does not model answers the read rule
// (default, empty list or 404) and 501 on writes, and the parent survives.
//
//nolint:funlen // one block per named regression keeps them traceable
func TestDeferredChildrenNeverReachTheParent(t *testing.T) {
	kv := cpRes("Microsoft.KeyVault/vaults", "kv1")
	mi := cpRes("Microsoft.ManagedIdentity/userAssignedIdentities", "id1")
	ai := cpRes("Microsoft.Insights/components", "ai1")
	env := cpRes("Microsoft.App/managedEnvironments", "env1")
	ag := cpRes("Microsoft.Insights/actionGroups", "ag1")
	sqlSrv := cpRes("Microsoft.Sql/servers", "sql1")
	db := sqlSrv + "/databases/db1"
	pe := cpRes("Microsoft.Network/privateEndpoints", "pe1")

	cases := []struct {
		row    string
		parent string
		setup  func(c *cpClient)
		probes []cpExpect
	}{
		{"AZKV-01", kv, put(kv, `{"location":"westus","tags":{"k":"v"},"properties":{`+
			`"tenantId":"00000000-0000-0000-0000-000000000001","sku":{"family":"A","name":"premium"},"accessPolicies":[]}}`),
			[]cpExpect{
				{http.MethodPut, "/accessPolicies/add", http.StatusOK, `"name":"add"`},
				{http.MethodDelete, "/accessPolicies/add", http.StatusMethodNotAllowed, ""},
				{http.MethodPut, "/secrets/s1", http.StatusNotImplemented, ""},
				{http.MethodGet, "/secrets", http.StatusOK, `"value":[]`},
				{http.MethodGet, "/secrets/s1", http.StatusNotFound, "ResourceNotFound"},
			}},
		{"AZKV-02", mi, put(mi, cpTagged), []cpExpect{
			{http.MethodPut, "/federatedIdentityCredentials/f1", http.StatusBadRequest, "BadRequest"},
			{http.MethodDelete, "/federatedIdentityCredentials/f1", http.StatusNoContent, ""},
			{http.MethodGet, "/federatedIdentityCredentials", http.StatusOK, `"value":[]`},
		}},
		{"AZOBS-01", ai, put(ai, `{"location":"westus","kind":"web","tags":{"k":"v"},`+
			`"properties":{"Application_Type":"web","RetentionInDays":30}}`), []cpExpect{
			{http.MethodGet, "/currentbillingfeatures", http.StatusOK, `"CurrentBillingFeatures":["Basic"]`},
			{http.MethodPut, "/currentbillingfeatures", http.StatusNotImplemented, ""},
			{http.MethodDelete, "/currentbillingfeatures", http.StatusNotImplemented, ""},
		}},
		{"AZAPP-01", env, put(env, cpTagged), []cpExpect{
			{http.MethodPut, "/daprComponents/d1", http.StatusBadRequest, "componentType"},
			{http.MethodDelete, "/storages/s1", http.StatusNoContent, ""},
			{http.MethodGet, "/daprComponents", http.StatusOK, `"value":[]`},
			{http.MethodGet, "/daprComponents/d1/zz", http.StatusNotFound, "InvalidResourceType"},
			{http.MethodPut, "/certificates/c1", http.StatusNotImplemented, ""},
		}},
		{"AZOBS-02", ag, put(ag, `{"location":"global","tags":{"k":"v"},"properties":{"groupShortName":"ag"}}`),
			[]cpExpect{
				{http.MethodDelete, "/networkSecurityPerimeterConfigurations/x", http.StatusNotImplemented, ""},
				{http.MethodDelete, "/zz/x", http.StatusNotFound, "InvalidResourceType"},
			}},
		{"AZDB-01", db, chain(put(sqlSrv, `{"location":"westus","properties":{"administratorLogin":"a",`+
			`"administratorLoginPassword":"P@ssw0rd1234!","version":"12.0"}}`),
			put(db, `{"location":"westus","properties":{}}`)), []cpExpect{
			{http.MethodDelete, "/backupShortTermRetentionPolicies/default", http.StatusMethodNotAllowed, ""},
			{http.MethodPut, "/backupShortTermRetentionPolicies/default", http.StatusOK, `"retentionDays":7`},
			{http.MethodGet, "/backupShortTermRetentionPolicies/default", http.StatusOK, `"retentionDays":7`},
			{http.MethodGet, "/backupLongTermRetentionPolicies/default", http.StatusOK, `"weeklyRetention":"PT0S"`},
			{http.MethodGet, "/securityAlertPolicies/Default", http.StatusOK, `"state":"Disabled"`},
			{http.MethodPut, "/securityAlertPolicies/Default", http.StatusNotImplemented, ""},
			{http.MethodGet, "/geoBackupPolicies/Default", http.StatusOK, `"state":"Disabled"`},
			{http.MethodGet, "/auditingSettings/default", http.StatusOK, `"state":"Disabled"`},
			{http.MethodGet, "/ledgerDigestUploads/current", http.StatusOK, `"state":"Disabled"`},
			{http.MethodGet, "/securityAlertPolicies", http.StatusOK, `"value":[]`},
			{http.MethodGet, "/replicationLinks", http.StatusOK, `"value":[]`},
			{http.MethodDelete, "/replicationLinks/l1", http.StatusNotImplemented, ""},
			{http.MethodGet, "/securityAlertPolicies/other", http.StatusNotFound, ""},
			{http.MethodGet, "/transparentDataEncryption/current", http.StatusOK, `"state":"Enabled"`},
			{http.MethodGet, "/transparentDataEncryption", http.StatusOK, `"value":[`},
			{http.MethodDelete, "/zzbogus/x", http.StatusNotFound, "InvalidResourceType"},
		}},
		{"connectionPolicies", sqlSrv, put(sqlSrv, `{"location":"westus","properties":{"administratorLogin":"a",`+
			`"administratorLoginPassword":"P@ssw0rd1234!","version":"12.0"}}`), []cpExpect{
			{http.MethodGet, "/connectionPolicies/default", http.StatusOK, `"connectionType":"Default"`},
			{http.MethodPut, "/connectionPolicies/default", http.StatusBadRequest, ""},
			{http.MethodGet, "/connectionPolicies", http.StatusOK, `"connectionType":"Default"`},
			{http.MethodGet, "/restorableDroppedDatabases", http.StatusOK, `"value":[]`},
			{http.MethodGet, "/sqlVulnerabilityAssessments/default", http.StatusOK, `"state":"Disabled"`},
			{http.MethodGet, "/connectionPolicies/other", http.StatusNotFound, ""},
		}},
		{"W1-D8", pe, nil, []cpExpect{
			{http.MethodGet, "/privateDnsZoneGroups", http.StatusOK, `"value":[]`},
			{http.MethodPut, "/privateDnsZoneGroups/g1", http.StatusNotImplemented, ""},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.row, func(t *testing.T) {
			c := newCPClient(t)
			if tc.setup != nil {
				tc.setup(c)
			}

			_, before := c.get(tc.parent)

			for _, p := range tc.probes {
				code, out := c.do(p.method, tc.parent+p.suffix, cpChildBody)
				if code != p.status || !bytes.Contains(out, []byte(p.body)) {
					t.Errorf("%s %s: %d %s, want %d containing %q", p.method, p.suffix, code, out, p.status, p.body)
				}
			}

			if _, after := c.get(tc.parent); !bytes.Equal(before, after) {
				t.Errorf("parent changed:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}

// TestDatabaseSingletonRestatingDefaultIsNoOp covers the write azurerm sends
// for securityAlertPolicies on every mssql_database update: restating the
// Disabled default is answered with the default; enabling it is not modeled.
func TestDatabaseSingletonRestatingDefaultIsNoOp(t *testing.T) {
	c := newCPClient(t)
	srv := cpRes("Microsoft.Sql/servers", "sql1")
	db := srv + "/databases/db1"

	c.mustPut(srv, `{"location":"westus","properties":{"administratorLogin":"a",`+
		`"administratorLoginPassword":"P@ssw0rd1234!","version":"12.0"}}`)
	c.mustPut(db, `{"location":"westus","tags":{"k":"v"},"properties":{}}`)

	_, before := c.get(db)

	if code, out := c.do(http.MethodPut, db+"/securityAlertPolicies/Default",
		`{"properties":{"state":"Disabled","emailAccountAdmins":false}}`); code != http.StatusOK ||
		!bytes.Contains(out, []byte(`"state":"Disabled"`)) {
		t.Errorf("PUT Disabled: %d %s, want 200 default", code, out)
	}

	if code, _ := c.do(http.MethodPut, db+"/securityAlertPolicies/Default",
		`{"properties":{"state":"Enabled"}}`); code != http.StatusNotImplemented {
		t.Errorf("PUT Enabled: %d, want 501", code)
	}

	if _, after := c.get(db); !bytes.Equal(before, after) {
		t.Errorf("database changed:\nbefore %s\nafter  %s", before, after)
	}
}

// TestDatabaseSingletonNeedsTheDatabase keeps the singleton defaults
// truthful: a database that does not exist has no policies.
func TestDatabaseSingletonNeedsTheDatabase(t *testing.T) {
	c := newCPClient(t)
	srv := cpRes("Microsoft.Sql/servers", "sql1")
	c.mustPut(srv, `{"location":"westus","properties":{"administratorLogin":"a",`+
		`"administratorLoginPassword":"P@ssw0rd1234!","version":"12.0"}}`)

	if code, out := c.get(srv + "/databases/nope/securityAlertPolicies/Default"); code != http.StatusNotFound {
		t.Errorf("singleton of a missing database: %d %s, want 404", code, out)
	}
}

// TestTopicScopedExtensionSubscriptionThroughGuardedLeaf sends the extension
// form of a topic event subscription, which eventgrid serves through a
// hand-built ResourcePath, past the guarded serveEventSubscription leaf.
func TestTopicScopedExtensionSubscriptionThroughGuardedLeaf(t *testing.T) {
	c := newCPClient(t)
	topic := cpRes("Microsoft.EventGrid/topics", "t1")
	c.mustPut(topic, `{"location":"westus","properties":{}}`)

	ext := topic + "/providers/Microsoft.EventGrid/eventSubscriptions/s1"
	c.mustPut(ext, `{"properties":{"destination":{"endpointType":"WebHook",`+
		`"properties":{"endpointUrl":"https://example.com/hook"}}}}`)

	code, out := c.get(topic + "/eventSubscriptions/s1")
	if code != http.StatusOK {
		t.Fatalf("GET direct form: %d %s", code, out)
	}

	var sub struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal(out, &sub); err != nil || sub.Name != "s1" {
		t.Errorf("direct form body %s (err %v)", out, err)
	}
}
