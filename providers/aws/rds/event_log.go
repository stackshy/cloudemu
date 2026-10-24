package rds

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	snsdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// RDS event source types (the DescribeEvents / CreateEventSubscription
// SourceType values).
const (
	sourceDBInstance        = "db-instance"
	sourceDBCluster         = "db-cluster"
	sourceDBSnapshot        = "db-snapshot"
	sourceDBClusterSnapshot = "db-cluster-snapshot"
	sourceDBParameterGroup  = "db-parameter-group"
	sourceDBProxy           = "db-proxy"
	sourceDBSecurityGroup   = "db-security-group"
)

// RDS event categories ("Amazon RDS event categories and event messages").
const (
	catAvailability        = "availability"
	catBackup              = "backup"
	catConfigurationChange = "configuration change"
	catCreation            = "creation"
	catDeletion            = "deletion"
	catFailover            = "failover"
	catFailure             = "failure"
	catMaintenance         = "maintenance"
	catNotification        = "notification"
	catRecovery            = "recovery"
	catRestoration         = "restoration"
)

// Event-log bounds. Real RDS returns events for the past 14 days and, absent a
// time window, the past hour (DescribeEvents API reference). maxRetainedEvents
// additionally caps the in-memory log so a long-running emulator under heavy
// churn stays bounded even within the 14-day window.
const (
	eventRetention          = 14 * 24 * time.Hour
	defaultEventWindowMins  = 60
	maxRetainedEvents       = 10000
	rdsNotificationSubject  = "RDS Notification Message"
	rdsEventDocURLPrefix    = "http://docs.amazonwebservices.com/AmazonRDS/latest/UserGuide/USER_Events.html#"
	rdsNotificationTimeFmt  = "2006-01-02 15:04:05.000"
	snsStringAttributeType  = "String"
	rdsConsoleInstanceLink  = "https://console.aws.amazon.com/rds/home?region=%s#dbinstance:id=%s"
	rdsEventAttributeID     = "EventID"
	rdsEventAttributeSource = "Resource"
)

// eventSpec is one entry of the "Amazon RDS event categories and event
// messages" catalog: the RDS event ID, its category, and its message (a
// fmt format when the documented message carries {{name}} placeholders).
// Messages follow the catalog wording without the trailing period, matching
// the Message text RDS returns from DescribeEvents and in notifications.
type eventSpec struct {
	id       string
	category string
	message  string
}

// The lifecycle events the emulator emits, verbatim from
// https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_Events.Messages.html
// and https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/USER_Events.Messages.html.
//
//nolint:gochecknoglobals // static event catalog
var (
	evInstanceDeleted   = eventSpec{"RDS-EVENT-0003", catDeletion, "DB instance deleted"}
	evInstanceCreated   = eventSpec{"RDS-EVENT-0005", catCreation, "DB instance created"}
	evInstanceRestarted = eventSpec{"RDS-EVENT-0006", catAvailability, "DB instance restarted"}
	evInstancePITR      = eventSpec{"RDS-EVENT-0019", catRestoration, "Restored from DB instance %s to %s"}
	evInstanceStopped   = eventSpec{"RDS-EVENT-0087", catNotification, "DB instance stopped"}
	evInstanceStarted   = eventSpec{"RDS-EVENT-0088", catNotification, "DB instance started"}

	evParameterUpdated = eventSpec{"RDS-EVENT-0037", catConfigurationChange, "Updated parameter %s to %s with apply method %s"}

	evSnapshotCreating = eventSpec{"RDS-EVENT-0040", catCreation, "Creating manual snapshot"}
	evSnapshotDeleted  = eventSpec{"RDS-EVENT-0041", catDeletion, "Deleted user snapshot"}
	evSnapshotCreated  = eventSpec{"RDS-EVENT-0042", catCreation, "Manual snapshot created"}

	evClusterFailover = eventSpec{"RDS-EVENT-0071", catFailover, "Completed failover to DB instance: %s"}
	evClusterStopped  = eventSpec{"RDS-EVENT-0150", catNotification, "DB cluster stopped"}
	evClusterStarted  = eventSpec{"RDS-EVENT-0151", catNotification, "DB cluster started"}
	evClusterCreated  = eventSpec{"RDS-EVENT-0170", catCreation, "DB cluster created"}
	evClusterDeleted  = eventSpec{"RDS-EVENT-0171", catDeletion, "DB cluster deleted"}

	evClusterSnapshotCreating = eventSpec{"RDS-EVENT-0074", catBackup, "Creating manual cluster snapshot"}
	evClusterSnapshotCreated  = eventSpec{"RDS-EVENT-0075", catBackup, "Manual cluster snapshot created"}

	evProxyCreated  = eventSpec{"RDS-EVENT-0203", catCreation, "RDS created DB proxy %s"}
	evProxyModified = eventSpec{"RDS-EVENT-0204", catConfigurationChange, "RDS modified DB proxy %s"}
	evProxyDeleted  = eventSpec{"RDS-EVENT-0205", catDeletion, "RDS deleted DB proxy %s"}
)

