package iam_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

const (
	testSubscription = "11111111-1111-1111-1111-111111111111"
	testScope        = "/subscriptions/" + testSubscription
)

// newClientFactory spins up an in-process Azure wire server backed by a fresh
// cloudemu Azure provider and returns an armauthorization client factory
// pointed at it. Individual tests pull the specific client(s) they need.
func newClientFactory(t *testing.T) *armauthorization.ClientFactory {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{IAM: cloudP.IAM})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armauthorization.NewClientFactory(testSubscription, fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	return cf
}

func newSDKClients(t *testing.T) (
	*armauthorization.RoleDefinitionsClient,
	*armauthorization.RoleAssignmentsClient,
) {
	t.Helper()

	cf := newClientFactory(t)

	return cf.NewRoleDefinitionsClient(), cf.NewRoleAssignmentsClient()
}

func TestSDKAzureIAMRoleDefinitionLifecycle(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	const roleID = "aaaaaaaa-1111-1111-1111-111111111111"

	created, err := roleDefs.CreateOrUpdate(ctx, testScope, roleID, armauthorization.RoleDefinition{
		Properties: &armauthorization.RoleDefinitionProperties{
			RoleName:    to.Ptr("Custom Reader"),
			Description: to.Ptr("Read-only access to a small set of resources"),
			RoleType:    to.Ptr("CustomRole"),
			Permissions: []*armauthorization.Permission{
				{
					Actions:    []*string{to.Ptr("Microsoft.Compute/virtualMachines/read")},
					NotActions: []*string{},
				},
			},
			AssignableScopes: []*string{to.Ptr(testScope)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate role definition: %v", err)
	}

	if got := getStringPtr(created.Properties.RoleName); got != "Custom Reader" {
		t.Fatalf("got RoleName %q, want Custom Reader", got)
	}

	got, err := roleDefs.Get(ctx, testScope, roleID, nil)
	if err != nil {
		t.Fatalf("Get role definition: %v", err)
	}

	if got.Properties == nil || getStringPtr(got.Properties.RoleName) != "Custom Reader" {
		t.Fatalf("Get returned wrong role: %+v", got.Properties)
	}

	pager := roleDefs.NewListPager(testScope, nil)

	var (
		foundCustom  bool
		foundBuiltIn bool
		count        int
	)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListPager.NextPage: %v", err)
		}
		for _, rd := range page.Value {
			count++
			switch getStringPtr(rd.Name) {
			case roleID:
				foundCustom = true
			case "acdd72a7-3385-48ef-bd42-f606fba81ae7": // built-in Reader
				foundBuiltIn = true
			}
		}
	}

	// The custom role plus the three seeded built-ins (Owner/Contributor/Reader)
	// are all listable at the subscription scope.
	if !foundCustom {
		t.Fatalf("list did not return the created custom role definition")
	}
	if !foundBuiltIn {
		t.Fatalf("list did not return the built-in Reader role definition")
	}
	if count < 4 {
		t.Fatalf("list returned %d role definitions, want >= 4 (custom + 3 built-ins)", count)
	}

	if _, err := roleDefs.Delete(ctx, testScope, roleID, nil); err != nil {
		t.Fatalf("Delete role definition: %v", err)
	}

	if _, err := roleDefs.Get(ctx, testScope, roleID, nil); err == nil {
		t.Fatalf("Get after Delete: expected error, got nil")
	}
}

func TestSDKAzureIAMRoleAssignmentLifecycle(t *testing.T) {
	roleDefs, roleAssigns := newSDKClients(t)
	ctx := context.Background()

	const (
		roleDefID    = "bbbbbbbb-2222-2222-2222-222222222222"
		assignmentID = "cccccccc-3333-3333-3333-333333333333"
		principalID  = "dddddddd-4444-4444-4444-444444444444"
	)

	if _, err := roleDefs.CreateOrUpdate(ctx, testScope, roleDefID, armauthorization.RoleDefinition{
		Properties: &armauthorization.RoleDefinitionProperties{
			RoleName: to.Ptr("Reader"),
			RoleType: to.Ptr("CustomRole"),
		},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate role definition (for assignment): %v", err)
	}

	roleDefArmID := testScope + "/providers/Microsoft.Authorization/roleDefinitions/" + roleDefID

	created, err := roleAssigns.Create(ctx, testScope, assignmentID, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: to.Ptr(roleDefArmID),
			PrincipalID:      to.Ptr(principalID),
			PrincipalType:    to.Ptr(armauthorization.PrincipalTypeUser),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Create role assignment: %v", err)
	}

	if got := getStringPtr(created.Properties.PrincipalID); got != principalID {
		t.Fatalf("got principalId %q, want %s", got, principalID)
	}

	got, err := roleAssigns.Get(ctx, testScope, assignmentID, nil)
	if err != nil {
		t.Fatalf("Get role assignment: %v", err)
	}

	if got.Properties == nil || getStringPtr(got.Properties.PrincipalID) != principalID {
		t.Fatalf("Get returned wrong principal: %+v", got.Properties)
	}

	pager := roleAssigns.NewListForScopePager(testScope, nil)

	var count int
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NewListForScopePager.NextPage: %v", err)
		}
		count += len(page.Value)
	}

	if count != 1 {
		t.Fatalf("list returned %d role assignments, want 1", count)
	}

	if _, err := roleAssigns.Delete(ctx, testScope, assignmentID, nil); err != nil {
		t.Fatalf("Delete role assignment: %v", err)
	}

	if _, err := roleAssigns.Get(ctx, testScope, assignmentID, nil); err == nil {
		t.Fatalf("Get after Delete: expected error, got nil")
	}
}

