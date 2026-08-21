package gcp

import (
	"context"
	"fmt"
	"testing"

	iamv1 "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGCPIAMCompat drives the real google.golang.org/api/iam/v1 client against
// the in-process wire server. GCP service accounts back the portable IAM
// driver's Users and custom roles back its Roles, so operation names match the
// portable driver in docs/coverage/coverage.json.
func TestGCPIAMCompat(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{IAM: provider.IAM})
	ctx := context.Background()

	svcClient, err := iamv1.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("iam service: %v", err)
	}

	const svc = "iam"

	parent := "projects/" + compat.GCPProject

	// --- Service accounts back portable Users: create -> get -> list -> delete.
	const accountID = "ci-deployer"

	saEmail := accountID + "@" + compat.GCPProject + ".iam.gserviceaccount.com"
	saName := "projects/-/serviceAccounts/" + saEmail

	sess.Op(svc, "CreateUser", func() error {
		created, err := svcClient.Projects.ServiceAccounts.Create(parent, &iamv1.CreateServiceAccountRequest{
			AccountId:      accountID,
			ServiceAccount: &iamv1.ServiceAccount{DisplayName: "CI Deployer"},
		}).Context(ctx).Do()
		if err != nil {
			return err
		}

		if created.Email != saEmail {
			return fmt.Errorf("CreateUser email = %q, want %q", created.Email, saEmail)
		}

		return nil
	})

	sess.Op(svc, "GetUser", func() error {
		got, err := svcClient.Projects.ServiceAccounts.Get(saName).Context(ctx).Do()
		if err != nil {
			return err
		}

		if got.Email != saEmail {
			return fmt.Errorf("GetUser email = %q, want %q", got.Email, saEmail)
		}

		return nil
	})

	sess.Op(svc, "ListUsers", func() error {
		list, err := svcClient.Projects.ServiceAccounts.List(parent).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(list.Accounts) != 1 {
			return fmt.Errorf("ListUsers = %d accounts, want 1", len(list.Accounts))
		}

		return nil
	})

	// --- Access keys back portable AccessKeys: create -> list -> delete.
	var keyName string

	sess.Op(svc, "CreateAccessKey", func() error {
		key, err := svcClient.Projects.ServiceAccounts.Keys.Create(saName,
			&iamv1.CreateServiceAccountKeyRequest{}).Context(ctx).Do()
		if err != nil {
			return err
		}

		if key.Name == "" {
			return fmt.Errorf("CreateAccessKey returned empty key name")
		}

		keyName = key.Name

		return nil
	})

	sess.Op(svc, "ListAccessKeys", func() error {
		listed, err := svcClient.Projects.ServiceAccounts.Keys.List(saName).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(listed.Keys) != 1 {
			return fmt.Errorf("ListAccessKeys = %d keys, want 1", len(listed.Keys))
		}

		return nil
	})

	sess.Op(svc, "DeleteAccessKey", func() error {
		_, err := svcClient.Projects.ServiceAccounts.Keys.Delete(keyName).Context(ctx).Do()

		return err
	})

	sess.Op(svc, "DeleteUser", func() error {
		_, err := svcClient.Projects.ServiceAccounts.Delete(saName).Context(ctx).Do()

		return err
	})

	// --- Custom roles back portable Roles: create -> get -> list -> delete.
	const roleID = "customViewer"

	roleName := parent + "/roles/" + roleID

	sess.Op(svc, "CreateRole", func() error {
		created, err := svcClient.Projects.Roles.Create(parent, &iamv1.CreateRoleRequest{
			RoleId: roleID,
			Role: &iamv1.Role{
				Title:               "Custom Viewer",
				IncludedPermissions: []string{"compute.instances.list", "compute.instances.get"},
				Stage:               "GA",
			},
		}).Context(ctx).Do()
		if err != nil {
			return err
		}

		if created.Name != roleName {
			return fmt.Errorf("CreateRole name = %q, want %q", created.Name, roleName)
		}

		return nil
	})

	sess.Op(svc, "GetRole", func() error {
		got, err := svcClient.Projects.Roles.Get(roleName).Context(ctx).Do()
		if err != nil {
			return err
		}

		if got.Title != "Custom Viewer" {
			return fmt.Errorf("GetRole title = %q, want Custom Viewer", got.Title)
		}

		return nil
	})

	sess.Op(svc, "ListRoles", func() error {
		list, err := svcClient.Projects.Roles.List(parent).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(list.Roles) != 1 {
			return fmt.Errorf("ListRoles = %d roles, want 1", len(list.Roles))
		}

		return nil
	})

	sess.Op(svc, "DeleteRole", func() error {
		_, err := svcClient.Projects.Roles.Delete(roleName).Context(ctx).Do()

		return err
	})
}