// SNSPublisher publishes a message to an SNS topic. The SNS mock satisfies it,
// so an RDS event matching an enabled event subscription is delivered to the
// subscription's topic (and from there to its SQS/Lambda subscribers).
type SNSPublisher interface {
	Publish(ctx context.Context, input snsdriver.PublishInput) (*snsdriver.PublishOutput, error)
}

// SetSNSPublisher wires the SNS backend that event subscriptions deliver to.
// Without it events are still recorded for DescribeEvents but not delivered.
func (m *Mock) SetSNSPublisher(p SNSPublisher) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sns = p
}

// eventNotification is one queued delivery of an event to a subscription's
// SNS topic. Deliveries are queued under the lock and published after it is
// released (flushEventNotifications), so an SNS subscriber that calls back
// into RDS can never deadlock on the provider lock.
type eventNotification struct {
	topicARN string
	input    snsdriver.PublishInput
}

// eventSource names the resource an event is about.
type eventSource struct {
	sourceType string
	id         string
	arn        string
}

// recordEventLocked appends an event to the log and queues a notification for
// every enabled subscription that matches it. The caller holds m.mu (write)
// and must call flushEventNotifications after releasing it.
func (m *Mock) recordEventLocked(src eventSource, spec eventSpec, args ...any) {
	msg := spec.message
	if len(args) > 0 {
		msg = fmt.Sprintf(spec.message, args...)
	}

	ev := rdsdriver.Event{
		SourceIdentifier: src.id,
		SourceType:       src.sourceType,
		SourceARN:        src.arn,
		Message:          msg,
		EventCategories:  []string{spec.category},
		Date:             m.opts.Clock.Now().UTC(),
		EventID:          spec.id,
	}

	m.events = append(pruneEvents(m.events, ev.Date), ev)
	if over := len(m.events) - maxRetainedEvents; over > 0 {
		m.events = slices.Clone(m.events[over:])
	}

	if m.sns == nil {
		return
	}

	subs := m.eventSubs.SortedValues()
	for i := range subs {
		if !subscriptionMatches(&subs[i], &ev) {
			continue
		}

		m.outbox = append(m.outbox, eventNotification{
			topicARN: subs[i].SnsTopicARN,
			input:    m.notificationFor(subs[i].SnsTopicARN, &ev),
		})
	}
}

// pruneEvents drops events older than the 14-day retention window.
func pruneEvents(events []rdsdriver.Event, now time.Time) []rdsdriver.Event {
	cutoff := now.Add(-eventRetention)

	i := 0
	for i < len(events) && events[i].Date.Before(cutoff) {
		i++
	}

	if i == 0 {
		return events
	}

	return slices.Clone(events[i:])
}

