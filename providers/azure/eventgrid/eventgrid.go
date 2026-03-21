// Package eventgrid provides an in-memory mock implementation of Azure Event Grid.
package eventgrid

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
	maxEventHistory  = 1000
	defaultRuleState = "ENABLED"
	activeTopicState = "ACTIVE"
	resourceProvider = "Microsoft.EventGrid"
	resourceType     = "topics"
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

// Mock is an in-memory mock implementation of Azure Event Grid.
type Mock struct {
	buses      *memstore.Store[*busData]
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(topicName string, metrics map[string]float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	data := make([]mondriver.MetricDatum, 0, len(metrics))

	for name, value := range metrics {
		data = append(data, mondriver.MetricDatum{
			Namespace:  "Microsoft.EventGrid/topics",
			MetricName: name,
			Value:      value,
			Unit:       "None",
			Dimensions: map[string]string{"topicName": topicName},
			Timestamp:  now,
		})
	}

	_ = m.monitoring.PutMetricData(context.Background(), data)
}

// New creates a new Event Grid mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		buses: memstore.New[*busData](),
		opts:  opts,
	}
}

// CreateEventBus creates a new Event Grid topic.
func (m *Mock) CreateEventBus(_ context.Context, cfg driver.EventBusConfig) (*driver.EventBusInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "topic name is required")
	}

	if m.buses.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "topic %q already exists", cfg.Name)
	}

	topicID := idgen.AzureID(m.opts.AccountID, m.opts.Region, resourceProvider, resourceType, cfg.Name)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.EventBusInfo{
		Name:      cfg.Name,
		ARN:       topicID,
		State:     activeTopicState,
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

// DeleteEventBus deletes an Event Grid topic.
func (m *Mock) DeleteEventBus(_ context.Context, name string) error {
	if !m.buses.Delete(name) {
		return errors.Newf(errors.NotFound, "topic %q not found", name)
	}

	return nil
}

// GetEventBus retrieves information about an Event Grid topic.
func (m *Mock) GetEventBus(_ context.Context, name string) (*driver.EventBusInfo, error) {
	bd, ok := m.buses.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", name)
	}

	result := bd.info

	return &result, nil
}

// ListEventBuses lists all Event Grid topics.
func (m *Mock) ListEventBuses(_ context.Context) ([]driver.EventBusInfo, error) {
	all := m.buses.All()

	buses := make([]driver.EventBusInfo, 0, len(all))
	for _, bd := range all {
		buses = append(buses, bd.info)
	}

	return buses, nil
}

// PutRule creates or updates an event subscription on a topic.
func (m *Mock) PutRule(_ context.Context, cfg *driver.RuleConfig) (*driver.Rule, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "subscription name is required")
	}

	busName := cfg.EventBus
	if busName == "" {
		return nil, errors.New(errors.InvalidArgument, "topic name is required")
	}

	bd, ok := m.buses.Get(busName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", busName)
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

// DeleteRule deletes an event subscription from a topic.
func (m *Mock) DeleteRule(_ context.Context, eventBus, ruleName string) error {
	if eventBus == "" {
		return errors.New(errors.InvalidArgument, "topic name is required")
	}

	bd, ok := m.buses.Get(eventBus)
	if !ok {
		return errors.Newf(errors.NotFound, "topic %q not found", eventBus)
	}

	if !bd.rules.Delete(ruleName) {
		return errors.Newf(errors.NotFound, "subscription %q not found on topic %q", ruleName, eventBus)
	}

	return nil
}

// GetRule retrieves an event subscription from a topic.
func (m *Mock) GetRule(_ context.Context, eventBus, ruleName string) (*driver.Rule, error) {
	if eventBus == "" {
		return nil, errors.New(errors.InvalidArgument, "topic name is required")
	}

	bd, ok := m.buses.Get(eventBus)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", eventBus)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "subscription %q not found on topic %q", ruleName, eventBus)
	}

	result := rd.rule

	return &result, nil
}

