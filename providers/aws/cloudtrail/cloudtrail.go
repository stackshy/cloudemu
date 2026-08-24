// Package cloudtrail provides an in-memory mock implementation of AWS
// CloudTrail: trails with logging status, event data stores, channels,
// dashboards, imports, event/insight selectors, ad-hoc queries, resource
// policies, tags, and the read-only lookup/insight/public-key surfaces.
//
// Read-only analytics surfaces (LookupEvents, ListInsightsData,
// ListInsightsMetricData, GetQueryResults, ListPublicKeys) have no real event
// stream behind them, so they return synthesized/empty results — the local-dev
// analog of an account with no recorded activity. Queries are accepted, stored,
// and immediately marked FINISHED with an empty result set.
package cloudtrail

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// Compile-time check that Mock implements driver.CloudTrail.
var _ driver.CloudTrail = (*Mock)(nil)

const (
	minTrailNameLen   = 3
	maxTrailNameLen   = 128
	defaultRetention  = 366
	minRetention      = 7
	maxRetention      = 3653
	arnParts          = 6
	defaultMaxResults = 50
	maxEventSizeStd   = "Standard"
	arnPrefix         = "arn"
	serviceName       = "cloudtrail"
)

// trailData is a trail plus its logging status, selectors, and lock.
type trailData struct {
	trail    driver.Trail
	status   driver.TrailStatus
	selors   []driver.EventSelector
	advSel   []driver.AdvancedEventSelector
	insights []driver.InsightSelector
	mu       sync.RWMutex
}

// edsData is an event data store plus federation state and its lock.
type edsData struct {
	eds               driver.EventDataStore
	insights          []driver.InsightSelector
	federationRoleARN string
	federationStatus  string
	maxEventSize      string
	mu                sync.RWMutex
}

// channelData is a channel plus its lock.
type channelData struct {
	channel      driver.Channel
	maxEventSize string
	mu           sync.RWMutex
}

// dashboardData is a dashboard plus its lock.
type dashboardData struct {
	dashboard driver.Dashboard
	mu        sync.RWMutex
}

// Mock is an in-memory implementation of AWS CloudTrail.
type Mock struct {
	// trails is keyed by trail name; trailARNIdx resolves ARN -> name.
	trails      *memstore.Store[*trailData]
	trailARNIdx *memstore.Store[string]

	eds         *memstore.Store[*edsData] // keyed by ARN
	edsNameIdx  *memstore.Store[string]   // EDS name -> ARN, for atomic name claim
	chanNameIdx *memstore.Store[string]   // channel name -> ARN, for atomic name claim
	channels    *memstore.Store[*channelData]
	dashboards  *memstore.Store[*dashboardData] // keyed by name
	imports     *memstore.Store[*importData]    // keyed by ID
	queries     *memstore.Store[*queryData]     // keyed by ID

	policyMu sync.RWMutex
	policies map[string]string // resourceARN -> policy JSON

	tagsMu sync.RWMutex
	tags   map[string]map[string]string // resourceID (ARN) -> tags

	orgMu     sync.Mutex
	delegated map[string]struct{}

	// eventsMu guards events, the bounded, newest-last log of management events
	// LookupEvents queries. Fed by RecordEvent as API activity flows through the
	// wire server (real CloudTrail records management events for API calls).
	eventsMu sync.RWMutex
	events   []driver.Event

	opts *config.Options
}

// New creates a new CloudTrail mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		trails:      memstore.New[*trailData](),
		trailARNIdx: memstore.New[string](),
		eds:         memstore.New[*edsData](),
		edsNameIdx:  memstore.New[string](),
		chanNameIdx: memstore.New[string](),
		channels:    memstore.New[*channelData](),
		dashboards:  memstore.New[*dashboardData](),
		imports:     memstore.New[*importData](),
		queries:     memstore.New[*queryData](),
		policies:    map[string]string{},
		tags:        map[string]map[string]string{},
		delegated:   map[string]struct{}{},
		opts:        opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

func (m *Mock) trailARN(name string) string {
	return idgen.AWSARN("cloudtrail", m.opts.Region, m.opts.AccountID, "trail/"+name)
}

func (m *Mock) edsARN() string {
	return idgen.AWSARN("cloudtrail", m.opts.Region, m.opts.AccountID, "eventdatastore/"+idgen.GenerateID(""))
}

func (m *Mock) channelARN(id string) string {
	return idgen.AWSARN("cloudtrail", m.opts.Region, m.opts.AccountID, "channel/"+id)
}

func (m *Mock) dashboardARN(name string) string {
	return idgen.AWSARN("cloudtrail", m.opts.Region, m.opts.AccountID, "dashboard/"+name)
}

// resolveTrail finds a trail by name or ARN. Name takes precedence when both
// resolve; an ARN is mapped through the ARN index.
func (m *Mock) resolveTrail(nameOrARN string) (*trailData, error) {
	if nameOrARN == "" {
		return nil, errInvalidTrailName("trail name is required")
	}

	name := nameOrARN

	if strings.HasPrefix(nameOrARN, "arn:") {
		n, ok := m.trailARNIdx.Get(nameOrARN)
		if !ok {
			return nil, errTrailNotFound(nameOrARN)
		}

		name = n
	}

	td, ok := m.trails.Get(name)
	if !ok {
		return nil, errTrailNotFound(nameOrARN)
	}

	return td, nil
}

