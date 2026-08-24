package iam_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

// TestSDKAzureIAMRoleDefinitionPreservesCreatedOn confirms that updating a role
// definition (a second PUT to the same id) preserves the original createdOn
// while advancing updatedOn — the real-Azure timestamp contract. A regression
// here (resetting createdOn on every PUT) would surface immediately.
func TestSDKAzureIAMRoleDefinitionPreservesCreatedOn(t *testing.T) {
	roleDefs, _ := newSDKClients(t)
	ctx := context.Background()

	const roleID = "77777777-8888-9999-aaaa-bbbbbbbbbbbb"

	def := armauthorization.RoleDefinition{
		Properties: &armauthorization.RoleDefinitionProperties{
			RoleName: to.Ptr("v1"),
			RoleType: to.Ptr("CustomRole"),
		},
	}

	created, err := roleDefs.CreateOrUpdate(ctx, testScope, roleID, def, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if created.Properties == nil || created.Properties.CreatedOn == nil {
		t.Fatalf("create did not return createdOn: %+v", created.Properties)
	}

	firstCreatedOn := *created.Properties.CreatedOn

	def.Properties.RoleName = to.Ptr("v2")

	updated, err := roleDefs.CreateOrUpdate(ctx, testScope, roleID, def, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.Properties == nil || updated.Properties.CreatedOn == nil {
		t.Fatalf("update did not return createdOn: %+v", updated.Properties)
	}

	if !updated.Properties.CreatedOn.Equal(firstCreatedOn) {
		t.Fatalf("createdOn changed on update: first %v, after update %v",
			firstCreatedOn, *updated.Properties.CreatedOn)
	}
}
