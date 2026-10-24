package rds

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	snsdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// fakeSNS records every publish it receives.
type fakeSNS struct {
	mu   sync.Mutex
	msgs []snsdriver.PublishInput
}

//nolint:gocritic // matches the SNSPublisher signature.
func (f *fakeSNS) Publish(_ context.Context, in snsdriver.PublishInput) (*snsdriver.PublishOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.msgs = append(f.msgs, in)

	return &snsdriver.PublishOutput{MessageID: "m"}, nil
}

func (f *fakeSNS) published() []snsdriver.PublishInput {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]snsdriver.PublishInput(nil), f.msgs...)
}

func newEventTestMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
	)

	return New(opts), fc
}

func eventIDs(events []rdsdriver.Event) []string {
	out := make([]string, 0, len(events))
	for i := range events {
		out = append(out, events[i].EventID)
	}

	return out
}

func TestDescribeEventsRecordsInstanceLifecycle(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", Engine: "postgres", EngineVersion: "15.4"})
	requireNoError(t, err)
	requireNoError(t, m.StopInstance(ctx, "db1"))
	requireNoError(t, m.StartInstance(ctx, "db1"))
	requireNoError(t, m.RebootInstance(ctx, "db1"))
	requireNoError(t, m.DeleteInstance(ctx, "db1"))

	events, err := m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{SourceType: "db-instance", SourceIdentifier: "db1"})
	requireNoError(t, err)

	want := []string{"RDS-EVENT-0005", "RDS-EVENT-0087", "RDS-EVENT-0088", "RDS-EVENT-0006", "RDS-EVENT-0003"}
	assertStrings(t, want, eventIDs(events))

	first := events[0]
	assertEqual(t, "DB instance created", first.Message)
	assertStrings(t, []string{"creation"}, first.EventCategories)
	assertEqual(t, "arn:aws:rds:us-east-1:123456789012:db:db1", first.SourceARN)
	assertEqual(t, "db-instance", first.SourceType)
}

func TestDescribeEventsFilters(t *testing.T) {
	ctx := context.Background()
	m, fc := newEventTestMock()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "a", Engine: "mysql"})
	requireNoError(t, err)

	fc.Advance(2 * time.Hour)

	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "b", Engine: "mysql"})
	requireNoError(t, err)
	_, err = m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "s1", InstanceID: "b"})
	requireNoError(t, err)

	// Default window is the past hour: instance "a" (2h ago) is excluded.
	got, err := m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{})
	requireNoError(t, err)
	assertStrings(t, []string{"RDS-EVENT-0005", "RDS-EVENT-0040", "RDS-EVENT-0042"}, eventIDs(got))

	// Duration widens the window.
	got, err = m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{Duration: 180, SourceType: "db-instance"})
	requireNoError(t, err)
	assertEqual(t, 2, len(got))

	// StartTime/EndTime bound the window explicitly.
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err = m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{StartTime: start, EndTime: start.Add(time.Minute)})
	requireNoError(t, err)
	assertEqual(t, 1, len(got))
	assertEqual(t, "a", got[0].SourceIdentifier)

	// Category filter.
	got, err = m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{EventCategories: []string{"creation"}, SourceType: "db-snapshot"})
	requireNoError(t, err)
	assertEqual(t, 2, len(got))

	got, err = m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{EventCategories: []string{"deletion"}})
	requireNoError(t, err)
	assertEqual(t, 0, len(got))
}

func TestEventLogRetention(t *testing.T) {
	ctx := context.Background()
	m, fc := newEventTestMock()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "old", Engine: "mysql"})
	requireNoError(t, err)

	fc.Advance(15 * 24 * time.Hour)

	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "new", Engine: "mysql"})
	requireNoError(t, err)

	// A window reaching back past 14 days never returns the expired event.
	got, err := m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{Duration: 30 * 24 * 60})
	requireNoError(t, err)
	assertEqual(t, 1, len(got))
	assertEqual(t, "new", got[0].SourceIdentifier)

	// And the expired event was pruned from the stored log.
	m.mu.RLock()
	n := len(m.events)
	m.mu.RUnlock()
	assertEqual(t, 1, n)
}

