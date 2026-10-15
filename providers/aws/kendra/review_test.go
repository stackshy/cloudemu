package kendra_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/providers/aws/kendra"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

func TestFaqFileFormatIsEchoedOnlyWhenSent(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	s3 := driver.S3Path{Bucket: "my-bucket", Key: "k.csv"}

	none, err := m.CreateFaq(bg, &driver.CreateFaqInput{IndexID: idx.ID, Name: "a", RoleArn: roleArn, S3Path: s3})
	requireNoError(t, err)

	got, _ := m.DescribeFaq(bg, idx.ID, none.ID)
	assertEqual(t, got.FileFormat, "")

	sent, err := m.CreateFaq(bg, &driver.CreateFaqInput{IndexID: idx.ID, Name: "b", RoleArn: roleArn, S3Path: s3, FileFormat: "JSON"})
	requireNoError(t, err)
	assertEqual(t, sent.FileFormat, "JSON")

	_, err = m.CreateFaq(bg, &driver.CreateFaqInput{IndexID: idx.ID, Name: "c", RoleArn: roleArn, S3Path: s3, FileFormat: "XLS"})
	requireException(t, err, driver.ExValidation)
}

func TestQueryReturnsFeaturedResultsItems(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	putText(t, m, idx.ID, "home", "Home page", "welcome to the site")
	putText(t, m, idx.ID, "other", "Other", "vacation days")

	_, err := m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{
		IndexID: idx.ID, Name: "Vacation", QueryTexts: []string{"Vacation"}, FeaturedDocuments: []string{"home", "gone"},
	})
	requireNoError(t, err)

	inactive, err := m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{
		IndexID: idx.ID, Name: "Off", QueryTexts: []string{"holiday"}, FeaturedDocuments: []string{"home"}, Status: driver.FeaturedInactive,
	})
	requireNoError(t, err)
	_ = inactive

	out, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: " vacation "})
	requireNoError(t, err)
	assertEqual(t, len(out.FeaturedResultsItems), 1) // the missing document is skipped
	assertEqual(t, out.FeaturedResultsItems[0].DocumentID, "home")
	assertEqual(t, out.FeaturedResultsItems[0].Title.Text, "Home page")

	none, _ := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "holiday"})
	assertEqual(t, len(none.FeaturedResultsItems), 0) // an INACTIVE set features nothing

	page2, _ := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "vacation", PageNumber: 2})
	assertEqual(t, len(page2.FeaturedResultsItems), 0) // first page only
}

func TestFeaturedConflictCarriesConflictingItems(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	first, err := m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "First", QueryTexts: []string{"x"}})
	requireNoError(t, err)

	_, err = m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "Second", QueryTexts: []string{"X"}})

	var apiErr *driver.APIError
	if !isAPIError(err, &apiErr) || len(apiErr.ConflictingItems) != 1 ||
		apiErr.ConflictingItems[0].SetID != first.ID || apiErr.ConflictingItems[0].SetName != "First" {
		t.Fatalf("ConflictingItems = %+v (%v)", apiErr, err)
	}
}

func TestSnapshotKeepsQueryLogAndSyncOrder(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	putText(t, m, idx.ID, "d1", "Parking", "parking garage rules")

	one := int32(1)
	requireNoError(t, m.UpdateQuerySuggestionsConfig(bg, &driver.UpdateSuggestionsConfigInput{IndexID: idx.ID, MinimumQueryCount: &one, MinimumNumberOfQueryingUsers: &one}))

	_, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "parking"})
	requireNoError(t, err)

	ds, err := m.CreateDataSource(bg, &driver.CreateDataSourceInput{IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom})
	requireNoError(t, err)

	var execs []string

	for i := 0; i < 3; i++ {
		e, serr := m.StartDataSourceSyncJob(bg, idx.ID, ds.ID)
		requireNoError(t, serr)

		execs = append(execs, e)
	}

	data, err := m.Snapshot(bg, false)
	requireNoError(t, err)

	m2 := newMock()
	requireNoError(t, m2.Restore(bg, data))

	sug, err := m2.GetQuerySuggestions(bg, &driver.GetSuggestionsInput{IndexID: idx.ID, QueryText: "par"})
	requireNoError(t, err)
	assertEqual(t, len(sug.Suggestions), 1)

	newest, err := m2.StartDataSourceSyncJob(bg, idx.ID, ds.ID)
	requireNoError(t, err)

	jobs, _, err := m2.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID})
	requireNoError(t, err)
	assertEqual(t, jobs[0].ExecutionID, newest) // the post-restore job sorts first
	assertEqual(t, len(jobs), 4)
}

