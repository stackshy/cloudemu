package kendra_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kendra"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

var bg = context.Background()

func str(s string) *string { return &s }

func i64(n int64) *int64 { return &n }

// newAsyncMock returns a mock with async settling on and the clock it uses.
func newAsyncMock() (*kendra.Mock, *config.FakeClock) {
	clk := config.NewFakeClock(time.Unix(1_700_000_000, 0))

	return kendra.New(config.NewOptions(config.WithClock(clk), config.WithAsyncSettle())), clk
}

func mustIndex(t *testing.T, m *kendra.Mock) *driver.Index {
	t.Helper()

	return createIndex(t, m)
}

func putText(t *testing.T, m *kendra.Mock, indexID, id, title, text string, attrs ...driver.DocumentAttribute) {
	t.Helper()

	failed, err := m.BatchPutDocument(bg, &driver.PutDocumentsInput{IndexID: indexID, Documents: []driver.PutDocument{{
		ID: id, Title: title, Blob: []byte(text), ContentType: "PLAIN_TEXT", Attributes: attrs,
	}}})
	requireNoError(t, err)
	assertEqual(t, len(failed), 0)
}

func strAttr(key, val string) driver.DocumentAttribute {
	return driver.DocumentAttribute{Key: key, Value: driver.DocumentAttributeValue{StringValue: &val}}
}

func longAttr(key string, n int64) driver.DocumentAttribute {
	return driver.DocumentAttribute{Key: key, Value: driver.DocumentAttributeValue{LongValue: &n}}
}

func TestMalformedIdsAreValidationExceptions(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	for _, bad := range []string{"", "short", strings.Repeat("a", 37), "-0000000-0000-4000-8000-000000000000", "0000000_-0000-4000-8000-000000000000"} {
		_, err := m.DescribeIndex(bg, bad)
		requireException(t, err, driver.ExValidation)

		requireException(t, m.DeleteIndex(bg, bad), driver.ExValidation)

		_, _, err = m.ListDataSources(bg, bad, driver.Page{})
		requireException(t, err, driver.ExValidation)

		_, err = m.CreateDataSource(bg, &driver.CreateDataSourceInput{IndexID: bad, Name: "ds", Type: driver.DataSourceTypeCustom})
		requireException(t, err, driver.ExValidation)
	}

	_, err := m.DescribeDataSource(bg, idx.ID, "bad id!")
	requireException(t, err, driver.ExValidation)

	_, err = m.DescribeDataSource(bg, idx.ID, strings.Repeat("a", 101))
	requireException(t, err, driver.ExValidation)

	// A well-formed id that does not exist is still ResourceNotFound.
	_, err = m.DescribeIndex(bg, missingIndexID)
	requireException(t, err, driver.ExResourceNotFound)

	_, err = m.DescribeDataSource(bg, idx.ID, "abc123")
	requireException(t, err, driver.ExResourceNotFound)
}

func TestNameValidation(t *testing.T) {
	m := newMock()

	for _, bad := range []string{"", "-lead", "has space", "dot.name", "semi;colon", strings.Repeat("a", 1001)} {
		_, err := m.CreateIndex(bg, &driver.CreateIndexInput{Name: bad, RoleArn: roleArn})
		requireException(t, err, driver.ExValidation)
	}

	for _, good := range []string{"a", "Docs_1-x", strings.Repeat("a", 1000)} {
		_, err := m.CreateIndex(bg, &driver.CreateIndexInput{Name: good, RoleArn: roleArn})
		requireNoError(t, err)
	}

	idx := mustIndex(t, m)
	bad := "has space"
	requireException(t, m.UpdateIndex(bg, &driver.UpdateIndexInput{ID: idx.ID, Name: &bad}), driver.ExValidation)

	_, err := m.CreateDataSource(bg, &driver.CreateDataSourceInput{IndexID: idx.ID, Name: "bad name", Type: driver.DataSourceTypeCustom})
	requireException(t, err, driver.ExValidation)
}

func TestPaginationTokensAreOpaqueAndScoped(t *testing.T) {
	m := newMock()
	for range 3 {
		mustIndex(t, m)
	}

	seen := map[string]bool{}
	token := ""

	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatalf("pagination did not terminate")
		}

		list, next, err := m.ListIndices(bg, driver.Page{MaxResults: 1, NextToken: token})
		requireNoError(t, err)

		for _, i := range list {
			seen[i.ID] = true
		}

		if next == "" {
			break
		}

		if next == "1" || next == "2" {
			t.Fatalf("token %q is a predictable numeric offset", next)
		}

		token = next
	}

	assertEqual(t, len(seen), 3)

	_, next, err := m.ListIndices(bg, driver.Page{MaxResults: 1})
	requireNoError(t, err)

	for _, bad := range []string{"1", "garbage", next + "x", strings.Repeat("a", 801)} {
		_, _, err = m.ListIndices(bg, driver.Page{NextToken: bad})
		requireException(t, err, driver.ExValidation)
	}

	// A token minted for one list is not valid on another.
	idx := mustIndex(t, m)
	_, _, err = m.ListDataSources(bg, idx.ID, driver.Page{NextToken: next})
	requireException(t, err, driver.ExValidation)

	for _, n := range []int32{-1, 101} {
		_, _, err = m.ListIndices(bg, driver.Page{MaxResults: n})
		requireException(t, err, driver.ExValidation)
	}
}

