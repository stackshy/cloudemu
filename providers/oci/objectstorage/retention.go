package objectstorage

import (
	"context"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

const (
	hoursPerDay = 24
	daysPerYear = 365
)

// RetentionDuration is how long a rule retains an object after its last
// modification.
type RetentionDuration struct {
	TimeAmount int64
	TimeUnit   string
}

// RetentionRule is a bucket retention rule. A rule with no duration is an
// indefinite hold on the whole bucket; a locked rule cannot be shortened or
// deleted, which is what makes OCI retention a compliance control.
type RetentionRule struct {
	ID             string
	DisplayName    string
	Duration       *RetentionDuration
	TimeRuleLocked string
	TimeCreated    string
	TimeModified   string
	ETag           string
}

// RetentionRuleSpec is a rule to create or update.
type RetentionRuleSpec struct {
	DisplayName    string
	Duration       *RetentionDuration
	TimeRuleLocked *time.Time
}

type retentionRuleData struct {
	ID             string
	DisplayName    string
	Duration       *RetentionDuration
	TimeRuleLocked string
	TimeCreated    string
	TimeModified   string
	ETag           string
}

func (d RetentionDuration) span() (time.Duration, error) {
	switch strings.ToUpper(d.TimeUnit) {
	case RetentionDays:
		return time.Duration(d.TimeAmount) * hoursPerDay * time.Hour, nil
	case RetentionYears:
		return time.Duration(d.TimeAmount) * daysPerYear * hoursPerDay * time.Hour, nil
	default:
		return 0, cerrors.Newf(cerrors.InvalidArgument, "unsupported timeUnit %q, want DAYS or YEARS", d.TimeUnit)
	}
}

// CreateRetentionRule adds a retention rule to a bucket.
func (m *Mock) CreateRetentionRule(_ context.Context, bucket string, spec RetentionRuleSpec) (*RetentionRule, error) {
	if err := validateRule(spec); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	now := m.now()
	rule := &retentionRuleData{
		ID:           idgen.OCID(typeRetentionRule, m.opts.Realm, m.opts.OCIRegion()),
		DisplayName:  spec.DisplayName,
		Duration:     spec.Duration,
		TimeCreated:  now,
		TimeModified: now,
		ETag:         newETag(),
	}

	if spec.TimeRuleLocked != nil {
		rule.TimeRuleLocked = spec.TimeRuleLocked.UTC().Format(timeFormat)
	}

	bkt.retention.Set(rule.ID, rule)

	return projectRule(rule), nil
}

func validateRule(spec RetentionRuleSpec) error {
	if spec.Duration == nil {
		return nil
	}

	if spec.Duration.TimeAmount <= 0 {
		return cerrors.New(cerrors.InvalidArgument, "timeAmount must be positive")
	}

	_, err := spec.Duration.span()

	return err
}

// GetRetentionRule returns one rule.
func (m *Mock) GetRetentionRule(_ context.Context, bucket, ruleID string) (*RetentionRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	rule, ok := bkt.retention.Get(ruleID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "retention rule %q not found in bucket %q", ruleID, bucket)
	}

	return projectRule(rule), nil
}

// ListRetentionRules returns a bucket's rules, ordered by id.
func (m *Mock) ListRetentionRules(_ context.Context, bucket string) ([]RetentionRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	ids := bkt.retention.Keys()
	sort.Strings(ids)

	out := make([]RetentionRule, 0, len(ids))

	for _, id := range ids {
		if rule, ok := bkt.retention.Get(id); ok {
			out = append(out, *projectRule(rule))
		}
	}

	return out, nil
}

