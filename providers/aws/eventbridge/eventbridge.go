// Package eventbridge provides an in-memory mock implementation of AWS EventBridge.
package eventbridge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/eventmatch"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	defaultBusName   = "default"
	maxEventHistory  = 1000
	defaultRuleState = "ENABLED"
	activeBusState   = "ACTIVE"
)

// Compile-time check that Mock implements driver.EventBus.
var _ driver.EventBus = (*Mock)(nil)

type ruleData struct {
	rule    driver.Rule
	targets *memstore.Store[driver.Target]
}

type busData struct {
	info   driver.EventBusInfo
	rules  *memstore.Store[*ruleData]
	mu     sync.RWMutex
	events []driver.Event
}

// SQSDeliverer delivers an event to an SQS queue identified by its ARN.
type SQSDeliverer interface {
	DeliverExternal(ctx context.Context, queueARN, body string) error
}

// Mock is an in-memory mock implementation of AWS EventBridge.
type Mock struct {
	buses      *memstore.Store[*busData]
	opts       *config.Options
	monitoring mondriver.Monitoring
	sqs        SQSDeliverer
	tagsByARN  tagStore
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetSQSDeliverer wires the SQS backend so PutEvents delivers to SQS targets.
func (m *Mock) SetSQSDeliverer(d SQSDeliverer) {
	m.sqs = d
}

func (m *Mock) emitMetric(metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "AWS/Events", MetricName: metricName, Value: value, Unit: "Count",
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

// New creates a new EventBridge mock with the given configuration options.
func New(opts *config.Options) *Mock {
	m := &Mock{
		buses: memstore.New[*busData](),
		opts:  opts,
	}

	// Create the default event bus automatically.
	busARN := idgen.AWSARN("events", opts.Region, opts.AccountID, "event-bus/"+defaultBusName)
	defaultBus := &busData{
		info: driver.EventBusInfo{
			Name:      defaultBusName,
			ARN:       busARN,
			State:     activeBusState,
			CreatedAt: opts.Clock.Now().UTC().Format(time.RFC3339),
			Tags:      map[string]string{},
		},
		rules:  memstore.New[*ruleData](),
		events: []driver.Event{},
	}
	m.buses.Set(defaultBusName, defaultBus)

	return m
}

// CreateEventBus creates a new EventBridge event bus.
func (m *Mock) CreateEventBus(_ context.Context, cfg driver.EventBusConfig) (*driver.EventBusInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "event bus name is required")
	}

	if m.buses.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "event bus %q already exists", cfg.Name)
	}

	busARN := idgen.AWSARN("events", m.opts.Region, m.opts.AccountID, "event-bus/"+cfg.Name)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.EventBusInfo{
		Name:      cfg.Name,
		Scope:     cfg.Scope,
		ARN:       busARN,
		State:     activeBusState,
		CreatedAt: m.opts.Clock.Now().UTC().Format(time.RFC3339),
		Tags:      tags,
	}

	bd := &busData{
		info:   info,
		rules:  memstore.New[*ruleData](),
		events: []driver.Event{},
	}

	m.buses.Set(cfg.Name, bd)

	// Seed the ARN-keyed tag store so create-time tags are visible to
	// ListTagsForResource, matching real EventBridge.
	if len(tags) > 0 {
		m.tagsByARN.tag(busARN, tags)
	}

	result := info

	return &result, nil
}

// DeleteEventBus deletes an EventBridge event bus.
func (m *Mock) DeleteEventBus(_ context.Context, name string) error {
	if name == defaultBusName {
		return errors.New(errors.InvalidArgument, "cannot delete the default event bus")
	}

	if !m.buses.Delete(name) {
		return errors.Newf(errors.NotFound, "event bus %q not found", name)
	}

	return nil
}

// GetEventBus retrieves information about an EventBridge event bus.
func (m *Mock) GetEventBus(_ context.Context, name string) (*driver.EventBusInfo, error) {
	bd, ok := m.buses.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "event bus %q not found", name)
	}

	result := bd.info

	return &result, nil
}

