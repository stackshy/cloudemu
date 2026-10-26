package cloudwatchlogs

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/s3"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const exportBucket = "exp-bucket"

// exportPolicy is the bucket policy the CloudWatch Logs guide gives for
// exports.
const exportPolicy = `{"Version":"2012-10-17","Statement":[
 {"Effect":"Allow","Principal":{"Service":"logs.us-east-1.amazonaws.com"},"Action":"s3:GetBucketAcl",
  "Resource":"arn:aws:s3:::exp-bucket","Condition":{"StringEquals":{"aws:SourceAccount":["123456789012"]}}},
 {"Effect":"Allow","Principal":{"Service":"logs.us-east-1.amazonaws.com"},"Action":"s3:PutObject",
  "Resource":"arn:aws:s3:::exp-bucket/*"}]}`

type exportFixture struct {
	logs  *Mock
	store *s3.Mock
	clock *config.FakeClock
}

func newExportFixture(t *testing.T, async bool, policy string) *exportFixture {
	t.Helper()

	fc := config.NewFakeClock(testClockStart())
	opts := []config.Option{config.WithClock(fc), config.WithRegion("us-east-1")}

	if async {
		opts = append(opts, config.WithAsyncSettle())
	}

	o := config.NewOptions(opts...)
	f := &exportFixture{logs: New(o), store: s3.New(o), clock: fc}
	f.logs.SetExportStore(f.store)

	ctx := context.Background()
	require.NoError(t, f.store.CreateBucket(ctx, exportBucket))

	if policy != "" {
		require.NoError(t, f.store.PutBucketConfig(ctx, exportBucket, "policy", []byte(policy)))
	}

	_, err := f.logs.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g"})
	require.NoError(t, err)

	base := testEventTime()

	for _, s := range []string{"app-1", "app-2", "other"} {
		_, err = f.logs.CreateLogStream(ctx, "g", s)
		require.NoError(t, err)

		_, err = f.logs.PutLogEventsWithResult(ctx, "g", s, []driver.LogEvent{
			{Timestamp: base, Message: s + " first"},
			{Timestamp: base.Add(time.Minute), Message: s + " second"},
			{Timestamp: base.Add(2 * time.Hour), Message: s + " late"},
		})
		require.NoError(t, err)
	}

	return f
}

func (f *exportFixture) input() *driver.CreateExportTaskInput {
	base := testEventTime()

	return &driver.CreateExportTaskInput{
		TaskName: "t1", LogGroupName: "g", From: base, To: base.Add(3 * time.Hour), Destination: exportBucket,
	}
}

func (f *exportFixture) object(t *testing.T, key string) []byte {
	t.Helper()

	obj, err := f.store.GetObject(context.Background(), exportBucket, key)
	require.NoError(t, err, key)

	return obj.Data
}

func gunzip(t *testing.T, data []byte) string {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)

	out, err := io.ReadAll(zr)
	require.NoError(t, err)

	return string(out)
}

func (f *exportFixture) keys(t *testing.T) []string {
	t.Helper()

	res, err := f.store.ListObjects(context.Background(), exportBucket, storagedriver.ListOptions{})
	require.NoError(t, err)

	out := make([]string, 0, len(res.Objects))
	for _, o := range res.Objects {
		out = append(out, o.Key)
	}

	return out
}

func TestExportTaskWritesObjects(t *testing.T) {
	ctx := context.Background()
	f := newExportFixture(t, false, exportPolicy)

	_, err := f.logs.PutLogEventsWithResult(ctx, "g", "app-1", []driver.LogEvent{
		{Timestamp: testClockStart().Add(30 * time.Minute), Message: "app-1 after to"},
	})
	require.NoError(t, err)

	in := f.input()
	in.LogStreamNamePrefix = "app-"
	in.From = testEventTime().Add(time.Minute)
	in.To = testClockStart()

	id, err := f.logs.CreateExportTask(ctx, in)
	require.NoError(t, err)

	tasks, err := f.logs.DescribeExportTasks(ctx, driver.DescribeExportTasksInput{TaskID: id})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, driver.ExportTaskCompleted, tasks[0].StatusCode)
	assert.Equal(t, "Completed successfully", tasks[0].StatusMessage)
	assert.Equal(t, "exportedlogs", tasks[0].DestinationPrefix)
	assert.Equal(t, "t1", tasks[0].TaskName)
	assert.False(t, tasks[0].CompletionTime.IsZero())

	assert.ElementsMatch(t, []string{
		"exportedlogs/aws-logs-write-test",
		"exportedlogs/" + id + "/app-1/000000.gz",
		"exportedlogs/" + id + "/app-2/000000.gz",
	}, f.keys(t), "only streams matching the prefix are exported")

	assert.Equal(t, "Permission Check Successful", string(f.object(t, "exportedlogs/aws-logs-write-test")))

	// from and to bound the event timestamps, both inclusive.
	got := gunzip(t, f.object(t, "exportedlogs/"+id+"/app-1/000000.gz"))
	assert.Equal(t, "2024-12-31T21:01:00.000Z app-1 second\n2024-12-31T23:00:00.000Z app-1 late\n", got)
}

