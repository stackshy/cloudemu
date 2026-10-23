package eventbridge

import (
	"context"
	"strings"
	"testing"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSource     = "aws.partner/acme.com/app/events"
	testAccount    = "123456789012"
	otherAccount   = "210987654321"
	testSourceARN  = "arn:aws:events:us-east-1::event-source/" + testSource
	testPartnerQ   = "arn:aws:sqs:us-east-1:123456789012:partner"
	partnerPattern = `{"source":[{"prefix":"aws.partner/"}]}`
)

func partnerEvent(source string) driver.Event {
	return driver.Event{Source: source, DetailType: "t", Detail: `{"k":1}`}
}

func requireSourceState(t *testing.T, m *Mock, want string) *driver.EventSource {
	t.Helper()

	got, err := m.DescribeEventSource(context.Background(), testSource)
	require.NoError(t, err)
	assert.Equal(t, want, got.State)

	return got
}

// TestPartnerEventSourceLifecycle drives a partner source through PENDING,
// ACTIVE (partner event bus), PENDING (deactivate), ACTIVE and DELETED.
func TestPartnerEventSourceLifecycle(t *testing.T) {
	ctx := context.Background()
	m, fc := newTestMock()
	sqs := &recordingSQS{}
	m.SetSQSDeliverer(sqs)

	arn, err := m.CreatePartnerEventSource(ctx, testSource, testAccount)
	require.NoError(t, err)
	assert.Equal(t, testSourceARN, arn)

	src := requireSourceState(t, m, sourceStatePending)
	assert.Equal(t, "acme.com", src.CreatedBy)
	assert.Equal(t, testSourceARN, src.ARN)
	assert.Equal(t, src.CreatedAt.Add(14*24*time.Hour), src.ExpiresAt)

	fc.Advance(time.Minute)

	_, err = m.CreateEventBus(ctx, driver.EventBusConfig{Name: testSource, EventSourceName: testSource})
	require.NoError(t, err)

	src = requireSourceState(t, m, sourceStateActive)
	assert.True(t, src.ExpiresAt.IsZero(), "an ACTIVE source has no expiration time")

	_, err = m.PutRule(ctx, &driver.RuleConfig{Name: "r", EventBus: testSource, EventPattern: partnerPattern})
	require.NoError(t, err)
	require.NoError(t, m.PutTargets(ctx, testSource, "r", []driver.Target{{ID: "q", ARN: testPartnerQ}}))

	res, err := m.PutPartnerEventEntries(ctx, []driver.Event{partnerEvent(testSource)})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.NotEmpty(t, res[0].EventID)

	got := sqs.got(testPartnerQ)
	require.Len(t, got, 1)
	assert.Equal(t, testSource, got[0]["source"])

	require.NoError(t, m.DeactivateEventSource(ctx, testSource))
	src = requireSourceState(t, m, sourceStatePending)
	assert.Equal(t, fc.Now().UTC().Add(14*24*time.Hour), src.ExpiresAt, "expiry counts from deactivation")

	res, err = m.PutPartnerEventEntries(ctx, []driver.Event{partnerEvent(testSource)})
	require.NoError(t, err)
	assert.NotEmpty(t, res[0].EventID, "an event for a PENDING source is accepted")
	assert.Len(t, sqs.got(testPartnerQ), 1, "and dropped")

	require.NoError(t, m.ActivateEventSource(ctx, testSource))
	requireSourceState(t, m, sourceStateActive)

	require.NoError(t, m.DeletePartnerEventSource(ctx, testSource, testAccount))
	requireSourceState(t, m, sourceStateDeleted)

	_, err = m.PutPartnerEventEntries(ctx, []driver.Event{partnerEvent(testSource)})
	require.NoError(t, err)
	assert.Len(t, sqs.got(testPartnerQ), 1, "a DELETED source delivers nothing")

	for _, op := range []func(context.Context, string) error{m.ActivateEventSource, m.DeactivateEventSource} {
		err = op(ctx, testSource)
		assert.True(t, cerrors.IsFailedPrecondition(err), "got %v", err)
		assert.Equal(t, "Event source "+testSource+" is in state DELETED.", cerrors.Message(err))
	}

	_, err = m.GetEventBus(ctx, testSource)
	require.NoError(t, err, "deleting the source leaves the partner event bus")

	_, err = m.CreatePartnerEventSource(ctx, testSource, testAccount)
	require.NoError(t, err, "a deleted source can be created again")
	requireSourceState(t, m, sourceStatePending)
}