// ListEventBuses lists all EventBridge event buses.
func (m *Mock) ListEventBuses(_ context.Context, filter scope.Scope) ([]driver.EventBusInfo, error) {
	all := m.buses.SortedValues()

	buses := make([]driver.EventBusInfo, 0, len(all))
	for _, bd := range all {
		if !bd.info.Scope.Matches(filter) {
			continue
		}
		buses = append(buses, bd.info)
	}

	return buses, nil
}

// PutRule creates or updates a rule on an event bus.
func (m *Mock) PutRule(_ context.Context, cfg *driver.RuleConfig) (*driver.Rule, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "rule name is required")
	}

	busName := cfg.EventBus
	if busName == "" {
		busName = defaultBusName
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "event bus %q not found", busName)
	}

	state := cfg.State
	if state == "" {
		state = defaultRuleState
	}

	rule := driver.Rule{
		Name:               cfg.Name,
		EventBus:           busName,
		Description:        cfg.Description,
		EventPattern:       cfg.EventPattern,
		ScheduleExpression: cfg.ScheduleExpression,
		RoleARN:            cfg.RoleARN,
		State:              state,
		Targets:            []driver.Target{},
		CreatedAt:          m.opts.Clock.Now().UTC().Format(time.RFC3339),
	}

	// Preserve existing targets if updating.
	if existing, exists := bd.rules.Get(cfg.Name); exists {
		rule.Targets = existing.rule.Targets
		rule.CreatedAt = existing.rule.CreatedAt
	}

	rd := &ruleData{
		rule:    rule,
		targets: memstore.New[driver.Target](),
	}

	for _, t := range rule.Targets {
		rd.targets.Set(t.ID, t)
	}

	bd.rules.Set(cfg.Name, rd)

	result := rule

	return &result, nil
}

// DeleteRule deletes a rule from an event bus.
func (m *Mock) DeleteRule(_ context.Context, eventBus, ruleName string) error {
	busName := eventBus
	if busName == "" {
		busName = defaultBusName
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return errors.Newf(errors.NotFound, "event bus %q not found", busName)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return errors.Newf(errors.NotFound, "rule %q not found on event bus %q", ruleName, busName)
	}

	// Real EventBridge refuses to delete a rule that still has targets: the
	// caller must RemoveTargets first. (Force applies only to managed rules,
	// which this mock does not model, so it never bypasses this guard.)
	if rd.targets.Len() > 0 {
		//nolint:revive // exact AWS ValidationException wording, surfaced verbatim to the SDK
		return errors.New(errors.InvalidArgument, "Rule can't be deleted since it has targets.")
	}

	bd.rules.Delete(ruleName)

	return nil
}

// GetRule retrieves a rule from an event bus.
func (m *Mock) GetRule(_ context.Context, eventBus, ruleName string) (*driver.Rule, error) {
	busName := eventBus
	if busName == "" {
		busName = defaultBusName
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "event bus %q not found", busName)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "rule %q not found on event bus %q", ruleName, busName)
	}

	result := rd.rule

	return &result, nil
}

// ListRules lists all rules on an event bus.
func (m *Mock) ListRules(_ context.Context, eventBus string) ([]driver.Rule, error) {
	busName := eventBus
	if busName == "" {
		busName = defaultBusName
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "event bus %q not found", busName)
	}

	all := bd.rules.SortedValues()

	rules := make([]driver.Rule, 0, len(all))
	for _, rd := range all {
		rules = append(rules, rd.rule)
	}

	return rules, nil
}

// EnableRule enables a rule on an event bus.
func (m *Mock) EnableRule(_ context.Context, eventBus, ruleName string) error {
	return m.setRuleState(eventBus, ruleName, defaultRuleState)
}

// DisableRule disables a rule on an event bus.
func (m *Mock) DisableRule(_ context.Context, eventBus, ruleName string) error {
	return m.setRuleState(eventBus, ruleName, "DISABLED")
}

func (m *Mock) setRuleState(eventBus, ruleName, state string) error {
	busName := eventBus
	if busName == "" {
		busName = defaultBusName
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return errors.Newf(errors.NotFound, "event bus %q not found", busName)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return errors.Newf(errors.NotFound, "rule %q not found on event bus %q", ruleName, busName)
	}

	rd.rule.State = state
	bd.rules.Set(ruleName, rd)

	return nil
}

