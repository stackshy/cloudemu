package kendra_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/kendra"
	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

func kendraMetric(t *testing.T, cw *cloudwatch.Mock, clk *config.FakeClock, dims map[string]string, name string) (float64, string) {
	t.Helper()

	res, err := cw.GetMetricData(context.Background(), mondriver.GetMetricInput{
		Namespace: "AWS/Kendra", MetricName: name, Dimensions: dims,
		StartTime: clk.Now().Add(-time.Hour), EndTime: clk.Now().Add(time.Hour), Period: 7200, Stat: "Sum",
	})
	requireNoError(t, err)

	if len(res.Values) == 0 {
		return 0, res.Unit
	}

	return res.Values[0], res.Unit
}

// TestKendraCloudWatchMetrics pins the AWS/Kendra metrics: DocumentsIndexed,
// DocumentsFailedToIndex and IndexQueryCount on the IndexId dimension, and
// DocumentsSubmittedForDeletion on IndexId + DataSourceId.
func TestKendraCloudWatchMetrics(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk))
	m := kendra.New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	idx := createIndex(t, m)
	dims := map[string]string{"IndexId": idx.ID}

	failed, err := m.BatchPutDocument(bg, &driver.PutDocumentsInput{IndexID: idx.ID, Documents: []driver.PutDocument{
		{ID: "a", Blob: []byte("alpha"), ContentType: "PLAIN_TEXT"},
		{ID: "b", Blob: []byte("beta"), ContentType: "PLAIN_TEXT"},
		{ID: "big", Blob: make([]byte, 5<<20+1), ContentType: "PLAIN_TEXT"},
	}})
	requireNoError(t, err)
	assertEqual(t, len(failed), 1)

	if v, unit := kendraMetric(t, cw, clk, dims, "DocumentsIndexed"); v != 2 || unit != "Count" {
		t.Fatalf("DocumentsIndexed = %v %s, want 2 Count", v, unit)
	}

	if v, _ := kendraMetric(t, cw, clk, dims, "DocumentsFailedToIndex"); v != 1 {
		t.Fatalf("DocumentsFailedToIndex = %v, want 1", v)
	}

	for range 3 {
		_, err = m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "alpha"})
		requireNoError(t, err)
	}

	_, err = m.Retrieve(bg, &driver.RetrieveInput{IndexID: idx.ID, QueryText: "alpha"})
	requireNoError(t, err)

	if v, _ := kendraMetric(t, cw, clk, dims, "IndexQueryCount"); v != 4 {
		t.Fatalf("IndexQueryCount = %v, want 4 (3 queries + 1 retrieve)", v)
	}

	ds, err := m.CreateDataSource(bg, &driver.CreateDataSourceInput{IndexID: idx.ID, Name: "ds", Type: driver.DataSourceTypeCustom})
	requireNoError(t, err)

	exec, err := m.StartDataSourceSyncJob(bg, idx.ID, ds.ID)
	requireNoError(t, err)

	_, err = m.BatchDeleteDocument(bg, &driver.DeleteDocumentsInput{
		IndexID: idx.ID, DocumentIDs: []string{"a", "b"},
		MetricTarget: &driver.SyncJobMetricTarget{DataSourceID: ds.ID, DataSourceSyncJobID: exec},
	})
	requireNoError(t, err)

	dsDims := map[string]string{"IndexId": idx.ID, "DataSourceId": ds.ID}
	if v, _ := kendraMetric(t, cw, clk, dsDims, "DocumentsSubmittedForDeletion"); v != 2 {
		t.Fatalf("DocumentsSubmittedForDeletion = %v, want 2", v)
	}

	jobs, _, err := m.ListDataSourceSyncJobs(bg, &driver.ListSyncJobsInput{IndexID: idx.ID, DataSourceID: ds.ID})
	requireNoError(t, err)
	assertEqual(t, jobs[0].Metrics.DocumentsDeleted, "2")
}

func TestKendraWithoutMonitoringPublishesNothing(t *testing.T) {
	m := newMock()
	idx := createIndex(t, m)

	putText(t, m, idx.ID, "a", "A", "alpha")

	_, err := m.Query(bg, &driver.QueryInput{IndexID: idx.ID, QueryText: "alpha"})
	requireNoError(t, err)
}
