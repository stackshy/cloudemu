package azure_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// echoTestServer wires a full Azure server (with the unmodeled-property overlay)
// over a plain-HTTP httptest server. These tests drive the wire directly with an
// http.Client rather than an SDK, so they can send properties no typed SDK model
// exposes — which is the whole point of the fidelity fix.
func echoTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()

	srv := azureserver.NewFromProvider(cloudemu.NewAzure())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts, ts.Client()
}

func putJSON(t *testing.T, c *http.Client, url string, body map[string]any) map[string]any {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	return doJSON(t, c, req)
}

func getJSON(t *testing.T, c *http.Client, url string) map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	return doJSON(t, c, req)
}

func doJSON(t *testing.T, c *http.Client, req *http.Request) map[string]any {
	t.Helper()

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s %s: status %d: %s", req.Method, req.URL.Path, resp.StatusCode, data)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode %s: %v (%s)", req.URL.Path, err, data)
	}

	return out
}

func props(t *testing.T, resource map[string]any) map[string]any {
	t.Helper()

	p, ok := resource["properties"].(map[string]any)
	if !ok {
		t.Fatalf("resource has no properties object: %v", resource)
	}

	return p
}

// TestEchoUnmodeledPropertiesRoundTrip is the load-bearing fidelity test: an
// unmodeled top-level property and an unmodeled leaf nested under a modeled
// parent both survive the create response and a later GET, while the modeled
// fields the handler owns remain authoritative.
func TestEchoUnmodeledPropertiesRoundTrip(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm1?api-version=2023-07-01"

	created := putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"hardwareProfile": map[string]any{
				"vmSize":           "Standard_D2s_v5",
				"vmSizeProperties": map[string]any{"vCPUsAvailable": float64(2)},
			},
			"evictionPolicy": "Deallocate",
		},
	})

	cp := props(t, created)
	if cp["evictionPolicy"] != "Deallocate" {
		t.Errorf("create response dropped evictionPolicy: %v", cp["evictionPolicy"])
	}

	// A create is a long-running op: the handler's authoritative create value
	// is "Creating" (it settles to "Succeeded" on a later GET).
	if cp["provisioningState"] != "Creating" {
		t.Errorf("create response lost modeled provisioningState: %v", cp["provisioningState"])
	}

	got := getJSON(t, c, url)
	gp := props(t, got)

	if gp["evictionPolicy"] != "Deallocate" {
		t.Errorf("GET dropped unmodeled evictionPolicy: %v", gp["evictionPolicy"])
	}

	hw, ok := gp["hardwareProfile"].(map[string]any)
	if !ok {
		t.Fatalf("GET has no hardwareProfile: %v", gp)
	}

	if hw["vmSize"] != "Standard_D2s_v5" {
		t.Errorf("GET lost modeled vmSize: %v", hw["vmSize"])
	}

	nested, ok := hw["vmSizeProperties"].(map[string]any)
	if !ok || nested["vCPUsAvailable"] != float64(2) {
		t.Errorf("GET dropped unmodeled nested vmSizeProperties: %v", hw["vmSizeProperties"])
	}
}

// TestEchoDoesNotOverrideModeledFields confirms the overlay never overwrites a
// property the handler models: a request value for a modeled field is ignored
// in favor of the driver's authoritative value.
func TestEchoDoesNotOverrideModeledFields(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm2?api-version=2023-07-01"

	created := putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"hardwareProfile":   map[string]any{"vmSize": "Standard_D2s_v5"},
			"provisioningState": "Succeeded", // the handler owns this — must win
		},
	})

	// The handler's authoritative create value ("Creating") must win over the
	// request-supplied value rather than the overlay clobbering it.
	if got := props(t, created)["provisioningState"]; got != "Creating" {
		t.Errorf("overlay overrode modeled provisioningState: got %v, want Creating", got)
	}
}

func postJSON(t *testing.T, c *http.Client, url string) map[string]any {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	return doJSON(t, c, req)
}

