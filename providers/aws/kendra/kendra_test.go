package kendra_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kendra"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

const roleArn = "arn:aws:iam::123456789012:role/kendra"

func newMock() *kendra.Mock {
	return kendra.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireError(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func createIndex(t *testing.T, m *kendra.Mock) *driver.Index {
	t.Helper()

	idx, err := m.CreateIndex(context.Background(), &driver.CreateIndexInput{
		Name: "docs", Edition: driver.EditionDeveloper, RoleArn: roleArn, Description: "d",
	})
	requireNoError(t, err)

	return idx
}

func TestCreateIndexRequiresNameAndRole(t *testing.T) {
	m := newMock()

	_, err := m.CreateIndex(context.Background(), &driver.CreateIndexInput{Edition: driver.EditionDeveloper, RoleArn: roleArn})
	requireError(t, err)

	var apiErr *driver.APIError
	if !isAPIError(err, &apiErr) || apiErr.Exception != driver.ExValidation {
		t.Fatalf("expected ValidationException, got %v", err)
	}

	_, err = m.CreateIndex(context.Background(), &driver.CreateIndexInput{Name: "docs"})
	requireError(t, err)
}

func TestCreateIndexComputedFieldsStable(t *testing.T) {
	m := newMock()
	idx := createIndex(t, m)

	assertEqual(t, idx.Status, driver.IndexStatusActive)
	assertEqual(t, idx.Edition, driver.EditionDeveloper)
	assertEqual(t, idx.UserContextPolicy, driver.UserContextAttributeFilter)
	assertEqual(t, len(idx.ID), 36)

	// CapacityUnits defaulted and stored.
	if string(idx.CapacityUnits) != `{"QueryCapacityUnits":0,"StorageCapacityUnits":0}` {
		t.Fatalf("unexpected capacity units %q", idx.CapacityUnits)
	}

	// Computed fields must be byte-stable across every read.
	got, err := m.DescribeIndex(context.Background(), idx.ID)
	requireNoError(t, err)
	assertEqual(t, got.ID, idx.ID)
	assertEqual(t, got.Status, driver.IndexStatusActive)
	assertEqual(t, got.CreatedAt.Equal(idx.CreatedAt), true)
	assertEqual(t, got.Edition, idx.Edition)
}

func TestCreateIndexDefaultsEnterprise(t *testing.T) {
	m := newMock()

	idx, err := m.CreateIndex(context.Background(), &driver.CreateIndexInput{Name: "docs", RoleArn: roleArn})
	requireNoError(t, err)
	assertEqual(t, idx.Edition, driver.EditionEnterprise)
}

func TestDescribeIndexMissing404(t *testing.T) {
	m := newMock()

	_, err := m.DescribeIndex(context.Background(), "missing")
	requireError(t, err)

	var apiErr *driver.APIError
	if !isAPIError(err, &apiErr) || apiErr.Exception != driver.ExResourceNotFound {
		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}
}

func TestUpdateIndexPreservesComputed(t *testing.T) {
	m := newMock()
	idx := createIndex(t, m)

	newDesc := "updated"
	newCapacity := json.RawMessage(`{"QueryCapacityUnits":2,"StorageCapacityUnits":3}`)
	requireNoError(t, m.UpdateIndex(context.Background(), &driver.UpdateIndexInput{
		ID: idx.ID, Description: &newDesc, CapacityUnits: newCapacity,
	}))

	got, err := m.DescribeIndex(context.Background(), idx.ID)
	requireNoError(t, err)
	assertEqual(t, got.Description, "updated")
	assertEqual(t, got.ID, idx.ID)
	assertEqual(t, got.CreatedAt.Equal(idx.CreatedAt), true)
	assertEqual(t, string(got.CapacityUnits), string(newCapacity))
}

func TestDataSourceRequiresIndex(t *testing.T) {
	m := newMock()

	_, err := m.CreateDataSource(context.Background(), &driver.CreateDataSourceInput{
		IndexID: "missing", Name: "ds", Type: driver.DataSourceTypeCustom,
	})
	requireError(t, err)

	var apiErr *driver.APIError
	if !isAPIError(err, &apiErr) || apiErr.Exception != driver.ExResourceNotFound {
		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}
}

func TestCustomDataSourceRejectsConfiguration(t *testing.T) {
	m := newMock()
	idx := createIndex(t, m)

	_, err := m.CreateDataSource(context.Background(), &driver.CreateDataSourceInput{
		IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom, RoleArn: roleArn,
	})
	requireError(t, err)
}

func TestDataSourceLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	idx := createIndex(t, m)

	ds, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
		IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom, Description: "d",
	})
	requireNoError(t, err)
	assertEqual(t, ds.Status, driver.DataSourceStatusActive)
	assertEqual(t, ds.IndexID, idx.ID)

	got, err := m.DescribeDataSource(ctx, idx.ID, ds.ID)
	requireNoError(t, err)
	assertEqual(t, got.ID, ds.ID)
	assertEqual(t, got.CreatedAt.Equal(ds.CreatedAt), true)

	newDesc := "updated"
	requireNoError(t, m.UpdateDataSource(ctx, &driver.UpdateDataSourceInput{ID: ds.ID, IndexID: idx.ID, Description: &newDesc}))

	got, err = m.DescribeDataSource(ctx, idx.ID, ds.ID)
	requireNoError(t, err)
	assertEqual(t, got.Description, "updated")

	requireNoError(t, m.DeleteDataSource(ctx, idx.ID, ds.ID))
	_, err = m.DescribeDataSource(ctx, idx.ID, ds.ID)
	requireError(t, err)
}

