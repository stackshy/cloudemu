package bedrockagent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
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

	_, err = m.StartIngestionJob(ctx, kb.ID, dsID, "")
	assert.True(t, cerrors.IsFailedPrecondition(err), "one running job per data source")

	other := mustDataSource(t, m, kb.ID, "S3")
	_, err = m.StartIngestionJob(ctx, kb.ID, other, "")
	require.NoError(t, err, "another data source can run its own job")

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