func TestAsyncSettlingRaisesConflictUntilActive(t *testing.T) {
	m, clk := newAsyncMock()
	idx := mustIndex(t, m)
	assertEqual(t, idx.Status, driver.IndexStatusCreating)

	got, err := m.DescribeIndex(bg, idx.ID)
	requireNoError(t, err)
	assertEqual(t, got.Status, driver.IndexStatusCreating)

	name := "renamed"
	requireException(t, m.UpdateIndex(bg, &driver.UpdateIndexInput{ID: idx.ID, Name: &name}), driver.ExConflict)
	requireException(t, m.DeleteIndex(bg, idx.ID), driver.ExConflict)

	_, err = m.CreateDataSource(bg, &driver.CreateDataSourceInput{IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom})
	requireException(t, err, driver.ExConflict)

	_, err = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "x"})
	requireException(t, err, driver.ExConflict)

	clk.Advance(time.Minute)

	got, err = m.DescribeIndex(bg, idx.ID)
	requireNoError(t, err)
	assertEqual(t, got.Status, driver.IndexStatusActive)

	ds, err := m.CreateDataSource(bg, &driver.CreateDataSourceInput{IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom})
	requireNoError(t, err)
	assertEqual(t, ds.Status, driver.DataSourceStatusCreating)
	requireException(t, m.DeleteDataSource(bg, idx.ID, ds.ID), driver.ExConflict)

	clk.Advance(time.Minute)
	requireNoError(t, m.UpdateIndex(bg, &driver.UpdateIndexInput{ID: idx.ID, Name: &name}))

	got, _ = m.DescribeIndex(bg, idx.ID)
	assertEqual(t, got.Status, driver.IndexStatusUpdating)
}

func TestIndexStatisticsAndErrorMessage(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	got, err := m.DescribeIndex(bg, idx.ID)
	requireNoError(t, err)

	if got.Statistics == nil || got.Statistics.IndexedTextDocuments != 0 {
		t.Fatalf("a new index must report zeroed statistics, got %+v", got.Statistics)
	}

	putText(t, m, idx.ID, "a", "A", "hello world")
	putText(t, m, idx.ID, "b", "B", "second doc")

	got, _ = m.DescribeIndex(bg, idx.ID)
	assertEqual(t, got.Statistics.IndexedTextDocuments, int32(2))
	assertEqual(t, got.Statistics.IndexedTextBytes, int64(len("hello world")+len("second doc")))
	assertEqual(t, got.ErrorMessage, "")
}

func TestBatchPutDocumentLimitsAndStatus(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	_, err := m.BatchPutDocument(bg, &driver.PutDocumentsInput{IndexID: idx.ID})
	requireException(t, err, driver.ExValidation)

	many := make([]driver.PutDocument, 11)
	for i := range many {
		many[i] = driver.PutDocument{ID: fmt.Sprint(i), Blob: []byte("x")}
	}

	_, err = m.BatchPutDocument(bg, &driver.PutDocumentsInput{IndexID: idx.ID, Documents: many})
	requireException(t, err, driver.ExValidation)

	_, err = m.BatchPutDocument(bg, &driver.PutDocumentsInput{IndexID: idx.ID, Documents: []driver.PutDocument{{ID: ""}}})
	requireException(t, err, driver.ExValidation)

	_, err = m.BatchPutDocument(bg, &driver.PutDocumentsInput{IndexID: idx.ID, Documents: []driver.PutDocument{{ID: "a", ContentType: "EXE"}}})
	requireException(t, err, driver.ExValidation)

	_, err = m.BatchPutDocument(bg, &driver.PutDocumentsInput{IndexID: missingIndexID, Documents: many[:1]})
	requireException(t, err, driver.ExResourceNotFound)

	putText(t, m, idx.ID, "doc1", "Title", "body text")

	res, err := m.BatchGetDocumentStatus(bg, idx.ID, []driver.DocumentInfo{{DocumentID: "doc1"}, {DocumentID: "nope"}})
	requireNoError(t, err)
	assertEqual(t, res.Statuses[0].Status, driver.DocStatusIndexed)
	assertEqual(t, res.Statuses[1].Status, driver.DocStatusNotFound)

	// Putting the same id again replaces it and reports UPDATED.
	putText(t, m, idx.ID, "doc1", "Title", "new body")

	res, _ = m.BatchGetDocumentStatus(bg, idx.ID, []driver.DocumentInfo{{DocumentID: "doc1"}})
	assertEqual(t, res.Statuses[0].Status, driver.DocStatusUpdated)

	failed, err := m.BatchDeleteDocument(bg, &driver.DeleteDocumentsInput{IndexID: idx.ID, DocumentIDs: []string{"doc1", "never-existed"}})
	requireNoError(t, err)
	assertEqual(t, len(failed), 0)

	res, _ = m.BatchGetDocumentStatus(bg, idx.ID, []driver.DocumentInfo{{DocumentID: "doc1"}})
	assertEqual(t, res.Statuses[0].Status, driver.DocStatusNotFound)

	_, err = m.BatchDeleteDocument(bg, &driver.DeleteDocumentsInput{IndexID: idx.ID})
	requireException(t, err, driver.ExValidation)

	_, err = m.BatchGetDocumentStatus(bg, idx.ID, nil)
	requireException(t, err, driver.ExValidation)
}

func TestBatchPutDocumentExtractedTextLimit(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	big := []byte(strings.Repeat("a", 5<<20+1))

	failed, err := m.BatchPutDocument(bg, &driver.PutDocumentsInput{IndexID: idx.ID, Documents: []driver.PutDocument{
		{ID: "big", Blob: big, ContentType: "PLAIN_TEXT"}, {ID: "ok", Blob: []byte("fine"), ContentType: "PLAIN_TEXT"},
	}})
	requireNoError(t, err)
	assertEqual(t, len(failed), 1)
	assertEqual(t, failed[0].ID, "big")
	assertEqual(t, failed[0].ErrorCode, driver.ErrCodeInvalidRequest)

	res, _ := m.BatchGetDocumentStatus(bg, idx.ID, []driver.DocumentInfo{{DocumentID: "big"}, {DocumentID: "ok"}})
	assertEqual(t, res.Statuses[0].Status, driver.DocStatusNotFound)
	assertEqual(t, res.Statuses[1].Status, driver.DocStatusIndexed)
}

func TestDocumentStatusProcessesUnderAsyncSettle(t *testing.T) {
	m, clk := newAsyncMock()
	idx := mustIndex(t, m)
	clk.Advance(time.Minute)

	putText(t, m, idx.ID, "d", "T", "text")

	res, _ := m.BatchGetDocumentStatus(bg, idx.ID, []driver.DocumentInfo{{DocumentID: "d"}})
	assertEqual(t, res.Statuses[0].Status, driver.DocStatusProcessing)

	clk.Advance(time.Minute)

	res, _ = m.BatchGetDocumentStatus(bg, idx.ID, []driver.DocumentInfo{{DocumentID: "d"}})
	assertEqual(t, res.Statuses[0].Status, driver.DocStatusIndexed)
}

