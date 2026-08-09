package cloudtrail

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

func newMock() *Mock {
	return New(config.NewOptions())
}

func TestCreateGetTrail(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	tr, err := m.CreateTrail(ctx, driver.CreateTrailInput{Name: "my-trail", S3BucketName: "bkt"})
	require.NoError(t, err)
	assert.Contains(t, tr.TrailARN, ":cloudtrail:")
	assert.Equal(t, "my-trail", tr.Name)

	got, err := m.GetTrail(ctx, "my-trail")
	require.NoError(t, err)
	assert.Equal(t, "bkt", got.S3BucketName)

	// resolvable by ARN too
	byARN, err := m.GetTrail(ctx, tr.TrailARN)
	require.NoError(t, err)
	assert.Equal(t, "my-trail", byARN.Name)
}

func TestCreateTrailInvalidName(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, name := range []string{"ab", "-bad", "bad-", "a..b", "192.168.1.1"} {
		_, err := m.CreateTrail(ctx, driver.CreateTrailInput{Name: name, S3BucketName: "b"})
		require.Error(t, err, name)

		var apiErr *driver.APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, driver.ExInvalidTrailName, apiErr.Exception, name)
	}
}

func TestCreateTrailDuplicate(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateTrail(ctx, driver.CreateTrailInput{Name: "dup", S3BucketName: "b"})
	require.NoError(t, err)

	_, err = m.CreateTrail(ctx, driver.CreateTrailInput{Name: "dup", S3BucketName: "b"})
	require.Error(t, err)

	var apiErr *driver.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, driver.ExTrailAlreadyExists, apiErr.Exception)
}

func TestGetTrailNotFound(t *testing.T) {
	m := newMock()

	_, err := m.GetTrail(context.Background(), "nope")
	require.Error(t, err)

	var apiErr *driver.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, driver.ExTrailNotFound, apiErr.Exception)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestUpdateTrailAndLoggingLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateTrail(ctx, driver.CreateTrailInput{Name: "trail-1", S3BucketName: "b"})
	require.NoError(t, err)

	st, err := m.GetTrailStatus(ctx, "trail-1")
	require.NoError(t, err)
	assert.False(t, st.IsLogging)

	require.NoError(t, m.StartLogging(ctx, "trail-1"))
	st, _ = m.GetTrailStatus(ctx, "trail-1")
	assert.True(t, st.IsLogging)
	assert.False(t, st.StartLoggingTime.IsZero())

	require.NoError(t, m.StopLogging(ctx, "trail-1"))
	st, _ = m.GetTrailStatus(ctx, "trail-1")
	assert.False(t, st.IsLogging)
	assert.False(t, st.StopLoggingTime.IsZero())

	newBkt := "new-bucket"
	upd, err := m.UpdateTrail(ctx, driver.UpdateTrailInput{Name: "trail-1", S3BucketName: &newBkt})
	require.NoError(t, err)
	assert.Equal(t, "new-bucket", upd.S3BucketName)
}

func TestListAndDeleteTrail(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, n := range []string{"a-trail", "b-trail", "c-trail"} {
		_, err := m.CreateTrail(ctx, driver.CreateTrailInput{Name: n, S3BucketName: "b"})
		require.NoError(t, err)
	}

	trails, _, err := m.ListTrails(ctx, "")
	require.NoError(t, err)
	assert.Len(t, trails, 3)

	require.NoError(t, m.DeleteTrail(ctx, "a-trail"))

	trails, _, _ = m.ListTrails(ctx, "")
	assert.Len(t, trails, 2)

	_, err = m.GetTrail(ctx, "a-trail")
	require.Error(t, err)
}

func TestEventDataStoreCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	noProt := false
	eds, err := m.CreateEventDataStore(ctx, driver.CreateEventDataStoreInput{
		Name: "eds1", TerminationProtectionEnabled: &noProt,
	})
	require.NoError(t, err)
	assert.Equal(t, driver.EDSStatusEnabled, eds.Status)
	assert.Contains(t, eds.ARN, ":eventdatastore/")

	got, err := m.GetEventDataStore(ctx, eds.ARN)
	require.NoError(t, err)
	assert.Equal(t, "eds1", got.Name)

	newName := "eds1-renamed"
	upd, err := m.UpdateEventDataStore(ctx, driver.UpdateEventDataStoreInput{ARN: eds.ARN, Name: &newName})
	require.NoError(t, err)
	assert.Equal(t, "eds1-renamed", upd.Name)

	require.NoError(t, m.StopEventDataStoreIngestion(ctx, eds.ARN))
	got, _ = m.GetEventDataStore(ctx, eds.ARN)
	assert.Equal(t, driver.EDSStatusStoppedIngestion, got.Status)

	require.NoError(t, m.DeleteEventDataStore(ctx, eds.ARN))
	got, _ = m.GetEventDataStore(ctx, eds.ARN)
	assert.Equal(t, driver.EDSStatusPendingDeletion, got.Status)

	rst, err := m.RestoreEventDataStore(ctx, eds.ARN)
	require.NoError(t, err)
	assert.Equal(t, driver.EDSStatusEnabled, rst.Status)
}