func TestDeleteIndexCascadesDataSources(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	idx := createIndex(t, m)

	ds, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
		IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom,
	})
	requireNoError(t, err)

	requireNoError(t, m.DeleteIndex(ctx, idx.ID))

	_, err = m.DescribeIndex(ctx, idx.ID)
	requireError(t, err)

	// The cascade removed the child data source too.
	_, err = m.DescribeDataSource(ctx, idx.ID, ds.ID)
	requireError(t, err)

	// Listing under the deleted index is a ResourceNotFoundException.
	_, _, err = m.ListDataSources(ctx, idx.ID, driver.Page{})
	requireException(t, err, driver.ExResourceNotFound)
}

func TestTagLifecycleIndexAndDataSource(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	idx := createIndex(t, m)

	indexARN := "arn:aws:kendra:us-east-1:000000000000:index/" + idx.ID
	requireNoError(t, m.TagResource(ctx, indexARN, []driver.Tag{{Key: "env", Value: "dev"}}))

	tags, err := m.ListTagsForResource(ctx, indexARN)
	requireNoError(t, err)
	assertEqual(t, len(tags), 1)
	assertEqual(t, tagValue(tags, "env"), "dev")

	ds, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
		IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom,
	})
	requireNoError(t, err)

	dsARN := indexARN + "/data-source/" + ds.ID
	requireNoError(t, m.TagResource(ctx, dsARN, []driver.Tag{{Key: "team", Value: "ops"}}))

	tags, err = m.ListTagsForResource(ctx, dsARN)
	requireNoError(t, err)
	assertEqual(t, len(tags), 1)
	assertEqual(t, tagValue(tags, "team"), "ops")

	requireNoError(t, m.UntagResource(ctx, dsARN, []string{"team"}))
	tags, err = m.ListTagsForResource(ctx, dsARN)
	requireNoError(t, err)
	assertEqual(t, len(tags), 0)
}

func TestListIndices(t *testing.T) {
	m := newMock()
	createIndex(t, m)
	createIndex(t, m)

	list, _, err := m.ListIndices(context.Background(), driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(list), 2)
}

func TestSnapshotRestore(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	idx := createIndex(t, m)

	ds, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
		IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom,
	})
	requireNoError(t, err)

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, data))

	got, err := restored.DescribeIndex(ctx, idx.ID)
	requireNoError(t, err)
	assertEqual(t, got.ID, idx.ID)

	gotDS, err := restored.DescribeDataSource(ctx, idx.ID, ds.ID)
	requireNoError(t, err)
	assertEqual(t, gotDS.ID, ds.ID)
}

func tagValue(tags []driver.Tag, key string) string {
	for _, t := range tags {
		if t.Key == key {
			return t.Value
		}
	}

	return ""
}

func isAPIError(err error, target **driver.APIError) bool {
	for e := err; e != nil; {
		if ae, ok := e.(*driver.APIError); ok {
			*target = ae

			return true
		}

		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}

		e = u.Unwrap()
	}

	return false
}

func requireException(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr *driver.APIError
	if !isAPIError(err, &apiErr) || apiErr.Exception != want {
		t.Fatalf("expected %s, got %v", want, err)
	}
}

func TestCreateIndexClientTokenIsIdempotent(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	in := &driver.CreateIndexInput{ClientToken: "tok-1", Name: "docs", RoleArn: roleArn}

	first, err := m.CreateIndex(ctx, in)
	requireNoError(t, err)

	second, err := m.CreateIndex(ctx, in)
	requireNoError(t, err)
	assertEqual(t, second.ID, first.ID)

	list, _, err := m.ListIndices(ctx, driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(list), 1)

	other, err := m.CreateIndex(ctx, &driver.CreateIndexInput{ClientToken: "tok-2", Name: "docs", RoleArn: roleArn})
	requireNoError(t, err)

	if other.ID == first.ID {
		t.Fatalf("a different token must create a new index")
	}
}

