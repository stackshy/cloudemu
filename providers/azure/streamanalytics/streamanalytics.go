// Package streamanalytics provides an in-memory mock of Azure Stream Analytics
// (Microsoft.StreamAnalytics/streamingjobs) — the ARM control plane only. It
// manages the streaming-job lifecycle (create/replace, get, patch, delete,
// list-by-group, list-by-subscription), the job start/stop/scale actions and
// their jobState state machine, and the nested transformation / inputs /
// outputs / functions child resources (create-or-replace, get, update, delete,
// list plus the test / RetrieveDefaultDefinition actions).
//
// The Stream Analytics data plane — the streaming query engine that ingests
// input events, evaluates the SQL query and writes to outputs — is out of
// scope; this surface is the management-plane resource provider only. No events
// are processed and no query is executed; a "running" job is a state label, not
// a live pipeline.
//
// Every service-minted field stays stable for the lifetime of the resource so
// infrastructure-as-code tools (Terraform's azurerm_stream_analytics_job and
// its child resources) see no drift on re-plan:
//   - job jobId (a computed GUID), provisioningState ("Succeeded"), createdDate
//     and etag, minted once at create and byte-stable across every read/patch.
//   - jobState, driven only by the start/stop/scale actions.
//   - child etag and the child's stamped resource id, minted once at create.
//
// The input/output/function datasource, serialization and binding blocks are
// stored as raw JSON and round-trip verbatim, so a caller reads back exactly
// the type discriminator and properties it sent without the mock enumerating
// every datasource subtype.
package streamanalytics

