// Package logging provides an in-memory mock implementation of OCI Logging.
// It implements the portable logging driver: a log group is the log group, an
// OCI log is the log stream, and an ingested log entry is the log event.
//
// Real OCI splits the service across three API surfaces — the logging control
// plane for log groups and logs, loggingingestion for PutLogs and
// loggingsearch for SearchLogs. The mock holds all three behind one type; the
// wire handler keeps their paths apart.
package logging

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const timeFormat = time.RFC3339

// defaultRetentionDays is what a log group hands to logs created in it when
// the caller names no retention. Real OCI defaults a log to 30 days.
const defaultRetentionDays = 30

// defaultLogLimit caps a portable read that names no limit.
const defaultLogLimit = 100

// OCI lifecycle states for log groups and logs.
const (
	StateCreating = "CREATING"
	StateActive   = "ACTIVE"
)

// Log types. A CUSTOM log takes entries from PutLogs; a SERVICE log is fed by
// an OCI service named in its configuration.
const (
	LogTypeCustom  = "CUSTOM"
	LogTypeService = "SERVICE"
)

// sourceTypeOCIService is the only source type OCI defines for a service log.
const sourceTypeOCIService = "OCISERVICE"

// OCID resource type segments.
const (
	typeLogGroup = "loggroup"
	typeLog      = "log"
)

// metricNamespace is the OCI Monitoring namespace Logging publishes under.
const metricNamespace = "oci_logging"

// Compile-time check that Mock implements driver.Logging.
var _ driver.Logging = (*Mock)(nil)

// LogGroup is an OCI log group.
type LogGroup struct {
	ID               string
	CompartmentID    string
	DisplayName      string
	Description      string
	LifecycleState   string
	TimeCreated      string
	TimeLastModified string
	FreeformTags     map[string]string
	// RetentionDays is the retention new logs in the group inherit. Real OCI
	// carries retention on the log; the portable driver carries it on the
	// group, so the group holds the default the two agree on.
	RetentionDays int
}

// LogSource names the OCI service and resource feeding a SERVICE log.
type LogSource struct {
	SourceType string
	Service    string
	Resource   string
	Category   string
	Parameters map[string]string
}

// LogConfiguration is a log's source and archiving configuration.
type LogConfiguration struct {
	CompartmentID    string
	Source           LogSource
	ArchivingEnabled bool
}

// Log is an OCI log inside a log group.
type Log struct {
	ID                string
	LogGroupID        string
	CompartmentID     string
	DisplayName       string
	LogType           string
	IsEnabled         bool
	RetentionDuration int
	Configuration     *LogConfiguration
	LifecycleState    string
	TimeCreated       string
	TimeLastModified  string
	FreeformTags      map[string]string
}

// LogEntry is a single ingested log entry.
type LogEntry struct {
	ID           string
	LogID        string
	Time         time.Time
	IngestedTime time.Time
	Data         string
	Source       string
	Subject      string
	Type         string
}

// LogEntryItem is one entry of a PutLogs batch.
type LogEntryItem struct {
	ID   string
	Data string
	Time time.Time
}

// LogEntryBatch is one batch of a PutLogs request. Entries with no time of
// their own take DefaultLogEntryTime.
type LogEntryBatch struct {
	Entries             []LogEntryItem
	Source              string
	Type                string
	Subject             string
	DefaultLogEntryTime time.Time
}

// LogGroupSpec describes a log group to create.
type LogGroupSpec struct {
	CompartmentID string
	DisplayName   string
	Description   string
	FreeformTags  map[string]string
	RetentionDays int
}

// LogGroupUpdate carries the mutable fields of a log group. A nil pointer
// leaves the field untouched.
type LogGroupUpdate struct {
	DisplayName  *string
	Description  *string
	FreeformTags map[string]string
}

// LogSpec describes a log to create.
type LogSpec struct {
	DisplayName       string
	LogType           string
	IsEnabled         bool
	RetentionDuration int
	Configuration     *LogConfiguration
	FreeformTags      map[string]string
}

