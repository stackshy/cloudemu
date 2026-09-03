// Package eventgrid provides an in-memory mock implementation of Azure Event Grid.
package eventgrid

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	maxEventHistory  = 1000
	defaultRuleState = "ENABLED"
	activeTopicState = "ACTIVE"
	resourceProvider = "Microsoft.EventGrid"
	resourceType     = "topics"
	// defaultInputSchema is the event schema a topic accepts when the caller
	// doesn't specify one, matching real Event Grid's default.
	defaultInputSchema = "EventGridSchema"
	// defaultPublicNetworkAccess is the public-network-access setting a topic
	// gets when the caller doesn't specify one, matching real Event Grid.
	defaultPublicNetworkAccess = "Enabled"
)

// Compile-time check that Mock implements driver.EventBus.
var _ driver.EventBus = (*Mock)(nil)

type ruleData struct {
	rule    driver.Rule
	targets *memstore.Store[driver.Target]
	// filter and dest are parsed from rule.Description (the raw ARM
	// EventSubscription properties JSON) — Event Grid's subscription filter
	// and destination shapes have no equivalent in the portable eventbus
	// driver, so PutRule derives them here for PutEvents to apply/deliver to.
	filter subscriptionFilter
	dest   subscriptionDestination
}

type busData struct {
	info   driver.EventBusInfo
	rules  *memstore.Store[*ruleData]
	mu     sync.RWMutex
	events []driver.Event
}

// ServiceBusDeliverer enqueues an Event Grid event envelope into a Service Bus
// queue or topic identified by name (the leaf of the destination's ARM
// resourceId). The servicebus.Mock satisfies this via DeliverExternal, enabling
// EventGrid -> ServiceBusQueue/ServiceBusTopic delivery.
type ServiceBusDeliverer interface {
	DeliverExternal(ctx context.Context, name, body string) error
}

// FunctionInvoker asynchronously invokes an Azure Function app by name with the
// event envelope. The functions.Mock satisfies this via InvokeExternal, enabling
// EventGrid -> AzureFunction delivery.
type FunctionInvoker interface {
	InvokeExternal(ctx context.Context, name string, payload []byte) error
}

// Mock is an in-memory mock implementation of Azure Event Grid.
type Mock struct {
	buses *memstore.Store[*busData]
	// systemBuses backs system-topic delivery. It is a store distinct from
	// buses (user-facing custom topics) so a system topic's delivery bus and a
	// custom topic can share a name — e.g. the fixed storage-account name a Blob
	// Storage system topic keys on — without either clobbering or leaking into
	// the other. The custom-topic CRUD paths (Create/Update/Delete/ListEventBus,
	// PutRule) never touch it; it is managed only through the SystemDelivery*
	// methods and consulted by PutEvents for system-topic-sourced events.
	systemBuses *memstore.Store[*busData]
	opts        *config.Options
	monitoring  mondriver.Monitoring
	httpClient  *http.Client
	serviceBus  ServiceBusDeliverer
	functions   FunctionInvoker
	// storageQueue delivers to StorageQueue destinations. It reuses the same
	// ServiceBusDeliverer contract as serviceBus (DeliverExternal(ctx, name,
	// body) enqueues by name) because the Azure Queue Storage provider is the
	// same servicebus.Mock implementation, wired as a distinct instance.
	storageQueue ServiceBusDeliverer
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetServiceBusDeliverer wires the Service Bus backend so PutEvents delivers to
// ServiceBusQueue/ServiceBusTopic subscription destinations.
func (m *Mock) SetServiceBusDeliverer(d ServiceBusDeliverer) {
	m.serviceBus = d
}

// SetFunctionInvoker wires the Functions backend so PutEvents invokes
// AzureFunction subscription destinations.
func (m *Mock) SetFunctionInvoker(i FunctionInvoker) {
	m.functions = i
}

// SetStorageQueueDeliverer wires the Azure Queue Storage backend so PutEvents
// delivers to StorageQueue subscription destinations.
func (m *Mock) SetStorageQueueDeliverer(d ServiceBusDeliverer) {
	m.storageQueue = d
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
		buses:       memstore.New[*busData](),
		systemBuses: memstore.New[*busData](),
		opts:        opts,
		httpClient:  &http.Client{Timeout: webhookDeliveryTimeout},
	}
}

