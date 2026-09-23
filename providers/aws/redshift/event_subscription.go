package redshift

import (
	"context"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	snsdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// Event subscription limits and defaults, from the Redshift API reference.
const (
	maxSubscriptionNameLen = 255
	maxSubscriptionTags    = 50
	defaultSeverity        = "INFO"
	subscriptionActive     = "active"
)

// Source types that name a resource cloudemu stores.
const (
	sourceCluster         = "cluster"
	sourceParameterGroup  = "cluster-parameter-group"
	sourceSecurityGroup   = "cluster-security-group"
	sourceClusterSnapshot = "cluster-snapshot"
	sourceScheduledAction = "scheduled-action"
)

// EventSubscription is a Redshift event notification subscription.
type EventSubscription struct {
	Name            string
	ARN             string
	CustomerAWSID   string
	SnsTopicARN     string
	Status          string
	SourceType      string
	SourceIDs       []string
	EventCategories []string
	Severity        string
	Enabled         bool
	CreatedAt       time.Time
}

// EventSubscriptionConfig is the CreateEventSubscription input.
type EventSubscriptionConfig struct {
	Name            string
	SnsTopicARN     string
	SourceType      string
	SourceIDs       []string
	EventCategories []string
	Severity        string
	Enabled         *bool
	Tags            map[string]string
}

// ModifyEventSubscriptionInput holds the fields to change. A nil field is
// left as it is.
type ModifyEventSubscriptionInput struct {
	SnsTopicARN     *string
	SourceType      *string
	SourceIDs       *[]string
	EventCategories *[]string
	Severity        *string
	Enabled         *bool
}

// TopicLookup finds an SNS topic by name. It lets a subscription check that
// its topic exists.
type TopicLookup interface {
	GetTopic(ctx context.Context, id string) (*snsdriver.TopicInfo, error)
}

// SetTopicLookup wires the SNS mock in. Without it the topic ARN is only
// checked for shape.
func (m *Mock) SetTopicLookup(t TopicLookup) {
	m.topics = t
}

//nolint:gochecknoglobals // static lookup table
var eventSourceTypes = map[string]struct{}{
	sourceCluster: {}, sourceParameterGroup: {}, sourceSecurityGroup: {},
	sourceClusterSnapshot: {}, sourceScheduledAction: {},
}

//nolint:gochecknoglobals // static lookup table
var eventCategories = map[string]struct{}{
	"configuration": {}, "management": {}, "monitoring": {}, "security": {}, "pending": {},
}

//nolint:gochecknoglobals // static lookup table
var eventSeverities = map[string]struct{}{"ERROR": {}, "INFO": {}}

func eventSubscriptionARN(region, accountID, name string) string {
	return idgen.AWSARN("redshift", region, accountID, "eventsubscription:"+name)
}

func errSubscriptionNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "event subscription %q not found", name)
}

// CreateEventSubscription creates a subscription and stores its tags.
//
//nolint:gocritic // cfg is passed by value like the other create inputs here.
func (m *Mock) CreateEventSubscription(ctx context.Context, cfg EventSubscriptionConfig) (*EventSubscription, error) {
	if err := validateSubscriptionName(cfg.Name); err != nil {
		return nil, err
	}

	if len(cfg.Tags) > maxSubscriptionTags {
		return nil, cerrors.Newf(cerrors.ResourceExhausted, "number of tags exceeds the limit of %d", maxSubscriptionTags)
	}

	sub := EventSubscription{
		Name:            cfg.Name,
		ARN:             eventSubscriptionARN(m.opts.Region, m.opts.AccountID, cfg.Name),
		CustomerAWSID:   m.opts.AccountID,
		SnsTopicARN:     cfg.SnsTopicARN,
		Status:          subscriptionActive,
		SourceType:      cfg.SourceType,
		SourceIDs:       cloneStrings(cfg.SourceIDs),
		EventCategories: cloneStrings(cfg.EventCategories),
		Severity:        cfg.Severity,
		Enabled:         cfg.Enabled == nil || *cfg.Enabled,
		CreatedAt:       m.opts.Clock.Now().UTC(),
	}

	if sub.Severity == "" {
		sub.Severity = defaultSeverity
	}

	if err := m.validateTopic(ctx, sub.SnsTopicARN); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateSubscription(&sub); err != nil {
		return nil, err
	}

	if m.eventSubs.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "event subscription %q already exists", cfg.Name)
	}

	m.eventSubs.Set(cfg.Name, sub)
	m.setTagsLocked(sub.ARN, cfg.Tags)

	out := cloneSubscription(sub)

	return &out, nil
}

// DescribeEventSubscriptions returns the named subscription, or all of them
// sorted by name when name is empty.
func (m *Mock) DescribeEventSubscriptions(_ context.Context, name string) ([]EventSubscription, error) {
	if name != "" {
		sub, ok := m.eventSubs.Get(name)
		if !ok {
			return nil, errSubscriptionNotFound(name)
		}

		return []EventSubscription{cloneSubscription(sub)}, nil
	}

	all := m.eventSubs.SortedValues()
	for i := range all {
		all[i] = cloneSubscription(all[i])
	}

	return all, nil
}