func TestPartnerEventSourcePartnerSide(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	_, err := m.CreatePartnerEventSource(ctx, testSource, testAccount)
	require.NoError(t, err)

	_, err = m.CreatePartnerEventSource(ctx, testSource, testAccount)
	assert.True(t, cerrors.IsAlreadyExists(err))
	assert.Equal(t, "Event source "+testSource+" already exists.", cerrors.Message(err))

	_, err = m.CreatePartnerEventSource(ctx, testSource, otherAccount)
	require.NoError(t, err)

	_, err = m.CreatePartnerEventSource(ctx, "aws.partner/acme.com/other", otherAccount)
	require.NoError(t, err)

	got, err := m.DescribePartnerEventSource(ctx, testSource)
	require.NoError(t, err)
	assert.Equal(t, driver.PartnerEventSource{Name: testSource, ARN: testSourceARN}, *got)

	_, err = m.DescribePartnerEventSource(ctx, "aws.partner/acme.com/nope")
	assert.True(t, cerrors.IsNotFound(err))

	list, next, err := m.ListPartnerEventSources(ctx, driver.EventSourceFilter{NamePrefix: "aws.partner/acme.com/", Limit: 1})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "aws.partner/acme.com/app/events", list[0].Name)
	require.NotEmpty(t, next)

	list, next, err = m.ListPartnerEventSources(ctx, driver.EventSourceFilter{NamePrefix: "aws.partner/acme.com/", NextToken: next})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "aws.partner/acme.com/other", list[0].Name)
	assert.Empty(t, next)

	accounts, _, err := m.ListPartnerEventSourceAccounts(ctx, testSource, 0, "")
	require.NoError(t, err)
	require.Len(t, accounts, 2)
	assert.Equal(t, testAccount, accounts[0].Account)
	assert.Equal(t, otherAccount, accounts[1].Account)
	assert.Equal(t, sourceStatePending, accounts[1].State)

	_, _, err = m.ListPartnerEventSourceAccounts(ctx, "aws.partner/acme.com/nope", 0, "")
	assert.True(t, cerrors.IsNotFound(err))

	require.NoError(t, m.DeletePartnerEventSource(ctx, testSource, otherAccount))
	require.NoError(t, m.DeletePartnerEventSource(ctx, "aws.partner/acme.com/nope", otherAccount), "delete is idempotent")

	accounts, _, err = m.ListPartnerEventSourceAccounts(ctx, testSource, 0, "")
	require.NoError(t, err)
	assert.Equal(t, sourceStateDeleted, accounts[1].State)
	assert.True(t, accounts[1].ExpiresAt.IsZero())
	assert.Equal(t, sourceStatePending, accounts[0].State, "other accounts keep their state")
}