func TestEDSInvalidARN(t *testing.T) {
	m := newMock()

	_, err := m.GetEventDataStore(context.Background(), "not-an-arn")
	require.Error(t, err)

	var apiErr *driver.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, driver.ExEventDataStoreARNInvalid, apiErr.Exception)
}

func TestEDSNotFound(t *testing.T) {
	m := newMock()
	arn := "arn:aws:cloudtrail:us-east-1:000000000000:eventdatastore/missing"

	_, err := m.GetEventDataStore(context.Background(), arn)
	require.Error(t, err)

	var apiErr *driver.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, driver.ExEventDataStoreNotFound, apiErr.Exception)
}

func TestEDSTerminationProtection(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	eds, err := m.CreateEventDataStore(ctx, driver.CreateEventDataStoreInput{Name: "protected"})
	require.NoError(t, err)
	assert.True(t, eds.TerminationProtectionEnabled) // default on

	err = m.DeleteEventDataStore(ctx, eds.ARN)
	require.Error(t, err)

	var apiErr *driver.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, driver.ExEDSTerminationProtected, apiErr.Exception)
}

func TestTagsRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	tr, err := m.CreateTrail(ctx, driver.CreateTrailInput{
		Name: "tagged", S3BucketName: "b", Tags: map[string]string{"env": "prod"},
	})
	require.NoError(t, err)

	tags, err := m.ListTags(ctx, []string{tr.TrailARN})
	require.NoError(t, err)
	assert.Equal(t, "prod", tags[tr.TrailARN]["env"])

	require.NoError(t, m.AddTags(ctx, tr.TrailARN, map[string]string{"team": "platform"}))
	tags, _ = m.ListTags(ctx, []string{tr.TrailARN})
	assert.Len(t, tags[tr.TrailARN], 2)

	require.NoError(t, m.RemoveTags(ctx, tr.TrailARN, []string{"env"}))
	tags, _ = m.ListTags(ctx, []string{tr.TrailARN})
	assert.Len(t, tags[tr.TrailARN], 1)
	assert.Equal(t, "platform", tags[tr.TrailARN]["team"])
}

func TestEventSelectorsMutualExclusion(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateTrail(ctx, driver.CreateTrailInput{Name: "sel-trail", S3BucketName: "b"})
	require.NoError(t, err)

	_, _, _, err = m.PutEventSelectors(ctx, "sel-trail",
		[]driver.EventSelector{{ReadWriteType: "All"}},
		[]driver.AdvancedEventSelector{{Name: "x"}})
	require.Error(t, err)

	arn, sel, _, err := m.PutEventSelectors(ctx, "sel-trail",
		[]driver.EventSelector{{ReadWriteType: "WriteOnly"}}, nil)
	require.NoError(t, err)
	assert.Contains(t, arn, ":cloudtrail:")
	assert.Len(t, sel, 1)

	_, gotSel, _, err := m.GetEventSelectors(ctx, "sel-trail")
	require.NoError(t, err)
	require.Len(t, gotSel, 1)
	assert.Equal(t, "WriteOnly", gotSel[0].ReadWriteType)
}

// TestNoAliasOnReads asserts Get returns deep copies: mutating a returned
// value's Tags map or nested selector slices must not affect stored state.
func TestNoAliasOnReads(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	eds, err := m.CreateEventDataStore(ctx, driver.CreateEventDataStoreInput{
		Name: "alias-eds",
		Tags: map[string]string{"k": "v"},
		AdvancedEventSelectors: []driver.AdvancedEventSelector{
			{Name: "s1", FieldSelectors: []driver.AdvancedFieldSelector{{Field: "eventCategory", Equals: []string{"Management"}}}},
		},
	})
	require.NoError(t, err)

	got, err := m.GetEventDataStore(ctx, eds.ARN)
	require.NoError(t, err)

	// Mutate the returned copy aggressively.
	got.Tags["k"] = "MUTATED"
	got.Tags["new"] = "x"
	got.AdvancedEventSelectors[0].Name = "MUTATED"
	got.AdvancedEventSelectors[0].FieldSelectors[0].Equals[0] = "MUTATED"

	fresh, err := m.GetEventDataStore(ctx, eds.ARN)
	require.NoError(t, err)
	assert.Equal(t, "v", fresh.Tags["k"])
	assert.NotContains(t, fresh.Tags, "new")
	assert.Equal(t, "s1", fresh.AdvancedEventSelectors[0].Name)
	assert.Equal(t, "Management", fresh.AdvancedEventSelectors[0].FieldSelectors[0].Equals[0])
}

