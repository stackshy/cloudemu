package backupdr_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	gapic "cloud.google.com/go/backupdr/apiv1"
	"cloud.google.com/go/backupdr/apiv1/backupdrpb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func assertVaultEnums(t *testing.T, step string, v *backupdrpb.BackupVault) {
	t.Helper()

	if v.GetAccessRestriction() != backupdrpb.BackupVault_WITHIN_ORGANIZATION {
		t.Errorf("%s: accessRestriction=%v want WITHIN_ORGANIZATION", step, v.GetAccessRestriction())
	}

	if v.GetBackupRetentionInheritance() != backupdrpb.BackupVault_INHERIT_VAULT_RETENTION {
		t.Errorf("%s: backupRetentionInheritance=%v want INHERIT_VAULT_RETENTION", step, v.GetBackupRetentionInheritance())
	}

	if v.GetState() != backupdrpb.BackupVault_ACTIVE {
		t.Errorf("%s: state=%v want ACTIVE", step, v.GetState())
	}
}

// TestGAPICBackupVaultNumericEnumsLifecycle is GBDR-01: the apiv1 REST client
// sends accessRestriction and backupRetentionInheritance as numbers. Create
// used to fail with "cannot unmarshal number into Go struct field
// vaultInput.accessRestriction of type string". The update resends the
// fetched vault, so the output-only state goes back as a number as well.
func TestGAPICBackupVaultNumericEnumsLifecycle(t *testing.T) {
	cloud := cloudemu.NewGCP(config.WithClock(config.NewFakeClock(fixedNow)))
	c := newGAPIC(t, gcpserver.NewFromProvider(cloud))
	ctx := context.Background()
	parent := "projects/" + sdkProject + "/locations/" + sdkLocation

	op, err := c.CreateBackupVault(ctx, &backupdrpb.CreateBackupVaultRequest{
		Parent:        parent,
		BackupVaultId: "enum-vault",
		BackupVault: &backupdrpb.BackupVault{
			Description:                            proto.String("v1"),
			AccessRestriction:                      backupdrpb.BackupVault_WITHIN_ORGANIZATION,
			BackupRetentionInheritance:             backupdrpb.BackupVault_INHERIT_VAULT_RETENTION.Enum(),
			BackupMinimumEnforcedRetentionDuration: durationpb.New(86400e9),
		},
	})
	if err != nil {
		t.Fatalf("CreateBackupVault: %v", err)
	}

	created, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateBackupVault Wait: %v", err)
	}

	assertVaultEnums(t, "create", created)

	name := created.GetName()

	got, err := c.GetBackupVault(ctx, &backupdrpb.GetBackupVaultRequest{Name: name})
	if err != nil {
		t.Fatalf("GetBackupVault: %v", err)
	}

	assertVaultEnums(t, "get", got)

	assertVaultListed(t, c, parent, name)

	got.Description = proto.String("v2")

	uop, err := c.UpdateBackupVault(ctx, &backupdrpb.UpdateBackupVaultRequest{
		BackupVault: got,
		UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"description"}},
	})
	if err != nil {
		t.Fatalf("UpdateBackupVault: %v", err)
	}

	updated, err := uop.Wait(ctx)
	if err != nil {
		t.Fatalf("UpdateBackupVault Wait: %v", err)
	}

	if updated.GetDescription() != "v2" {
		t.Errorf("update: description=%q want v2", updated.GetDescription())
	}

	assertVaultEnums(t, "update", updated)

	dop, err := c.DeleteBackupVault(ctx, &backupdrpb.DeleteBackupVaultRequest{Name: name})
	if err != nil {
		t.Fatalf("DeleteBackupVault: %v", err)
	}

	if err := dop.Wait(ctx); err != nil {
		t.Fatalf("DeleteBackupVault Wait: %v", err)
	}

	_, err = c.GetBackupVault(ctx, &backupdrpb.GetBackupVaultRequest{Name: name})

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusNotFound {
		t.Fatalf("GetBackupVault after delete: err=%v want 404", err)
	}
}

func assertVaultListed(t *testing.T, c *gapic.Client, parent, name string) {
	t.Helper()

	it := c.ListBackupVaults(context.Background(), &backupdrpb.ListBackupVaultsRequest{Parent: parent})

	for {
		v, err := it.Next()
		if errors.Is(err, iterator.Done) {
			t.Fatalf("ListBackupVaults: %s not listed", name)
		}

		if err != nil {
			t.Fatalf("ListBackupVaults: %v", err)
		}

		if v.GetName() == name {
			assertVaultEnums(t, "list", v)
			return
		}
	}
}