func TestQueryRankingExcerptAndPaging(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	putText(t, m, idx.ID, "vacation", "Vacation policy", "Employees accrue vacation days every month. Vacation requests need approval.")
	putText(t, m, idx.ID, "expenses", "Expense policy", "Submit expenses within thirty days. Vacation travel is not reimbursed.")
	putText(t, m, idx.ID, "other", "Parking", "Parking is available in lot B.")

	out, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "what is the vacation policy"})
	requireNoError(t, err)
	assertEqual(t, out.Total, int32(2))
	assertEqual(t, out.Items[0].DocumentID, "vacation")
	assertEqual(t, out.Items[0].Type, driver.ResultTypeDocument)
	assertEqual(t, out.Items[0].Score, driver.ScoreVeryHigh)
	assertEqual(t, out.Items[1].DocumentID, "expenses")

	if len(out.Items[0].Excerpt.Highlights) == 0 || !strings.Contains(strings.ToLower(out.Items[0].Excerpt.Text), "vacation") {
		t.Fatalf("excerpt must contain and highlight the match, got %+v", out.Items[0].Excerpt)
	}

	for _, h := range out.Items[0].Excerpt.Highlights {
		got := strings.ToLower(string([]rune(out.Items[0].Excerpt.Text)[h.BeginOffset:h.EndOffset]))
		if got != "vacation" && got != "policy" {
			t.Fatalf("highlight %d-%d covers %q", h.BeginOffset, h.EndOffset, got)
		}
	}

	// Page size 1 walks the result list.
	p1, _ := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "vacation", PageSize: 1, PageNumber: 1})
	p2, _ := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "vacation", PageSize: 1, PageNumber: 2})
	p3, _ := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "vacation", PageSize: 1, PageNumber: 3})
	assertEqual(t, len(p1.Items), 1)
	assertEqual(t, len(p2.Items), 1)
	assertEqual(t, len(p3.Items), 0)

	if p1.Items[0].DocumentID == p2.Items[0].DocumentID {
		t.Fatalf("pages must not repeat a result")
	}

	_, err = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "x", PageNumber: -1})
	requireException(t, err, driver.ExValidation)

	_, err = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "x", ResultTypeFilter: "NOPE"})
	requireException(t, err, driver.ExValidation)

	qa, _ := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "vacation", ResultTypeFilter: driver.ResultTypeQuestionAnswer})
	assertEqual(t, len(qa.Items), 0)

	none, _ := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "zebra"})
	assertEqual(t, none.Total, int32(0))
}

func TestQueryAttributeFiltersFacetsAndSorting(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	putText(t, m, idx.ID, "a", "Alpha report", "quarterly report", strAttr("dept", "hr"), longAttr("year", 2023))
	putText(t, m, idx.ID, "b", "Beta report", "quarterly report", strAttr("dept", "eng"), longAttr("year", 2024))
	putText(t, m, idx.ID, "c", "Gamma report", "quarterly report", strAttr("dept", "eng"), longAttr("year", 2022))

	eq := func(k, v string) *driver.AttributeFilter {
		a := strAttr(k, v)

		return &driver.AttributeFilter{EqualsTo: &a}
	}

	ids := func(out *driver.QueryOutput) string {
		var s []string
		for _, it := range out.Items {
			s = append(s, it.DocumentID)
		}

		return strings.Join(s, ",")
	}

	run := func(f *driver.AttributeFilter) string {
		out, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "report", AttributeFilter: f})
		requireNoError(t, err)

		return ids(out)
	}

	assertEqual(t, run(eq("dept", "eng")), "b,c")

	year := longAttr("year", 2023)
	assertEqual(t, run(&driver.AttributeFilter{GreaterThan: &year}), "b")
	assertEqual(t, run(&driver.AttributeFilter{GreaterThanOrEquals: &year}), "a,b")
	assertEqual(t, run(&driver.AttributeFilter{LessThan: &year}), "c")

	assertEqual(t, run(&driver.AttributeFilter{AndAll: []driver.AttributeFilter{*eq("dept", "eng"), {GreaterThan: &year}}}), "b")
	assertEqual(t, run(&driver.AttributeFilter{OrAll: []driver.AttributeFilter{*eq("dept", "hr"), {LessThan: &year}}}), "a,c")
	assertEqual(t, run(&driver.AttributeFilter{Not: eq("dept", "eng")}), "a")

	// Filter members: none or two set is invalid, and a comparison needs the right type.
	_, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "report", AttributeFilter: &driver.AttributeFilter{}})
	requireException(t, err, driver.ExValidation)

	both := strAttr("dept", "hr")
	_, err = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "report", AttributeFilter: &driver.AttributeFilter{EqualsTo: &both, GreaterThan: &year}})
	requireException(t, err, driver.ExValidation)

	_, err = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "report", AttributeFilter: &driver.AttributeFilter{GreaterThan: &both}})
	requireException(t, err, driver.ExValidation)

	// Sorting by an attribute descending.
	out, _ := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "report", Sorting: []driver.SortingConfig{{DocumentAttributeKey: "year", SortOrder: "DESC"}}})
	assertEqual(t, ids(out), "b,a,c")

	// Facets count the values of the matched documents, most common first.
	out, _ = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "report", Facets: []driver.Facet{{DocumentAttributeKey: "dept"}}})
	assertEqual(t, len(out.Facets), 1)
	assertEqual(t, *out.Facets[0].Counts[0].Value.StringValue, "eng")
	assertEqual(t, out.Facets[0].Counts[0].Count, int32(2))

	// Requested attributes narrow what each result carries.
	out, _ = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "alpha", RequestedAttributes: []string{"dept"}})
	assertEqual(t, len(out.Items[0].Attributes), 1)
	assertEqual(t, out.Items[0].Attributes[0].Key, "dept")

	_, err = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "x", RequestedAttributes: []string{"bad key!"}})
	requireException(t, err, driver.ExValidation)

	// An empty query returns the documents that pass the filter.
	out, err = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, AttributeFilter: eq("dept", "eng")})
	requireNoError(t, err)
	assertEqual(t, ids(out), "b,c")
}