// TestEchoSurvivesBodyReturningLifecycleAction is the regression for the High
// finding: a lifecycle action (POST .../stop) that returns a full resource body
// must not wipe the unmodeled properties preserved on create. Flex servers take
// this path (their stop echoes the server), unlike VMs which return a bodiless
// 202.
func TestEchoSurvivesBodyReturningLifecycleAction(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{MySQLFlex: cloudP.MySQLFlex})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	c := ts.Client()

	base := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DBforMySQL/flexibleServers/db1"

	putJSON(t, c, base+"?api-version=2023-12-30", map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"administratorLogin": "adm",
			"version":            "8.0.21",
			// maintenanceWindow is not modeled by the flex handler; the overlay
			// must preserve it.
			"maintenanceWindow": map[string]any{"dayOfWeek": float64(3)},
		},
	})

	if props(t, getJSON(t, c, base+"?api-version=2023-12-30"))["maintenanceWindow"] == nil {
		t.Fatal("unmodeled maintenanceWindow was not preserved after create")
	}

	// Stop returns the full server body — the path that previously wiped the overlay.
	postJSON(t, c, base+"/stop?api-version=2023-12-30")

	mw := props(t, getJSON(t, c, base+"?api-version=2023-12-30"))["maintenanceWindow"]
	if mw == nil {
		t.Fatal("lifecycle action (stop) wiped the preserved maintenanceWindow")
	}

	if m, ok := mw.(map[string]any); !ok || m["dayOfWeek"] != float64(3) {
		t.Errorf("maintenanceWindow corrupted after stop: %v", mw)
	}
}

// TestEchoPartialPatchKeepsPreservedProps confirms a partial PATCH that does not
// resend an earlier unmodeled property keeps it (union, not replace).
func TestEchoPartialPatchKeepsPreservedProps(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{MySQLFlex: cloudP.MySQLFlex})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	c := ts.Client()

	base := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DBforMySQL/flexibleServers/db2"

	putJSON(t, c, base+"?api-version=2023-12-30", map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"administratorLogin": "adm",
			"maintenanceWindow":  map[string]any{"dayOfWeek": float64(3)},
		},
	})

	// PATCH a different (also unmodeled) field, without resending maintenanceWindow.
	patch, err := http.NewRequestWithContext(context.Background(), http.MethodPatch,
		base+"?api-version=2023-12-30",
		bytesReader(t, map[string]any{"properties": map[string]any{"tags2": "x"}}))
	if err != nil {
		t.Fatal(err)
	}

	patch.Header.Set("Content-Type", "application/json")
	doJSON(t, c, patch)

	got := props(t, getJSON(t, c, base+"?api-version=2023-12-30"))
	if got["maintenanceWindow"] == nil {
		t.Error("partial PATCH dropped the earlier preserved maintenanceWindow")
	}
}

// TestEchoDoesNotLeakSQLServerPassword is the SECURITY regression: a write-only
// secret (administratorLoginPassword) sent on a Microsoft.Sql/servers PUT must
// never be reflected on the create response or a later GET — real Azure omits
// it. The non-secret modeled fields (administratorLogin, version) must still be
// returned, and a PATCH must not reintroduce the password.
func TestEchoDoesNotLeakSQLServerPassword(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Sql/servers/pwsrv1?api-version=2021-11-01"

	created := putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"administratorLogin":         "adminuser",
			"administratorLoginPassword": "S3cret-P@ssw0rd!",
			"version":                    "12.0",
		},
	})

	if _, leaked := props(t, created)["administratorLoginPassword"]; leaked {
		t.Fatal("create response leaked write-only administratorLoginPassword")
	}

	if props(t, created)["administratorLogin"] != "adminuser" {
		t.Errorf("create response dropped modeled administratorLogin: %v", props(t, created)["administratorLogin"])
	}

	got := props(t, getJSON(t, c, url))
	if _, leaked := got["administratorLoginPassword"]; leaked {
		t.Fatal("GET leaked write-only administratorLoginPassword")
	}

	if got["administratorLogin"] != "adminuser" {
		t.Errorf("GET dropped modeled administratorLogin: %v", got["administratorLogin"])
	}

	// A PATCH that resends the password must still not reintroduce it on read.
	patch, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, url,
		bytesReader(t, map[string]any{"properties": map[string]any{"administratorLoginPassword": "Another-P@ss1!"}}))
	if err != nil {
		t.Fatal(err)
	}

	patch.Header.Set("Content-Type", "application/json")
	doJSON(t, c, patch)

	if _, leaked := props(t, getJSON(t, c, url))["administratorLoginPassword"]; leaked {
		t.Fatal("GET after PATCH reintroduced write-only administratorLoginPassword")
	}
}