// EnsureSystemDeliveryBus makes sure an isolated system-topic delivery bus with
// the given name exists (no-op when it already does, or the name is empty). The
// wire handler calls this when a system topic is created so the source
// producer's PutEvents has a bus to match. The bus lives in systemBuses, so it
// never appears on — nor is clobbered by — the custom-topic surface.
func (m *Mock) EnsureSystemDeliveryBus(name string) {
	if name == "" || m.systemBuses.Has(name) {
		return
	}

	m.systemBuses.Set(name, &busData{
		info:   driver.EventBusInfo{Name: name, State: activeTopicState},
		rules:  memstore.New[*ruleData](),
		events: []driver.Event{},
	})
}

// PutSystemDeliveryRule registers (or replaces) a system-topic subscription as a
// delivery rule on its isolated bus, carrying the raw ARM EventSubscription
// properties (destination + filter) verbatim — mirroring PutRule for custom
// topics, but against systemBuses. Returns NotFound when the delivery bus was
// never provisioned.
func (m *Mock) PutSystemDeliveryRule(busName, ruleName, properties string) error {
	if ruleName == "" {
		return errors.New(errors.InvalidArgument, "subscription name is required")
	}

	bd, ok := m.systemBuses.Get(busName)
	if !ok {
		return errors.Newf(errors.NotFound, "system delivery bus %q not found", busName)
	}

	bd.rules.Set(ruleName, &ruleData{
		rule: driver.Rule{
			Name:        ruleName,
			EventBus:    busName,
			Description: properties,
			State:       defaultRuleState,
			Targets:     []driver.Target{},
			CreatedAt:   m.opts.Clock.Now().UTC().Format(time.RFC3339),
		},
		targets: memstore.New[driver.Target](),
		filter:  parseSubscriptionFilter(properties),
		dest:    parseSubscriptionDestination(properties),
	})

	return nil
}

// DeleteSystemDeliveryRule removes a system-topic subscription's delivery rule
// from its isolated bus. Idempotent: a missing bus or rule is not an error, so a
// deleted subscription (or system topic) stops delivery cleanly.
func (m *Mock) DeleteSystemDeliveryRule(busName, ruleName string) error {
	if bd, ok := m.systemBuses.Get(busName); ok {
		bd.rules.Delete(ruleName)
	}

	return nil
}

// lookupBus resolves the bus an event delivers through: a system-topic-sourced
// event (the producer stamps the source resource id on Topic) resolves against
// the isolated system delivery store; a custom-topic publish (Topic empty)
// resolves against the user-facing store. This routing keeps a same-named
// custom topic and system delivery bus from ever cross-delivering.
func (m *Mock) lookupBus(event *driver.Event) (*busData, bool) {
	if event.Topic != "" {
		return m.systemBuses.Get(event.EventBus)
	}

	return m.buses.Get(event.EventBus)
}