import (
	"context"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

const (
	// providerNamespace is the ARM provider namespace.
	providerNamespace = "Microsoft.StreamAnalytics"
	// jobType is the ARM streaming-job resource type segment.
	jobType = "streamingjobs"

	// stateSucceeded is the terminal provisioningState a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"

	// JobStateCreated is the jobState a freshly created job carries, before any
	// start action.
	JobStateCreated = "Created"
	// JobStateRunning is the jobState a started job settles to.
	JobStateRunning = "Running"
	// JobStateStopped is the jobState a stopped job settles to.
	JobStateStopped = "Stopped"
	// JobStateFailed is a terminal error state a job may be restarted from.
	JobStateFailed = "Failed"
	// JobStateDegraded is a running-but-impaired state a job may be stopped from.
	JobStateDegraded = "Degraded"

	// skuStandard is the default streaming-job SKU real Azure assigns.
	skuStandard = "Standard"
	// defaultCompatibilityLevel is the compatibilityLevel real Azure defaults to.
	defaultCompatibilityLevel = "1.0"
	// defaultDataLocale is the dataLocale real Azure defaults to.
	defaultDataLocale = "en-US"
	// createdDateLayout is the RFC3339 form real Azure stamps createdDate in.
	createdDateLayout = "2006-01-02T15:04:05.000Z07:00"
)

// StreamingJob is a stored Microsoft.StreamAnalytics/streamingjobs resource.
// Subscription, ResourceGroup and Name preserve the caller's casing; the
// computed fields are minted at create and never regenerated on a read.
type StreamingJob struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`

	SkuName                            string `json:"skuName"`
	EventsOutOfOrderPolicy             string `json:"eventsOutOfOrderPolicy,omitempty"`
	OutputErrorPolicy                  string `json:"outputErrorPolicy,omitempty"`
	EventsOutOfOrderMaxDelayInSeconds  *int   `json:"eventsOutOfOrderMaxDelayInSeconds,omitempty"`
	EventsLateArrivalMaxDelayInSeconds *int   `json:"eventsLateArrivalMaxDelayInSeconds,omitempty"`
	DataLocale                         string `json:"dataLocale,omitempty"`
	CompatibilityLevel                 string `json:"compatibilityLevel,omitempty"`
	JobType                            string `json:"jobType,omitempty"`
	OutputStartMode                    string `json:"outputStartMode,omitempty"`
	OutputStartTime                    string `json:"outputStartTime,omitempty"`

	// Computed, stable fields.
	JobID             string `json:"jobId"`
	ProvisioningState string `json:"provisioningState"`
	JobState          string `json:"jobState"`
	CreatedDate       string `json:"createdDate"`
	Etag              string `json:"etag"`
}

// ARMID returns the fully-qualified ARM resource id of the job.
func (j *StreamingJob) ARMID() string {
	return idgen.AzureID(j.Subscription, j.ResourceGroup, providerNamespace, jobType, j.Name)
}

// JobInput carries the mutable fields of a job create/update request. Pointer
// fields distinguish "not supplied" (nil, preserve existing) from an explicit
// value, so a PATCH overlays only what it names while a create seeds defaults.
type JobInput struct {
	Tags                               map[string]string
	SkuName                            *string
	EventsOutOfOrderPolicy             *string
	OutputErrorPolicy                  *string
	EventsOutOfOrderMaxDelayInSeconds  *int
	EventsLateArrivalMaxDelayInSeconds *int
	DataLocale                         *string
	CompatibilityLevel                 *string
	JobType                            *string
	OutputStartMode                    *string
	OutputStartTime                    *string
}

// Mock is the in-memory backend for streaming jobs and their child resources.
type Mock struct {
	mu       sync.RWMutex
	clock    config.Clock
	jobs     *memstore.Store[*StreamingJob]
	children *memstore.Store[*Child]
}

// New creates an empty Stream Analytics mock. The clock stamps a job's stable
// createdDate; it falls back to the real clock when opts (or its clock) is nil
// so the mock stays usable standalone (e.g. New(nil) in tests).
func New(opts *config.Options) *Mock {
	clock := config.Clock(config.RealClock{})
	if opts != nil && opts.Clock != nil {
		clock = opts.Clock
	}

	return &Mock{
		clock:    clock,
		jobs:     memstore.New[*StreamingJob](),
		children: memstore.New[*Child](),
	}
}

// jobKey is the case-insensitive store key for a streaming job.
func jobKey(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, jobType, name))
}

// CreateOrUpdateJob creates a new streaming job or updates an existing one. The
// computed fields (jobId, provisioningState, jobState, createdDate, etag) are
// minted once at create and preserved across updates. Location is immutable in
// real Azure and is preserved on update. It returns the stored job and whether
// it was newly created.
func (m *Mock) CreateOrUpdateJob(
	_ context.Context, sub, rg, name, location string, in *JobInput,
) (StreamingJob, bool, error) {
	if err := validateJob(sub, rg, name, location); err != nil {
		return StreamingJob{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := jobKey(sub, rg, name)

	existing, existed := m.jobs.Get(k)
	created := !existed

	var j StreamingJob
	if existed {
		j = *existing
	} else {
		j = m.newJob(sub, rg, name, location)
	}

	applyJobIdentity(&j, in)
	applyJobPolicies(&j, in)
	m.jobs.Set(k, &j)

	return cloneJob(&j), created, nil
}

// newJob seeds a fresh job with its immutable identity, ARM defaults and its
// computed, stable fields. The jobId derives deterministically from the
// resource id so it is stable yet distinct per job.
func (m *Mock) newJob(sub, rg, name, location string) StreamingJob {
	id := jobKey(sub, rg, name)

	return StreamingJob{
		Subscription:       sub,
		ResourceGroup:      rg,
		Name:               name,
		Location:           location,
		SkuName:            skuStandard,
		CompatibilityLevel: defaultCompatibilityLevel,
		DataLocale:         defaultDataLocale,
		JobID:              idgen.SyntheticGUID("streamanalytics/jobid/" + id),
		ProvisioningState:  stateSucceeded,
		JobState:           JobStateCreated,
		CreatedDate:        m.clock.Now().UTC().Format(createdDateLayout),
		Etag:               idgen.SyntheticGUID("streamanalytics/etag/" + id),
	}
}

// GetJob returns the job, or a NotFound error.
func (m *Mock) GetJob(_ context.Context, sub, rg, name string) (StreamingJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	j, ok := m.jobs.Get(jobKey(sub, rg, name))
	if !ok {
		return StreamingJob{}, cerrors.Newf(cerrors.NotFound, "streaming job %q not found", name)
	}

	return cloneJob(j), nil
}

// DeleteJob removes the job and cascades to every child resource under it,
// reporting whether the job existed.
func (m *Mock) DeleteJob(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existed := m.jobs.Delete(jobKey(sub, rg, name))

	prefix := jobKey(sub, rg, name) + "/"
	for ck := range m.children.All() {
		if strings.HasPrefix(ck, prefix) {
			m.children.Delete(ck)
		}
	}

	return existed, nil
}

// ListJobsByResourceGroup returns every job in the group, sorted by name.
func (m *Mock) ListJobsByResourceGroup(_ context.Context, sub, rg string) ([]StreamingJob, error) {
	return m.filterJobs(func(j *StreamingJob) bool {
		return strings.EqualFold(j.Subscription, sub) && strings.EqualFold(j.ResourceGroup, rg)
	}), nil
}

// ListJobsBySubscription returns every job in the subscription, sorted by name.
func (m *Mock) ListJobsBySubscription(_ context.Context, sub string) ([]StreamingJob, error) {
	return m.filterJobs(func(j *StreamingJob) bool {
		return strings.EqualFold(j.Subscription, sub)
	}), nil
}

// DiscoverJobs returns every stored job, for the inventory walk.
func (m *Mock) DiscoverJobs(_ context.Context) ([]StreamingJob, error) {
	return m.filterJobs(func(*StreamingJob) bool { return true }), nil
}

// JobExists reports whether the named job is stored, without cloning it.
func (m *Mock) JobExists(_ context.Context, sub, rg, name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.jobs.Has(jobKey(sub, rg, name))
}

// PurgeResourceGroup deletes every job and child under sub/rg, so a
// resource-group delete cascades into its Stream Analytics resources.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, j := range m.jobs.All() {
		if strings.EqualFold(j.Subscription, sub) && strings.EqualFold(j.ResourceGroup, rg) {
			m.jobs.Delete(k)
		}
	}

	for k, c := range m.children.All() {
		if strings.EqualFold(c.Subscription, sub) && strings.EqualFold(c.ResourceGroup, rg) {
			m.children.Delete(k)
		}
	}

	return nil
}

// filterJobs returns the jobs matching pred, sorted by name.
func (m *Mock) filterJobs(pred func(*StreamingJob) bool) []StreamingJob {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []StreamingJob

	for _, j := range m.jobs.All() {
		if pred(j) {
			out = append(out, cloneJob(j))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyJobIdentity overlays the identity/sku request fields onto j, leaving the
// immutable location and computed fields untouched. A nil pointer/map means
// "not supplied": the stored (or default) value is preserved, so a PATCH merges
// only what it names. A non-nil tags map replaces the whole set, matching ARM
// resource-level PATCH tags semantics.
func applyJobIdentity(j *StreamingJob, in *JobInput) {
	if in.Tags != nil {
		j.Tags = maps.Clone(in.Tags)
	}

	if in.SkuName != nil && *in.SkuName != "" {
		j.SkuName = *in.SkuName
	}

	if in.DataLocale != nil {
		j.DataLocale = *in.DataLocale
	}

	if in.CompatibilityLevel != nil && *in.CompatibilityLevel != "" {
		j.CompatibilityLevel = *in.CompatibilityLevel
	}

	if in.JobType != nil {
		j.JobType = *in.JobType
	}

	if j.SkuName == "" {
		j.SkuName = skuStandard
	}
}

// applyJobPolicies overlays the event-ordering / output-error policy request
// fields onto j. The int-pointer fields let an explicit 0 round-trip while a
// nil preserves the stored value.
func applyJobPolicies(j *StreamingJob, in *JobInput) {
	if in.EventsOutOfOrderPolicy != nil {
		j.EventsOutOfOrderPolicy = *in.EventsOutOfOrderPolicy
	}

	if in.OutputErrorPolicy != nil {
		j.OutputErrorPolicy = *in.OutputErrorPolicy
	}

	if in.EventsOutOfOrderMaxDelayInSeconds != nil {
		v := *in.EventsOutOfOrderMaxDelayInSeconds
		j.EventsOutOfOrderMaxDelayInSeconds = &v
	}

	if in.EventsLateArrivalMaxDelayInSeconds != nil {
		v := *in.EventsLateArrivalMaxDelayInSeconds
		j.EventsLateArrivalMaxDelayInSeconds = &v
	}

	if in.OutputStartMode != nil {
		j.OutputStartMode = *in.OutputStartMode
	}

	if in.OutputStartTime != nil {
		j.OutputStartTime = *in.OutputStartTime
	}
}

// validateJob rejects a job create/update with missing required fields.
func validateJob(sub, rg, name, location string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "job name is required")
	case location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// cloneJob deep-copies a stored job so callers never alias the backing store.
func cloneJob(j *StreamingJob) StreamingJob {
	out := *j
	out.Tags = maps.Clone(j.Tags)
	out.EventsOutOfOrderMaxDelayInSeconds = cloneIntPtr(j.EventsOutOfOrderMaxDelayInSeconds)
	out.EventsLateArrivalMaxDelayInSeconds = cloneIntPtr(j.EventsLateArrivalMaxDelayInSeconds)

	return out
}

// cloneIntPtr returns a fresh pointer to a copy of *p, or nil, so a cloned job
// never aliases a stored int field.
func cloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}

	v := *p

	return &v
}