// TestEchoDoesNotLeakFlexServerPassword confirms the same secret suppression on
// the MySQL and PostgreSQL flexible-server handlers, whose armServerProps also
// omit administratorLoginPassword. A non-secret unmodeled field (version) still
// round-trips through the handler.
func TestEchoDoesNotLeakFlexServerPassword(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"mysql", "/providers/Microsoft.DBforMySQL/flexibleServers/pwflex1"},
		{"postgres", "/providers/Microsoft.DBforPostgreSQL/flexibleServers/pwflex1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, c := echoTestServer(t)
			url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1" + tc.path + "?api-version=2023-12-30"

			created := putJSON(t, c, url, map[string]any{
				"location": "eastus",
				"properties": map[string]any{
					"administratorLogin":         "adm",
					"administratorLoginPassword": "Fl3x-P@ssw0rd!",
					"version":                    "8.0.21",
				},
			})

			if _, leaked := props(t, created)["administratorLoginPassword"]; leaked {
				t.Fatal("create response leaked flexible-server administratorLoginPassword")
			}

			got := props(t, getJSON(t, c, url))
			if _, leaked := got["administratorLoginPassword"]; leaked {
				t.Fatal("GET leaked flexible-server administratorLoginPassword")
			}

			if got["administratorLogin"] != "adm" {
				t.Errorf("GET dropped modeled administratorLogin: %v", got["administratorLogin"])
			}
		})
	}
}

// TestEchoDoesNotLeakVMAdminPassword confirms the nested-secret case: a VM
// osProfile.adminPassword (write-only, dropped by the handler) must not be
// reflected, while the sibling non-secret unmodeled leaf under osProfile still
// round-trips through the overlay.
func TestEchoDoesNotLeakVMAdminPassword(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/pwvm1?api-version=2023-07-01"

	putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"hardwareProfile": map[string]any{"vmSize": "Standard_D2s_v5"},
			"osProfile": map[string]any{
				"computerName":  "host1",
				"adminUsername": "azureuser",
				"adminPassword": "VM-S3cret-P@ss!",
				"unmodeledLeaf": "keepme",
			},
		},
	})

	osp, ok := props(t, getJSON(t, c, url))["osProfile"].(map[string]any)
	if !ok {
		t.Fatalf("GET has no osProfile: %v", props(t, getJSON(t, c, url)))
	}

	if _, leaked := osp["adminPassword"]; leaked {
		t.Fatal("GET leaked nested write-only osProfile.adminPassword")
	}

	if osp["unmodeledLeaf"] != "keepme" {
		t.Errorf("overlay dropped a non-secret nested unmodeled leaf: %v", osp["unmodeledLeaf"])
	}
}

// TestEchoDoesNotLeakCassandraAdminPassword confirms the managed-Cassandra
// write-only secret (initialCassandraAdminPassword, dropped by toARMCluster) is
// not reflected, while a non-secret modeled field (cassandraVersion) still
// returns.
func TestEchoDoesNotLeakCassandraAdminPassword(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DocumentDB/cassandraClusters/pwcc1?api-version=2024-11-15"

	created := putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"cassandraVersion":              "4.0",
			"initialCassandraAdminPassword": "C@ssandra-S3cret!",
		},
	})

	if _, leaked := props(t, created)["initialCassandraAdminPassword"]; leaked {
		t.Fatal("create response leaked write-only initialCassandraAdminPassword")
	}

	got := props(t, getJSON(t, c, url))
	if _, leaked := got["initialCassandraAdminPassword"]; leaked {
		t.Fatal("GET leaked write-only initialCassandraAdminPassword")
	}

	if got["cassandraVersion"] != "4.0" {
		t.Errorf("GET dropped modeled cassandraVersion: %v", got["cassandraVersion"])
	}
}