// TestEventSourceCustomerView proves the customer side sees only sources
// created for this account.
func TestEventSourceCustomerView(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	_, err := m.CreatePartnerEventSource(ctx, testSource, testAccount)
	require.NoError(t, err)

	_, err = m.CreatePartnerEventSource(ctx, "aws.partner/acme.com/elsewhere", otherAccount)
	require.NoError(t, err)

	list, _, err := m.ListEventSources(ctx, driver.EventSourceFilter{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, testSource, list[0].Name)

	list, _, err = m.ListEventSources(ctx, driver.EventSourceFilter{NamePrefix: "aws.partner/other"})
	require.NoError(t, err)
	assert.Empty(t, list)

	_, err = m.DescribeEventSource(ctx, "aws.partner/acme.com/elsewhere")
	assert.True(t, cerrors.IsNotFound(err), "a source for another account is invisible")
	assert.Equal(t, "Event source aws.partner/acme.com/elsewhere does not exist.", cerrors.Message(err))

	for _, op := range []func(context.Context, string) error{m.ActivateEventSource, m.DeactivateEventSource} {
		assert.True(t, cerrors.IsNotFound(op(ctx, "aws.partner/acme.com/elsewhere")))
	}
}

func TestPartnerEventBusCreateErrors(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	_, err := m.CreatePartnerEventSource(ctx, testSource, testAccount)
	require.NoError(t, err)

	_, err = m.CreateEventBus(ctx, driver.EventBusConfig{Name: "custom", EventSourceName: testSource})
	assert.True(t, cerrors.IsInvalidArgument(err), "the bus must be named after its source: %v", err)

	_, err = m.CreateEventBus(ctx, driver.EventBusConfig{Name: "aws.partner/acme.com/x", EventSourceName: "aws.partner/acme.com/x"})
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.CreateEventBus(ctx, driver.EventBusConfig{Name: "bad", EventSourceName: "bad"})
	assert.True(t, cerrors.IsInvalidArgument(err))
	assert.Contains(t, cerrors.Message(err), "at 'eventSourceName' failed to satisfy constraint")

	_, err = m.CreateEventBus(ctx, driver.EventBusConfig{Name: testSource, EventSourceName: testSource})
	require.NoError(t, err)

	_, err = m.CreateEventBus(ctx, driver.EventBusConfig{Name: testSource, EventSourceName: testSource})
	assert.True(t, cerrors.IsAlreadyExists(err))

	require.NoError(t, m.DeleteEventBus(ctx, testSource))
	requireSourceState(t, m, sourceStatePending)

	require.NoError(t, m.DeletePartnerEventSource(ctx, testSource, testAccount))

	_, err = m.CreateEventBus(ctx, driver.EventBusConfig{Name: testSource, EventSourceName: testSource})
	assert.True(t, cerrors.IsFailedPrecondition(err), "a DELETED source cannot get a bus: %v", err)

	_, err = m.GetEventBus(ctx, testSource)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestPartnerSourceValidation(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	_, err := m.CreatePartnerEventSource(ctx, "aws.partner/acme.com", "12")
	require.Error(t, err)
	assert.Equal(t, "3 validation errors detected: "+
		"Value 'aws.partner/acme.com' at 'name' failed to satisfy constraint: Member must satisfy regular expression pattern: "+
		eventSourceNamePattern+"; "+
		"Value '12' at 'account' failed to satisfy constraint: Member must have length greater than or equal to 12; "+
		"Value '12' at 'account' failed to satisfy constraint: Member must satisfy regular expression pattern: \\d{12}",
		cerrors.Message(err))

	_, err = m.CreatePartnerEventSource(ctx, "", "")
	assert.Equal(t, "2 validation errors detected: Value null at 'name' failed to satisfy constraint: Member must not be null; "+
		"Value null at 'account' failed to satisfy constraint: Member must not be null", cerrors.Message(err))

	_, _, err = m.ListPartnerEventSources(ctx, driver.EventSourceFilter{})
	assert.True(t, cerrors.IsInvalidArgument(err), "NamePrefix is required")

	_, _, err = m.ListPartnerEventSources(ctx, driver.EventSourceFilter{NamePrefix: "acme"})
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, _, err = m.ListEventSources(ctx, driver.EventSourceFilter{Limit: 101})
	assert.Contains(t, cerrors.Message(err), "at 'limit' failed to satisfy constraint")

	_, _, err = m.ListEventSources(ctx, driver.EventSourceFilter{NextToken: "garbage"})
	assert.Equal(t, msgBadNextPage, cerrors.Message(err))

	_, err = m.DescribeEventSource(ctx, "aws.partner/x")
	assert.True(t, cerrors.IsInvalidArgument(err))
}

func TestPutPartnerEventsValidation(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	_, err := m.PutPartnerEventEntries(ctx, nil)
	assert.Equal(t, "1 validation error detected: Value at 'entries' failed to satisfy constraint: "+
		"Member must have length less than or equal to 20 and greater than or equal to 1.", cerrors.Message(err))

	_, err = m.PutPartnerEventEntries(ctx, make([]driver.Event, 21))
	assert.True(t, cerrors.IsInvalidArgument(err))

	_, err = m.PutPartnerEventEntries(ctx, []driver.Event{partnerEvent("my.app")})
	assert.Equal(t, "1 validation error detected: Value 'my.app' at 'entries.1.member.source' failed to satisfy "+
		"constraint: Member must satisfy regular expression pattern: "+eventSourceNamePattern, cerrors.Message(err))

	big := partnerEvent(testSource)
	big.Detail = `{"k":"` + strings.Repeat("x", 1<<20) + `"}`
	_, err = m.PutPartnerEventEntries(ctx, []driver.Event{big})
	assert.Equal(t, putEventsSizeMessage, cerrors.Message(err))

	res, err := m.PutPartnerEventEntries(ctx, []driver.Event{
		{DetailType: "t", Detail: "{}"},
		{Source: testSource, Detail: "{}"},
		{Source: testSource, DetailType: "t", Detail: "[]"},
		partnerEvent(testSource),
	})
	require.NoError(t, err)
	assert.Equal(t, errCodeInvalidArgument, res[0].ErrorCode)
	assert.Equal(t, msgDetailTypeRequired, res[1].ErrorMessage)
	assert.Equal(t, errCodeMalformedDetail, res[2].ErrorCode)
	assert.NotEmpty(t, res[3].EventID, "an unknown source is accepted and dropped")
}

func TestSnapshotPartnerSources(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	_, err := src.CreatePartnerEventSource(ctx, testSource, testAccount)
	require.NoError(t, err)

	_, err = src.CreateEventBus(ctx, driver.EventBusConfig{Name: testSource, EventSourceName: testSource})
	require.NoError(t, err)

	_, err = src.CreatePartnerEventSource(ctx, "aws.partner/acme.com/b", otherAccount)
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.JSONEq(t, string(data), string(data2))

	got, err := dst.DescribeEventSource(ctx, testSource)
	require.NoError(t, err)
	assert.Equal(t, sourceStateActive, got.State)

	accounts, _, err := dst.ListPartnerEventSourceAccounts(ctx, "aws.partner/acme.com/b", 0, "")
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, otherAccount, accounts[0].Account)
}
