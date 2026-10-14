package bedrockagent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	s3prov "github.com/stackshy/cloudemu/v2/providers/aws/s3"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func mustDataSource(t *testing.T, m *Mock, kbID, dsType string) string {
	t.Helper()

	ds, err := m.CreateDataSource(context.Background(), driver.DataSourceConfig{
		KnowledgeBaseID: kbID, Name: "src", DataSourceConfiguration: []byte(`{"type":"` + dsType + `"}`),
	})
	require.NoError(t, err)

	return ds.ID
}

func TestIngestionJobQueryValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	kb := mustKB(t, m, "kb")
	dsID := mustDataSource(t, m, kb.ID, "S3")

	cases := map[string]driver.ListIngestionJobsInput{
		"bad attribute": {Filters: []driver.IngestionJobFilter{{Attribute: "NAME", Operator: "EQ", Values: []string{"COMPLETE"}}}},
		"bad operator":  {Filters: []driver.IngestionJobFilter{{Attribute: "STATUS", Operator: "NE", Values: []string{"COMPLETE"}}}},
		"bad value":     {Filters: []driver.IngestionJobFilter{{Attribute: "STATUS", Operator: "EQ", Values: []string{"DONE"}}}},
		"two filters": {Filters: []driver.IngestionJobFilter{
			{Attribute: "STATUS", Operator: "EQ", Values: []string{"COMPLETE"}},
			{Attribute: "STATUS", Operator: "EQ", Values: []string{"STOPPED"}},
		}},
		"bad sort": {SortBy: &driver.IngestionJobSort{Attribute: "NAME", Order: "ASCENDING"}},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			in.KnowledgeBaseID, in.DataSourceID = kb.ID, dsID
			_, _, err := m.ListIngestionJobs(ctx, in)
			assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)
		})
	}

	_, err := m.StartIngestionJob(ctx, kb.ID, dsID, string(make([]byte, 201)))
	assert.True(t, cerrors.IsInvalidArgument(err), "description is at most 200 characters")
}

func TestIngestionJobsSortByStatus(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(clock), config.WithAsyncSettle()))
	ctx := context.Background()
	kb := mustKB(t, m, "kb")
	dsID := mustDataSource(t, m, kb.ID, "S3")

	done, err := m.StartIngestionJob(ctx, kb.ID, dsID, "")
	require.NoError(t, err)
	clock.Advance(time.Minute)

	running, err := m.StartIngestionJob(ctx, kb.ID, dsID, "")
	require.NoError(t, err)

	jobs, _, err := m.ListIngestionJobs(ctx, driver.ListIngestionJobsInput{
		KnowledgeBaseID: kb.ID, DataSourceID: dsID, SortBy: &driver.IngestionJobSort{Attribute: "STATUS", Order: "ASCENDING"},
	})
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	assert.Equal(t, done.ID, jobs[0].ID, "COMPLETE sorts before IN_PROGRESS")
	assert.Equal(t, running.ID, jobs[1].ID)

	_, err = m.DeleteDataSource(ctx, kb.ID, dsID)
	require.NoError(t, err)

	_, err = m.GetIngestionJob(ctx, kb.ID, dsID, running.ID)
	assert.True(t, cerrors.IsNotFound(err))
	assert.True(t, m.jobSettle.Settled(running.ID, clock.Now()), "the settle window goes with the job")
}

