package healthlake_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/healthlake"
	"github.com/stackshy/cloudemu/v2/services/healthlake/driver"
)

func newMock() *healthlake.Mock {
	return healthlake.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertException(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *driver.APIError, got %T: %v", err, err)
	}

	if apiErr.Exception != want {
		t.Fatalf("exception = %q, want %q", apiErr.Exception, want)
	}
}

func createDatastore(t *testing.T, m *healthlake.Mock, name string) *driver.Datastore {
	t.Helper()

	ds, err := m.CreateFHIRDatastore(context.Background(), &driver.CreateFHIRDatastoreInput{
		DatastoreName:        name,
		DatastoreTypeVersion: driver.FHIRVersionR4,
	})
	requireNoError(t, err)

	return ds
}

func TestCreateAndDescribeComputedFieldsStable(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	ds := createDatastore(t, m, "clinical")

	assertEqual(t, ds.DatastoreStatus, driver.StatusActive)
	assertEqual(t, ds.DatastoreTypeVersion, driver.FHIRVersionR4)

	if ds.DatastoreID == "" || len(ds.DatastoreID) != 32 {
		t.Fatalf("datastore id = %q, want 32 hex chars", ds.DatastoreID)
	}

	wantArn := "arn:aws:healthlake:us-east-1:123456789012:datastore/fhir/" + ds.DatastoreID
	assertEqual(t, ds.DatastoreArn, wantArn)

	wantEndpoint := "https://healthlake.us-east-1.amazonaws.com/datastore/" + ds.DatastoreID + "/r4/"
	assertEqual(t, ds.DatastoreEndpoint, wantEndpoint)

	// Default SSE is an AWS-owned KMS key.
	if ds.SseConfiguration == nil || ds.SseConfiguration.KmsEncryptionConfig == nil ||
		ds.SseConfiguration.KmsEncryptionConfig.CmkType != driver.CmkTypeAWSOwned {
		t.Fatalf("expected AWS-owned KMS SSE config, got %+v", ds.SseConfiguration)
	}

	// The computed fields do not drift across repeated reads.
	for range 3 {
		got, err := m.DescribeFHIRDatastore(ctx, ds.DatastoreID)
		requireNoError(t, err)

		assertEqual(t, got.DatastoreArn, wantArn)
		assertEqual(t, got.DatastoreEndpoint, wantEndpoint)
		assertEqual(t, got.CreatedAt.Equal(ds.CreatedAt), true)
	}
}

func TestCreateRejectsNonR4(t *testing.T) {
	m := newMock()

	_, err := m.CreateFHIRDatastore(context.Background(), &driver.CreateFHIRDatastoreInput{
		DatastoreName:        "bad",
		DatastoreTypeVersion: "R5",
	})
	assertException(t, err, driver.ExValidation)
}

func TestCreateRequiresVersion(t *testing.T) {
	m := newMock()

	_, err := m.CreateFHIRDatastore(context.Background(), &driver.CreateFHIRDatastoreInput{
		DatastoreName: "bad",
	})
	assertException(t, err, driver.ExValidation)
}

func TestDescribeMissing(t *testing.T) {
	m := newMock()

	_, err := m.DescribeFHIRDatastore(context.Background(), "nope")
	assertException(t, err, driver.ExResourceNotFound)
}

func TestDeleteRemovesAndReports(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	ds := createDatastore(t, m, "clinical")

	del, err := m.DeleteFHIRDatastore(ctx, ds.DatastoreID)
	requireNoError(t, err)
	assertEqual(t, del.DatastoreStatus, driver.StatusDeleted)
	assertEqual(t, del.DatastoreArn, ds.DatastoreArn)

	_, err = m.DescribeFHIRDatastore(ctx, ds.DatastoreID)
	assertException(t, err, driver.ExResourceNotFound)

	_, err = m.DeleteFHIRDatastore(ctx, ds.DatastoreID)
	assertException(t, err, driver.ExResourceNotFound)
}

func TestCustomerManagedKeyRoundTrips(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	const kmsKey = "arn:aws:kms:us-east-1:123456789012:key/abc"

	ds, err := m.CreateFHIRDatastore(ctx, &driver.CreateFHIRDatastoreInput{
		DatastoreName:        "enc",
		DatastoreTypeVersion: driver.FHIRVersionR4,
		SseConfiguration: &driver.SseConfiguration{
			KmsEncryptionConfig: &driver.KmsEncryptionConfig{
				CmkType:  driver.CmkTypeCustomerManaged,
				KmsKeyID: kmsKey,
			},
		},
		PreloadDataConfig: &driver.PreloadDataConfig{PreloadDataType: "SYNTHEA"},
	})
	requireNoError(t, err)

	got, err := m.DescribeFHIRDatastore(ctx, ds.DatastoreID)
	requireNoError(t, err)

	assertEqual(t, got.SseConfiguration.KmsEncryptionConfig.CmkType, driver.CmkTypeCustomerManaged)
	assertEqual(t, got.SseConfiguration.KmsEncryptionConfig.KmsKeyID, kmsKey)
	assertEqual(t, got.PreloadDataConfig.PreloadDataType, "SYNTHEA")
}

func TestListAndFilter(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	a := createDatastore(t, m, "alpha")
	createDatastore(t, m, "beta")

	all, _, err := m.ListFHIRDatastores(ctx, driver.ListFilter{}, driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(all), 2)

	byName, _, err := m.ListFHIRDatastores(ctx, driver.ListFilter{DatastoreName: "alpha"}, driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(byName), 1)
	assertEqual(t, byName[0].DatastoreID, a.DatastoreID)

	byStatus, _, err := m.ListFHIRDatastores(ctx, driver.ListFilter{DatastoreStatus: driver.StatusActive}, driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(byStatus), 2)

	none, _, err := m.ListFHIRDatastores(ctx, driver.ListFilter{DatastoreStatus: driver.StatusDeleting}, driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(none), 0)
}

func TestTagLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	ds := createDatastore(t, m, "tagged")

	requireNoError(t, m.TagResource(ctx, ds.DatastoreArn, []driver.Tag{{Key: "env", Value: "prod"}}))

	tags, err := m.ListTagsForResource(ctx, ds.DatastoreArn)
	requireNoError(t, err)
	assertEqual(t, len(tags), 1)
	assertEqual(t, tags[0].Value, "prod")

	// Upsert overwrites the value in place.
	requireNoError(t, m.TagResource(ctx, ds.DatastoreArn, []driver.Tag{{Key: "env", Value: "stage"}}))

	tags, err = m.ListTagsForResource(ctx, ds.DatastoreArn)
	requireNoError(t, err)
	assertEqual(t, len(tags), 1)
	assertEqual(t, tags[0].Value, "stage")

	requireNoError(t, m.UntagResource(ctx, ds.DatastoreArn, []string{"env"}))

	tags, err = m.ListTagsForResource(ctx, ds.DatastoreArn)
	requireNoError(t, err)
	assertEqual(t, len(tags), 0)
}

func TestTagInvalidARN(t *testing.T) {
	m := newMock()

	err := m.TagResource(context.Background(), "not-an-arn", nil)
	assertException(t, err, driver.ExValidation)
}
