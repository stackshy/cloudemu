package resourcediscovery

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/blobstorage"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestWalkStorageAzureAccounts checks storage accounts are discovered as their
// own rows (with their resource group), a legacy container sharing an
// account's name is reported once, and a named account's containers never
// leak in as accounts.
func TestWalkStorageAzureAccounts(t *testing.T) {
	ctx := context.Background()
	m := blobstorage.New(config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0)))))

	for _, name := range []string{"acct", "legacy"} {
		if _, err := m.CreateStorageAccount(ctx, storagedriver.StorageAccountRef{
			Name: name, Subscription: "sub", ResourceGroup: "rg1",
		}); err != nil {
			t.Fatalf("CreateStorageAccount(%s): %v", name, err)
		}
	}

	for _, key := range []string{"legacy", "plain", "acct/ctr"} {
		if err := m.CreateBucket(ctx, key); err != nil {
			t.Fatalf("CreateBucket(%s): %v", key, err)
		}
	}

	m.SetBucketAttributes("acct", storagedriver.AccountAttributes{ResourceGroup: "rg1", Tags: map[string]string{"env": "dev"}})

	res, err := New(ProviderAzure, "sub", "eastus", &Drivers{Storage: m}).ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	rows := map[string]int{}

	for i := range res {
		if res[i].Service != ServiceStorage {
			continue
		}

		rows[res[i].ID]++

		if res[i].ID == "acct" {
			if !strings.Contains(res[i].ARN, "/resourceGroups/rg1/") || res[i].Tags["env"] != "dev" {
				t.Fatalf("account row = %+v", res[i])
			}
		}
	}

	want := map[string]int{"acct": 1, "legacy": 1, "plain": 1}
	if len(rows) != len(want) {
		t.Fatalf("storage rows = %v, want %v", rows, want)
	}

	for id, n := range want {
		if rows[id] != n {
			t.Fatalf("storage rows = %v, want %v", rows, want)
		}
	}
}
