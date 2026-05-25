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

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

const (
	testSubscription = "11111111-1111-1111-1111-111111111111"
	testScope        = "/subscriptions/" + testSubscription
)

func newSDKClients(t *testing.T) (
	*armauthorization.RoleDefinitionsClient,
	*armauthorization.RoleAssignmentsClient,
) {
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

	var count int
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListPager.NextPage: %v", err)
		}
		count += len(page.Value)
	}

	if count != 1 {
		t.Fatalf("list returned %d role definitions, want 1", count)
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

func getStringPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
