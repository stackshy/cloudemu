package healthlake_test

import (
	"context"
	"sync"
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

func dsInput(token string) *driver.CreateFHIRDatastoreInput {
	return &driver.CreateFHIRDatastoreInput{DatastoreName: "ds", DatastoreTypeVersion: driver.FHIRVersionR4, ClientToken: token}
}

func TestCreateFHIRDatastoreTokenAfterDelete(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	first, err := m.CreateFHIRDatastore(ctx, dsInput("g1"))
	requireNoError(t, err)

	_, err = m.DeleteFHIRDatastore(ctx, first.DatastoreID)
	requireNoError(t, err)

	again, err := m.CreateFHIRDatastore(ctx, dsInput("g1"))
	requireNoError(t, err)

	if again.DatastoreID == first.DatastoreID {
		t.Fatalf("same-token create after delete replayed the deleted data store %s", first.DatastoreID)
	}

	_, err = m.DescribeFHIRDatastore(ctx, again.DatastoreID)
	requireNoError(t, err)
}

func TestCreateFHIRDatastoreTokenAfterUpdate(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	first, err := m.CreateFHIRDatastore(ctx, dsInput("g1"))
	requireNoError(t, err)
	requireNoError(t, m.TagResource(ctx, first.DatastoreArn, []driver.Tag{{Key: "env", Value: "prod"}}))

	retry, err := m.CreateFHIRDatastore(ctx, dsInput("g1"))
	requireNoError(t, err)
	assertEqual(t, retry.DatastoreID, first.DatastoreID)

	if len(retry.Tags) != 1 || retry.Tags[0].Key != "env" {
		t.Fatalf("replay tags = %v, want the live tag set [env=prod]", retry.Tags)
	}
}

func TestCreateFHIRDatastoreTokenConcurrent(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	const n = 20

	ids := make([]string, n)
	errs := make([]error, n)

	var wg sync.WaitGroup

	start := make(chan struct{})

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			ds, err := m.CreateFHIRDatastore(ctx, dsInput("burst"))
			if err == nil {
				ids[i] = ds.DatastoreID
			}

			errs[i] = err
		}()
	}

	close(start)
	wg.Wait()

	for i := range n {
		requireNoError(t, errs[i])
		assertEqual(t, ids[i], ids[0])
	}

	list, _, err := m.ListFHIRDatastores(ctx, driver.ListFilter{}, driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(list), 1)
}
