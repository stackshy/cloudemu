package cloudwatchlogs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs/insights"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dpARN = "arn:aws:dataprotection::aws:data-identifier/"

// dpPolicy builds a valid data protection policy over the managed names.
func dpPolicy(names ...string) string {
	ids := make([]string, 0, len(names))
	for _, n := range names {
		ids = append(ids, `"`+dpARN+n+`"`)
	}

	list := strings.Join(ids, ",")

	return `{"Name":"p","Version":"2021-06-01","Statement":[
{"Sid":"audit","DataIdentifier":[` + list + `],"Operation":{"Audit":{"FindingsDestination":{}}}},
{"Sid":"redact","DataIdentifier":[` + list + `],"Operation":{"Deidentify":{"MaskConfig":{}}}}]}`
}

func putOne(t *testing.T, m *Mock, msg string) {
	t.Helper()

	require.NoError(t, m.PutLogEvents(context.Background(), "test-group", "test-stream",
		[]driver.LogEvent{{Timestamp: testEventTime(), Message: msg}}))
}

func messages(events []driver.LogEvent) []string {
	out := make([]string, 0, len(events))
	for i := range events {
		out = append(out, events[i].Message)
	}

	return out
}

func TestDataProtectionMasksOnlyAfterPut(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	setupGroupAndStream(t, m)

	putOne(t, m, "before a@b.io")

	p, err := m.PutDataProtectionPolicy(ctx, "test-group", dpPolicy("EmailAddress"))
	require.NoError(t, err)
	assert.Equal(t, "test-group", p.LogGroupIdentifier)
	assert.Equal(t, testClockStart(), p.LastUpdatedTime)

	putOne(t, m, "after c@d.io")

	q := &driver.LogQueryInput{LogGroup: "test-group", LogStream: "test-stream"}

	masked, err := m.GetLogEvents(ctx, q)
	require.NoError(t, err)
	assert.Equal(t, []string{"before a@b.io", "after ******"}, messages(masked))

	for i := range masked {
		assert.Empty(t, masked[i].Unmasked, "Unmasked must not leave the provider")
	}

	clear, err := m.GetAWSLogEvents(ctx, q, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"before a@b.io", "after c@d.io"}, messages(clear))

	fin := &driver.FilterLogEventsInput{LogGroup: "test-group", FilterPattern: "after"}

	filtered, err := m.FilterLogEvents(ctx, fin)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "after ******", filtered[0].Message)

	filtered, err = m.FilterAWSLogEvents(ctx, fin, true)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "after c@d.io", filtered[0].Message)
}

func TestDataProtectionGetLogRecordUnmask(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	setupGroupAndStream(t, m)

	_, err := m.PutDataProtectionPolicy(ctx, "test-group", dpPolicy("IpAddress"))
	require.NoError(t, err)
	putOne(t, m, "from 10.1.2.3")

	id, err := m.StartQuery(ctx, &driver.StartQueryInput{
		LogGroupName: "test-group", StartTime: testEventTime().Add(-time.Hour), EndTime: testClockStart(),
		QueryString: "fields @message",
	})
	require.NoError(t, err)

	res, err := m.GetQueryResults(ctx, id)
	require.NoError(t, err)
	require.Len(t, res.Results, 1)
	assert.Equal(t, "from ********", rowField(res.Results[0], insights.FieldMessage))

	ptr := rowField(res.Results[0], insights.FieldPtr)

	rec, err := m.GetLogRecord(ctx, ptr, false)
	require.NoError(t, err)
	assert.Equal(t, "from ********", rec[insights.FieldMessage])

	rec, err = m.GetLogRecord(ctx, ptr, true)
	require.NoError(t, err)
	assert.Equal(t, "from 10.1.2.3", rec[insights.FieldMessage])
}

func TestDataProtectionPolicyLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	setupGroupAndStream(t, m)

	arn := "arn:aws:logs:us-east-1:" + m.opts.AccountID + ":log-group:test-group"

	got, err := m.GetDataProtectionPolicy(ctx, arn)
	require.NoError(t, err)
	assert.Empty(t, got.Document, "no policy yet")

	_, err = m.PutDataProtectionPolicy(ctx, arn, dpPolicy("EmailAddress"))
	require.NoError(t, err)

	got, err = m.GetDataProtectionPolicy(ctx, "test-group")
	require.NoError(t, err)
	assert.Equal(t, dpPolicy("EmailAddress"), got.Document)

	groups, err := m.ListAWSLogGroups(ctx, driver.ListLogGroupsFilter{})
	require.NoError(t, err)
	assert.Equal(t, driver.DataProtectionStatusActivated, groups[0].DataProtectionStatus)

	require.NoError(t, m.DeleteDataProtectionPolicy(ctx, "test-group"))

	err = m.DeleteDataProtectionPolicy(ctx, "test-group")
	assert.True(t, errors.IsNotFound(err), "second delete: %v", err)

	groups, err = m.ListAWSLogGroups(ctx, driver.ListLogGroupsFilter{})
	require.NoError(t, err)
	assert.Empty(t, groups[0].DataProtectionStatus)

	putOne(t, m, "now a@b.io")

	events, err := m.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "test-group"})
	require.NoError(t, err)
	assert.Equal(t, []string{"now a@b.io"}, messages(events), "no masking after delete")
}