// TestConcurrentCreateTrail asserts exactly one of N racing creates of the same
// name wins (SetIfAbsent atomicity). Run with -race.
func TestConcurrentCreateTrail(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	const n = 20

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		success int
	)

	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()

			if _, err := m.CreateTrail(ctx, driver.CreateTrailInput{Name: "race-trail", S3BucketName: "b"}); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, 1, success, "exactly one concurrent create should win")
}

func TestConcurrentCreateEDS(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	const n = 20

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		success int
	)

	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()

			if _, err := m.CreateEventDataStore(ctx, driver.CreateEventDataStoreInput{Name: "race-eds"}); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, 1, success, "exactly one concurrent EDS create should win")
}

func TestChannelCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	ch, err := m.CreateChannel(ctx, "ch1", "aws.partner/example",
		[]driver.Destination{{Type: "EVENT_DATA_STORE", Location: "arn:x"}}, nil)
	require.NoError(t, err)
	assert.Contains(t, ch.ARN, ":channel/")

	got, err := m.GetChannel(ctx, ch.ARN)
	require.NoError(t, err)
	assert.Equal(t, "ch1", got.Name)

	require.NoError(t, m.DeleteChannel(ctx, ch.ARN))
	_, err = m.GetChannel(ctx, ch.ARN)
	require.Error(t, err)
}

func TestResourcePolicy(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	arn := "arn:aws:cloudtrail:us-east-1:000000000000:channel/c"

	_, _, err := m.PutResourcePolicy(ctx, arn, `{"x":1}`)
	require.NoError(t, err)

	_, pol, err := m.GetResourcePolicy(ctx, arn)
	require.NoError(t, err)
	assert.Equal(t, `{"x":1}`, pol)

	require.NoError(t, m.DeleteResourcePolicy(ctx, arn))
	_, _, err = m.GetResourcePolicy(ctx, arn)
	require.Error(t, err)
}

func TestQueryLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	id, err := m.StartQuery(ctx, "eds", "", "", "SELECT * FROM x")
	require.NoError(t, err)

	q, err := m.DescribeQuery(ctx, "eds", id, "")
	require.NoError(t, err)
	assert.Equal(t, driver.QueryStatusFinished, q.Status)

	res, err := m.GetQueryResults(ctx, "eds", id, "", 0)
	require.NoError(t, err)
	assert.Empty(t, res.ResultRows)
}

func TestConcurrentStopImportAndRead(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	imp, err := m.StartImport(ctx, driver.Import{Destinations: []string{"eds-1"}, S3LocationURI: "s3://b/x"})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)

		go func() { defer wg.Done(); _, _ = m.StopImport(ctx, imp.ID) }()
		go func() { defer wg.Done(); _, _ = m.GetImport(ctx, imp.ID) }()
	}

	wg.Wait()

	got, err := m.GetImport(ctx, imp.ID)
	require.NoError(t, err)
	assert.Equal(t, driver.ImportStatusStopped, got.Status)
}

func TestConcurrentCancelQueryAndRead(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	id, err := m.StartQuery(ctx, "eds-1", "SELECT 1", "", "")
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)

		go func() { defer wg.Done(); _, _ = m.CancelQuery(ctx, "", id) }()
		go func() { defer wg.Done(); _, _ = m.DescribeQuery(ctx, "", id, "") }()
	}

	wg.Wait()

	got, err := m.DescribeQuery(ctx, "", id, "")
	require.NoError(t, err)
	assert.Equal(t, driver.QueryStatusCancelled, got.Status)
}

func TestRestoreRequiresPendingDeletion(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	noProtect := false
	eds, err := m.CreateEventDataStore(ctx, driver.CreateEventDataStoreInput{
		Name:                         "eds-restore",
		TerminationProtectionEnabled: &noProtect,
	})
	require.NoError(t, err)

	// Restoring an ENABLED (never-deleted) store is rejected with the typed
	// InvalidEventDataStoreStatusException.
	_, err = m.RestoreEventDataStore(ctx, eds.ARN)
	require.Error(t, err)

	var apiErr *driver.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, driver.ExInvalidEDSStatus, apiErr.Exception)

	// After a soft delete it can be restored, and the freed name can be reused.
	require.NoError(t, m.DeleteEventDataStore(ctx, eds.ARN))

	restored, err := m.RestoreEventDataStore(ctx, eds.ARN)
	require.NoError(t, err)
	assert.Equal(t, driver.EDSStatusEnabled, restored.Status)
}