func TestIngestionJobSyncsS3(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC))))
	m := New(opts)
	s3 := s3prov.New(opts)
	m.SetObjectStore(s3)

	require.NoError(t, s3.CreateBucket(ctx, "docs"))
	put := func(key, body string) {
		t.Helper()
		require.NoError(t, s3.PutObject(ctx, "docs", key, []byte(body), "", nil))
	}

	put("kb/refunds.txt", "Refunds take 5 days.")
	put("kb/refunds.txt.metadata.json", `{"metadataAttributes":{"team":"billing","year":2024,`+
		`"tags":["money"],"public":{"value":{"type":"BOOLEAN","booleanValue":true}}}}`)
	put("kb/guide.pdf", "%PDF-1.7 \xff")
	put("other/skip.txt", "outside the prefix")

	kb := mustKB(t, m, "kb")
	ds, err := m.CreateDataSource(ctx, driver.DataSourceConfig{
		KnowledgeBaseID: kb.ID, Name: "s3",
		DataSourceConfiguration: []byte(`{"type":"S3","s3Configuration":{"bucketArn":"arn:aws:s3:::docs","inclusionPrefixes":["kb/"]}}`),
	})
	require.NoError(t, err)

	job, err := m.StartIngestionJob(ctx, kb.ID, ds.ID, "")
	require.NoError(t, err)
	assert.Equal(t, driver.IngestionJobStatistics{
		NumberOfDocumentsScanned: 2, NumberOfMetadataDocumentsScanned: 1, NumberOfNewDocumentsIndexed: 1, NumberOfDocumentsFailed: 1,
	}, job.Statistics, "the text file is indexed, the PDF can't be parsed, other/ is outside the prefix")

	docs := m.IndexedDocuments(kb.ID)
	require.Len(t, docs, 1)
	assert.Equal(t, "s3://docs/kb/refunds.txt", docs[0].Identifier.S3URI)
	assert.Equal(t, "Refunds take 5 days.", docs[0].Text)
	assert.Equal(t, map[string]any{"team": "billing", "year": 2024.0, "tags": []string{"money"}, "public": true}, docs[0].Attributes)

	again, err := m.StartIngestionJob(ctx, kb.ID, ds.ID, "")
	require.NoError(t, err)
	assert.Zero(t, again.Statistics.NumberOfNewDocumentsIndexed+again.Statistics.NumberOfModifiedDocumentsIndexed,
		"an unchanged bucket re-indexes nothing")

	put("kb/refunds.txt", "Refunds take 3 days.")
	put("kb/new.md", "# Shipping")

	changed, err := m.StartIngestionJob(ctx, kb.ID, ds.ID, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), changed.Statistics.NumberOfNewDocumentsIndexed)
	assert.Equal(t, int64(1), changed.Statistics.NumberOfModifiedDocumentsIndexed)

	require.NoError(t, s3.DeleteObject(ctx, "docs", "kb/new.md"))
	put("kb/refunds.txt.metadata.json", `{"metadataAttributes":{"team":"support"}}`)

	pruned, err := m.StartIngestionJob(ctx, kb.ID, ds.ID, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), pruned.Statistics.NumberOfDocumentsDeleted, "a deleted object leaves the index")
	assert.Equal(t, int64(1), pruned.Statistics.NumberOfMetadataDocumentsModified)

	docs = m.IndexedDocuments(kb.ID)
	require.Len(t, docs, 1)
	assert.Equal(t, "Refunds take 3 days.", docs[0].Text)
	assert.Equal(t, map[string]any{"team": "support"}, docs[0].Attributes)

	listed, _, err := m.ListKnowledgeBaseDocuments(ctx, kb.ID, ds.ID, driver.Page{})
	require.NoError(t, err)
	assert.Len(t, listed, 1, "synced documents are listed like ingested ones")
}

func TestIngestS3DocumentReadsObject(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()
	m := New(opts)
	s3 := s3prov.New(opts)
	m.SetObjectStore(s3)

	require.NoError(t, s3.CreateBucket(ctx, "docs"))
	require.NoError(t, s3.PutObject(ctx, "docs", "a.txt", []byte("Alpha text"), "text/plain", nil))
	require.NoError(t, s3.PutObject(ctx, "docs", "a.meta", []byte(`{"metadataAttributes":{"lang":"en"}}`), "", nil))

	kb := mustKB(t, m, "kb")
	dsID := mustDataSource(t, m, kb.ID, "S3")

	_, err := m.IngestKnowledgeBaseDocuments(ctx, kb.ID, dsID, []driver.KnowledgeBaseDocument{{
		Content:  []byte(`{"dataSourceType":"S3","s3":{"s3Location":{"uri":"s3://docs/a.txt"}}}`),
		Metadata: []byte(`{"type":"S3_LOCATION","s3Location":{"uri":"s3://docs/a.meta"}}`),
	}})
	require.NoError(t, err)

	docs := m.IndexedDocuments(kb.ID)
	require.Len(t, docs, 1)
	assert.Equal(t, "Alpha text", docs[0].Text)
	assert.Equal(t, map[string]any{"lang": "en"}, docs[0].Attributes)
}

func TestIngestionJobFailsWhenBucketIsGone(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()
	m := New(opts)
	s3 := s3prov.New(opts)
	m.SetObjectStore(s3)

	require.NoError(t, s3.CreateBucket(ctx, "gone"))
	require.NoError(t, s3.PutObject(ctx, "gone", "a.txt", []byte("Alpha"), "", nil))

	kb := mustKB(t, m, "kb")
	ds, err := m.CreateDataSource(ctx, driver.DataSourceConfig{
		KnowledgeBaseID: kb.ID, Name: "s3", DataSourceConfiguration: []byte(`{"type":"S3","s3Configuration":{"bucketArn":"arn:aws:s3:::gone"}}`),
	})
	require.NoError(t, err)

	_, err = m.StartIngestionJob(ctx, kb.ID, ds.ID, "")
	require.NoError(t, err)

	require.NoError(t, s3.DeleteObject(ctx, "gone", "a.txt"))
	require.NoError(t, s3.DeleteBucket(ctx, "gone"))

	job, err := m.StartIngestionJob(ctx, kb.ID, ds.ID, "")
	require.NoError(t, err)
	assert.Equal(t, driver.IngestionJobFailed, job.Status)
	require.Len(t, job.FailureReasons, 1)
	assert.Contains(t, job.FailureReasons[0], "gone")
	assert.Len(t, m.IndexedDocuments(kb.ID), 1, "a bucket that can't be read leaves the index as it was")
}