func TestExportTaskCustomPrefix(t *testing.T) {
	f := newExportFixture(t, false, exportPolicy)

	in := f.input()
	in.DestinationPrefix = "my/prefix/"

	id, err := f.logs.CreateExportTask(context.Background(), in)
	require.NoError(t, err)

	got := gunzip(t, f.object(t, "my/prefix/"+id+"/other/000000.gz"))
	assert.Contains(t, got, "other late")
}

func TestExportTaskLifecycleSettles(t *testing.T) {
	ctx := context.Background()
	f := newExportFixture(t, true, exportPolicy)

	id, err := f.logs.CreateExportTask(ctx, f.input())
	require.NoError(t, err)

	status := func() string {
		tasks, err := f.logs.DescribeExportTasks(ctx, driver.DescribeExportTasksInput{TaskID: id})
		require.NoError(t, err)
		require.Len(t, tasks, 1)

		return tasks[0].StatusCode
	}

	assert.Equal(t, driver.ExportTaskPending, status())
	assert.Equal(t, []string{"exportedlogs/aws-logs-write-test"}, f.keys(t), "no events are written before the task runs")

	_, err = f.logs.CreateExportTask(ctx, f.input())
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err), "one active task per account")

	f.clock.Advance(time.Second)
	assert.Equal(t, driver.ExportTaskRunning, status())

	f.clock.Advance(2 * time.Second)
	assert.Equal(t, driver.ExportTaskCompleted, status())
	assert.Len(t, f.keys(t), 4)

	_, err = f.logs.CreateExportTask(ctx, f.input())
	require.NoError(t, err, "a finished task frees the slot")
}

func TestCancelExportTask(t *testing.T) {
	ctx := context.Background()
	f := newExportFixture(t, true, exportPolicy)

	id, err := f.logs.CreateExportTask(ctx, f.input())
	require.NoError(t, err)

	f.clock.Advance(time.Second)
	require.NoError(t, f.logs.CancelExportTask(ctx, id))

	f.clock.Advance(time.Minute)

	tasks, err := f.logs.DescribeExportTasks(ctx, driver.DescribeExportTasksInput{StatusCode: driver.ExportTaskCancelled})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.False(t, tasks[0].CompletionTime.IsZero())
	assert.Equal(t, []string{"exportedlogs/aws-logs-write-test"}, f.keys(t), "a cancelled task writes no events")

	err = f.logs.CancelExportTask(ctx, id)
	assert.True(t, errors.IsFailedPrecondition(err))

	err = f.logs.CancelExportTask(ctx, "nope")
	assert.True(t, errors.IsNotFound(err))
}

func TestCancelCompletedExportTask(t *testing.T) {
	ctx := context.Background()
	f := newExportFixture(t, false, exportPolicy)

	id, err := f.logs.CreateExportTask(ctx, f.input())
	require.NoError(t, err)

	assert.True(t, errors.IsFailedPrecondition(f.logs.CancelExportTask(ctx, id)))
}