// TestEchoDoesNotLeakCosmosPGRolePassword confirms a Cosmos DB for PostgreSQL
// role password (toARMRole drops it) is not reflected, while the modeled
// provisioningState still returns.
func TestEchoDoesNotLeakCosmosPGRolePassword(t *testing.T) {
	ts, c := echoTestServer(t)
	base := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.DBforPostgreSQL/serverGroupsv2/pgc1"

	putJSON(t, c, base+"?api-version=2023-03-02-preview", map[string]any{"location": "eastus"})

	roleURL := base + "/roles/role1?api-version=2023-03-02-preview"

	created := putJSON(t, c, roleURL, map[string]any{
		"properties": map[string]any{"password": "R0le-S3cret!"},
	})

	if _, leaked := props(t, created)["password"]; leaked {
		t.Fatal("create response leaked write-only role password")
	}

	if _, leaked := props(t, getJSON(t, c, roleURL))["password"]; leaked {
		t.Fatal("GET leaked write-only role password")
	}
}

// TestEchoDoesNotLeakAKSServicePrincipalSecret confirms the nested-object case:
// servicePrincipalProfile is unmodeled by the AKS handler, so it is captured
// wholesale — its secret must be stripped while the non-secret clientId leaf
// still round-trips.
func TestEchoDoesNotLeakAKSServicePrincipalSecret(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.ContainerService/managedClusters/aks1?api-version=2024-02-01"

	putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"kubernetesVersion": "1.28.0",
			"servicePrincipalProfile": map[string]any{
				"clientId": "abc-123",
				"secret":   "SP-S3cret!",
			},
		},
	})

	spp, ok := props(t, getJSON(t, c, url))["servicePrincipalProfile"].(map[string]any)
	if !ok {
		t.Fatalf("GET dropped the whole unmodeled servicePrincipalProfile: %v", props(t, getJSON(t, c, url)))
	}

	if _, leaked := spp["secret"]; leaked {
		t.Fatal("GET leaked write-only servicePrincipalProfile.secret")
	}

	if spp["clientId"] != "abc-123" {
		t.Errorf("overlay dropped non-secret servicePrincipalProfile.clientId: %v", spp["clientId"])
	}
}

// TestEchoDoesNotLeakContainerRegistryPassword is the ARRAY-recursion regression:
// imageRegistryCredentials is an unmodeled array of objects, each carrying a
// write-only password. The array must round-trip (server/username preserved)
// with every element's password stripped.
func TestEchoDoesNotLeakContainerRegistryPassword(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.ContainerInstance/containerGroups/cg1?api-version=2023-05-01"

	putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"osType": "Linux",
			"imageRegistryCredentials": []any{
				map[string]any{"server": "reg.azurecr.io", "username": "u1", "password": "Reg-S3cret!"},
			},
		},
	})

	creds, ok := props(t, getJSON(t, c, url))["imageRegistryCredentials"].([]any)
	if !ok || len(creds) != 1 {
		t.Fatalf("GET dropped the unmodeled imageRegistryCredentials array: %v", props(t, getJSON(t, c, url))["imageRegistryCredentials"])
	}

	cred, ok := creds[0].(map[string]any)
	if !ok {
		t.Fatalf("imageRegistryCredentials element is not an object: %v", creds[0])
	}

	if _, leaked := cred["password"]; leaked {
		t.Fatal("GET leaked write-only password inside imageRegistryCredentials array element")
	}

	if cred["server"] != "reg.azurecr.io" || cred["username"] != "u1" {
		t.Errorf("array recursion dropped non-secret siblings: %v", cred)
	}
}

// TestEchoDoesNotLeakNotificationHubCredentials confirms the Notification Hubs
// PNS credential objects (dropped by toHubJSON, served only via
// GetPnsCredentials) are not reflected on the generic hub GET, while the modeled
// registrationTtl still returns.
func TestEchoDoesNotLeakNotificationHubCredentials(t *testing.T) {
	ts, c := echoTestServer(t)
	nsURL := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.NotificationHubs/namespaces/ns1"

	putJSON(t, c, nsURL+"?api-version=2023-09-01", map[string]any{"location": "eastus"})

	hubURL := nsURL + "/notificationHubs/hub1?api-version=2023-09-01"

	putJSON(t, c, hubURL, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"registrationTtl": "P1D",
			"gcmCredential":   map[string]any{"properties": map[string]any{"googleApiKey": "GKEY"}},
			"apnsCredential":  map[string]any{"properties": map[string]any{"token": "APNS-TOKEN"}},
		},
	})

	got := props(t, getJSON(t, c, hubURL))
	for _, k := range []string{"gcmCredential", "apnsCredential"} {
		if _, leaked := got[k]; leaked {
			t.Fatalf("GET leaked write-only Notification Hubs %s", k)
		}
	}

	if got["registrationTtl"] != "P1D" {
		t.Errorf("overlay dropped modeled registrationTtl: %v", got["registrationTtl"])
	}
}