func TestCreateIndexEmptyClientTokenNeverDeduplicates(t *testing.T) {
	m := newMock()
	a := createIndex(t, m)
	b := createIndex(t, m)

	if a.ID == b.ID {
		t.Fatalf("creates without a token must each create an index")
	}
}

func TestCreateIndexClientTokenAfterDeleteCreatesNew(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	in := &driver.CreateIndexInput{ClientToken: "tok", Name: "docs", RoleArn: roleArn}

	first, err := m.CreateIndex(ctx, in)
	requireNoError(t, err)
	requireNoError(t, m.DeleteIndex(ctx, first.ID))

	second, err := m.CreateIndex(ctx, in)
	requireNoError(t, err)

	if second.ID == first.ID {
		t.Fatalf("a deleted index must not be returned for a reused token")
	}
}

func TestCreateIndexClientTokenConcurrent(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	const workers = 50

	ids := make([]string, workers)

	var wg sync.WaitGroup

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			idx, err := m.CreateIndex(ctx, &driver.CreateIndexInput{ClientToken: "same", Name: "docs", RoleArn: roleArn})
			if err == nil {
				ids[i] = idx.ID
			}
		}()
	}

	wg.Wait()

	for _, id := range ids {
		assertEqual(t, id, ids[0])
	}

	list, _, err := m.ListIndices(ctx, driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(list), 1)
}

func TestCreateDataSourceClientTokenIsIdempotentPerIndex(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	idxA := createIndex(t, m)
	idxB := createIndex(t, m)
	in := func(index string) *driver.CreateDataSourceInput {
		return &driver.CreateDataSourceInput{ClientToken: "tok", IndexID: index, Name: "ds", Type: driver.DataSourceTypeCustom}
	}

	first, err := m.CreateDataSource(ctx, in(idxA.ID))
	requireNoError(t, err)

	again, err := m.CreateDataSource(ctx, in(idxA.ID))
	requireNoError(t, err)
	assertEqual(t, again.ID, first.ID)

	onOther, err := m.CreateDataSource(ctx, in(idxB.ID))
	requireNoError(t, err)

	if onOther.ID == first.ID {
		t.Fatalf("the same token under a different index must create its own data source")
	}

	list, _, err := m.ListDataSources(ctx, idxA.ID, driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(list), 1)
}

func TestClientTokenSurvivesSnapshotRestore(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	in := &driver.CreateIndexInput{ClientToken: "tok", Name: "docs", RoleArn: roleArn}

	first, err := m.CreateIndex(ctx, in)
	requireNoError(t, err)

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, data))

	again, err := restored.CreateIndex(ctx, in)
	requireNoError(t, err)
	assertEqual(t, again.ID, first.ID)
}

func TestListDataSourcesMissingIndex(t *testing.T) {
	m := newMock()

	_, _, err := m.ListDataSources(context.Background(), "00000000-0000-4000-8000-000000000000", driver.Page{})
	requireException(t, err, driver.ExResourceNotFound)
}

func TestRoleArnValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	idx := createIndex(t, m)

	for _, bad := range []string{"not-an-arn", "arn:aws", "arn:aws:iam::123:/leading-slash", "role/kendra"} {
		_, err := m.CreateIndex(ctx, &driver.CreateIndexInput{Name: "docs", RoleArn: bad})
		requireException(t, err, driver.ExValidation)

		_, err = m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
			IndexID: idx.ID, Name: "ds", Type: "S3", RoleArn: bad, Configuration: []byte(`{}`),
		})
		requireException(t, err, driver.ExValidation)

		err = m.UpdateIndex(ctx, &driver.UpdateIndexInput{ID: idx.ID, RoleArn: &bad})
		requireException(t, err, driver.ExValidation)
	}

	tooLong := "arn:aws:iam::123456789012:role/" + strings.Repeat("a", 1300)
	_, err := m.CreateIndex(ctx, &driver.CreateIndexInput{Name: "docs", RoleArn: tooLong})
	requireException(t, err, driver.ExValidation)

	good := "arn:aws:iam::123456789012:role/other"
	requireNoError(t, m.UpdateIndex(ctx, &driver.UpdateIndexInput{ID: idx.ID, RoleArn: &good}))
}

func TestUpdateDataSourceRoleArnValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	idx := createIndex(t, m)

	ds, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
		IndexID: idx.ID, Name: "ds", Type: "S3", RoleArn: roleArn, Configuration: []byte(`{}`),
	})
	requireNoError(t, err)

	bad := "nope"
	err = m.UpdateDataSource(ctx, &driver.UpdateDataSourceInput{ID: ds.ID, IndexID: idx.ID, RoleArn: &bad})
	requireException(t, err, driver.ExValidation)
}