func TestDescribeExportTasksFilters(t *testing.T) {
	ctx := context.Background()
	f := newExportFixture(t, false, exportPolicy)

	first, err := f.logs.CreateExportTask(ctx, f.input())
	require.NoError(t, err)

	f.clock.Advance(time.Second)

	second, err := f.logs.CreateExportTask(ctx, f.input())
	require.NoError(t, err)

	all, err := f.logs.DescribeExportTasks(ctx, driver.DescribeExportTasksInput{})
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, second, all[0].TaskID, "newest first")
	assert.Equal(t, first, all[1].TaskID)

	none, err := f.logs.DescribeExportTasks(ctx, driver.DescribeExportTasksInput{StatusCode: driver.ExportTaskRunning})
	require.NoError(t, err)
	assert.Empty(t, none)

	_, err = f.logs.DescribeExportTasks(ctx, driver.DescribeExportTasksInput{StatusCode: "DONE"})
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestCreateExportTaskErrors(t *testing.T) {
	denyPut := `{"Statement":[
	 {"Effect":"Allow","Principal":{"Service":"logs.amazonaws.com"},"Action":"s3:*","Resource":"arn:aws:s3:::exp-bucket*"},
	 {"Effect":"Deny","Principal":"*","Action":"s3:PutObject","Resource":"arn:aws:s3:::exp-bucket/*"}]}`
	aclOnly := `{"Statement":{"Effect":"Allow","Principal":{"Service":"logs.us-east-1.amazonaws.com"},
	 "Action":"s3:GetBucketAcl","Resource":"arn:aws:s3:::exp-bucket"}}`
	otherPrincipal := `{"Statement":[{"Effect":"Allow","Principal":{"Service":"delivery.logs.amazonaws.com"},
	 "Action":"s3:*","Resource":"*"}]}`

	tests := []struct {
		name   string
		policy string
		mutate func(*driver.CreateExportTaskInput)
		check  func(error) bool
		msg    string
	}{
		{"missing group", exportPolicy, func(in *driver.CreateExportTaskInput) { in.LogGroupName = "nope" }, errors.IsNotFound, ""},
		{"missing bucket", exportPolicy, func(in *driver.CreateExportTaskInput) { in.Destination = "nope" },
			errors.IsInvalidArgument, bucketMissingMsg},
		{"no policy", "", nil, errors.IsInvalidArgument, getBucketACLMsg},
		{"other principal", otherPrincipal, nil, errors.IsInvalidArgument, getBucketACLMsg},
		{"no put grant", aclOnly, nil, errors.IsInvalidArgument, putObjectFailedMsg},
		{"put denied", denyPut, nil, errors.IsInvalidArgument, putObjectFailedMsg},
		{"to before from", exportPolicy, func(in *driver.CreateExportTaskInput) { in.To = in.From.Add(-time.Second) },
			errors.IsInvalidArgument, ""},
		{"to before group creation", exportPolicy, func(in *driver.CreateExportTaskInput) { in.To = in.From },
			errors.IsInvalidArgument, ""},
		{"bad stream prefix", exportPolicy, func(in *driver.CreateExportTaskInput) { in.LogStreamNamePrefix = "a*" },
			errors.IsInvalidArgument, ""},
		{"no destination", exportPolicy, func(in *driver.CreateExportTaskInput) { in.Destination = "" }, errors.IsInvalidArgument, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newExportFixture(t, false, tc.policy)

			in := f.input()
			if tc.mutate != nil {
				tc.mutate(in)
			}

			_, err := f.logs.CreateExportTask(context.Background(), in)
			require.Error(t, err)
			assert.True(t, tc.check(err), err.Error())

			if tc.msg != "" {
				assert.Equal(t, tc.msg, errors.Message(err))
			}
		})
	}
}

func TestCreateExportTaskWithoutStore(t *testing.T) {
	m := newWindowMock(t, 0)

	_, err := m.CreateExportTask(context.Background(), &driver.CreateExportTaskInput{
		LogGroupName: "g", From: testClockStart(), To: testClockStart(), Destination: exportBucket,
	})
	assert.Equal(t, bucketMissingMsg, errors.Message(err))
}

func TestExportTaskTypedBucketPolicy(t *testing.T) {
	ctx := context.Background()
	f := newExportFixture(t, false, "")

	require.NoError(t, f.store.PutBucketPolicy(ctx, exportBucket, storagedriver.BucketPolicy{
		Statements: []storagedriver.PolicyStatement{{
			Effect: "Allow", Principal: "logs.amazonaws.com", Actions: []string{"s3:GetBucketAcl", "s3:PutObject"},
			Resources: []string{"arn:aws:s3:::exp-bucket", "arn:aws:s3:::exp-bucket/*"},
		}},
	}))

	_, err := f.logs.CreateExportTask(ctx, f.input())
	require.NoError(t, err)
}

func TestExportTaskFailsWhenBucketGoes(t *testing.T) {
	ctx := context.Background()
	f := newExportFixture(t, true, exportPolicy)

	id, err := f.logs.CreateExportTask(ctx, f.input())
	require.NoError(t, err)

	require.NoError(t, f.store.DeleteObject(ctx, exportBucket, "exportedlogs/aws-logs-write-test"))
	require.NoError(t, f.store.DeleteBucket(ctx, exportBucket))

	f.clock.Advance(time.Minute)

	tasks, err := f.logs.DescribeExportTasks(ctx, driver.DescribeExportTasksInput{TaskID: id})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, driver.ExportTaskFailed, tasks[0].StatusCode)
	assert.Equal(t, putObjectFailedMsg, tasks[0].StatusMessage)
}

func TestExportTasksSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := newExportFixture(t, true, exportPolicy)

	id, err := f.logs.CreateExportTask(ctx, f.input())
	require.NoError(t, err)

	data, err := f.logs.Snapshot(ctx, false)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Contains(t, raw, "exportTasks")

	restored := New(f.logs.opts)
	restored.SetExportStore(f.store)
	require.NoError(t, restored.Restore(ctx, data))

	tasks, err := restored.DescribeExportTasks(ctx, driver.DescribeExportTasksInput{})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, id, tasks[0].TaskID)
	assert.Equal(t, driver.ExportTaskPending, tasks[0].StatusCode)

	f.clock.Advance(time.Minute)

	tasks, err = restored.DescribeExportTasks(ctx, driver.DescribeExportTasksInput{})
	require.NoError(t, err)
	assert.Equal(t, driver.ExportTaskCompleted, tasks[0].StatusCode)
	assert.Len(t, f.keys(t), 4, "the restored task writes its objects")
}
