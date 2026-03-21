// Package eventbridge provides an in-memory mock implementation of AWS EventBridge.
package eventbridge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/eventbus/driver"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
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

// Mock is an in-memory mock implementation of AWS EventBridge.
type Mock struct {
	buses      *memstore.Store[*busData]
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
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
func (m *Mock) ListEventBuses(_ context.Context) ([]driver.EventBusInfo, error) {
	all := m.buses.All()

	buses := make([]driver.EventBusInfo, 0, len(all))
	for _, bd := range all {
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
		Name:         cfg.Name,
		EventBus:     busName,
		Description:  cfg.Description,
		EventPattern: cfg.EventPattern,
		State:        state,
		Targets:      []driver.Target{},
		CreatedAt:    m.opts.Clock.Now().UTC().Format(time.RFC3339),
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

	if !bd.rules.Delete(ruleName) {
		return errors.Newf(errors.NotFound, "rule %q not found on event bus %q", ruleName, busName)
	}

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

	all := bd.rules.All()

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
func (m *Mock) PutEvents(_ context.Context, events []driver.Event) (*driver.PublishResult, error) {
	result := &driver.PublishResult{
		EventIDs: make([]string, 0, len(events)),
	}

	for i := range events {
		eventID := generateEventID(&events[i], m.opts.Clock.Now())
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

		dims := map[string]string{"EventBusName": busName}
		m.emitMetric("PutEventsRequestCount", 1, dims)
		m.emitMetric("MatchedEvents", float64(len(matched)), dims)

		result.SuccessCount++
		result.EventIDs = append(result.EventIDs, eventID)
	}

	return result, nil
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

func generateEventID(event *driver.Event, now time.Time) string {
	data := fmt.Sprintf("%s:%s:%s:%s:%d", event.Source, event.DetailType, event.Detail, event.EventBus, now.UnixNano())
	hash := sha256.Sum256([]byte(data))

	return fmt.Sprintf("%x", hash[:16])
}

func matchesPattern(event *driver.Event, pattern string) bool {
	if pattern == "" {
		return true
	}

	var p map[string]any
	if err := json.Unmarshal([]byte(pattern), &p); err != nil {
		return false
	}

	if sources, ok := p["source"]; ok {
		if !matchesField(event.Source, sources) {
			return false
		}
	}

	if detailTypes, ok := p["detail-type"]; ok {
		if !matchesField(event.DetailType, detailTypes) {
			return false
		}
	}

	return true
}

func matchesField(value string, allowed any) bool {
	arr, ok := allowed.([]any)
	if !ok {
		return false
	}

	for _, v := range arr {
		if fmt.Sprintf("%v", v) == value {
			return true
		}
	}

	return false
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