func TestExperienceUpdateHasNoUpdatingStatus(t *testing.T) {
	m, clk := newAsyncMock()
	idx := mustIndex(t, m)
	clk.Advance(time.Minute)

	e, err := m.CreateExperience(bg, &driver.CreateExperienceInput{IndexID: idx.ID, Name: "exp", RoleArn: roleArn})
	requireNoError(t, err)
	clk.Advance(time.Minute)

	name := "renamed"
	requireNoError(t, m.UpdateExperience(bg, &driver.UpdateExperienceInput{IndexID: idx.ID, ID: e.ID, Name: &name}))

	got, err := m.DescribeExperience(bg, idx.ID, e.ID)
	requireNoError(t, err)
	assertEqual(t, got.Status, driver.ChildStatusActive)
}

func TestRunningSyncJobHasNoEndTime(t *testing.T) {
	m, clk := newAsyncMock()
	idx := mustIndex(t, m)
	clk.Advance(time.Minute)

	ds, _ := m.CreateDataSource(bg, &driver.CreateDataSourceInput{IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom})
	clk.Advance(time.Minute)

	_, err := m.StartDataSourceSyncJob(bg, idx.ID, ds.ID)
	requireNoError(t, err)

	jobs, _, _ := m.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID})
	assertEqual(t, jobs[0].Status, driver.SyncStatusSyncing)

	if !jobs[0].EndTime.IsZero() {
		t.Fatalf("a running job must have no EndTime, got %v", jobs[0].EndTime)
	}

	clk.Advance(time.Minute)

	jobs, _, _ = m.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID})
	if jobs[0].Status != driver.SyncStatusSucceeded || jobs[0].EndTime.IsZero() {
		t.Fatalf("a finished job has an EndTime: %+v", jobs[0])
	}
}

func TestDocumentIdsDoNotShareSettleWindowsWithOtherResources(t *testing.T) {
	m, clk := newAsyncMock()
	idx := mustIndex(t, m)
	clk.Advance(time.Minute)

	// A document called "suggestions" must not put the suggestions config into a
	// settle window (UpdateQuerySuggestionsConfig would then conflict).
	putText(t, m, idx.ID, "suggestions", "T", "text")

	one := int32(1)
	requireNoError(t, m.UpdateQuerySuggestionsConfig(bg, &driver.UpdateSuggestionsConfigInput{IndexID: idx.ID, MinimumQueryCount: &one}))

	// Deleting it must not clear the suggestions window either.
	_, err := m.BatchDeleteDocument(bg, &driver.DeleteDocumentsInput{IndexID: idx.ID, DocumentIDs: []string{"suggestions"}})
	requireNoError(t, err)
	requireException(t, m.UpdateQuerySuggestionsConfig(bg, &driver.UpdateSuggestionsConfigInput{IndexID: idx.ID, MinimumQueryCount: &one}), driver.ExConflict)
}

func TestDeletesClearDependentState(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	putText(t, m, idx.ID, "d1", "Parking", "parking garage rules")

	_, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "parking"})
	requireNoError(t, err)

	ds, err := m.CreateDataSource(bg, &driver.CreateDataSourceInput{IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom})
	requireNoError(t, err)

	requireNoError(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{
		IndexID: idx.ID, DataSourceID: ds.ID, GroupID: "eng", GroupMembers: []byte(`{"MemberUsers":{"MemberUsers":[{"UserId":"u"}]}}`),
		OrderingID: i64(1),
	}))
	requireNoError(t, m.DeleteDataSource(bg, idx.ID, ds.ID))

	// The data source's principal mappings went with it.
	_, err = m.DescribePrincipalMapping(bg, idx.ID, ds.ID, "eng")
	requireException(t, err, driver.ExResourceNotFound)

	data, err := m.Snapshot(bg, false)
	requireNoError(t, err)

	if strings.Contains(string(data), `"eng"`) {
		t.Fatalf("snapshot still holds the deleted data source's mapping: %s", data)
	}

	requireNoError(t, m.DeleteIndex(bg, idx.ID))

	data, _ = m.Snapshot(bg, false)
	if strings.Contains(string(data), "queryLog") {
		t.Fatalf("deleting the index must clear its query log: %s", data)
	}
}