// UpdateRetentionRule replaces a rule. A locked rule may only be extended,
// which is the whole point of locking one.
func (m *Mock) UpdateRetentionRule(
	_ context.Context, bucket, ruleID string, spec RetentionRuleSpec,
) (*RetentionRule, error) {
	if err := validateRule(spec); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	rule, ok := bkt.retention.Get(ruleID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "retention rule %q not found in bucket %q", ruleID, bucket)
	}

	if err := checkLockedUpdate(rule, spec, m.opts.Clock.Now()); err != nil {
		return nil, err
	}

	rule.DisplayName = spec.DisplayName
	rule.Duration = spec.Duration
	rule.TimeModified = m.now()
	rule.ETag = newETag()

	if spec.TimeRuleLocked != nil {
		rule.TimeRuleLocked = spec.TimeRuleLocked.UTC().Format(timeFormat)
	}

	return projectRule(rule), nil
}

// checkLockedUpdate refuses a change that would weaken an active locked rule.
func checkLockedUpdate(rule *retentionRuleData, spec RetentionRuleSpec, now time.Time) error {
	if !ruleLocked(rule, now) {
		return nil
	}

	if spec.Duration == nil {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"retention rule %q is locked; its duration cannot be removed", rule.ID)
	}

	current, err := rule.Duration.span()
	if err != nil {
		return err
	}

	next, err := spec.Duration.span()
	if err != nil {
		return err
	}

	if next < current {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"retention rule %q is locked; its duration can only be extended", rule.ID)
	}

	return nil
}

// DeleteRetentionRule removes a rule. A locked rule cannot be deleted.
func (m *Mock) DeleteRetentionRule(_ context.Context, bucket, ruleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	rule, ok := bkt.retention.Get(ruleID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "retention rule %q not found in bucket %q", ruleID, bucket)
	}

	if ruleLocked(rule, m.opts.Clock.Now()) {
		return cerrors.Newf(cerrors.FailedPrecondition, "retention rule %q is locked and cannot be deleted", rule.ID)
	}

	bkt.retention.Delete(ruleID)

	return nil
}

// ruleLocked reports whether a rule's lock has taken effect. OCI gives the
// caller a grace period between requesting the lock and it engaging.
func ruleLocked(rule *retentionRuleData, now time.Time) bool {
	if rule.TimeRuleLocked == "" {
		return false
	}

	at, err := time.Parse(timeFormat, rule.TimeRuleLocked)
	if err != nil {
		return false
	}

	return !now.Before(at)
}

// retentionBlocksLocked refuses a delete or overwrite that an active retention
// rule protects. A rule with no duration holds every object indefinitely; a
// rule with one holds an object until its last modification ages out.
// Callers hold mu.
func retentionBlocksLocked(bkt *bucketData, name string, now time.Time) error {
	if bkt.retention.Len() == 0 {
		return nil
	}

	obj, ok := bkt.objects.Get(name)
	if !ok {
		return nil
	}

	modified, err := time.Parse(timeFormat, obj.TimeModified)
	if err != nil {
		return nil //nolint:nilerr // an unparseable timestamp cannot prove the object is retained
	}

	for _, id := range bkt.retention.Keys() {
		rule, exists := bkt.retention.Get(id)
		if !exists {
			continue
		}

		if rule.Duration == nil {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"object %q is held indefinitely by retention rule %q", name, rule.ID)
		}

		span, spanErr := rule.Duration.span()
		if spanErr != nil {
			continue
		}

		if now.Before(modified.Add(span)) {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"object %q is retained until %s by retention rule %q",
				name, modified.Add(span).UTC().Format(timeFormat), rule.ID)
		}
	}

	return nil
}

func projectRule(rule *retentionRuleData) *RetentionRule {
	out := &RetentionRule{
		ID:             rule.ID,
		DisplayName:    rule.DisplayName,
		TimeRuleLocked: rule.TimeRuleLocked,
		TimeCreated:    rule.TimeCreated,
		TimeModified:   rule.TimeModified,
		ETag:           rule.ETag,
	}

	if rule.Duration != nil {
		d := *rule.Duration
		out.Duration = &d
	}

	return out
}
