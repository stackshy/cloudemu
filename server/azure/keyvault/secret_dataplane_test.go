package keyvault_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

func TestSDKKeyVaultContentTypeRoundTrip(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	set, err := client.SetSecret(ctx, "typed", azsecrets.SetSecretParameters{
		Value:       to.Ptr("v"),
		ContentType: to.Ptr("text/plain"),
	}, nil)
	if err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	if set.ContentType == nil || *set.ContentType != "text/plain" {
		t.Fatalf("set contentType = %v, want text/plain", set.ContentType)
	}

	got, err := client.GetSecret(ctx, "typed", "", nil)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}

	if got.ContentType == nil || *got.ContentType != "text/plain" {
		t.Fatalf("get contentType = %v, want text/plain", got.ContentType)
	}
}

func TestSDKKeyVaultEnabledRoundTrip(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	_, err := client.SetSecret(ctx, "disabled", azsecrets.SetSecretParameters{
		Value:            to.Ptr("v"),
		SecretAttributes: &azsecrets.SecretAttributes{Enabled: to.Ptr(false)},
	}, nil)
	if err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	// A disabled secret is listed with enabled=false but 403s on read.
	_, err = client.GetSecret(ctx, "disabled", "", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 403 {
		t.Fatalf("GetSecret(disabled): got %v, want 403", err)
	}

	if respErr.ErrorCode != "Forbidden" {
		t.Fatalf("error code = %q, want Forbidden", respErr.ErrorCode)
	}

	pager := client.NewListSecretPropertiesPager(nil)

	var sawDisabled bool

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		for _, it := range page.Value {
			if it.ID.Name() == "disabled" {
				sawDisabled = true

				if it.Attributes == nil || it.Attributes.Enabled == nil || *it.Attributes.Enabled {
					t.Fatalf("listed disabled secret enabled = %v, want false", it.Attributes)
				}
			}
		}
	}

	if !sawDisabled {
		t.Fatal("disabled secret missing from list")
	}
}

func TestSDKKeyVaultExpiryRoundTrip(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	nbf := time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)

	_, err := client.SetSecret(ctx, "timed", azsecrets.SetSecretParameters{
		Value: to.Ptr("v"),
		SecretAttributes: &azsecrets.SecretAttributes{
			Expires:   to.Ptr(exp),
			NotBefore: to.Ptr(nbf),
		},
	}, nil)
	if err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	got, err := client.GetSecret(ctx, "timed", "", nil)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}

	if got.Attributes.Expires == nil || !got.Attributes.Expires.Equal(exp) {
		t.Fatalf("exp = %v, want %v", got.Attributes.Expires, exp)
	}

	if got.Attributes.NotBefore == nil || !got.Attributes.NotBefore.Equal(nbf) {
		t.Fatalf("nbf = %v, want %v", got.Attributes.NotBefore, nbf)
	}
}

