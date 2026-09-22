package healthlake_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/healthlake/driver"
)

func TestCreateFHIRDatastoreClientToken(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := &driver.CreateFHIRDatastoreInput{
		DatastoreName:        "ds",
		DatastoreTypeVersion: driver.FHIRVersionR4,
		ClientToken:          "tok-1",
	}

	first, err := m.CreateFHIRDatastore(ctx, in)
	requireNoError(t, err)

	retry, err := m.CreateFHIRDatastore(ctx, in)
	requireNoError(t, err)
	assertEqual(t, retry.DatastoreID, first.DatastoreID)

	in.ClientToken = "tok-2"

	other, err := m.CreateFHIRDatastore(ctx, in)
	requireNoError(t, err)

	if other.DatastoreID == first.DatastoreID {
		t.Fatalf("different token must mint a new data store, got %s again", other.DatastoreID)
	}

	list, _, err := m.ListFHIRDatastores(ctx, driver.ListFilter{}, driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(list), 2)
}