// ListRules lists all event subscriptions on a topic.
func (m *Mock) ListRules(_ context.Context, eventBus string) ([]driver.Rule, error) {
	if eventBus == "" {
		return nil, errors.New(errors.InvalidArgument, "topic name is required")
	}

	bd, ok := m.buses.Get(eventBus)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", eventBus)
	}

	all := bd.rules.All()

	rules := make([]driver.Rule, 0, len(all))
	for _, rd := range all {
		rules = append(rules, rd.rule)
	}

	return rules, nil
}

// EnableRule enables an event subscription on a topic.
func (m *Mock) EnableRule(_ context.Context, eventBus, ruleName string) error {
	return m.setRuleState(eventBus, ruleName, defaultRuleState)
}

// DisableRule disables an event subscription on a topic.
func (m *Mock) DisableRule(_ context.Context, eventBus, ruleName string) error {
	return m.setRuleState(eventBus, ruleName, "DISABLED")
}

func (m *Mock) setRuleState(eventBus, ruleName, state string) error {
	if eventBus == "" {
		return errors.New(errors.InvalidArgument, "topic name is required")
	}

	bd, ok := m.buses.Get(eventBus)
	if !ok {
		return errors.Newf(errors.NotFound, "topic %q not found", eventBus)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return errors.Newf(errors.NotFound, "subscription %q not found on topic %q", ruleName, eventBus)
	}

	rd.rule.State = state
	bd.rules.Set(ruleName, rd)

	return nil
}

// PutTargets adds targets to an event subscription.
func (m *Mock) PutTargets(_ context.Context, eventBus, ruleName string, targets []driver.Target) error {
	if eventBus == "" {
		return errors.New(errors.InvalidArgument, "topic name is required")
	}

	bd, ok := m.buses.Get(eventBus)
	if !ok {
		return errors.Newf(errors.NotFound, "topic %q not found", eventBus)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return errors.Newf(errors.NotFound, "subscription %q not found on topic %q", ruleName, eventBus)
	}

	for _, t := range targets {
		rd.targets.Set(t.ID, t)
	}

	rd.rule.Targets = targetsFromStore(rd.targets)
	bd.rules.Set(ruleName, rd)

	return nil
}

// RemoveTargets removes targets from an event subscription.
func (m *Mock) RemoveTargets(_ context.Context, eventBus, ruleName string, targetIDs []string) error {
	if eventBus == "" {
		return errors.New(errors.InvalidArgument, "topic name is required")
	}

	bd, ok := m.buses.Get(eventBus)
	if !ok {
		return errors.Newf(errors.NotFound, "topic %q not found", eventBus)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return errors.Newf(errors.NotFound, "subscription %q not found on topic %q", ruleName, eventBus)
	}

	for _, id := range targetIDs {
		rd.targets.Delete(id)
	}

	rd.rule.Targets = targetsFromStore(rd.targets)
	bd.rules.Set(ruleName, rd)

	return nil
}

// ListTargets lists all targets for an event subscription.
func (m *Mock) ListTargets(_ context.Context, eventBus, ruleName string) ([]driver.Target, error) {
	if eventBus == "" {
		return nil, errors.New(errors.InvalidArgument, "topic name is required")
	}

	bd, ok := m.buses.Get(eventBus)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", eventBus)
	}

	rd, ok := bd.rules.Get(ruleName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "subscription %q not found on topic %q", ruleName, eventBus)
	}

	return targetsFromStore(rd.targets), nil
}

// PutEvents publishes events to Event Grid topics.
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
			result.FailCount++

			continue
		}

		bd, ok := m.buses.Get(busName)
		if !ok {
			result.FailCount++

			continue
		}

		m.storeEvent(bd, &events[i])

		matchedCount := len(m.MatchedRules(&events[i]))
		m.emitMetric(busName, map[string]float64{
			"PublishedEvents": 1, "MatchedEvents": float64(matchedCount),
		})

		result.SuccessCount++
		result.EventIDs = append(result.EventIDs, eventID)
	}

	return result, nil
}

// GetEventHistory retrieves event history for a topic.
func (m *Mock) GetEventHistory(_ context.Context, eventBus string, limit int) ([]driver.Event, error) {
	if eventBus == "" {
		return nil, errors.New(errors.InvalidArgument, "topic name is required")
	}

	bd, ok := m.buses.Get(eventBus)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", eventBus)
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

// MatchedRules returns all subscriptions that match the given event (exported for testing).
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
