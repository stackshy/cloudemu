package azure

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzureIAMCompat drives an Azure role-definition lifecycle through the real
// azure-sdk-for-go armauthorization client. Azure IAM is Microsoft.Authorization
// role definitions/assignments; the emulator's IAM wire handler maps each ARM
// role definition onto the portable "iam" driver Role, so operation names match
// the Role CRUD ops in docs/coverage/coverage.json. ARM is a bearer-token API,
// so the client runs over the harness's TLS server with a fake credential.
func TestAzureIAMCompat(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{IAM: provider.IAM})

	const subscriptionID = "11111111-1111-1111-1111-111111111111"

	scope := "/subscriptions/" + subscriptionID

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: sess.Endpoint(),
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armauthorization.NewClientFactory(subscriptionID, compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("armauthorization client factory: %v", err)
	}

	roleDefs := cf.NewRoleDefinitionsClient()

	ctx := context.Background()

	const (
		svc      = "iam"
		roleID   = "aaaaaaaa-1111-1111-1111-111111111111"
		roleName = "Custom Reader"
	)

	sess.Op(svc, "CreateRole", func() error {
		created, err := roleDefs.CreateOrUpdate(ctx, scope, roleID, armauthorization.RoleDefinition{
			Properties: &armauthorization.RoleDefinitionProperties{
				RoleName:    to.Ptr(roleName),
				Description: to.Ptr("Read-only access to a small set of resources"),
				RoleType:    to.Ptr("CustomRole"),
				Permissions: []*armauthorization.Permission{
					{
						Actions:    []*string{to.Ptr("Microsoft.Compute/virtualMachines/read")},
						NotActions: []*string{},
					},
				},
				AssignableScopes: []*string{to.Ptr(scope)},
			},
		}, nil)
		if err != nil {
			return err
		}

		if created.Properties == nil || ptrStr(created.Properties.RoleName) != roleName {
			return fmt.Errorf("CreateRole RoleName = %v, want %q", created.Properties, roleName)
		}

		return nil
	})

	sess.Op(svc, "GetRole", func() error {
		got, err := roleDefs.Get(ctx, scope, roleID, nil)
		if err != nil {
			return err
		}

		if got.Properties == nil || ptrStr(got.Properties.RoleName) != roleName {
			return fmt.Errorf("GetRole RoleName = %v, want %q", got.Properties, roleName)
		}

		return nil
	})

	sess.Op(svc, "ListRoles", func() error {
		var count int

		pager := roleDefs.NewListPager(scope, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return err
			}

			count += len(page.Value)
		}

		if count != 1 {
			return fmt.Errorf("ListRoles returned %d definitions, want 1", count)
		}

		return nil
	})

	sess.Op(svc, "DeleteRole", func() error {
		if _, err := roleDefs.Delete(ctx, scope, roleID, nil); err != nil {
			return err
		}

		if _, err := roleDefs.Get(ctx, scope, roleID, nil); err == nil {
			return fmt.Errorf("GetRole after DeleteRole: expected error, got nil")
		}

		return nil
	})
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}