// ModifyEventSubscription applies the set fields and checks the result.
func (m *Mock) ModifyEventSubscription(
	ctx context.Context, name string, in ModifyEventSubscriptionInput,
) (*EventSubscription, error) {
	if in.SnsTopicARN != nil && *in.SnsTopicARN != "" {
		if err := m.validateTopic(ctx, *in.SnsTopicARN); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.eventSubs.Get(name)
	if !ok {
		return nil, errSubscriptionNotFound(name)
	}

	applySubscriptionChanges(&sub, &in)

	if err := m.validateSubscription(&sub); err != nil {
		return nil, err
	}

	m.eventSubs.Set(name, sub)

	out := cloneSubscription(sub)

	return &out, nil
}

// DeleteEventSubscription removes a subscription and its tags.
func (m *Mock) DeleteEventSubscription(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.eventSubs.Get(name)
	if !ok {
		return errSubscriptionNotFound(name)
	}

	m.eventSubs.Delete(name)
	delete(m.tagsByARN, sub.ARN)

	return nil
}

func applySubscriptionChanges(sub *EventSubscription, in *ModifyEventSubscriptionInput) {
	if in.SnsTopicARN != nil && *in.SnsTopicARN != "" {
		sub.SnsTopicARN = *in.SnsTopicARN
	}

	if in.SourceType != nil {
		sub.SourceType = *in.SourceType
	}

	if in.SourceIDs != nil {
		sub.SourceIDs = cloneStrings(*in.SourceIDs)
	}

	if in.EventCategories != nil {
		sub.EventCategories = cloneStrings(*in.EventCategories)
	}

	if in.Severity != nil && *in.Severity != "" {
		sub.Severity = *in.Severity
	}

	if in.Enabled != nil {
		sub.Enabled = *in.Enabled
	}
}

// validateSubscriptionName applies the Redshift naming rules: 1 to 255
// letters, digits or hyphens, starting with a letter, with no trailing
// hyphen and no two hyphens in a row.
func validateSubscriptionName(name string) error {
	bad := name == "" || len(name) > maxSubscriptionNameLen || !isLetter(name[0]) ||
		strings.HasSuffix(name, "-") || strings.Contains(name, "--") || !nameChars(name)

	if bad {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid SubscriptionName %q", name)
	}

	return nil
}

// nameChars reports whether name holds only letters, digits and hyphens.
func nameChars(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !isLetter(c) && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// validateTopic checks the SNS topic ARN shape and, when SNS is wired, that
// the topic exists.
func (m *Mock) validateTopic(ctx context.Context, arn string) error {
	const arnParts = 6

	parts := strings.Split(arn, ":")
	if len(parts) != arnParts || parts[0] != "arn" || parts[2] != "sns" || parts[5] == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid SNS topic ARN %q", arn)
	}

	if m.topics == nil {
		return nil
	}

	topic, err := m.topics.GetTopic(ctx, parts[5])
	if err != nil || topic.ResourceID != arn {
		return cerrors.Newf(cerrors.NotFound, "SNS topic %q not found", arn)
	}

	return nil
}

// validateSubscription checks severity, categories and sources. The caller
// holds m.mu.
func (m *Mock) validateSubscription(sub *EventSubscription) error {
	if _, ok := eventSeverities[sub.Severity]; !ok {
		return cerrors.Newf(cerrors.NotFound, "event severity %q not found", sub.Severity)
	}

	for _, c := range sub.EventCategories {
		if _, ok := eventCategories[c]; !ok {
			return cerrors.Newf(cerrors.NotFound, "event category %q not found", c)
		}
	}

	if sub.SourceType == "" {
		if len(sub.SourceIDs) > 0 {
			return cerrors.New(cerrors.InvalidArgument, "SourceType must be set to specify SourceIds")
		}

		return nil
	}

	if _, ok := eventSourceTypes[sub.SourceType]; !ok {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid SourceType %q", sub.SourceType)
	}

	for _, id := range sub.SourceIDs {
		if !m.sourceExists(sub.SourceType, id) {
			return cerrors.Newf(cerrors.NotFound, "event source %q of type %s not found", id, sub.SourceType)
		}
	}

	return nil
}

// sourceExists reports whether a source ID names a stored resource. Types
// cloudemu does not store are accepted as given.
func (m *Mock) sourceExists(sourceType, id string) bool {
	switch sourceType {
	case sourceCluster:
		return m.clusters.Has(id)
	case sourceParameterGroup:
		return id == defaultParameterGroupName || m.parameterGroups.Has(id)
	case sourceClusterSnapshot:
		return m.clusterSnapshots.Has(id)
	default:
		return true
	}
}

// setTagsLocked adds tags to the ARN-keyed tag store. The caller holds m.mu.
func (m *Mock) setTagsLocked(arn string, tags map[string]string) {
	if len(tags) == 0 {
		return
	}

	if m.tagsByARN == nil {
		m.tagsByARN = map[string]map[string]string{}
	}

	if m.tagsByARN[arn] == nil {
		m.tagsByARN[arn] = map[string]string{}
	}

	for k, v := range tags {
		m.tagsByARN[arn][k] = v
	}
}

//nolint:gocritic // takes a value on purpose: it returns an independent copy.
func cloneSubscription(s EventSubscription) EventSubscription {
	s.SourceIDs = cloneStrings(s.SourceIDs)
	s.EventCategories = cloneStrings(s.EventCategories)

	return s
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}
