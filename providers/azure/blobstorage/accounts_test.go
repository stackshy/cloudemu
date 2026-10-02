package blobstorage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

func mustCreateAccount(t *testing.T, m *Mock, name, sub, rg string) {
	t.Helper()

	if _, err := m.CreateStorageAccount(context.Background(), driver.StorageAccountRef{
		Name: name, Subscription: sub, ResourceGroup: rg,
	}); err != nil {
		t.Fatalf("CreateStorageAccount(%s): %v", name, err)
	}
}

func mustPutContainerBlob(t *testing.T, m *Mock, container, blob string) {
	t.Helper()

	ctx := context.Background()
	if err := m.CreateBucket(ctx, container); err != nil && !cerrors.IsAlreadyExists(err) {
		t.Fatalf("CreateBucket(%s): %v", container, err)
	}

	if err := m.PutObject(ctx, container, blob, []byte("data"), "text/plain", nil); err != nil {
		t.Fatalf("PutObject(%s/%s): %v", container, blob, err)
	}
}

func hasContainer(m *Mock, key string) bool {
	return m.containers.Has(key)
}

// TestDeleteStorageAccountKeepsSameNamedContainer is AZSTO-01: an account is no
// longer the container of the same name, so deleting the account leaves the
// user's default-namespace container (and its blob) alone.
func TestDeleteStorageAccountKeepsSameNamedContainer(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	mustCreateAccount(t, m, "pg2", "sub", "rg")
	mustPutContainerBlob(t, m, "pg2", "keep.txt")

	if err := m.DeleteStorageAccount(ctx, "pg2"); err != nil {
		t.Fatalf("DeleteStorageAccount: %v", err)
	}

	if _, err := m.GetObject(ctx, "pg2", "keep.txt"); err != nil {
		t.Fatalf("default container pg2 lost its blob: %v", err)
	}

	if _, err := m.GetStorageAccount(ctx, "pg2"); !cerrors.IsNotFound(err) {
		t.Fatalf("account still present: %v", err)
	}
}

// TestDeleteStorageAccountCascadeIsPrefixBounded deletes account pg with a
// non-empty container and checks pg2's same-named container survives.
func TestDeleteStorageAccountCascadeIsPrefixBounded(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	mustCreateAccount(t, m, "pg", "sub", "rg")
	mustCreateAccount(t, m, "pg2", "sub", "rg")
	mustPutContainerBlob(t, m, driver.AzureContainerKey("pg", "ctr"), "a.txt")
	mustPutContainerBlob(t, m, driver.AzureContainerKey("pg2", "ctr"), "b.txt")

	if _, err := m.ListStorageAccountKeys(ctx, "pg"); err != nil {
		t.Fatalf("ListStorageAccountKeys: %v", err)
	}

	if err := m.DeleteStorageAccount(ctx, "pg"); err != nil {
		t.Fatalf("DeleteStorageAccount of a non-empty account: %v", err)
	}

	if hasContainer(m, "pg/ctr") {
		t.Fatal("pg/ctr survived its account's delete")
	}

	if _, ok := m.accountKeys.Get("pg"); ok {
		t.Fatal("pg's keys survived its account's delete")
	}

	if _, err := m.GetObject(ctx, "pg2/ctr", "b.txt"); err != nil {
		t.Fatalf("pg2/ctr lost its blob: %v", err)
	}
}

func TestListBucketsHidesAccountContainers(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	mustCreateAccount(t, m, "acct", "sub", "rg")
	mustPutContainerBlob(t, m, "acct/ctr", "x")
	mustPutContainerBlob(t, m, "plain", "y")

	buckets, err := m.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}

	if len(buckets) != 1 || buckets[0].Name != "plain" {
		t.Fatalf("ListBuckets = %+v, want only plain", buckets)
	}

	list, err := m.ListAccountContainers(ctx, "acct")
	if err != nil {
		t.Fatalf("ListAccountContainers: %v", err)
	}

	if len(list) != 1 || list[0].Name != "ctr" {
		t.Fatalf("ListAccountContainers = %+v, want [ctr]", list)
	}

	if _, err := m.ListAccountContainers(ctx, "nope"); !cerrors.IsNotFound(err) {
		t.Fatalf("ListAccountContainers(nope) err = %v, want NotFound", err)
	}
}