// TestSDKAzureIAMRoleDefinitionListScopeAndAbove verifies real Azure's
// documented RoleDefinitions List semantics (MS Learn:
// rest/api/authorization/role-definitions/list — "Get all role definitions
// that are applicable at scope and above"): a list at a scope returns role
// definitions scoped to that scope and its ancestors, never one scoped only
// to a descendant resource beneath it.
func TestSDKAzureIAMRoleDefinitionListScopeAndAbove(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	const (
		subRoleID = "11111111-aaaa-aaaa-aaaa-111111111111"
		rgRoleID  = "22222222-bbbb-bbbb-bbbb-222222222222"
		vmRoleID  = "33333333-cccc-cccc-cccc-333333333333"
	)

	rgScope := testScope + "/resourceGroups/rg1"
	vmScope := rgScope + "/providers/Microsoft.Compute/virtualMachines/vm1"

	mustCreateRole(t, roleDefs, ctx, testScope, subRoleID, "Sub Role")
	mustCreateRole(t, roleDefs, ctx, rgScope, rgRoleID, "RG Role")
	mustCreateRole(t, roleDefs, ctx, vmScope, vmRoleID, "VM Role")

	// Listing at the subscription scope must return only the subscription-
	// scoped role — the RG- and VM-scoped roles are descendants, not
	// ancestors, of the subscription.
	names := listRoleNames(t, roleDefs, testScope)
	if !names[subRoleID] {
		t.Fatalf("list at subscription scope missing sub-scoped role: %v", names)
	}
	if names[rgRoleID] || names[vmRoleID] {
		t.Fatalf("list at subscription scope leaked a descendant-scoped role: %v", names)
	}

	// Listing at the resource-group scope must return the RG-scoped role and
	// its subscription ancestor, but not the VM-scoped descendant.
	names = listRoleNames(t, roleDefs, rgScope)
	if !names[subRoleID] || !names[rgRoleID] {
		t.Fatalf("list at RG scope missing scope-and-above roles: %v", names)
	}
	if names[vmRoleID] {
		t.Fatalf("list at RG scope leaked the VM-scoped descendant role: %v", names)
	}

	// Listing at the VM scope must return all three: itself and both ancestors.
	names = listRoleNames(t, roleDefs, vmScope)
	if !names[subRoleID] || !names[rgRoleID] || !names[vmRoleID] {
		t.Fatalf("list at VM scope missing scope-and-above roles: %v", names)
	}
}

func mustCreateRole(
	t *testing.T, roleDefs *armauthorization.RoleDefinitionsClient, ctx context.Context, scope, id, name string,
) {
	t.Helper()

	if _, err := roleDefs.CreateOrUpdate(ctx, scope, id, armauthorization.RoleDefinition{
		Properties: &armauthorization.RoleDefinitionProperties{
			RoleName:         to.Ptr(name),
			RoleType:         to.Ptr("CustomRole"),
			AssignableScopes: []*string{to.Ptr(scope)},
		},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate role definition %s at scope %s: %v", id, scope, err)
	}
}

func listRoleNames(t *testing.T, roleDefs *armauthorization.RoleDefinitionsClient, scope string) map[string]bool {
	t.Helper()

	ctx := context.Background()
	pager := roleDefs.NewListPager(scope, nil)
	out := make(map[string]bool)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListPager.NextPage at scope %s: %v", scope, err)
		}

		for _, rd := range page.Value {
			out[getStringPtr(rd.Name)] = true
		}
	}

	return out
}

func getStringPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