// TestSDKKeyVaultExpiredSecretForbidsGet proves an expired secret's current
// version 403s on get rather than returning the (unusable) value — matching
// real Key Vault, which never falls back to an earlier version either.
func TestSDKKeyVaultExpiredSecretForbidsGet(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	past := time.Now().Add(-time.Hour)

	if _, err := client.SetSecret(ctx, "expired", azsecrets.SetSecretParameters{
		Value:            to.Ptr("v"),
		SecretAttributes: &azsecrets.SecretAttributes{Expires: to.Ptr(past)},
	}, nil); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	_, err := client.GetSecret(ctx, "expired", "", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 403 {
		t.Fatalf("GetSecret(expired): got %v, want 403", err)
	}

	if respErr.ErrorCode != "Forbidden" {
		t.Fatalf("error code = %q, want Forbidden", respErr.ErrorCode)
	}
}

// TestSDKKeyVaultNotYetValidSecretForbidsGet mirrors the expired case for a
// secret whose notBefore window has not opened yet.
func TestSDKKeyVaultNotYetValidSecretForbidsGet(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	future := time.Now().Add(time.Hour)

	if _, err := client.SetSecret(ctx, "premature", azsecrets.SetSecretParameters{
		Value:            to.Ptr("v"),
		SecretAttributes: &azsecrets.SecretAttributes{NotBefore: to.Ptr(future)},
	}, nil); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	_, err := client.GetSecret(ctx, "premature", "", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 403 {
		t.Fatalf("GetSecret(not yet valid): got %v, want 403", err)
	}

	if respErr.ErrorCode != "Forbidden" {
		t.Fatalf("error code = %q, want Forbidden", respErr.ErrorCode)
	}
}

func TestSDKKeyVaultUpdateSecretProperties(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	set, err := client.SetSecret(ctx, "upd", azsecrets.SetSecretParameters{Value: to.Ptr("v")}, nil)
	if err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	upd, err := client.UpdateSecretProperties(ctx, "upd", set.ID.Version(), azsecrets.UpdateSecretPropertiesParameters{
		ContentType:      to.Ptr("application/json"),
		Tags:             map[string]*string{"team": to.Ptr("core")},
		SecretAttributes: &azsecrets.SecretAttributes{Enabled: to.Ptr(false)},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateSecretProperties: %v", err)
	}

	if upd.ContentType == nil || *upd.ContentType != "application/json" {
		t.Fatalf("updated contentType = %v", upd.ContentType)
	}

	if upd.Tags["team"] == nil || *upd.Tags["team"] != "core" {
		t.Fatalf("updated tags = %v", upd.Tags)
	}

	if upd.Attributes.Enabled == nil || *upd.Attributes.Enabled {
		t.Fatalf("updated enabled = %v, want false", upd.Attributes.Enabled)
	}
}

func TestSDKKeyVaultSoftDeleteAndRecover(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.SetSecret(ctx, "sd", azsecrets.SetSecretParameters{Value: to.Ptr("v")}, nil); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	del, err := client.DeleteSecret(ctx, "sd", nil)
	if err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	if del.RecoveryID == nil || *del.RecoveryID == "" {
		t.Fatal("DeleteSecret: empty recoveryId")
	}

	if del.ScheduledPurgeDate == nil || del.DeletedDate == nil {
		t.Fatalf("DeleteSecret: missing purge/deleted dates: %+v", del)
	}

	dg, err := client.GetDeletedSecret(ctx, "sd", nil)
	if err != nil {
		t.Fatalf("GetDeletedSecret: %v", err)
	}

	if dg.ID == nil || dg.ID.Name() != "sd" {
		t.Fatalf("GetDeletedSecret id = %v", dg.ID)
	}

	var listedDeleted bool

	dpager := client.NewListDeletedSecretPropertiesPager(nil)
	for dpager.More() {
		page, err := dpager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListDeletedSecretProperties: %v", err)
		}

		for _, it := range page.Value {
			if it.ID.Name() == "sd" {
				listedDeleted = true
			}
		}
	}

	if !listedDeleted {
		t.Fatal("deleted secret missing from deleted list")
	}

	if _, err := client.RecoverDeletedSecret(ctx, "sd", nil); err != nil {
		t.Fatalf("RecoverDeletedSecret: %v", err)
	}

	if _, err := client.GetSecret(ctx, "sd", "", nil); err != nil {
		t.Fatalf("GetSecret after recover: %v", err)
	}
}

func TestSDKKeyVaultSetSecretDeletedNameConflict(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.SetSecret(ctx, "conflicted", azsecrets.SetSecretParameters{Value: to.Ptr("v")}, nil); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	if _, err := client.DeleteSecret(ctx, "conflicted", nil); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	// Setting a secret whose name is soft-deleted must fail with 409 Conflict
	// and the ObjectIsDeletedButRecoverable inner error code — matching real
	// Key Vault, which forbids reusing the name until recover or purge.
	_, err := client.SetSecret(ctx, "conflicted", azsecrets.SetSecretParameters{Value: to.Ptr("v2")}, nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 409 {
		t.Fatalf("SetSecret(deleted name): got %v, want 409 ResponseError", err)
	}

	if !strings.Contains(respErr.Error(), "ObjectIsDeletedButRecoverable") {
		t.Fatalf("SetSecret(deleted name): body %q missing ObjectIsDeletedButRecoverable", respErr.Error())
	}
}

func TestSDKKeyVaultPurge(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.SetSecret(ctx, "pg", azsecrets.SetSecretParameters{Value: to.Ptr("v")}, nil); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	if _, err := client.DeleteSecret(ctx, "pg", nil); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	if _, err := client.PurgeDeletedSecret(ctx, "pg", nil); err != nil {
		t.Fatalf("PurgeDeletedSecret: %v", err)
	}

	_, err := client.GetDeletedSecret(ctx, "pg", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("GetDeletedSecret after purge: got %v, want 404", err)
	}
}

func TestSDKKeyVaultBackupRestore(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.SetSecret(ctx, "bk", azsecrets.SetSecretParameters{
		Value:       to.Ptr("secret-value"),
		ContentType: to.Ptr("text/plain"),
	}, nil); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	backup, err := client.BackupSecret(ctx, "bk", nil)
	if err != nil {
		t.Fatalf("BackupSecret: %v", err)
	}

	if len(backup.Value) == 0 {
		t.Fatal("BackupSecret: empty blob")
	}

	// Purge the original so restore can recreate it.
	if _, err := client.DeleteSecret(ctx, "bk", nil); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	if _, err := client.PurgeDeletedSecret(ctx, "bk", nil); err != nil {
		t.Fatalf("PurgeDeletedSecret: %v", err)
	}

	restored, err := client.RestoreSecret(ctx, azsecrets.RestoreSecretParameters{SecretBackup: backup.Value}, nil)
	if err != nil {
		t.Fatalf("RestoreSecret: %v", err)
	}

	if restored.ID == nil || restored.ID.Name() != "bk" {
		t.Fatalf("RestoreSecret id = %v", restored.ID)
	}

	got, err := client.GetSecret(ctx, "bk", "", nil)
	if err != nil {
		t.Fatalf("GetSecret after restore: %v", err)
	}

	if got.Value == nil || *got.Value != "secret-value" {
		t.Fatalf("restored value = %v, want secret-value", got.Value)
	}
}

func TestSDKKeyVaultListVersionsMetadata(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.SetSecret(ctx, "mv", azsecrets.SetSecretParameters{
		Value:       to.Ptr("v1"),
		ContentType: to.Ptr("text/plain"),
		Tags:        map[string]*string{"k": to.Ptr("val")},
	}, nil); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	var sawTyped bool

	pager := client.NewListSecretPropertiesVersionsPager("mv", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListSecretPropertiesVersions: %v", err)
		}

		for _, it := range page.Value {
			if it.ContentType != nil && *it.ContentType == "text/plain" {
				sawTyped = true

				if it.Tags["k"] == nil || *it.Tags["k"] != "val" {
					t.Fatalf("version tags = %v", it.Tags)
				}
			}
		}
	}

	if !sawTyped {
		t.Fatal("version metadata (contentType/tags) missing from list")
	}
}