func TestRetrievePassages(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	long := strings.Repeat("filler ", 250) + "needle appears here"
	putText(t, m, idx.ID, "doc", "Long doc", long)

	out, err := m.Retrieve(bg, &driver.RetrieveInput{IndexID: idx.ID, QueryText: "needle"})
	requireNoError(t, err)
	assertEqual(t, len(out.Items), 1)
	assertEqual(t, out.Items[0].DocumentID, "doc")

	if !strings.Contains(out.Items[0].Content, "needle") || len(strings.Fields(out.Items[0].Content)) > 200 {
		t.Fatalf("passage must hold the match and at most 200 tokens, got %d tokens", len(strings.Fields(out.Items[0].Content)))
	}

	_, err = m.Retrieve(bg, &driver.RetrieveInput{IndexID: idx.ID})
	requireException(t, err, driver.ExValidation)
}

func TestSyncJobLifecycle(t *testing.T) {
	m, clk := newAsyncMock()
	idx := mustIndex(t, m)
	clk.Advance(time.Minute)

	ds, err := m.CreateDataSource(bg, &driver.CreateDataSourceInput{IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom})
	requireNoError(t, err)
	clk.Advance(time.Minute)

	exec, err := m.StartDataSourceSyncJob(bg, idx.ID, ds.ID)
	requireNoError(t, err)

	// A second start while one runs, and deleting the data source, are ResourceInUse.
	_, err = m.StartDataSourceSyncJob(bg, idx.ID, ds.ID)
	requireException(t, err, driver.ExResourceInUse)
	requireException(t, m.DeleteDataSource(bg, idx.ID, ds.ID), driver.ExResourceInUse)

	jobs, _, err := m.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID})
	requireNoError(t, err)
	assertEqual(t, len(jobs), 1)
	assertEqual(t, jobs[0].ExecutionID, exec)
	assertEqual(t, jobs[0].Status, driver.SyncStatusSyncing)

	requireNoError(t, m.StopDataSourceSyncJob(bg, idx.ID, ds.ID))

	jobs, _, _ = m.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID})
	assertEqual(t, jobs[0].Status, driver.SyncStatusAborted)

	// Stopping with nothing running is a no-op.
	requireNoError(t, m.StopDataSourceSyncJob(bg, idx.ID, ds.ID))

	exec2, err := m.StartDataSourceSyncJob(bg, idx.ID, ds.ID)
	requireNoError(t, err)
	clk.Advance(time.Minute)

	jobs, _, _ = m.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID})
	assertEqual(t, len(jobs), 2)
	assertEqual(t, jobs[0].ExecutionID, exec2)
	assertEqual(t, jobs[0].Status, driver.SyncStatusSucceeded)
	assertEqual(t, jobs[0].Metrics.DocumentsScanned, "0")

	filtered, _, _ := m.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID, StatusFilter: driver.SyncStatusAborted})
	assertEqual(t, len(filtered), 1)
	assertEqual(t, filtered[0].ExecutionID, exec)

	_, _, err = m.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID, StatusFilter: "BOGUS"})
	requireException(t, err, driver.ExValidation)

	_, _, err = m.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID, Page: driver.Page{MaxResults: 11}})
	requireException(t, err, driver.ExValidation)

	_, err = m.StartDataSourceSyncJob(bg, idx.ID, "missingds")
	requireException(t, err, driver.ExResourceNotFound)

	requireNoError(t, m.DeleteDataSource(bg, idx.ID, ds.ID))

	_, _, err = m.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID})
	requireException(t, err, driver.ExResourceNotFound)
}

func TestFaqLifecycleAndValidation(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	in := func() *driver.CreateFaqInput {
		return &driver.CreateFaqInput{
			IndexID: idx.ID, Name: "faq-1", RoleArn: roleArn, S3Path: driver.S3Path{Bucket: "my-bucket", Key: "faq.csv"},
			ClientToken: "tok", Tags: []driver.Tag{{Key: "k", Value: "v"}},
		}
	}

	f, err := m.CreateFaq(bg, in())
	requireNoError(t, err)
	assertEqual(t, f.FileFormat, "CSV")
	assertEqual(t, f.LanguageCode, "en")
	assertEqual(t, f.Status, driver.ChildStatusActive)

	again, err := m.CreateFaq(bg, in())
	requireNoError(t, err)
	assertEqual(t, again.ID, f.ID)

	got, err := m.DescribeFaq(bg, idx.ID, f.ID)
	requireNoError(t, err)
	assertEqual(t, got.Name, "faq-1")

	list, _, err := m.ListFaqs(bg, idx.ID, driver.Page{})
	requireNoError(t, err)
	assertEqual(t, len(list), 1)

	arn := "arn:aws:kendra:us-east-1:000000000000:index/" + idx.ID + "/faq/" + f.ID
	tags, err := m.ListTagsForResource(bg, arn)
	requireNoError(t, err)
	assertEqual(t, tagValue(tags, "k"), "v")
	requireNoError(t, m.TagResource(bg, arn, []driver.Tag{{Key: "k2", Value: "v2"}}))

	tags, _ = m.ListTagsForResource(bg, arn)
	assertEqual(t, len(tags), 2)

	for _, mutate := range []func(*driver.CreateFaqInput){
		func(c *driver.CreateFaqInput) { c.Name = "" },
		func(c *driver.CreateFaqInput) { c.RoleArn = "nope" },
		func(c *driver.CreateFaqInput) { c.S3Path.Bucket = "ab" },
		func(c *driver.CreateFaqInput) { c.S3Path.Key = "" },
		func(c *driver.CreateFaqInput) { c.FileFormat = "XML" },
		func(c *driver.CreateFaqInput) { c.ClientToken = strings.Repeat("t", 101) },
	} {
		c := in()
		c.ClientToken = ""
		mutate(c)
		_, err = m.CreateFaq(bg, c)
		requireException(t, err, driver.ExValidation)
	}

	_, err = m.DescribeFaq(bg, idx.ID, "bad id")
	requireException(t, err, driver.ExValidation)

	_, err = m.DescribeFaq(bg, idx.ID, "nosuchfaq")
	requireException(t, err, driver.ExResourceNotFound)

	requireNoError(t, m.DeleteFaq(bg, idx.ID, f.ID))
	requireException(t, m.DeleteFaq(bg, idx.ID, f.ID), driver.ExResourceNotFound)

	_, err = m.CreateFaq(bg, &driver.CreateFaqInput{IndexID: missingIndexID, Name: "f", RoleArn: roleArn, S3Path: driver.S3Path{Bucket: "abc", Key: "k"}})
	requireException(t, err, driver.ExResourceNotFound)
}

