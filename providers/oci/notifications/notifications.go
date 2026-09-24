// Package notifications provides an in-memory mock implementation of OCI
// Notifications (ONS). It implements the portable notification driver: an ONS
// topic is the topic and an ONS subscription is the subscription, with the
// PENDING-until-confirmed step ONS puts in front of delivery.
package notifications

import (
	"context"
	"maps"
	"regexp"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Compile-time check that Mock implements driver.Notification. The OCI-shaped
// capabilities live in server/oci/notifications and are checked there.
var _ driver.Notification = (*Mock)(nil)

const timeFormat = time.RFC3339

// Lifecycle states ONS reports.
const (
	StateActive  = "ACTIVE"
	StatePending = "PENDING"
	StateDeleted = "DELETED"
)

// Subscription statuses as driver.SubscriptionInfo spells them.
const (
	StatusPending   = "pending"
	StatusConfirmed = "confirmed"
)

// OCID resource type segments.
const (
	typeTopic        = "onstopic"
	typeSubscription = "onssubscription"
)

// maxTopicNameLength is the limit ONS puts on a topic name.
const maxTopicNameLength = 256

// shortTopicIDLength is how much of the OCID's opaque suffix ONS reports as
// the topic's short id.
const shortTopicIDLength = 8

// topicNamePattern is the character set ONS allows in a topic name.
var topicNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// metricNamespace is the namespace ONS publishes its metrics under.
const metricNamespace = "oci_notification"

// TopicDetails is the OCI-only state of a topic; driver.TopicInfo has no room
// for it.
type TopicDetails struct {
	ShortTopicID   string
	LifecycleState string
	TimeCreated    string
	Etag           string
}

type topicData struct {
	ID             string
	Name           string
	Description    string
	ShortTopicID   string
	LifecycleState string
	TimeCreated    string
	Etag           string
	Scope          scope.Scope
	FreeformTags   map[string]string
}

// Mock is an in-memory mock implementation of OCI Notifications.
type Mock struct {
	// mu guards the stored values and spans the reads and writes a single
	// operation makes across stores: a publish walks the subscriptions of a
	// topic it has just read, and deleting a topic drops both.
	mu sync.RWMutex

	topics *memstore.Store[*topicData]
	subs   *memstore.Store[*Subscription]
	// deliveries records what each subscription received, keyed by its OCID.
	// Real ONS pushes to the endpoint; the emulator has nowhere to push.
	deliveries *memstore.Store[[]Message]

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new OCI Notifications mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		topics:     memstore.New[*topicData](),
		subs:       memstore.New[*Subscription](),
		deliveries: memstore.New[[]Message](),
		opts:       opts,
	}
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.monitoring = mon
}

// now returns the current time in OCI's timestamp format.
func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(timeFormat)
}

// CreateTopic creates an ONS topic.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateTopic(_ context.Context, cfg driver.TopicConfig) (*driver.TopicInfo, error) {
	if err := validateTopicName(cfg.Name); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	place := cfg.Scope
	if place.Compartment == "" {
		place.Compartment = m.opts.CompartmentID
	}

	if m.topicByName(place.Compartment, cfg.Name) != nil {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "topic %q already exists in compartment %s",
			cfg.Name, place.Compartment)
	}

	id := idgen.OCID(typeTopic, m.opts.Realm, m.opts.OCIRegion())

	td := &topicData{
		ID:             id,
		Name:           cfg.Name,
		Description:    cfg.DisplayName,
		ShortTopicID:   shortTopicID(id),
		LifecycleState: StateActive,
		TimeCreated:    m.now(),
		Etag:           idgen.GenerateID("etag-"),
		Scope:          place,
		FreeformTags:   maps.Clone(cfg.Tags),
	}

	m.topics.Set(id, td)

	return m.topicInfo(td), nil
}

// GetTopic returns a topic by OCID.
func (m *Mock) GetTopic(_ context.Context, id string) (*driver.TopicInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, ok := m.topics.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "topic %q not found", id)
	}

	return m.topicInfo(td), nil
}

// ListTopics lists the topics visible under a compartment filter.
func (m *Mock) ListTopics(_ context.Context, filter scope.Scope) ([]driver.TopicInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.topics.SortedValues()
	out := make([]driver.TopicInfo, 0, len(all))

	for _, td := range all {
		if !td.Scope.Matches(filter) {
			continue
		}

		out = append(out, *m.topicInfo(td))
	}

	return out, nil
}