// subscriptionMatches applies an event subscription's filters: it must be
// enabled, and its SourceType, SourceIds and EventCategories (each "all" when
// empty) must admit the event.
func subscriptionMatches(sub *rdsdriver.EventSubscription, ev *rdsdriver.Event) bool {
	if !sub.Enabled {
		return false
	}

	if sub.SourceType != "" && sub.SourceType != ev.SourceType {
		return false
	}

	if len(sub.SourceIDs) > 0 && !slices.Contains(sub.SourceIDs, ev.SourceIdentifier) {
		return false
	}

	if len(sub.EventCategories) == 0 {
		return true
	}

	for _, c := range ev.EventCategories {
		if slices.Contains(sub.EventCategories, c) {
			return true
		}
	}

	return false
}

// rdsNotificationBody is the JSON message RDS publishes to a subscription's
// SNS topic. Field names and order follow the RDS-to-SNS sample in
// https://docs.aws.amazon.com/lambda/latest/dg/services-rds.html.
type rdsNotificationBody struct {
	EventSource    string `json:"Event Source"`
	EventTime      string `json:"Event Time"`
	IdentifierLink string `json:"Identifier Link,omitempty"`
	SourceID       string `json:"Source ID"`
	EventID        string `json:"Event ID"`
	EventMessage   string `json:"Event Message"`
}

// notificationFor builds the SNS publish for an event: the JSON body, the
// "RDS Notification Message" subject, and the EventID / Resource message
// attributes RDS attaches (USER_Events.TagsAttributesForFiltering).
func (m *Mock) notificationFor(topicARN string, ev *rdsdriver.Event) snsdriver.PublishInput {
	body := rdsNotificationBody{
		EventSource:  ev.SourceType,
		EventTime:    ev.Date.UTC().Format(rdsNotificationTimeFmt),
		SourceID:     ev.SourceIdentifier,
		EventID:      rdsEventDocURLPrefix + ev.EventID,
		EventMessage: ev.Message,
	}

	// The documented sample only shows the console link of a DB instance, so
	// other source types omit it rather than invent a URL shape.
	if ev.SourceType == sourceDBInstance {
		body.IdentifierLink = fmt.Sprintf(rdsConsoleInstanceLink, arnRegion(ev.SourceARN, m.opts.Region), ev.SourceIdentifier)
	}

	raw, _ := json.Marshal(body) //nolint:errchkjson // a struct of strings always marshals.

	attrs := map[string]string{rdsEventAttributeID: ev.EventID}
	entries := map[string]snsdriver.MessageAttribute{
		rdsEventAttributeID: {DataType: snsStringAttributeType, Value: ev.EventID},
	}

	if ev.SourceARN != "" {
		attrs[rdsEventAttributeSource] = ev.SourceARN
		entries[rdsEventAttributeSource] = snsdriver.MessageAttribute{DataType: snsStringAttributeType, Value: ev.SourceARN}
	}

	return snsdriver.PublishInput{
		TopicID:          arnLastField(topicARN),
		Subject:          rdsNotificationSubject,
		Message:          string(raw),
		Attributes:       attrs,
		AttributeEntries: entries,
	}
}

// arnLastField returns the segment after the last colon of an ARN (the SNS
// topic name for a topic ARN).
func arnLastField(arn string) string {
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == ':' {
			return arn[i+1:]
		}
	}

	return arn
}

// flushEventNotifications publishes every queued event notification. It must
// be called WITHOUT m.mu held. Delivery is best-effort, like real RDS whose
// notifications are asynchronous: a missing topic or failed publish never
// fails the RDS operation that raised the event.
func (m *Mock) flushEventNotifications() {
	m.mu.Lock()
	pending := m.outbox
	m.outbox = nil
	sns := m.sns
	m.mu.Unlock()

	if sns == nil {
		return
	}

	for i := range pending {
		_, _ = sns.Publish(context.Background(), pending[i].input)
	}
}