func TestDataProtectionErrors(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	setupGroupAndStream(t, m)

	_, err := m.PutDataProtectionPolicy(ctx, "missing", dpPolicy("EmailAddress"))
	assert.True(t, errors.IsNotFound(err), "missing group: %v", err)

	_, err = m.GetDataProtectionPolicy(ctx, "missing")
	assert.True(t, errors.IsNotFound(err), "get missing group: %v", err)

	_, err = m.PutDataProtectionPolicy(ctx, "bad id!", dpPolicy("EmailAddress"))
	assert.True(t, errors.IsInvalidArgument(err), "bad identifier: %v", err)

	_, err = m.PutDataProtectionPolicy(ctx, "test-group", `{"Statement":[]}`)
	assert.True(t, errors.IsInvalidArgument(err), "bad document: %v", err)

	findings := strings.Replace(dpPolicy("EmailAddress"), `"FindingsDestination":{}`,
		`"FindingsDestination":{"CloudWatchLogs":{"LogGroup":"no-such-group"}}`, 1)
	_, err = m.PutDataProtectionPolicy(ctx, "test-group", findings)
	assert.True(t, errors.IsInvalidArgument(err), "missing findings group: %v", err)

	_, err = m.PutDataProtectionPolicy(ctx, "test-group", strings.Replace(findings, "no-such-group", "test-group", 1))
	assert.NoError(t, err, "existing findings group")
}

func TestDataProtectionAccountPolicyIsCumulative(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	setupGroupAndStream(t, m)

	_, err := m.PutDataProtectionPolicy(ctx, "test-group", dpPolicy("EmailAddress"))
	require.NoError(t, err)

	_, err = m.PutAccountPolicy(ctx, &driver.PutAccountPolicyInput{
		PolicyName: "acct", PolicyType: driver.AccountPolicyDataProtection, PolicyDocument: dpPolicy("IpAddress"),
	})
	require.NoError(t, err)

	putOne(t, m, "a@b.io from 10.0.0.1")

	// A group created after the account policy is covered too.
	_, err = m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "later"})
	require.NoError(t, err)
	_, err = m.CreateLogStream(ctx, "later", "s")
	require.NoError(t, err)
	require.NoError(t, m.PutLogEvents(ctx, "later", "s",
		[]driver.LogEvent{{Timestamp: testEventTime(), Message: "a@b.io from 10.0.0.1"}}))

	events, err := m.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "test-group"})
	require.NoError(t, err)
	assert.Equal(t, []string{"****** from ********"}, messages(events))

	events, err = m.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "later"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a@b.io from ********"}, messages(events))
}

func TestDataProtectionMasksMetricFiltersAndSubscriptions(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	inv := &recordingInvoker{}
	m.SetLambdaInvoker(inv)
	setupGroupAndStream(t, m)

	require.NoError(t, m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "all", LogGroup: "test-group", DestinationARN: lambdaDestARN,
	}))

	_, err := m.PutDataProtectionPolicy(ctx, "test-group", dpPolicy("EmailAddress"))
	require.NoError(t, err)
	putOne(t, m, "mail a@b.io")

	require.Len(t, inv.payloads, 1)
	ev := decodeAWSLogs(t, inv.payloads[0])
	assert.Equal(t, "mail ******", ev.LogEvents[0].Message)
}

func TestDataProtectionSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()
	setupGroupAndStream(t, src)

	_, err := src.PutDataProtectionPolicy(ctx, "test-group", dpPolicy("EmailAddress"))
	require.NoError(t, err)
	_, err = src.PutAccountPolicy(ctx, &driver.PutAccountPolicyInput{
		PolicyName: "acct", PolicyType: driver.AccountPolicyDataProtection, PolicyDocument: dpPolicy("IpAddress"),
	})
	require.NoError(t, err)
	putOne(t, src, "a@b.io 10.0.0.1")

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	clear, err := dst.GetAWSLogEvents(ctx, &driver.LogQueryInput{LogGroup: "test-group"}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"a@b.io 10.0.0.1"}, messages(clear))

	got, err := dst.GetDataProtectionPolicy(ctx, "test-group")
	require.NoError(t, err)
	assert.Equal(t, dpPolicy("EmailAddress"), got.Document)
	assert.Equal(t, testClockStart(), got.LastUpdatedTime)

	accts, err := dst.DescribeAccountPolicies(ctx, driver.DescribeAccountPoliciesInput{PolicyType: driver.AccountPolicyDataProtection})
	require.NoError(t, err)
	require.Len(t, accts, 1)

	// The restored policies still mask new events.
	putOne(t, dst, "c@d.io 10.0.0.2")

	events, err := dst.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "test-group"})
	require.NoError(t, err)
	assert.Equal(t, []string{"****** ********", "****** ********"}, messages(events))
}