// TestDeleteRecreateReDeleteNameIndex covers the delete -> recreate -> re-delete
// attack: a stale re-delete of the original store must not free the name claim
// now owned by the live recreated store. Exactly one live store owns the name.
func TestDeleteRecreateReDeleteNameIndex(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	noProt := false
	mk := func(name string) *driver.EventDataStore {
		eds, err := m.CreateEventDataStore(ctx, driver.CreateEventDataStoreInput{
			Name: name, TerminationProtectionEnabled: &noProt,
		})
		require.NoError(t, err)

		return eds
	}

	a := mk("foo")                                         // create foo -> A
	require.NoError(t, m.DeleteEventDataStore(ctx, a.ARN)) // delete A (frees "foo")
	b := mk("foo")                                         // create foo -> B (live)

	// Re-delete A (already PENDING_DELETION): must be a no-op and must NOT free
	// B's "foo" claim.
	require.NoError(t, m.DeleteEventDataStore(ctx, a.ARN))

	// Creating "foo" again must fail: B still owns the name.
	_, err := m.CreateEventDataStore(ctx, driver.CreateEventDataStoreInput{
		Name: "foo", TerminationProtectionEnabled: &noProt,
	})
	require.Error(t, err)

	var apiErr *driver.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, driver.ExEventDataStoreAlreadyEx, apiErr.Exception)

	// Exactly one live (non-PENDING_DELETION) store is named "foo": B.
	all, _, err := m.ListEventDataStores(ctx, "", 0)
	require.NoError(t, err)

	live := 0

	for i := range all {
		if all[i].Name == "foo" && all[i].Status != driver.EDSStatusPendingDeletion {
			live++
		}
	}

	assert.Equal(t, 1, live)

	got, err := m.GetEventDataStore(ctx, b.ARN)
	require.NoError(t, err)
	assert.Equal(t, driver.EDSStatusEnabled, got.Status)
}

// TestInactiveEDSGuards verifies update/ingestion/federation reject an inactive
// (PENDING_DELETION) store with InvalidEventDataStoreStatusException.
func TestInactiveEDSGuards(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	noProt := false
	eds, err := m.CreateEventDataStore(ctx, driver.CreateEventDataStoreInput{
		Name: "guarded", TerminationProtectionEnabled: &noProt,
	})
	require.NoError(t, err)

	require.NoError(t, m.DeleteEventDataStore(ctx, eds.ARN)) // -> PENDING_DELETION

	assertInvalidStatus := func(err error) {
		t.Helper()
		require.Error(t, err)

		var apiErr *driver.APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, driver.ExInvalidEDSStatus, apiErr.Exception)
	}

	newName := "guarded-renamed"
	_, err = m.UpdateEventDataStore(ctx, driver.UpdateEventDataStoreInput{ARN: eds.ARN, Name: &newName})
	assertInvalidStatus(err)

	assertInvalidStatus(m.StartEventDataStoreIngestion(ctx, eds.ARN))
	assertInvalidStatus(m.StopEventDataStoreIngestion(ctx, eds.ARN))

	_, _, _, err = m.EnableFederation(ctx, eds.ARN, "arn:aws:iam::000000000000:role/r")
	assertInvalidStatus(err)

	_, _, err = m.DisableFederation(ctx, eds.ARN)
	assertInvalidStatus(err)
}

// TestCreateEDSStartIngestionFalse verifies StartIngestion=false lands the store
// in STOPPED_INGESTION rather than ENABLED.
func TestCreateEDSStartIngestionFalse(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	start := false
	eds, err := m.CreateEventDataStore(ctx, driver.CreateEventDataStoreInput{
		Name: "stopped-eds", StartIngestion: &start,
	})
	require.NoError(t, err)
	assert.Equal(t, driver.EDSStatusStoppedIngestion, eds.Status)
}

// TestTagNonexistentResource verifies tagging an absent resource is
// ResourceNotFoundException, and a malformed ARN is CloudTrailARNInvalidException.
func TestTagNonexistentResource(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	absent := "arn:aws:cloudtrail:us-east-1:000000000000:eventdatastore/missing"

	err := m.AddTags(ctx, absent, map[string]string{"k": "v"})
	require.Error(t, err)

	var apiErr *driver.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, driver.ExResourceNotFound, apiErr.Exception)

	// A phantom tag must not have been created.
	tags, err := m.ListTags(ctx, []string{absent})
	require.NoError(t, err)
	assert.Empty(t, tags[absent])

	err = m.RemoveTags(ctx, absent, []string{"k"})
	require.Error(t, err)
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, driver.ExResourceNotFound, apiErr.Exception)

	err = m.AddTags(ctx, "not-an-arn", map[string]string{"k": "v"})
	require.Error(t, err)
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, driver.ExCloudTrailARNInvalid, apiErr.Exception)
}