// recordEvent records an event (taking the lock) and delivers its
// notifications. For call sites that do not already hold m.mu.
func (m *Mock) recordEvent(src eventSource, spec eventSpec, args ...any) {
	m.mu.Lock()
	m.recordEventLocked(src, spec, args...)
	m.mu.Unlock()

	m.flushEventNotifications()
}

// eventSourceTypes are DescribeEvents' valid SourceType values (DescribeEvents
// API reference, SourceType "Valid Values").
//
//nolint:gochecknoglobals // static lookup table
var eventSourceTypes = map[string]struct{}{
	sourceDBInstance: {}, sourceDBParameterGroup: {}, sourceDBSecurityGroup: {}, sourceDBSnapshot: {},
	sourceDBCluster: {}, sourceDBClusterSnapshot: {}, "custom-engine-version": {}, sourceDBProxy: {},
	"blue-green-deployment": {}, "db-shard-group": {}, "zero-etl": {},
}

// errSourceIdentifierWithoutType is the message for a DescribeEvents
// SourceIdentifier given without a SourceType. The wire handler maps an
// InvalidArgument carrying the "must also be provided" wording to
// InvalidParameterCombination, the fault real RDS returns.
const errSourceIdentifierWithoutType = "If SourceIdentifier is supplied, SourceType must also be provided."

// validateEventSource checks DescribeEvents' SourceType / SourceIdentifier: the
// type must be one of the documented values, and "If SourceIdentifier is
// supplied, SourceType must also be provided" (DescribeEvents API reference).
func validateEventSource(sourceType, sourceID string) error {
	if sourceType != "" {
		if _, ok := eventSourceTypes[sourceType]; !ok {
			return cerrors.New(cerrors.InvalidArgument, "Invalid source type: "+sourceType)
		}
	}

	if sourceID != "" && sourceType == "" {
		return cerrors.New(cerrors.InvalidArgument, errSourceIdentifierWithoutType)
	}

	return nil
}

// DescribeEvents returns the recorded events matching the input's filters,
// oldest first. The time window defaults to the past 60 minutes (or Duration
// minutes) ending at EndTime (default now), and never reaches back further
// than the 14-day retention window. An unknown SourceType, or a
// SourceIdentifier without a SourceType, is rejected with InvalidArgument.
//
//nolint:gocritic // input matches the driver interface signature.
func (m *Mock) DescribeEvents(_ context.Context, input rdsdriver.DescribeEventsInput) ([]rdsdriver.Event, error) {
	if err := validateEventSource(input.SourceType, input.SourceIdentifier); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := m.opts.Clock.Now()

	end := input.EndTime
	if end.IsZero() {
		end = now
	}

	start := input.StartTime
	if start.IsZero() {
		mins := input.Duration
		if mins <= 0 {
			mins = defaultEventWindowMins
		}

		start = end.Add(-time.Duration(mins) * time.Minute)
	}

	if floor := now.Add(-eventRetention); start.Before(floor) {
		start = floor
	}

	out := make([]rdsdriver.Event, 0)

	for i := range m.events {
		ev := &m.events[i]
		if ev.Date.Before(start) || ev.Date.After(end) {
			continue
		}

		if !eventMatchesFilter(ev, &input) {
			continue
		}

		c := *ev
		c.EventCategories = cloneSlice(ev.EventCategories)
		out = append(out, c)
	}

	return out, nil
}

// eventMatchesFilter applies DescribeEvents' SourceType, SourceIdentifier and
// EventCategories filters.
func eventMatchesFilter(ev *rdsdriver.Event, input *rdsdriver.DescribeEventsInput) bool {
	if input.SourceType != "" && ev.SourceType != input.SourceType {
		return false
	}

	if input.SourceIdentifier != "" && ev.SourceIdentifier != input.SourceIdentifier {
		return false
	}

	if len(input.EventCategories) == 0 {
		return true
	}

	for _, c := range ev.EventCategories {
		if slices.Contains(input.EventCategories, c) {
			return true
		}
	}

	return false
}