func TestThesaurusAndBlockListLifecycle(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	src := driver.S3Path{Bucket: "my-bucket", Key: "syn.txt"}

	th, err := m.CreateThesaurus(bg, &driver.CreateThesaurusInput{IndexID: idx.ID, Name: "syn", RoleArn: roleArn, SourceS3Path: src, ClientToken: "t"})
	requireNoError(t, err)

	again, _ := m.CreateThesaurus(bg, &driver.CreateThesaurusInput{IndexID: idx.ID, Name: "syn", RoleArn: roleArn, SourceS3Path: src, ClientToken: "t"})
	assertEqual(t, again.ID, th.ID)

	newName, desc := "syn2", "updated"
	requireNoError(t, m.UpdateThesaurus(bg, &driver.UpdateThesaurusInput{IndexID: idx.ID, ID: th.ID, Name: &newName, Description: &desc}))

	got, _ := m.DescribeThesaurus(bg, idx.ID, th.ID)
	assertEqual(t, got.Name, "syn2")
	assertEqual(t, got.Description, "updated")
	assertEqual(t, got.RoleArn, roleArn)

	bad := "nope"
	requireException(t, m.UpdateThesaurus(bg, &driver.UpdateThesaurusInput{IndexID: idx.ID, ID: th.ID, RoleArn: &bad}), driver.ExValidation)
	requireException(t, m.UpdateThesaurus(bg, &driver.UpdateThesaurusInput{IndexID: idx.ID, ID: "nosuch"}), driver.ExResourceNotFound)

	_, err = m.CreateThesaurus(bg, &driver.CreateThesaurusInput{IndexID: idx.ID, Name: "x y", RoleArn: roleArn, SourceS3Path: src})
	requireException(t, err, driver.ExValidation)

	tlist, _, _ := m.ListThesauri(bg, idx.ID, driver.Page{})
	assertEqual(t, len(tlist), 1)
	requireNoError(t, m.DeleteThesaurus(bg, idx.ID, th.ID))

	bl, err := m.CreateQuerySuggestionsBlockList(bg, &driver.CreateBlockListInput{IndexID: idx.ID, Name: "blk", RoleArn: roleArn, SourceS3Path: src})
	requireNoError(t, err)

	gotBL, err := m.DescribeQuerySuggestionsBlockList(bg, idx.ID, bl.ID)
	requireNoError(t, err)
	assertEqual(t, gotBL.Name, "blk")
	assertEqual(t, gotBL.Status, driver.ChildStatusActive)

	nm := "blk2"
	requireNoError(t, m.UpdateQuerySuggestionsBlockList(bg, &driver.UpdateBlockListInput{IndexID: idx.ID, ID: bl.ID, Name: &nm}))

	blists, _, _ := m.ListQuerySuggestionsBlockLists(bg, idx.ID, driver.Page{})
	assertEqual(t, blists[0].Name, "blk2")

	arn := "arn:aws:kendra:us-east-1:000000000000:index/" + idx.ID + "/query-suggestions-block-list/" + bl.ID
	requireNoError(t, m.TagResource(bg, arn, []driver.Tag{{Key: "a", Value: "b"}}))

	requireNoError(t, m.DeleteQuerySuggestionsBlockList(bg, idx.ID, bl.ID))
	requireException(t, m.DeleteQuerySuggestionsBlockList(bg, idx.ID, bl.ID), driver.ExResourceNotFound)
}

func TestExperienceAndAccessControlLifecycle(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	e, err := m.CreateExperience(bg, &driver.CreateExperienceInput{
		IndexID: idx.ID, Name: "exp", RoleArn: roleArn, Configuration: []byte(`{"UserIdentityConfiguration":{"IdentityAttributeName":"mail"}}`), ClientToken: "e",
	})
	requireNoError(t, err)

	again, _ := m.CreateExperience(bg, &driver.CreateExperienceInput{IndexID: idx.ID, Name: "exp", ClientToken: "e"})
	assertEqual(t, again.ID, e.ID)

	got, err := m.DescribeExperience(bg, idx.ID, e.ID)
	requireNoError(t, err)
	assertEqual(t, len(got.Endpoints), 1)
	assertEqual(t, got.Endpoints[0].EndpointType, "HOME")

	if !strings.Contains(string(got.Configuration), "IdentityAttributeName") {
		t.Fatalf("configuration must round-trip verbatim, got %s", got.Configuration)
	}

	nm := "exp2"
	requireNoError(t, m.UpdateExperience(bg, &driver.UpdateExperienceInput{IndexID: idx.ID, ID: e.ID, Name: &nm}))

	got, _ = m.DescribeExperience(bg, idx.ID, e.ID)
	assertEqual(t, got.Name, "exp2")
	assertEqual(t, len(got.Configuration) > 0, true)

	list, _, _ := m.ListExperiences(bg, idx.ID, driver.Page{})
	assertEqual(t, len(list), 1)
	requireNoError(t, m.DeleteExperience(bg, idx.ID, e.ID))
	requireException(t, m.DeleteExperience(bg, idx.ID, e.ID), driver.ExResourceNotFound)

	a, err := m.CreateAccessControlConfiguration(bg, &driver.CreateAccessControlInput{
		IndexID: idx.ID, Name: "acl", AccessControlList: []byte(`[{"Name":"u","Type":"USER","Access":"ALLOW"}]`),
	})
	requireNoError(t, err)

	gotA, err := m.DescribeAccessControlConfiguration(bg, idx.ID, a.ID)
	requireNoError(t, err)
	assertEqual(t, gotA.Name, "acl")

	desc := "d"
	requireNoError(t, m.UpdateAccessControlConfiguration(bg, &driver.UpdateAccessControlInput{IndexID: idx.ID, ID: a.ID, Description: &desc}))

	gotA, _ = m.DescribeAccessControlConfiguration(bg, idx.ID, a.ID)
	assertEqual(t, gotA.Description, "d")
	assertEqual(t, len(gotA.AccessControlList) > 0, true)

	alist, _, _ := m.ListAccessControlConfigurations(bg, idx.ID, driver.Page{})
	assertEqual(t, len(alist), 1)
	requireNoError(t, m.DeleteAccessControlConfiguration(bg, idx.ID, a.ID))

	_, err = m.CreateAccessControlConfiguration(bg, &driver.CreateAccessControlInput{IndexID: idx.ID, Name: "bad name"})
	requireException(t, err, driver.ExValidation)
}

