package bedrockagent_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
)

// createDataSource creates a data source of dsType under kbID and returns its
// id.
func createDataSource(t *testing.T, client *awsba.Client, kbID string, dsType batypes.DataSourceType) string {
	t.Helper()

	cfg := &batypes.DataSourceConfiguration{Type: dsType}
	if dsType == batypes.DataSourceTypeS3 {
		cfg.S3Configuration = &batypes.S3DataSourceConfiguration{BucketArn: aws.String(bucketArn)}
	}

	out, err := client.CreateDataSource(context.Background(), &awsba.CreateDataSourceInput{
		KnowledgeBaseId: aws.String(kbID), Name: aws.String("src"), DataSourceConfiguration: cfg,
	})
	require.NoError(t, err)

	return aws.ToString(out.DataSource.DataSourceId)
}

func TestSDKIngestionJobsSync(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	kbID := createKnowledgeBase(t, client, "jobs")
	dsID := createDataSource(t, client, kbID, batypes.DataSourceTypeS3)

	started, err := client.StartIngestionJob(ctx, &awsba.StartIngestionJobInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), Description: aws.String("first"),
	})
	require.NoError(t, err)
	requireStatus(t, started.ResultMetadata, http.StatusAccepted)

	jobID := aws.ToString(started.IngestionJob.IngestionJobId)
	assert.Equal(t, batypes.IngestionJobStatusComplete, started.IngestionJob.Status)
	require.NotNil(t, started.IngestionJob.Statistics)

	got, err := client.GetIngestionJob(ctx, &awsba.GetIngestionJobInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), IngestionJobId: aws.String(jobID),
	})
	require.NoError(t, err)
	assert.Equal(t, "first", aws.ToString(got.IngestionJob.Description))

	_, err = client.StartIngestionJob(ctx, &awsba.StartIngestionJobInput{KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID)})
	require.NoError(t, err)

	list, err := client.ListIngestionJobs(ctx, &awsba.ListIngestionJobsInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID),
		Filters: []batypes.IngestionJobFilter{{
			Attribute: batypes.IngestionJobFilterAttributeStatus, Operator: batypes.IngestionJobFilterOperatorEq,
			Values: []string{"COMPLETE"},
		}},
		SortBy: &batypes.IngestionJobSortBy{Attribute: batypes.IngestionJobSortByAttributeStartedAt, Order: batypes.SortOrderDescending},
	})
	require.NoError(t, err)
	assert.Len(t, list.IngestionJobSummaries, 2)

	stopped, err := client.ListIngestionJobs(ctx, &awsba.ListIngestionJobsInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID),
		Filters: []batypes.IngestionJobFilter{{
			Attribute: batypes.IngestionJobFilterAttributeStatus, Operator: batypes.IngestionJobFilterOperatorEq,
			Values: []string{"STOPPED"},
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, stopped.IngestionJobSummaries)

	_, err = client.StopIngestionJob(ctx, &awsba.StopIngestionJobInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), IngestionJobId: aws.String(jobID),
	})
	requireAPIError(t, err, "ConflictException", http.StatusConflict)

	_, err = client.GetIngestionJob(ctx, &awsba.GetIngestionJobInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), IngestionJobId: aws.String("MISSING000"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}

// TestSDKIngestionJobStopWhileRunning uses async settling so the job runs
// for a while and can be stopped.
func TestSDKIngestionJobStopWhileRunning(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	client := newClientWith(t, config.NewOptions(config.WithClock(clock), config.WithAsyncSettle()))
	ctx := context.Background()
	kbID := createKnowledgeBase(t, client, "async")
	dsID := createDataSource(t, client, kbID, batypes.DataSourceTypeS3)

	first, err := client.StartIngestionJob(ctx, &awsba.StartIngestionJobInput{KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID)})
	require.NoError(t, err)
	assert.Equal(t, batypes.IngestionJobStatusInProgress, first.IngestionJob.Status)

	stopped, err := client.StopIngestionJob(ctx, &awsba.StopIngestionJobInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), IngestionJobId: first.IngestionJob.IngestionJobId,
	})
	require.NoError(t, err)
	requireStatus(t, stopped.ResultMetadata, http.StatusAccepted)
	assert.Equal(t, batypes.IngestionJobStatusStopped, stopped.IngestionJob.Status)

	second, err := client.StartIngestionJob(ctx, &awsba.StartIngestionJobInput{KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID)})
	require.NoError(t, err)

	clock.Advance(time.Minute)

	got, err := client.GetIngestionJob(ctx, &awsba.GetIngestionJobInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), IngestionJobId: second.IngestionJob.IngestionJobId,
	})
	require.NoError(t, err)
	assert.Equal(t, batypes.IngestionJobStatusComplete, got.IngestionJob.Status)

	still, err := client.GetIngestionJob(ctx, &awsba.GetIngestionJobInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), IngestionJobId: first.IngestionJob.IngestionJobId,
	})
	require.NoError(t, err)
	assert.Equal(t, batypes.IngestionJobStatusStopped, still.IngestionJob.Status, "a stopped job stays stopped")
}