func TestCreateBucketQualifiedKeyValidation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	mustCreateAccount(t, m, "acct", "sub", "rg")

	tests := []struct {
		name string
		key  string
		want cerrors.Code
	}{
		{"missing account", "ghost/c1", cerrors.NotFound},
		{"invalid container name", "acct/Bad_Name", cerrors.InvalidArgument},
		{"too short", "acct/ab", cerrors.InvalidArgument},
		{"valid", "acct/good-name", cerrors.OK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cerrors.GetCode(m.CreateBucket(ctx, tt.key)); got != tt.want {
				t.Fatalf("CreateBucket(%s) code = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestCreateStorageAccountOwnership(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		existing    driver.StorageAccountRef
		req         driver.StorageAccountRef
		wantErr     bool
		wantSubAfer string
	}{
		{"same group is an update", driver.StorageAccountRef{Subscription: "s1", ResourceGroup: "rgA"},
			driver.StorageAccountRef{Subscription: "s1", ResourceGroup: "RGA"}, false, "s1"},
		{"other group conflicts", driver.StorageAccountRef{Subscription: "s1", ResourceGroup: "rgA"},
			driver.StorageAccountRef{Subscription: "s1", ResourceGroup: "rgB"}, true, "s1"},
		{"other subscription conflicts", driver.StorageAccountRef{Subscription: "s1", ResourceGroup: "rgA"},
			driver.StorageAccountRef{Subscription: "s2", ResourceGroup: "rgA"}, true, "s1"},
		{"migrated record matches and is stamped", driver.StorageAccountRef{ResourceGroup: "rgA"},
			driver.StorageAccountRef{Subscription: "s9", ResourceGroup: "rgA"}, false, "s9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMock()
			tt.existing.Name, tt.req.Name = "acct", "acct"
			m.accounts.Set("acct", tt.existing)

			created, err := m.CreateStorageAccount(ctx, tt.req)
			if created {
				t.Fatal("created = true for an existing account")
			}

			var exists *driver.AccountExistsError
			if tt.wantErr != errors.As(err, &exists) {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr && (!cerrors.IsAlreadyExists(err) || exists.ResourceGroup != "rgA") {
				t.Fatalf("err = %#v, want AlreadyExists owned by rgA", err)
			}

			got, _ := m.GetStorageAccount(ctx, "acct")
			if got.Subscription != tt.wantSubAfer || got.ResourceGroup != "rgA" {
				t.Fatalf("account after = %+v", got)
			}
		})
	}
}

func TestPurgeResourceGroupMatchesGroupExactly(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	mustCreateAccount(t, m, "inrg1", "sub", "Rg1")
	mustCreateAccount(t, m, "inrg10", "sub", "rg10")
	mustPutContainerBlob(t, m, "inrg1/ctr", "x")
	mustPutContainerBlob(t, m, "inrg10/ctr", "y")

	if err := m.PurgeResourceGroup(ctx, "sub", "rg1"); err != nil {
		t.Fatalf("PurgeResourceGroup: %v", err)
	}

	if _, err := m.GetStorageAccount(ctx, "inrg1"); !cerrors.IsNotFound(err) {
		t.Fatalf("inrg1 survived the purge: %v", err)
	}

	if hasContainer(m, "inrg1/ctr") {
		t.Fatal("inrg1/ctr survived the purge")
	}

	if _, err := m.GetObject(ctx, "inrg10/ctr", "y"); err != nil {
		t.Fatalf("rg10 data was purged: %v", err)
	}
}

// TestDefaultAccountNameIsReserved: the default account owns the default
// namespace, so no caller can register an account of that name, and a purge
// of any resource group leaves the bare containers alone.
func TestDefaultAccountNameIsReserved(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateStorageAccount(ctx, driver.StorageAccountRef{
		Name: AccountName, Subscription: "sub", ResourceGroup: "rg",
	})
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("CreateStorageAccount(%s) = %v, want AlreadyExists", AccountName, err)
	}

	mustPutContainerBlob(t, m, "bare", "x")

	if err := m.PurgeResourceGroup(ctx, "sub", "rg"); err != nil {
		t.Fatalf("PurgeResourceGroup: %v", err)
	}

	if !hasContainer(m, "bare") {
		t.Fatal("a purge deleted a default-namespace container")
	}
}

// legacySnapshot builds a snapshot in the format written before accounts were
// their own resource: the account is the same-named container, its attributes
// carry the resource group, and there is no "accounts" key.
func legacySnapshot(t *testing.T) json.RawMessage {
	t.Helper()

	ctx := context.Background()
	old := newTestMock()
	old.SetBucketAttributes("legacyacct", driver.AccountAttributes{SKU: "Standard_GRS", ResourceGroup: "r1"})
	mustPutContainerBlob(t, old, "legacyacct", "c/blob.txt")

	raw, err := old.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	delete(doc, "accounts")

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return out
}

func TestRestoreMigratesLegacyAccounts(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if err := m.Restore(ctx, legacySnapshot(t)); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	acct, err := m.GetStorageAccount(ctx, "legacyacct")
	if err != nil {
		t.Fatalf("migrated account missing: %v", err)
	}

	if acct.Subscription != "" || acct.ResourceGroup != "r1" {
		t.Fatalf("migrated account = %+v, want no subscription in r1", acct)
	}

	if _, err := m.GetObject(ctx, "legacyacct", "c/blob.txt"); err != nil {
		t.Fatalf("legacy blob lost: %v", err)
	}

	first, err := m.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	again := newTestMock()
	if err := again.Restore(ctx, first); err != nil {
		t.Fatalf("second Restore: %v", err)
	}

	second, err := again.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("second Snapshot: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Fatalf("snapshot round-trip not byte-stable:\n%s\n%s", first, second)
	}

	if err := again.DeleteStorageAccount(ctx, "legacyacct"); err != nil {
		t.Fatalf("DeleteStorageAccount: %v", err)
	}

	if hasContainer(again, "legacyacct") {
		t.Fatal("a migrated account's delete left its legacy container behind")
	}
}

func TestSnapshotRoundTripsAccountsAndEncryptionScope(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	mustCreateAccount(t, m, "acct", "sub", "rg")
	mustPutContainerBlob(t, m, "acct/ctr", "x")

	scope := driver.ContainerEncryptionScope{DefaultEncryptionScope: "scope1", DenyEncryptionScopeOverride: true}
	if err := m.SetContainerEncryptionScope(ctx, "acct/ctr", scope); err != nil {
		t.Fatalf("SetContainerEncryptionScope: %v", err)
	}

	raw, err := m.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newTestMock()
	if err := restored.Restore(ctx, raw); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := restored.GetStorageAccount(ctx, "acct")
	if err != nil || got.Subscription != "sub" || got.ResourceGroup != "rg" || got.CreatedAt == "" {
		t.Fatalf("restored account = %+v, %v", got, err)
	}

	gotScope, err := restored.ContainerEncryptionScope(ctx, "acct/ctr")
	if err != nil || gotScope != scope {
		t.Fatalf("restored scope = %+v, %v", gotScope, err)
	}
}