// UpdateTopic replaces a topic's mutable fields. cfg.Name identifies the topic
// by OCID or by name; an ONS topic cannot be renamed, so the name is never a
// new value. An empty field leaves the stored one alone.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) UpdateTopic(_ context.Context, cfg driver.TopicConfig) (*driver.TopicInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	td := m.resolveTopic(cfg.Name, cfg.Scope.Compartment)
	if td == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "topic %q not found", cfg.Name)
	}

	if cfg.DisplayName != "" {
		td.Description = cfg.DisplayName
	}

	if cfg.Tags != nil {
		td.FreeformTags = maps.Clone(cfg.Tags)
	}

	if !cfg.Scope.IsZero() {
		td.Scope = cfg.Scope
	}

	td.Etag = idgen.GenerateID("etag-")

	m.topics.Set(td.ID, td)

	return m.topicInfo(td), nil
}

// DeleteTopic deletes a topic and every subscription on it, as ONS does.
func (m *Mock) DeleteTopic(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.topics.Has(id) {
		return cerrors.Newf(cerrors.NotFound, "topic %q not found", id)
	}

	m.topics.Delete(id)

	for subID, sub := range m.subs.All() {
		if sub.TopicID == id {
			m.subs.Delete(subID)
			m.deliveries.Delete(subID)
		}
	}

	return nil
}

// TopicDetails returns the OCI-only state of a topic. It is an OPTIONAL
// capability, discovered by type assertion.
func (m *Mock) TopicDetails(id string) (TopicDetails, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, ok := m.topics.Get(id)
	if !ok {
		return TopicDetails{}, false
	}

	return TopicDetails{
		ShortTopicID:   td.ShortTopicID,
		LifecycleState: td.LifecycleState,
		TimeCreated:    td.TimeCreated,
		Etag:           td.Etag,
	}, true
}

// topicInfo projects stored state onto the portable shape. The caller holds mu.
func (m *Mock) topicInfo(td *topicData) *driver.TopicInfo {
	count := 0

	for _, sub := range m.subs.All() {
		if sub.TopicID == td.ID {
			count++
		}
	}

	return &driver.TopicInfo{
		ID:                td.ID,
		Name:              td.Name,
		ResourceID:        td.ID,
		DisplayName:       td.Description,
		SubscriptionCount: count,
		Tags:              maps.Clone(td.FreeformTags),
		Scope:             td.Scope,
	}
}

// topicByName finds a topic by name within a compartment. The caller holds mu.
func (m *Mock) topicByName(compartment, name string) *topicData {
	for _, td := range m.topics.SortedValues() {
		if td.Name == name && td.Scope.Compartment == compartment {
			return td
		}
	}

	return nil
}

// resolveTopic finds a topic by OCID, falling back to its name. The caller
// holds mu.
func (m *Mock) resolveTopic(ref, compartment string) *topicData {
	if td, ok := m.topics.Get(ref); ok {
		return td
	}

	if compartment == "" {
		compartment = m.opts.CompartmentID
	}

	return m.topicByName(compartment, ref)
}

// emitMetric records an ONS metric. Called with mu released.
func (m *Mock) emitMetric(name string, value float64, dims map[string]string) {
	m.mu.RLock()
	mon := m.monitoring
	m.mu.RUnlock()

	if mon == nil {
		return
	}

	_ = mon.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: metricNamespace, MetricName: name, Value: value, Unit: "Count",
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

// validateTopicName applies the constraints ONS puts on a topic name.
func validateTopicName(name string) error {
	switch {
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "topic name is required")
	case len(name) > maxTopicNameLength:
		return cerrors.Newf(cerrors.InvalidArgument, "topic name must be at most %d characters", maxTopicNameLength)
	case !topicNamePattern.MatchString(name):
		return cerrors.Newf(cerrors.InvalidArgument,
			"topic name %q may contain only letters, numbers, dashes and underscores", name)
	}

	return nil
}

// shortTopicID is the leading run of the OCID's opaque suffix, which ONS
// reports alongside the full OCID.
func shortTopicID(ocid string) string {
	suffix := ocid[len(ocid)-min(len(ocid), shortTopicIDLength):]

	return suffix
}
