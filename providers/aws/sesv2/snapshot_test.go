package sesv2_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// TestSnapshotRoundTripSESV2 proves a snapshot/restore round-trip preserves the
// promoted identity/config-set stores, a contact list with its nested contacts
// store, and the mutex-guarded account state under their original identities.
func TestSnapshotRoundTripSESV2(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	if _, err := src.CreateEmailIdentity(ctx, driver.CreateIdentityInput{EmailIdentity: "sender@example.com"}); err != nil {
		t.Fatalf("create identity: %v", err)
	}

	if err := src.CreateConfigurationSet(ctx, driver.CreateConfigurationSetInput{Name: "cs-1"}); err != nil {
		t.Fatalf("create config set: %v", err)
	}

	if err := src.CreateContactList(ctx, driver.ContactListInput{Name: "list-1", Description: "my list"}); err != nil {
		t.Fatalf("create contact list: %v", err)
	}

	if err := src.CreateContact(ctx, driver.ContactInput{
		ContactListName: "list-1", EmailAddress: "c@example.com",
	}); err != nil {
		t.Fatalf("create contact: %v", err)
	}

	// Mutate account state so its restore is observable.
	if err := src.PutAccountDetails(ctx, "", "", true); err != nil {
		t.Fatalf("put account details: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	id, err := dst.GetEmailIdentity(ctx, "sender@example.com")
	if err != nil || id == nil {
		t.Fatalf("restored identity = %+v, err %v", id, err)
	}

	cs, err := dst.GetConfigurationSet(ctx, "cs-1")
	if err != nil || cs.Name != "cs-1" {
		t.Fatalf("restored config set = %+v, err %v", cs, err)
	}

	// Nested contacts store survived under the promoted contact list.
	c, err := dst.GetContact(ctx, "list-1", "c@example.com")
	if err != nil || c.EmailAddress != "c@example.com" {
		t.Fatalf("restored contact = %+v, err %v", c, err)
	}

	// Mutex-guarded account state survived.
	acct, err := dst.GetAccount(ctx)
	if err != nil || !acct.ProductionAccessEnabled {
		t.Fatalf("restored account = %+v, err %v", acct, err)
	}
}