func TestEventSubscriptionDeliversToSNS(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()
	sns := &fakeSNS{}
	m.SetSNSPublisher(sns)

	topic := "arn:aws:sns:us-east-1:123456789012:rds-events"

	_, err := m.CreateEventSubscription(ctx, rdsdriver.EventSubscriptionConfig{
		Name: "all-db1", SnsTopicARN: topic, SourceType: "db-instance",
		SourceIDs: []string{"db1"}, Enabled: true,
	})
	requireNoError(t, err)

	_, err = m.CreateEventSubscription(ctx, rdsdriver.EventSubscriptionConfig{
		Name: "deletions", SnsTopicARN: topic, EventCategories: []string{"deletion"}, Enabled: true,
	})
	requireNoError(t, err)

	_, err = m.CreateEventSubscription(ctx, rdsdriver.EventSubscriptionConfig{
		Name: "disabled", SnsTopicARN: topic, Enabled: false,
	})
	requireNoError(t, err)

	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", Engine: "mysql"})
	requireNoError(t, err)
	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "other", Engine: "mysql"})
	requireNoError(t, err)
	requireNoError(t, m.DeleteInstance(ctx, "other"))

	msgs := sns.published()
	// db1 created -> "all-db1"; other created -> none; other deleted -> "deletions".
	assertEqual(t, 2, len(msgs))

	first := msgs[0]
	assertEqual(t, "rds-events", first.TopicID)
	assertEqual(t, "RDS Notification Message", first.Subject)
	assertEqual(t, "RDS-EVENT-0005", first.Attributes["EventID"])
	assertEqual(t, "arn:aws:rds:us-east-1:123456789012:db:db1", first.Attributes["Resource"])

	var body map[string]string
	requireNoError(t, json.Unmarshal([]byte(first.Message), &body))
	assertEqual(t, "db-instance", body["Event Source"])
	assertEqual(t, "2025-01-01 00:00:00.000", body["Event Time"])
	assertEqual(t, "db1", body["Source ID"])
	assertEqual(t, "http://docs.amazonwebservices.com/AmazonRDS/latest/UserGuide/USER_Events.html#RDS-EVENT-0005", body["Event ID"])
	assertEqual(t, "DB instance created", body["Event Message"])
	assertEqual(t, "https://console.aws.amazon.com/rds/home?region=us-east-1#dbinstance:id=db1", body["Identifier Link"])

	var second map[string]string
	requireNoError(t, json.Unmarshal([]byte(msgs[1].Message), &second))
	assertEqual(t, "other", second["Source ID"])
	assertEqual(t, "DB instance deleted", second["Event Message"])
}

func TestEventsForClusterSnapshotParamGroupProxy(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()

	_, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c1", Engine: "aurora-postgresql"})
	requireNoError(t, err)
	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "c1-a", Engine: "aurora-postgresql", ClusterID: "c1"})
	requireNoError(t, err)
	_, err = m.FailoverDBCluster(ctx, "c1", "c1-a")
	requireNoError(t, err)
	requireNoError(t, m.StopCluster(ctx, "c1"))
	requireNoError(t, m.StartCluster(ctx, "c1"))
	_, err = m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{ID: "cs1", ClusterID: "c1"})
	requireNoError(t, err)

	_, err = m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "pg1", Family: "postgres15", Description: "d"})
	requireNoError(t, err)
	_, err = m.ModifyDBParameterGroup(ctx, "pg1", []rdsdriver.Parameter{{Name: "work_mem", Value: "8192", ApplyMethod: "immediate"}})
	requireNoError(t, err)

	_, err = m.CreateDBProxy(ctx, rdsdriver.DBProxyConfig{Name: "px", EngineFamily: "POSTGRESQL"})
	requireNoError(t, err)
	_, err = m.DeleteDBProxy(ctx, "px")
	requireNoError(t, err)

	check := func(sourceType string, want []string) []rdsdriver.Event {
		t.Helper()

		got, err := m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{SourceType: sourceType})
		requireNoError(t, err)
		assertStrings(t, want, eventIDs(got))

		return got
	}

	cl := check("db-cluster", []string{"RDS-EVENT-0170", "RDS-EVENT-0071", "RDS-EVENT-0150", "RDS-EVENT-0151"})
	assertEqual(t, "Completed failover to DB instance: c1-a", cl[1].Message)

	check("db-cluster-snapshot", []string{"RDS-EVENT-0074", "RDS-EVENT-0075"})

	pg := check("db-parameter-group", []string{"RDS-EVENT-0037"})
	assertEqual(t, "Updated parameter work_mem to 8192 with apply method immediate", pg[0].Message)

	px := check("db-proxy", []string{"RDS-EVENT-0203", "RDS-EVENT-0205"})
	assertEqual(t, "RDS created DB proxy px", px[0].Message)
}

func TestEventLogSurvivesSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", Engine: "mysql"})
	requireNoError(t, err)

	raw, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored, _ := newEventTestMock()
	requireNoError(t, restored.Restore(ctx, raw))

	got, err := restored.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{})
	requireNoError(t, err)
	assertStrings(t, []string{"RDS-EVENT-0005"}, eventIDs(got))
}

func assertStrings(t *testing.T, want, got []string) {
	t.Helper()

	if !slices.Equal(want, got) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestTagsOnEventSubscriptionAndDefaultGroup: the "es:" ARN of an event
// subscription and the ARN of a synthesized default parameter group are
// taggable, as Terraform's aws_db_event_subscription requires.
func TestTagsOnEventSubscriptionAndDefaultGroup(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()

	sub, err := m.CreateEventSubscription(ctx, rdsdriver.EventSubscriptionConfig{
		Name: "tfsub", SnsTopicARN: "arn:aws:sns:us-east-1:123456789012:t", Enabled: true,
		Tags: map[string]string{"env": "dev"},
	})
	requireNoError(t, err)

	tags, err := m.ListTagsForResource(ctx, sub.ARN)
	requireNoError(t, err)
	assertEqual(t, "dev", tags["env"])

	requireNoError(t, m.AddTagsToResource(ctx, sub.ARN, map[string]string{"team": "db"}))
	requireNoError(t, m.RemoveTagsFromResource(ctx, sub.ARN, []string{"env"}))

	tags, err = m.ListTagsForResource(ctx, sub.ARN)
	requireNoError(t, err)
	assertEqual(t, 1, len(tags))
	assertEqual(t, "db", tags["team"])

	_, err = m.DeleteEventSubscription(ctx, "tfsub")
	requireNoError(t, err)

	_, err = m.ListTagsForResource(ctx, sub.ARN)
	requireCode(t, err, cerrors.IsNotFound)

	// A re-created subscription of the same name starts untagged.
	_, err = m.CreateEventSubscription(ctx, rdsdriver.EventSubscriptionConfig{
		Name: "tfsub", SnsTopicARN: "arn:aws:sns:us-east-1:123456789012:t", Enabled: true,
	})
	requireNoError(t, err)

	tags, err = m.ListTagsForResource(ctx, sub.ARN)
	requireNoError(t, err)
	assertEqual(t, 0, len(tags))

	pgARN := parameterGroupARN("us-east-1", "123456789012", "default.postgres15")

	tags, err = m.ListTagsForResource(ctx, pgARN)
	requireNoError(t, err)
	assertEqual(t, 0, len(tags))
}

// TestDescribeEventsSourceRulesInProvider: a library caller gets the same
// SourceType / SourceIdentifier validation the wire returns.
func TestDescribeEventsSourceRulesInProvider(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()

	_, err := m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{SourceIdentifier: "db1"})
	requireCode(t, err, cerrors.IsInvalidArgument)
	assertEqual(t, errSourceIdentifierWithoutType, err.(*cerrors.Error).Message) //nolint:errorlint // cerrors.New returns *Error.

	_, err = m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{SourceType: "db-bogus"})
	requireCode(t, err, cerrors.IsInvalidArgument)

	_, err = m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{SourceType: "db-instance", SourceIdentifier: "db1"})
	requireNoError(t, err)
}