// resolveEDS finds an event data store by ARN, validating the ARN shape first so
// a malformed ARN is an EventDataStoreARNInvalidException, distinct from a
// well-formed-but-absent ARN (EventDataStoreNotFoundException).
func (m *Mock) resolveEDS(arn string) (*edsData, error) {
	if !validEDSARN(arn) {
		return nil, errEDSARNInvalid(arn)
	}

	ed, ok := m.eds.Get(arn)
	if !ok {
		return nil, errEDSNotFound(arn)
	}

	return ed, nil
}

// resolveChannel finds a channel by ARN, validating the ARN shape first.
func (m *Mock) resolveChannel(arn string) (*channelData, error) {
	if !validChannelARN(arn) {
		return nil, errChannelARNInvalid(arn)
	}

	cd, ok := m.channels.Get(arn)
	if !ok {
		return nil, errChannelNotFound(arn)
	}

	return cd, nil
}

// validEDSARN reports whether arn has the CloudTrail event-data-store ARN shape
// (arn:aws:cloudtrail:<region>:<account>:eventdatastore/<id>).
func validEDSARN(arn string) bool {
	seg := strings.SplitN(arn, ":", arnParts)
	if len(seg) != arnParts {
		return false
	}

	return seg[0] == arnPrefix && seg[2] == serviceName &&
		strings.HasPrefix(seg[5], "eventdatastore/") && strings.TrimPrefix(seg[5], "eventdatastore/") != ""
}

// validChannelARN reports whether arn has the CloudTrail channel ARN shape.
func validChannelARN(arn string) bool {
	seg := strings.SplitN(arn, ":", arnParts)
	if len(seg) != arnParts {
		return false
	}

	return seg[0] == arnPrefix && seg[2] == serviceName &&
		strings.HasPrefix(seg[5], "channel/") && strings.TrimPrefix(seg[5], "channel/") != ""
}

// paginate returns a sorted-by-key page of a store's values plus the next token.
// keyOf yields the pagination key of a copied item (used as the next token), and
// cp copies a stored item under its own read lock via copyLocked. An empty
// nextToken starts at the beginning; a returned empty token means the last page.
func paginate[S any, T any](
	all map[string]S, nextToken string, maxResults int32,
	cp func(S) T, keyOf func(T) string,
) (page []T, next string) {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	limit := int(maxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	out := make([]T, 0, len(keys))
	started := nextToken == ""

	for _, k := range keys {
		if !started {
			if k == nextToken {
				started = true
			}

			continue
		}

		if len(out) == limit {
			return out, keyOf(out[len(out)-1])
		}

		out = append(out, cp(all[k]))
	}

	return out, ""
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyDestinations(in []driver.Destination) []driver.Destination {
	if in == nil {
		return nil
	}

	return append([]driver.Destination(nil), in...)
}

// copyAdvSelectors deep-copies advanced event selectors including their nested
// field-selector slices, so a returned value never aliases stored state.
func copyAdvSelectors(in []driver.AdvancedEventSelector) []driver.AdvancedEventSelector {
	if in == nil {
		return nil
	}

	out := make([]driver.AdvancedEventSelector, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].FieldSelectors = copyFieldSelectors(in[i].FieldSelectors)
	}

	return out
}

func copyFieldSelectors(in []driver.AdvancedFieldSelector) []driver.AdvancedFieldSelector {
	if in == nil {
		return nil
	}

	out := make([]driver.AdvancedFieldSelector, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Equals = append([]string(nil), in[i].Equals...)
		out[i].NotEquals = append([]string(nil), in[i].NotEquals...)
		out[i].StartsWith = append([]string(nil), in[i].StartsWith...)
		out[i].NotStartsWith = append([]string(nil), in[i].NotStartsWith...)
		out[i].EndsWith = append([]string(nil), in[i].EndsWith...)
		out[i].NotEndsWith = append([]string(nil), in[i].NotEndsWith...)
	}

	return out
}

// copyEventSelectors deep-copies basic event selectors including nested
// data-resource and management-source slices.
func copyEventSelectors(in []driver.EventSelector) []driver.EventSelector {
	if in == nil {
		return nil
	}

	out := make([]driver.EventSelector, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].ExcludeManagementEventSources =
			append([]string(nil), in[i].ExcludeManagementEventSources...)
		out[i].DataResources = make([]driver.DataResource, len(in[i].DataResources))

		for j := range in[i].DataResources {
			out[i].DataResources[j] = in[i].DataResources[j]
			out[i].DataResources[j].Values =
				append([]string(nil), in[i].DataResources[j].Values...)
		}
	}

	return out
}

func copyInsightSelectors(in []driver.InsightSelector) []driver.InsightSelector {
	if in == nil {
		return nil
	}

	return append([]driver.InsightSelector(nil), in...)
}

// copyEDS returns a deep copy of an event data store.
func copyEDS(e *driver.EventDataStore) driver.EventDataStore {
	out := *e
	out.Tags = copyTags(e.Tags)
	out.AdvancedEventSelectors = copyAdvSelectors(e.AdvancedEventSelectors)

	return out
}