// TestEchoStripsSecretNestedDeepInArray confirms sanitizeUnmodeled removes a
// secret buried two levels inside an array-of-objects (array -> object ->
// object.password) while leaving the non-secret siblings intact — the deep
// array-recursion guarantee.
func TestEchoStripsSecretNestedDeepInArray(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/deepvm?api-version=2023-07-01"

	putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			// wholly unmodeled array of objects, each with a nested auth object
			// carrying a secret.
			"customConfigs": []any{
				map[string]any{
					"name": "a",
					"auth": map[string]any{"user": "x", "password": "Deep-S3cret!"},
				},
			},
		},
	})

	cfgs, ok := props(t, getJSON(t, c, url))["customConfigs"].([]any)
	if !ok || len(cfgs) != 1 {
		t.Fatalf("GET dropped the unmodeled customConfigs array: %v", props(t, getJSON(t, c, url))["customConfigs"])
	}

	entry, _ := cfgs[0].(map[string]any)
	auth, ok := entry["auth"].(map[string]any)
	if !ok {
		t.Fatalf("customConfigs entry lost its nested auth object: %v", entry)
	}

	if _, leaked := auth["password"]; leaked {
		t.Fatal("secret buried in an array-of-objects survived sanitizeUnmodeled")
	}

	if entry["name"] != "a" || auth["user"] != "x" {
		t.Errorf("deep array recursion dropped non-secret siblings: %v", entry)
	}
}

// TestEchoDoesNotLeakAKSAADServerAppSecret is the 3rd-batch regression: the AKS
// handler does not model aadProfile, so the whole block is captured verbatim.
// serverAppSecret (which lowercases to serverappsecret, not an exact denylist
// entry) must still be stripped by the ENDS-WITH-secret rule, while the public
// serverAppID sibling — not ending in the suffix — round-trips as real Azure does.
func TestEchoDoesNotLeakAKSAADServerAppSecret(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.ContainerService/managedClusters/aksaad?api-version=2024-02-01"

	putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"kubernetesVersion": "1.28.0",
			"aadProfile": map[string]any{
				"managed":         true,
				"serverAppID":     "public-app-id",
				"serverAppSecret": "AAD-S3cret!",
				"clientSecret":    "Client-S3cret!",
			},
		},
	})

	aad, ok := props(t, getJSON(t, c, url))["aadProfile"].(map[string]any)
	if !ok {
		t.Fatalf("GET dropped the whole unmodeled aadProfile: %v", props(t, getJSON(t, c, url)))
	}

	if _, leaked := aad["serverAppSecret"]; leaked {
		t.Fatal("GET leaked write-only aadProfile.serverAppSecret (suffix rule failed)")
	}

	if _, leaked := aad["clientSecret"]; leaked {
		t.Fatal("GET leaked write-only aadProfile.clientSecret (suffix rule failed)")
	}

	if aad["serverAppID"] != "public-app-id" {
		t.Errorf("overlay dropped non-secret aadProfile.serverAppID: %v", aad["serverAppID"])
	}
}

// TestEchoSuffixRuleIsEndsWithNotContains locks the suffix semantics: keys
// ENDING in password/secret are stripped, while keys that merely CONTAIN the
// word (secretName, passwordPolicy) — a true endsWith, not a substring match —
// survive. All live inside one wholly-unmodeled block so the overlay carries them.
func TestEchoSuffixRuleIsEndsWithNotContains(t *testing.T) {
	ts, c := echoTestServer(t)
	url := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/suffixvm?api-version=2023-07-01"

	putJSON(t, c, url, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"customCreds": map[string]any{
				"adminPassword":  "P@ss!",     // ends password -> stripped
				"clientSecret":   "S3cret!",   // ends secret   -> stripped
				"secretName":     "kv-entry",  // contains, not ends -> kept
				"passwordPolicy": "Strong",    // contains, not ends -> kept
				"secretUri":      "https://x", // contains, not ends -> kept
			},
		},
	})

	cc, ok := props(t, getJSON(t, c, url))["customCreds"].(map[string]any)
	if !ok {
		t.Fatalf("GET dropped the unmodeled customCreds block: %v", props(t, getJSON(t, c, url)))
	}

	for _, k := range []string{"adminPassword", "clientSecret"} {
		if _, leaked := cc[k]; leaked {
			t.Fatalf("suffix rule failed to strip %s", k)
		}
	}

	for _, k := range []string{"secretName", "passwordPolicy", "secretUri"} {
		if _, ok := cc[k]; !ok {
			t.Fatalf("suffix rule over-suppressed non-secret %s (endsWith, not contains)", k)
		}
	}
}