func TestFeaturedResultsSets(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	putText(t, m, idx.ID, "home", "Home page", "welcome")

	f, err := m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{
		IndexID: idx.ID, Name: "Holiday set", QueryTexts: []string{"holiday", "vacation"}, FeaturedDocuments: []string{"home", "gone"},
	})
	requireNoError(t, err)
	assertEqual(t, f.Status, driver.FeaturedActive)

	v, err := m.DescribeFeaturedResultsSet(bg, idx.ID, f.ID)
	requireNoError(t, err)
	assertEqual(t, len(v.DocumentsWithMetadata), 1)
	assertEqual(t, v.DocumentsWithMetadata[0].Title, "Home page")
	assertEqual(t, len(v.DocumentsMissing), 1)
	assertEqual(t, v.DocumentsMissing[0], "gone")

	// A query text may belong to one set per index.
	_, err = m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "Other", QueryTexts: []string{"HOLIDAY"}})
	requireException(t, err, driver.ExFeaturedConflict)

	_, err = m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "Dup", QueryTexts: []string{"a", "A"}})
	requireException(t, err, driver.ExValidation)

	_, err = m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "Many", QueryTexts: manyTexts(50)})
	requireException(t, err, driver.ExValidation)

	_, err = m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "Bad", Status: "WHATEVER"})
	requireException(t, err, driver.ExValidation)

	second, err := m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "Second", Status: driver.FeaturedInactive})
	requireNoError(t, err)
	assertEqual(t, second.Status, driver.FeaturedInactive)

	updated, err := m.UpdateFeaturedResultsSet(bg, &driver.UpdateFeaturedResultsSetInput{IndexID: idx.ID, ID: second.ID, QueryTexts: []string{"new"}, QueryTextsSet: true})
	requireNoError(t, err)
	assertEqual(t, updated.QueryTexts[0], "new")

	_, err = m.UpdateFeaturedResultsSet(bg, &driver.UpdateFeaturedResultsSetInput{IndexID: idx.ID, ID: second.ID, QueryTexts: []string{"holiday"}, QueryTextsSet: true})
	requireException(t, err, driver.ExFeaturedConflict)

	// Updating a set to its own query text is not a conflict.
	_, err = m.UpdateFeaturedResultsSet(bg, &driver.UpdateFeaturedResultsSetInput{IndexID: idx.ID, ID: f.ID, QueryTexts: []string{"holiday"}, QueryTextsSet: true})
	requireNoError(t, err)

	list, _, _ := m.ListFeaturedResultsSets(bg, idx.ID, driver.Page{})
	assertEqual(t, len(list), 2)

	errs, err := m.BatchDeleteFeaturedResultsSet(bg, idx.ID, []string{f.ID, "nosuchset"})
	requireNoError(t, err)
	assertEqual(t, len(errs), 1)
	assertEqual(t, errs[0].ID, "nosuchset")

	_, err = m.DescribeFeaturedResultsSet(bg, idx.ID, f.ID)
	requireException(t, err, driver.ExResourceNotFound)
}

func manyTexts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("query-%d", i)
	}

	return out
}

func TestPrincipalMappings(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	ds, err := m.CreateDataSource(bg, &driver.CreateDataSourceInput{IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom})
	requireNoError(t, err)

	members := []byte(`{"MemberUsers":[{"UserId":"alice"}]}`)

	requireNoError(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{IndexID: idx.ID, DataSourceID: ds.ID, GroupID: "eng", GroupMembers: members, OrderingID: i64(100)}))
	requireNoError(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{IndexID: idx.ID, DataSourceID: ds.ID, GroupID: "ops", GroupMembers: members, OrderingID: i64(300)}))

	d, err := m.DescribePrincipalMapping(bg, idx.ID, ds.ID, "eng")
	requireNoError(t, err)
	assertEqual(t, len(d.Summaries), 1)
	assertEqual(t, d.Summaries[0].Status, driver.MappingSucceeded)
	assertEqual(t, d.Summaries[0].OrderingID, int64(100))

	// A later put and an older put: both are recorded, newest first.
	requireNoError(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{IndexID: idx.ID, DataSourceID: ds.ID, GroupID: "eng", GroupMembers: members, OrderingID: i64(200)}))
	requireNoError(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{IndexID: idx.ID, DataSourceID: ds.ID, GroupID: "eng", GroupMembers: members, OrderingID: i64(150)}))

	d, _ = m.DescribePrincipalMapping(bg, idx.ID, ds.ID, "eng")
	assertEqual(t, len(d.Summaries), 3)
	assertEqual(t, d.Summaries[0].OrderingID, int64(200))

	groups, _, err := m.ListGroupsOlderThanOrderingID(bg, &driver.ListGroupsInput{IndexID: idx.ID, DataSourceID: ds.ID, OrderingID: 250})
	requireNoError(t, err)
	assertEqual(t, len(groups), 1)
	assertEqual(t, groups[0].GroupID, "eng")

	requireNoError(t, m.DeletePrincipalMapping(bg, &driver.DeletePrincipalMappingInput{IndexID: idx.ID, DataSourceID: ds.ID, GroupID: "eng", OrderingID: i64(400)}))

	d, _ = m.DescribePrincipalMapping(bg, idx.ID, ds.ID, "eng")
	assertEqual(t, d.Summaries[0].Status, driver.MappingDeleted)

	groups, _, _ = m.ListGroupsOlderThanOrderingID(bg, &driver.ListGroupsInput{IndexID: idx.ID, DataSourceID: ds.ID, OrderingID: 1000})
	assertEqual(t, len(groups), 1)
	assertEqual(t, groups[0].GroupID, "ops")

	requireException(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{IndexID: idx.ID, GroupID: "g"}), driver.ExValidation)
	requireException(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{IndexID: idx.ID, GroupID: "", GroupMembers: members}), driver.ExValidation)
	requireException(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{IndexID: idx.ID, GroupID: "g", GroupMembers: members, OrderingID: i64(-1)}), driver.ExValidation)
	requireException(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{IndexID: idx.ID, DataSourceID: "nosuch", GroupID: "g", GroupMembers: members}), driver.ExResourceNotFound)
}