func TestFilterIsValidatedOnAnEmptyIndex(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	_, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "x", AttributeFilter: &driver.AttributeFilter{}})
	requireException(t, err, driver.ExValidation)
}

func TestBatchPutNeedsBlobOrS3Path(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	failed, err := m.BatchPutDocument(bg, &driver.PutDocumentsInput{IndexID: idx.ID, Documents: []driver.PutDocument{{ID: "empty", Title: "t"}}})
	requireNoError(t, err)
	assertEqual(t, len(failed), 1)
	assertEqual(t, failed[0].ID, "empty")
}

func TestGetQuerySuggestionsRequiresQueryText(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	_, err := m.GetQuerySuggestions(bg, &driver.GetSuggestionsInput{IndexID: idx.ID})
	requireException(t, err, driver.ExValidation)
}

func TestQueryOverLargeDocumentsKeepsTheMatchInTheExcerpt(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	body := strings.Repeat("lorem ipsum dolor sit amet ", 150_000) + " needle " + strings.Repeat("more filler words ", 10_000)

	for _, id := range []string{"a", "b", "c"} {
		putText(t, m, idx.ID, id, "Large "+id, body)
	}

	out, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "needle"})
	requireNoError(t, err)
	assertEqual(t, len(out.Items), 3)

	if !strings.Contains(out.Items[0].Excerpt.Text, "needle") || len(out.Items[0].Excerpt.Highlights) == 0 {
		t.Fatalf("excerpt must hold the match: %+v", out.Items[0].Excerpt)
	}
}

var _ = kendra.New

func TestExcerptKeepsTheOriginalCaseWhenACharacterChangesWidth(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	putText(t, m, idx.ID, "trip", "Trip", "Trip report: İstanbul OFFICE visit, Budget APPROVED by Finance.")

	out, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "budget"})
	requireNoError(t, err)
	assertEqual(t, len(out.Items), 1)
	assertEqual(t, out.Items[0].Excerpt.Text, "Trip report: İstanbul OFFICE visit, Budget APPROVED by Finance.")

	h := out.Items[0].Excerpt.Highlights
	assertEqual(t, len(h), 1)

	if got := string([]rune(out.Items[0].Excerpt.Text)[h[0].BeginOffset:h[0].EndOffset]); got != "Budget" {
		t.Fatalf("highlight covers %q, want Budget", got)
	}
}

func TestFeaturedDocumentIsNotRepeatedInResultItems(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	putText(t, m, idx.ID, "doc1", "Policy", "vacation policy for staff")
	putText(t, m, idx.ID, "doc2", "Handbook", "vacation policy handbook")

	_, err := m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{
		IndexID: idx.ID, Name: "F", QueryTexts: []string{"vacation policy"}, FeaturedDocuments: []string{"doc2"},
	})
	requireNoError(t, err)

	out, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "vacation policy"})
	requireNoError(t, err)
	assertEqual(t, len(out.FeaturedResultsItems), 1)
	assertEqual(t, len(out.Items), 1)
	assertEqual(t, out.Items[0].DocumentID, "doc1")
	assertEqual(t, out.Total, int32(1))
}

func TestFeaturedConflictListsEveryConflictingQuery(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	_, err := m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "S3", QueryTexts: []string{"a", "b"}})
	requireNoError(t, err)

	_, err = m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "S4", QueryTexts: []string{"a", "B", "c"}})

	var apiErr *driver.APIError
	if !isAPIError(err, &apiErr) || len(apiErr.ConflictingItems) != 2 {
		t.Fatalf("want both conflicting queries, got %+v (%v)", apiErr, err)
	}
}