// PutTargets adds targets to a rule.
func (m *Mock) PutTargets(_ context.Context, eventBus, ruleName string, targets []driver.Target) error {
	busName := eventBus
	if busName == "" {
		busName = defaultBusName
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return errors.Newf(errors.NotFound, "event bus %q not found", busName)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return errors.Newf(errors.NotFound, "rule %q not found on event bus %q", ruleName, busName)
	}

	for _, t := range targets {
		rd.targets.Set(t.ID, t)
	}

	rd.rule.Targets = targetsFromStore(rd.targets)
	bd.rules.Set(ruleName, rd)

	return nil
}

// RemoveTargets removes targets from a rule.
func (m *Mock) RemoveTargets(_ context.Context, eventBus, ruleName string, targetIDs []string) error {
	busName := eventBus
	if busName == "" {
		busName = defaultBusName
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return errors.Newf(errors.NotFound, "event bus %q not found", busName)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return errors.Newf(errors.NotFound, "rule %q not found on event bus %q", ruleName, busName)
	}

	for _, id := range targetIDs {
		rd.targets.Delete(id)
	}

	rd.rule.Targets = targetsFromStore(rd.targets)
	bd.rules.Set(ruleName, rd)

	return nil
}

// ListTargets lists all targets for a rule.
func (m *Mock) ListTargets(_ context.Context, eventBus, ruleName string) ([]driver.Target, error) {
	busName := eventBus
	if busName == "" {
		busName = defaultBusName
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "event bus %q not found", busName)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "rule %q not found on event bus %q", ruleName, busName)
	}

	return targetsFromStore(rd.targets), nil
}

// PutEvents publishes events to the event bus.
func (m *Mock) PutEvents(ctx context.Context, events []driver.Event) (*driver.PublishResult, error) {
	result := &driver.PublishResult{
		EventIDs: make([]string, 0, len(events)),
	}

	for i := range events {
		eventID := generateEventID(&events[i], m.opts.Clock.Now(), i)
		events[i].ID = eventID

		if events[i].Time.IsZero() {
			events[i].Time = m.opts.Clock.Now()
		}

		busName := events[i].EventBus
		if busName == "" {
			busName = defaultBusName
		}

		bd, ok := m.buses.Get(busName)
		if !ok {
			result.FailCount++

			continue
		}

		m.storeEvent(bd, &events[i])
		matched := m.MatchedRules(&events[i])
		m.deliverToTargets(ctx, matched, &events[i])

		dims := map[string]string{"EventBusName": busName}
		m.emitMetric("PutEventsRequestCount", 1, dims)
		m.emitMetric("MatchedEvents", float64(len(matched)), dims)

		result.SuccessCount++
		result.EventIDs = append(result.EventIDs, eventID)
	}

	return result, nil
}

// deliverToTargets delivers an event to the SQS targets of matched rules. The
// body delivered to each target is the event envelope by default, but is
// replaced by the target's Input (constant), InputPath (selected subtree), or
// InputTransformer (templated) when one is configured — matching how real
// EventBridge shapes each target's payload independently.
func (m *Mock) deliverToTargets(ctx context.Context, matched []driver.Rule, event *driver.Event) {
	if m.sqs == nil {
		return
	}

	envelope := m.eventEnvelope(event)

	for i := range matched {
		for _, t := range matched[i].Targets {
			if t.ARN == "" || !strings.Contains(t.ARN, ":sqs:") {
				continue
			}

			body := targetBody(&t, envelope)

			_ = m.sqs.DeliverExternal(ctx, t.ARN, body)
		}
	}
}

// eventEnvelope renders the standard EventBridge delivery envelope for an event.
func (m *Mock) eventEnvelope(event *driver.Event) []byte {
	detail := json.RawMessage(event.Detail)
	if len(detail) == 0 {
		detail = json.RawMessage("{}")
	}

	body, err := json.Marshal(map[string]any{
		"version":     "0",
		"id":          event.ID,
		"detail-type": event.DetailType,
		"source":      event.Source,
		"account":     m.opts.AccountID,
		"time":        event.Time.UTC().Format(time.RFC3339),
		"region":      m.opts.Region,
		"resources":   event.Resources,
		"detail":      detail,
	})
	if err != nil {
		return []byte("{}")
	}

	return body
}

