// Package configservice provides an in-memory mock implementation of AWS Config
// (configservice). It models configuration recorders, delivery channels, config
// rules, conformance packs, organization rules/packs, aggregators and
// authorizations, remediation, stored queries, retention, and resource-config
// queries.
//
// Behavioral surfaces (create/put/delete/start/stop lifecycles, uniqueness and
// limit invariants, tagging) are fully modeled. Read-only compliance,
// evaluation, and discovered-resource surfaces are synthesized from the
// emulator's own recorded state (rules/evaluations/PutResourceConfig items) or
// return plausible empty results — the emulator runs no real Config recording
// pipeline. Each synthesized method documents this on its declaration.
package configservice

import (
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// Compile-time check that Mock implements driver.Config.
var _ driver.Config = (*Mock)(nil)

// AWS Config service limits (real defaults).
const (
	maxTags        = 50
	defaultPageLim = 100
	maxPageLim     = 100
	// maxConfigRules is the default per-account config-rule limit.
	maxConfigRules = 150
	// maxConformancePacks is the default per-account conformance-pack limit.
	maxConformancePacks = 50
	// defaultName is the name Config assigns to a customer-managed recorder,
	// delivery channel, or retention configuration when none is supplied.
	defaultName = "default"
)

// recorderData is a recorder plus its own lock.
type recorderData struct {
	rec driver.ConfigurationRecorder
	mu  sync.RWMutex
}

// channelData is a delivery channel plus its own lock.
type channelData struct {
	ch driver.DeliveryChannel
	mu sync.RWMutex
}

// ruleData is a config rule plus its own lock.
type ruleData struct {
	rule  driver.ConfigRule
	evals []driver.Evaluation // synthesized/reported evaluations
	// resultToken is the opaque token currently issued for this rule. In real
	// Config a result token is a large opaque value delivered to a custom rule's
	// Lambda and passed back to PutEvaluations; it is never the rule name. The
	// emulator issues one at create time and refreshes it on
	// StartConfigRulesEvaluation, validating incoming tokens against the registry.
	resultToken string
	mu          sync.RWMutex
}

// packData is a conformance pack plus its own lock.
type packData struct {
	pack driver.ConformancePack
	mu   sync.RWMutex
}

// aggData is a configuration aggregator plus its own lock.
type aggData struct {
	agg driver.ConfigurationAggregator
	mu  sync.RWMutex
}

// Mock is an in-memory implementation of AWS Config.
type Mock struct {
	recorders   *memstore.Store[*recorderData]
	channels    *memstore.Store[*channelData]
	rules       *memstore.Store[*ruleData]
	packs       *memstore.Store[*packData]
	aggregators *memstore.Store[*aggData]
	orgRules    *memstore.Store[*driver.OrganizationConfigRule]
	orgPacks    *memstore.Store[*driver.OrganizationConformancePack]
	remediation *memstore.Store[*driver.RemediationConfiguration] // keyed by rule name
	storedQuery *memstore.Store[*driver.StoredQuery]
	retention   *memstore.Store[*driver.RetentionConfiguration]
	resources   *memstore.Store[*driver.ConfigurationItem] // keyed by type/id (PutResourceConfig)
	connectors  *memstore.Store[*connectorData]

	// authMu guards the aggregation-authorization and remediation-exception
	// secondary collections which are slices rather than keyed stores.
	authMu         sync.RWMutex
	authorizations []driver.AggregationAuthorization
	remExceptions  map[string][]driver.RemediationException // rule name -> exceptions

	// tagMu guards tag get/set on the stores whose values are plain pointers
	// without a per-item lock (org rules/packs, stored queries).
	tagMu sync.Mutex

	// createMu serializes the one-per-account create paths (recorders and
	// delivery channels) so the "at most one" cap holds under concurrent creates
	// with DIFFERENT names. A per-store scan+insert is otherwise not atomic:
	// SetIfAbsent only dedups the SAME key, so two distinct names would each pass
	// the scan and both insert. Held across scan+insert only, never across a read.
	createMu sync.Mutex

	// evalTokens maps opaque PutEvaluations result tokens to the config rule they
	// were issued for (by StartConfigRulesEvaluation). Guarded by createMu is
	// wrong scope, so it has its own lock.
	tokenMu    sync.RWMutex
	evalTokens map[string]string // resultToken -> config rule name

	opts *config.Options
}

type connectorData struct {
	name              string
	arn               string
	connectorAgentArn string
}

// New creates a new AWS Config mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		recorders:     memstore.New[*recorderData](),
		channels:      memstore.New[*channelData](),
		rules:         memstore.New[*ruleData](),
		packs:         memstore.New[*packData](),
		aggregators:   memstore.New[*aggData](),
		orgRules:      memstore.New[*driver.OrganizationConfigRule](),
		orgPacks:      memstore.New[*driver.OrganizationConformancePack](),
		remediation:   memstore.New[*driver.RemediationConfiguration](),
		storedQuery:   memstore.New[*driver.StoredQuery](),
		retention:     memstore.New[*driver.RetentionConfiguration](),
		resources:     memstore.New[*driver.ConfigurationItem](),
		connectors:    memstore.New[*connectorData](),
		remExceptions: map[string][]driver.RemediationException{},
		evalTokens:    map[string]string{},
		opts:          opts,
	}
}
