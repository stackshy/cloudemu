package iam_test

import (
	"context"
	"encoding/json"
	"net/http"
	neturl "net/url"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

// TestSDKAzureIAMRoleDefinitionFilterByName is what azurerm does to resolve
// role_definition_name: list roleDefinitions at the assignment scope with
// $filter=roleName eq '<name>' and require exactly one result. The id must be
// rooted at the queried scope and carry the well-known built-in GUID.
func TestSDKAzureIAMRoleDefinitionFilterByName(t *testing.T) {
	cf, ts := newClientFactory(t)
	roleDefs := cf.NewRoleDefinitionsClient()
	ctx := context.Background()

	ensureRG(t, ts, testSubscription, "rg1")

	rgScope := testScope + "/resourceGroups/rg1"
	resScope := rgScope + "/providers/Microsoft.Storage/storageAccounts/acct1"

	cases := []struct {
		scope, filter, guid string
	}{
		{rgScope, "roleName eq 'Reader'", builtInReaderGUID},
		{rgScope, "roleName eq 'reader'", builtInReaderGUID},
		{resScope, "roleName eq 'Storage Blob Data Contributor'", "ba92f5b4-2d11-453d-a403-e96b0029c9fe"},
		{resScope, "roleName eq 'AcrPull'", "7f951dda-4ed3-4680-a7ca-43fe172d538d"},
		{testScope, "roleName eq 'Key Vault Secrets User' and type eq 'BuiltInRole'", "4633458b-17de-408a-b874-0445c86b69e6"},
	}

	for _, c := range cases {
		t.Run(c.filter, func(t *testing.T) {
			var got []*armauthorization.RoleDefinition

			pager := roleDefs.NewListPager(c.scope, &armauthorization.RoleDefinitionsClientListOptions{Filter: to.Ptr(c.filter)})
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					t.Fatalf("NextPage: %v", err)
				}

				got = append(got, page.Value...)
			}

			if len(got) != 1 {
				t.Fatalf("got %d role definitions, want exactly 1", len(got))
			}

			wantID := c.scope + "/providers/Microsoft.Authorization/roleDefinitions/" + c.guid
			if id := getStringPtr(got[0].ID); id != wantID {
				t.Fatalf("id = %q, want %q", id, wantID)
			}
		})
	}
}

// TestAzureIAMRoleDefinitionFilterAtTenantRoot covers the scope-less list
// azurerm's role definition data source can issue: the id has no "//".
func TestAzureIAMRoleDefinitionFilterAtTenantRoot(t *testing.T) {
	_, ts := newClientFactory(t)

	url := ts.URL + "/providers/Microsoft.Authorization/roleDefinitions?api-version=2022-04-01&$filter=" +
		neturl.QueryEscape("roleName eq 'Owner'")

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	var out struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	want := "/providers/Microsoft.Authorization/roleDefinitions/" + builtInOwnerGUID
	if len(out.Value) != 1 || out.Value[0].ID != want {
		t.Fatalf("got %+v, want one role with id %s", out.Value, want)
	}
}

// TestSDKAzureIAMRoleDefinitionFilterNoMatch confirms an unknown name or a
// type filter that excludes built-ins returns an empty list, not every role.
func TestSDKAzureIAMRoleDefinitionFilterNoMatch(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	for _, filter := range []string{"roleName eq 'No Such Role'", "roleName eq 'Reader' and type eq 'CustomRole'"} {
		pager := roleDefs.NewListPager(testScope, &armauthorization.RoleDefinitionsClientListOptions{Filter: to.Ptr(filter)})
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("NextPage: %v", err)
			}

			if len(page.Value) != 0 {
				t.Fatalf("%s: got %d role definitions, want 0", filter, len(page.Value))
			}
		}
	}
}