// GetEventHistory retrieves event history for an event bus.
func (m *Mock) GetEventHistory(_ context.Context, eventBus string, limit int) ([]driver.Event, error) {
	busName := eventBus
	if busName == "" {
		busName = defaultBusName
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "event bus %q not found", busName)
	}

	bd.mu.RLock()
	defer bd.mu.RUnlock()

	history := bd.events
	if limit > 0 && limit < len(history) {
		history = history[len(history)-limit:]
	}

	result := make([]driver.Event, len(history))
	copy(result, history)

	return result, nil
}

func (*Mock) storeEvent(bd *busData, event *driver.Event) {
	bd.mu.Lock()
	defer bd.mu.Unlock()

	bd.events = append(bd.events, *event)
	if len(bd.events) > maxEventHistory {
		bd.events = bd.events[len(bd.events)-maxEventHistory:]
	}
}

func targetsFromStore(store *memstore.Store[driver.Target]) []driver.Target {
	all := store.All()

	targets := make([]driver.Target, 0, len(all))
	for _, t := range all {
		targets = append(targets, t)
	}

	return targets
}

// generateEventID hashes the event's identity plus the clock and its position
// within the PutEvents batch. The batch index is included because real
// EventBridge always issues unique IDs, and under a deterministic (fake) clock
// two byte-identical events in one call would otherwise collide — breaking any
// consumer that uses EventId as an idempotency/history key.
func generateEventID(event *driver.Event, now time.Time, index int) string {
	data := fmt.Sprintf("%s:%s:%s:%s:%d:%d",
		event.Source, event.DetailType, event.Detail, event.EventBus, now.UnixNano(), index)
	hash := sha256.Sum256([]byte(data))

	return fmt.Sprintf("%x", hash[:16])
}

// matchesPattern reports whether an event satisfies an EventBridge event
// pattern. An empty pattern matches everything (schedule-only rules). The
// pattern is evaluated against the full event envelope — source, detail-type,
// resources, and the nested detail object — using the shared content-filtering
// engine (exact, nested, prefix/suffix/anything-but/exists/numeric/cidr/wildcard).
func matchesPattern(event *driver.Event, pattern string) bool {
	if pattern == "" {
		return true
	}

	p, ok := eventmatch.ParsePattern(pattern)
	if !ok {
		return false
	}

	return eventmatch.MatchEvent(p, eventObject(event))
}

// eventObject renders an event into the JSON object shape EventBridge patterns
// match against. The detail body is parsed so nested "detail" constraints can
// reach into it; an unparsable detail is treated as an empty object.
func eventObject(event *driver.Event) map[string]any {
	obj := map[string]any{
		"source":      event.Source,
		"detail-type": event.DetailType,
	}

	if len(event.Resources) > 0 {
		res := make([]any, len(event.Resources))
		for i, r := range event.Resources {
			res[i] = r
		}

		obj["resources"] = res
	}

	if event.Detail != "" {
		var detail any
		if err := json.Unmarshal([]byte(event.Detail), &detail); err == nil {
			obj["detail"] = detail
		}
	}

	return obj
}

// UpdateEventBus replaces the mutable fields of an existing event bus —
// ARM CreateOrUpdate-on-existing semantics (tags come from the request;
// identity and CreatedAt are preserved).
func (m *Mock) UpdateEventBus(_ context.Context, cfg driver.EventBusConfig) (*driver.EventBusInfo, error) {
	bd, ok := m.buses.Get(cfg.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "event bus %q not found", cfg.Name)
	}

	if cfg.Tags != nil {
		bd.info.Tags = maps.Clone(cfg.Tags)
	}
	if !cfg.Scope.IsZero() {
		bd.info.Scope = cfg.Scope
	}

	m.buses.Set(cfg.Name, bd)

	result := bd.info

	return &result, nil
}

// MatchedRules returns all rules that match the given event (exported for testing).
func (m *Mock) MatchedRules(event *driver.Event) []driver.Rule {
	var matched []driver.Rule

	all := m.buses.All()
	for _, bd := range all {
		rules := bd.rules.All()
		for _, rd := range rules {
			if rd.rule.State != defaultRuleState {
				continue
			}

			if matchesPattern(event, rd.rule.EventPattern) {
				matched = append(matched, rd.rule)
			}
		}
	}

	return matched
}
