package rds

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.EventSubscriptions = (*Mock)(nil)

func eventSubscriptionARN(region, accountID, name string) string {
	return idgen.AWSARN("rds", region, accountID, "es:"+name)
}

func errEventSubscriptionNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "event subscription %q not found", name)
}

// eventCategoryCatalog is the set of event categories each RDS source type
// emits. Mirrors AWS's published categories.
//
//nolint:gochecknoglobals // static lookup table
var eventCategoryCatalog = map[string][]string{
	"db-instance": {
		"availability", "backup", "configuration change", "creation", "deletion",
		"failover", "failure", "maintenance", "notification", "recovery", "restoration",
	},
	"db-cluster":          {"configuration change", "creation", "deletion", "failover", "failure", "maintenance", "notification"},
	"db-snapshot":         {"creation", "deletion", "notification", "restoration"},
	"db-cluster-snapshot": {"backup", "notification"},
	"db-parameter-group":  {"configuration change"},
	"db-security-group":   {"configuration change", "failure"},
	"db-proxy":            {"configuration change", "creation", "deletion"},
}

//nolint:gocritic // cfg matches the driver interface signature.
func (m *Mock) CreateEventSubscription(_ context.Context, cfg rdsdriver.EventSubscriptionConfig) (*rdsdriver.EventSubscription, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "SubscriptionName is required")
	}

	if cfg.SnsTopicARN == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "SnsTopicArn is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.eventSubs.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "event subscription %q already exists", cfg.Name)
	}

	sub := rdsdriver.EventSubscription{
		Name:            cfg.Name,
		ARN:             eventSubscriptionARN(m.opts.Region, m.opts.AccountID, cfg.Name),
		CustomerAWSID:   m.opts.AccountID,
		SnsTopicARN:     cfg.SnsTopicARN,
		SourceType:      cfg.SourceType,
		Status:          "active",
		EventCategories: append([]string(nil), cfg.EventCategories...),
		SourceIDs:       append([]string(nil), cfg.SourceIDs...),
		Enabled:         cfg.Enabled,
		CreatedAt:       m.opts.Clock.Now().UTC(),
	}
	m.eventSubs.Set(cfg.Name, sub)

	out := sub

	return &out, nil
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (m *Mock) DescribeEventSubscriptions(_ context.Context, names []string) ([]rdsdriver.EventSubscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(names) == 0 {
		return m.eventSubs.SortedValues(), nil
	}

	out := make([]rdsdriver.EventSubscription, 0, len(names))

	for _, name := range names {
		sub, ok := m.eventSubs.Get(name)
		if !ok {
			return nil, errEventSubscriptionNotFound(name)
		}

		out = append(out, sub)
	}

	return out, nil
}

func (m *Mock) ModifyEventSubscription(
	_ context.Context, name string, input rdsdriver.ModifyEventSubscriptionInput,
) (*rdsdriver.EventSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.eventSubs.Get(name)
	if !ok {
		return nil, errEventSubscriptionNotFound(name)
	}

	if input.SnsTopicARN != "" {
		sub.SnsTopicARN = input.SnsTopicARN
	}

	if input.SourceType != "" {
		sub.SourceType = input.SourceType
	}

	if input.EventCategories != nil {
		sub.EventCategories = append([]string(nil), input.EventCategories...)
	}

	if input.Enabled != nil {
		sub.Enabled = *input.Enabled
	}

	m.eventSubs.Set(name, sub)

	out := sub

	return &out, nil
}

func (m *Mock) DeleteEventSubscription(_ context.Context, name string) (*rdsdriver.EventSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.eventSubs.Get(name)
	if !ok {
		return nil, errEventSubscriptionNotFound(name)
	}

	m.eventSubs.Delete(name)

	out := sub

	return &out, nil
}

// DescribeEvents returns an empty list: the emulator does not retain an event
// timeline, so there are truthfully no events to report for any window.
func (*Mock) DescribeEvents(_ context.Context, _, _ string, _ []string) ([]rdsdriver.Event, error) {
	return []rdsdriver.Event{}, nil
}

func (*Mock) DescribeEventCategories(_ context.Context, sourceType string) ([]rdsdriver.EventCategoryGroup, error) {
	if sourceType != "" {
		cats, ok := eventCategoryCatalog[sourceType]
		if !ok {
			return []rdsdriver.EventCategoryGroup{}, nil
		}

		return []rdsdriver.EventCategoryGroup{{SourceType: sourceType, EventCategories: cats}}, nil
	}

	out := make([]rdsdriver.EventCategoryGroup, 0, len(eventCategoryCatalog))
	for _, st := range eventCategorySourceTypes {
		out = append(out, rdsdriver.EventCategoryGroup{SourceType: st, EventCategories: eventCategoryCatalog[st]})
	}

	return out, nil
}

// eventCategorySourceTypes fixes the iteration order of the catalog for
// deterministic output.
//
//nolint:gochecknoglobals // static lookup table
var eventCategorySourceTypes = []string{
	"db-instance", "db-cluster", "db-snapshot", "db-cluster-snapshot",
	"db-parameter-group", "db-security-group", "db-proxy",
}