// TestEchoStillReflectsNonSecretProperty is the REGRESSION guard: the secret
// denylist must not disturb the overlay's legitimate job — a non-secret
// unmodeled property (a SQL database's maxSizeBytes, which only round-trips via
// the overlay) must still be echoed on create and GET.
func TestEchoStillReflectsNonSecretProperty(t *testing.T) {
	ts, c := echoTestServer(t)
	base := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Sql/servers/regsrv1"

	putJSON(t, c, base+"?api-version=2021-11-01", map[string]any{"location": "eastus"})

	dbURL := base + "/databases/regdb1?api-version=2021-11-01"

	created := putJSON(t, c, dbURL, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			"maxSizeBytes": float64(268435456000),
		},
	})

	if props(t, created)["maxSizeBytes"] != float64(268435456000) {
		t.Fatalf("create response dropped non-secret maxSizeBytes: %v", props(t, created)["maxSizeBytes"])
	}

	if props(t, getJSON(t, c, dbURL))["maxSizeBytes"] != float64(268435456000) {
		t.Fatalf("GET dropped non-secret maxSizeBytes: %v", props(t, getJSON(t, c, dbURL))["maxSizeBytes"])
	}
}

func bytesReader(t *testing.T, v map[string]any) *bytes.Reader {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return bytes.NewReader(raw)
}

// deleteOK sends a DELETE and requires a 2xx response; the overlay eviction
// hook only fires on success, matching echoUnmodeledProperties.
func deleteOK(t *testing.T, c *http.Client, url string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE %s: status %d: %s", url, resp.StatusCode, data)
	}
}

// TestEchoEvictsSubResourceOverlayOnDelete is the MEDIUM regression: deleting
// a named sub-resource (a SQL database under its server) must evict its
// overlay entry the same way deleting a top-level resource does. Before the
// fix, resourceIDFromPath returned "" for any path with a SubResource, so
// DELETE .../servers/{s}/databases/{d} never called overlay.evict — a
// same-named database recreated afterward resurrected the previous
// incarnation's unmodeled properties.
func TestEchoEvictsSubResourceOverlayOnDelete(t *testing.T) {
	ts, c := echoTestServer(t)
	base := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Sql/servers/srv1"

	putJSON(t, c, base+"?api-version=2021-11-01", map[string]any{"location": "eastus"})

	dbURL := base + "/databases/db1?api-version=2021-11-01"

	created := putJSON(t, c, dbURL, map[string]any{
		"location": "eastus",
		"properties": map[string]any{
			// readScale is not modeled by the SQL database handler; the
			// overlay is what makes it round-trip at all.
			"readScale": "Enabled",
		},
	})

	if props(t, created)["readScale"] != "Enabled" {
		t.Fatal("unmodeled readScale was not preserved after create")
	}

	if props(t, getJSON(t, c, dbURL))["readScale"] != "Enabled" {
		t.Fatal("unmodeled readScale not echoed on GET before delete")
	}

	deleteOK(t, c, dbURL)

	// Re-create the same database name with no unmodeled properties at all.
	recreated := putJSON(t, c, dbURL, map[string]any{"location": "eastus"})

	if v, ok := props(t, recreated)["readScale"]; ok {
		t.Fatalf("recreated database resurrected stale readScale from the deleted one: %v", v)
	}

	if v, ok := props(t, getJSON(t, c, dbURL))["readScale"]; ok {
		t.Fatalf("GET on recreated database still carries the deleted database's readScale: %v", v)
	}
}