// CreateEventBus creates a new Event Grid topic.
func (m *Mock) CreateEventBus(_ context.Context, cfg driver.EventBusConfig) (*driver.EventBusInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "topic name is required")
	}

	if m.buses.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "topic %q already exists", cfg.Name)
	}

	rg := cfg.Scope.ResourceGroup
	if rg == "" {
		rg = m.opts.Region
	}

	sub := cfg.Scope.Subscription
	if sub == "" {
		sub = m.opts.AccountID
	}

	topicID := idgen.AzureID(sub, rg, resourceProvider, resourceType, cfg.Name)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	inputSchema := cfg.InputSchema
	if inputSchema == "" {
		inputSchema = defaultInputSchema
	}

	publicNetworkAccess := cfg.PublicNetworkAccess
	if publicNetworkAccess == "" {
		publicNetworkAccess = defaultPublicNetworkAccess
	}

	info := driver.EventBusInfo{
		Name:                cfg.Name,
		Scope:               cfg.Scope,
		ARN:                 topicID,
		State:               activeTopicState,
		CreatedAt:           m.opts.Clock.Now().UTC().Format(time.RFC3339),
		Tags:                tags,
		Region:              cfg.Region,
		InputSchema:         inputSchema,
		PublicNetworkAccess: publicNetworkAccess,
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
		filter:  parseSubscriptionFilter(cfg.Description),
		dest:    parseSubscriptionDestination(cfg.Description),
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

	all := bd.rules.SortedValues()

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
func (m *Mock) PutEvents(ctx context.Context, events []driver.Event) (*driver.PublishResult, error) {
	result := &driver.PublishResult{
		EventIDs: make([]string, 0, len(events)),
	}

	for i := range events {
		// Real Event Grid preserves the publisher-supplied event id end-to-end
		// (subscribers dedup on it); only synthesize one when the publisher
		// omitted it.
		if events[i].ID == "" {
			events[i].ID = generateEventID(&events[i], m.opts.Clock.Now())
		}

		if events[i].Time.IsZero() {
			events[i].Time = m.opts.Clock.Now()
		}

		busName := events[i].EventBus
		if busName == "" {
			result.FailCount++

			continue
		}

		bd, ok := m.lookupBus(&events[i])
		if !ok {
			result.FailCount++

			continue
		}

		m.storeEvent(bd, &events[i])

		matched := m.matchedRuleData(bd, &events[i])
		m.deliverToTargets(ctx, matched, &events[i], bd.info.ARN)

		m.emitMetric(busName, map[string]float64{
			"PublishedEvents": 1, "MatchedEvents": float64(len(matched)),
		})

		result.SuccessCount++
		result.EventIDs = append(result.EventIDs, events[i].ID)
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

// UpdateEventBus replaces the mutable fields of an existing topic — ARM
// CreateOrUpdate-on-existing semantics (tags come from the request; identity
// and CreatedAt are preserved).
func (m *Mock) UpdateEventBus(_ context.Context, cfg driver.EventBusConfig) (*driver.EventBusInfo, error) {
	bd, ok := m.buses.Get(cfg.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", cfg.Name)
	}

	if cfg.Tags != nil {
		bd.info.Tags = maps.Clone(cfg.Tags)
	}
	if !cfg.Scope.IsZero() {
		bd.info.Scope = cfg.Scope
	}
	if cfg.Region != "" {
		bd.info.Region = cfg.Region
	}
	// InputSchema is immutable after creation, so it is intentionally not
	// updated here; PublicNetworkAccess is mutable and applied when supplied.
	if cfg.PublicNetworkAccess != "" {
		bd.info.PublicNetworkAccess = cfg.PublicNetworkAccess
	}

	m.buses.Set(cfg.Name, bd)

	result := bd.info

	return &result, nil
}

// MatchedRules returns the subscriptions on the event's own topic that match
// it (exported for testing). Scoped to event.EventBus so a rule on one topic
// never counts as matched for an event published to a different topic.
func (m *Mock) MatchedRules(event *driver.Event) []driver.Rule {
	bd, ok := m.lookupBus(event)
	if !ok {
		return nil
	}

	rds := m.matchedRuleData(bd, event)

	matched := make([]driver.Rule, 0, len(rds))
	for _, rd := range rds {
		matched = append(matched, rd.rule)
	}

	return matched
}

// matchedRuleData returns the enabled subscriptions on bd (the event's own
// topic) whose event pattern and Event Grid filter (subject prefix/suffix,
// included event types, advanced filters) both match event.
func (*Mock) matchedRuleData(bd *busData, event *driver.Event) []*ruleData {
	var matched []*ruleData

	for _, rd := range bd.rules.All() {
		if rd.rule.State != defaultRuleState {
			continue
		}

		if !matchesPattern(event, rd.rule.EventPattern) {
			continue
		}

		if !rd.filter.matches(event) {
			continue
		}

		matched = append(matched, rd)
	}

	return matched
}