func TestQuerySuggestions(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	putText(t, m, idx.ID, "d", "Vacation policy", "vacation days policy")

	cfg, err := m.DescribeQuerySuggestionsConfig(bg, idx.ID)
	requireNoError(t, err)
	assertEqual(t, cfg.Mode, driver.SuggestionsEnabled)
	assertEqual(t, cfg.Status, driver.SuggestionsActive)
	assertEqual(t, cfg.QueryLogLookBackWindowInDays, int32(180))

	one := int32(1)
	requireNoError(t, m.UpdateQuerySuggestionsConfig(bg, &driver.UpdateSuggestionsConfigInput{IndexID: idx.ID, MinimumQueryCount: &one, MinimumNumberOfQueryingUsers: &one}))

	for range 2 {
		_, err = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "vacation policy"})
		requireNoError(t, err)
	}

	got, err := m.GetQuerySuggestions(bg, &driver.GetSuggestionsInput{IndexID: idx.ID, QueryText: "vac"})
	requireNoError(t, err)
	assertEqual(t, len(got.Suggestions), 1)
	assertEqual(t, got.Suggestions[0].Text, "vacation policy")

	none, _ := m.GetQuerySuggestions(bg, &driver.GetSuggestionsInput{IndexID: idx.ID, QueryText: "v"})
	assertEqual(t, len(none.Suggestions), 0)

	learn := driver.SuggestionsLearnOnly
	requireNoError(t, m.UpdateQuerySuggestionsConfig(bg, &driver.UpdateSuggestionsConfigInput{IndexID: idx.ID, Mode: &learn}))

	none, _ = m.GetQuerySuggestions(bg, &driver.GetSuggestionsInput{IndexID: idx.ID, QueryText: "vac"})
	assertEqual(t, len(none.Suggestions), 0)

	enabled := driver.SuggestionsEnabled
	requireNoError(t, m.UpdateQuerySuggestionsConfig(bg, &driver.UpdateSuggestionsConfigInput{IndexID: idx.ID, Mode: &enabled}))
	requireNoError(t, m.ClearQuerySuggestions(bg, idx.ID))

	cleared, _ := m.GetQuerySuggestions(bg, &driver.GetSuggestionsInput{IndexID: idx.ID, QueryText: "vac"})
	assertEqual(t, len(cleared.Suggestions), 0)

	cfg, _ = m.DescribeQuerySuggestionsConfig(bg, idx.ID)
	assertEqual(t, cfg.LastClearTime.IsZero(), false)

	bad := "BOGUS"
	requireException(t, m.UpdateQuerySuggestionsConfig(bg, &driver.UpdateSuggestionsConfigInput{IndexID: idx.ID, Mode: &bad}), driver.ExValidation)

	zero := int32(0)
	requireException(t, m.UpdateQuerySuggestionsConfig(bg, &driver.UpdateSuggestionsConfigInput{IndexID: idx.ID, MinimumQueryCount: &zero}), driver.ExValidation)

	_, err = m.GetQuerySuggestions(bg, &driver.GetSuggestionsInput{IndexID: idx.ID, QueryText: "vac", SuggestionTypes: []string{"NOPE"}})
	requireException(t, err, driver.ExValidation)
}

func TestDeleteIndexCascadesEverything(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	src := driver.S3Path{Bucket: "my-bucket", Key: "k"}

	putText(t, m, idx.ID, "d", "T", "x")

	faq, _ := m.CreateFaq(bg, &driver.CreateFaqInput{IndexID: idx.ID, Name: "f", RoleArn: roleArn, S3Path: src})
	th, _ := m.CreateThesaurus(bg, &driver.CreateThesaurusInput{IndexID: idx.ID, Name: "t", RoleArn: roleArn, SourceS3Path: src})
	bl, _ := m.CreateQuerySuggestionsBlockList(bg, &driver.CreateBlockListInput{IndexID: idx.ID, Name: "b", RoleArn: roleArn, SourceS3Path: src})
	ex, _ := m.CreateExperience(bg, &driver.CreateExperienceInput{IndexID: idx.ID, Name: "e"})
	ac, _ := m.CreateAccessControlConfiguration(bg, &driver.CreateAccessControlInput{IndexID: idx.ID, Name: "a"})
	fr, _ := m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "Fr"})
	requireNoError(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{IndexID: idx.ID, GroupID: "g", GroupMembers: []byte(`{}`)}))

	requireNoError(t, m.DeleteIndex(bg, idx.ID))

	// Recreate an index: nothing leaks into any new index, and the old ids are gone.
	idx2 := mustIndex(t, m)

	for name, list := range map[string]func() (int, error){
		"faqs":    func() (int, error) { l, _, err := m.ListFaqs(bg, idx2.ID, driver.Page{}); return len(l), err },
		"thesaur": func() (int, error) { l, _, err := m.ListThesauri(bg, idx2.ID, driver.Page{}); return len(l), err },
		"blocks": func() (int, error) {
			l, _, err := m.ListQuerySuggestionsBlockLists(bg, idx2.ID, driver.Page{})
			return len(l), err
		},
		"exps": func() (int, error) { l, _, err := m.ListExperiences(bg, idx2.ID, driver.Page{}); return len(l), err },
		"acls": func() (int, error) {
			l, _, err := m.ListAccessControlConfigurations(bg, idx2.ID, driver.Page{})
			return len(l), err
		},
		"featured": func() (int, error) {
			l, _, err := m.ListFeaturedResultsSets(bg, idx2.ID, driver.Page{})

			return len(l), err
		},
	} {
		n, err := list()
		requireNoError(t, err)

		if n != 0 {
			t.Fatalf("%s leaked %d entries into a new index", name, n)
		}
	}

	_, err := m.DescribeFaq(bg, idx.ID, faq.ID)
	requireException(t, err, driver.ExResourceNotFound)
	_, err = m.DescribeThesaurus(bg, idx.ID, th.ID)
	requireException(t, err, driver.ExResourceNotFound)
	_, err = m.DescribeQuerySuggestionsBlockList(bg, idx.ID, bl.ID)
	requireException(t, err, driver.ExResourceNotFound)
	_, err = m.DescribeExperience(bg, idx.ID, ex.ID)
	requireException(t, err, driver.ExResourceNotFound)
	_, err = m.DescribeAccessControlConfiguration(bg, idx.ID, ac.ID)
	requireException(t, err, driver.ExResourceNotFound)
	_, err = m.DescribeFeaturedResultsSet(bg, idx.ID, fr.ID)
	requireException(t, err, driver.ExResourceNotFound)

	res, err := m.BatchGetDocumentStatus(bg, idx2.ID, []driver.DocumentInfo{{DocumentID: "d"}})
	requireNoError(t, err)
	assertEqual(t, res.Statuses[0].Status, driver.DocStatusNotFound)
}