// LogUpdate carries the mutable fields of a log. A nil pointer leaves the
// field untouched.
type LogUpdate struct {
	DisplayName       *string
	IsEnabled         *bool
	RetentionDuration *int
	Configuration     *LogConfiguration
	FreeformTags      map[string]string
}

// LogFilter narrows a ListLogs call to what the query parameters name.
type LogFilter struct {
	DisplayName    string
	LogType        string
	SourceService  string
	SourceResource string
	LifecycleState string
}

// logRecord is a log and the entries ingested into it.
type logRecord struct {
	log     Log
	entries []LogEntry
}

// Mock is an in-memory mock implementation of the OCI Logging service.
type Mock struct {
	// mu guards every store and the values they hold. Operations span more
	// than one store — deleting a group walks the logs, search walks groups
	// and logs together — so one lock covers them rather than each store's own.
	mu sync.RWMutex

	groups *memstore.Store[*LogGroup]
	logs   *memstore.Store[*logRecord]
	opts   *config.Options

	monitoring mondriver.Monitoring
}

// New creates a new OCI Logging mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		groups: memstore.New[*LogGroup](),
		logs:   memstore.New[*logRecord](),
		opts:   opts,
	}
}

// SetMonitoring points the mock at the monitoring service so ingestion
// publishes OCI Logging's metrics.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.monitoring = mon
}

// newOCID mints an OCID for the given resource type in the configured realm
// and region.
func (m *Mock) newOCID(resourceType string) string {
	return idgen.OCID(resourceType, m.opts.Realm, m.opts.OCIRegion())
}

// now returns the current time in OCI's timestamp format.
func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(timeFormat)
}

// compartmentOr falls back to the configured default compartment, which is
// where a resource lands when the caller names none.
func (m *Mock) compartmentOr(id string) string {
	if id != "" {
		return id
	}

	return m.opts.CompartmentID
}

// groupByName resolves a log group by display name. Display names are unique
// across the mock, which is what lets the portable driver key groups by name.
// The caller holds mu.
func (m *Mock) groupByName(name string) (*LogGroup, bool) {
	for _, g := range m.groups.SortedValues() {
		if g.DisplayName == name {
			return g, true
		}
	}

	return nil, false
}

// logByName resolves a log by display name within a group. The caller holds mu.
func (m *Mock) logByName(groupID, name string) (*logRecord, bool) {
	for _, rec := range m.logs.SortedValues() {
		if rec.log.LogGroupID == groupID && rec.log.DisplayName == name {
			return rec, true
		}
	}

	return nil, false
}

// logsIn returns every log belonging to a group, ordered by OCID. The caller
// holds mu.
func (m *Mock) logsIn(groupID string) []*logRecord {
	var out []*logRecord

	for _, rec := range m.logs.SortedValues() {
		if rec.log.LogGroupID == groupID {
			out = append(out, rec)
		}
	}

	return out
}

// storedBytes sums the entry payloads held under a group. The caller holds mu.
func (m *Mock) storedBytes(groupID string) int64 {
	var total int64

	for _, rec := range m.logsIn(groupID) {
		for i := range rec.entries {
			total += int64(len(rec.entries[i].Data))
		}
	}

	return total
}

// emitMetric publishes one Logging metric. Called with mu released, so a
// monitoring driver reaching back into this mock cannot deadlock.
func (m *Mock) emitMetric(
	ctx context.Context, mon mondriver.Monitoring, name string, value float64, dims map[string]string,
) {
	if mon == nil {
		return
	}

	// Publication is best-effort: a metric the monitoring mock refuses must
	// not fail the ingestion that produced it.
	_ = mon.PutMetricData(ctx, []mondriver.MetricDatum{{
		Namespace:  metricNamespace,
		MetricName: name,
		Value:      value,
		Unit:       "Count",
		Dimensions: dims,
		Timestamp:  m.opts.Clock.Now(),
	}})
}

// copyTags returns a copy of a tag map, or nil for an empty one.
func copyTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	return maps.Clone(tags)
}

// requireName rejects an empty resource name, which OCI does not accept.
func requireName(name, what string) error {
	if name == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "%s is required", what)
	}

	return nil
}