func TestExtendedStateSurvivesSnapshotRestore(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)
	putText(t, m, idx.ID, "d", "Title", "persisted text")

	faq, _ := m.CreateFaq(bg, &driver.CreateFaqInput{IndexID: idx.ID, Name: "f", RoleArn: roleArn, S3Path: driver.S3Path{Bucket: "my-bucket", Key: "k"}, ClientToken: "tk"})
	fr, _ := m.CreateFeaturedResultsSet(bg, &driver.CreateFeaturedResultsSetInput{IndexID: idx.ID, Name: "Fr", QueryTexts: []string{"q"}})
	requireNoError(t, m.PutPrincipalMapping(bg, &driver.PutPrincipalMappingInput{IndexID: idx.ID, GroupID: "g", GroupMembers: []byte(`{}`), OrderingID: i64(5)}))

	one := int32(2)
	requireNoError(t, m.UpdateQuerySuggestionsConfig(bg, &driver.UpdateSuggestionsConfigInput{IndexID: idx.ID, MinimumQueryCount: &one}))

	data, err := m.Snapshot(bg, false)
	requireNoError(t, err)

	r := newMock()
	requireNoError(t, r.Restore(bg, data))

	out, err := r.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "persisted"})
	requireNoError(t, err)
	assertEqual(t, len(out.Items), 1)

	got, err := r.DescribeFaq(bg, idx.ID, faq.ID)
	requireNoError(t, err)
	assertEqual(t, got.Name, "f")

	again, err := r.CreateFaq(bg, &driver.CreateFaqInput{IndexID: idx.ID, Name: "f", RoleArn: roleArn, S3Path: driver.S3Path{Bucket: "my-bucket", Key: "k"}, ClientToken: "tk"})
	requireNoError(t, err)
	assertEqual(t, again.ID, faq.ID)

	_, err = r.DescribeFeaturedResultsSet(bg, idx.ID, fr.ID)
	requireNoError(t, err)

	d, _ := r.DescribePrincipalMapping(bg, idx.ID, "", "g")
	assertEqual(t, len(d.Summaries), 1)

	cfg, _ := r.DescribeQuerySuggestionsConfig(bg, idx.ID)
	assertEqual(t, cfg.MinimumQueryCount, int32(2))
}

func TestExtendedConcurrency(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	const workers = 50

	var wg sync.WaitGroup

	ids := make([]string, workers)

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			f, err := m.CreateFaq(bg, &driver.CreateFaqInput{
				IndexID: idx.ID, Name: "f", RoleArn: roleArn, S3Path: driver.S3Path{Bucket: "my-bucket", Key: "k"}, ClientToken: "same",
			})
			if err == nil {
				ids[i] = f.ID
			}

			_, _ = m.BatchPutDocument(bg, &driver.PutDocumentsInput{IndexID: idx.ID, Documents: []driver.PutDocument{{ID: "shared", Blob: []byte("x")}}})
			_, _ = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "x"})
		}()
	}

	wg.Wait()

	for _, id := range ids {
		assertEqual(t, id, ids[0])
	}

	faqs, _, _ := m.ListFaqs(bg, idx.ID, driver.Page{})
	assertEqual(t, len(faqs), 1)

	// Deleting the index while creators race never leaves orphans behind.
	var wg2 sync.WaitGroup

	for range 20 {
		wg2.Add(1)

		go func() {
			defer wg2.Done()

			_, _ = m.CreateFaq(bg, &driver.CreateFaqInput{IndexID: idx.ID, Name: "g", RoleArn: roleArn, S3Path: driver.S3Path{Bucket: "my-bucket", Key: "k"}})
		}()
	}

	_ = m.DeleteIndex(bg, idx.ID)

	wg2.Wait()

	idx2 := mustIndex(t, m)
	l, _, _ := m.ListFaqs(bg, idx2.ID, driver.Page{})
	assertEqual(t, len(l), 0)
}

func TestReadsReturnCopies(t *testing.T) {
	m := newMock()
	idx := mustIndex(t, m)

	faq, _ := m.CreateFaq(bg, &driver.CreateFaqInput{IndexID: idx.ID, Name: "f", RoleArn: roleArn, S3Path: driver.S3Path{Bucket: "my-bucket", Key: "k"}, Tags: []driver.Tag{{Key: "a", Value: "1"}}})
	faq.Tags[0].Value = "mutated"

	got, _ := m.DescribeFaq(bg, idx.ID, faq.ID)
	assertEqual(t, got.Tags[0].Value, "1")

	putText(t, m, idx.ID, "d", "T", "text", strAttr("k", "v"))

	out, _ := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "text"})
	*out.Items[0].Attributes[0].Value.StringValue = "mutated"

	out2, _ := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "text"})
	assertEqual(t, *out2.Items[0].Attributes[0].Value.StringValue, "v")

	_ = str
}
