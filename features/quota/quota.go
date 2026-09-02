// Package quota provides a provider-agnostic registry of cloud service quotas
// (a.k.a. service limits): the per service-code + quota-code default value, an
// optional applied override, and a history of requested increases.
//
// It is the backing store for CloudEmu's AWS Service Quotas wire handler
// (server/aws/servicequotas) and can be consulted directly by Go callers. The
// registry only models the quota values and increase-request lifecycle;
// enforcing a quota against live resource usage (blocking a create once a limit
// is reached) is a separate, cross-service concern that this package does not
// implement.
package quota

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Status values for a requested quota increase, mirroring AWS Service Quotas.
const (
	// StatusPending is the initial status of a newly requested increase.
	StatusPending = "PENDING"
	// StatusCaseOpened indicates a support case was opened for the request.
	StatusCaseOpened = "CASE_OPENED"
	// StatusApproved indicates the requested increase was approved.
	StatusApproved = "APPROVED"
	// StatusDenied indicates the requested increase was denied.
	StatusDenied = "DENIED"
)

// Quota describes a single service quota: its identity, its AWS default value,
// and the currently applied value (the default unless an override is set).
type Quota struct {
	ServiceCode  string
	ServiceName  string
	QuotaCode    string
	QuotaName    string
	Unit         string
	DefaultValue float64
	Value        float64 // applied value; equals DefaultValue until overridden
	Adjustable   bool
	GlobalQuota  bool
}

// ChangeRequest records a requested quota increase and its lifecycle.
type ChangeRequest struct {
	ID           string
	ServiceCode  string
	ServiceName  string
	QuotaCode    string
	QuotaName    string
	Unit         string
	DesiredValue float64
	Status       string
	GlobalQuota  bool
	Created      time.Time
	LastUpdated  time.Time
}

// Registry is a thread-safe store of quotas and increase requests.
type Registry struct {
	mu      sync.RWMutex
	quotas  map[string]*Quota // keyed by serviceCode + "\x00" + quotaCode
	history []ChangeRequest   // newest first
	seq     int
	clock   config.Clock
}

// New returns an empty registry. Callers seed it with Set, or use NewAWSDefaults
// for the built-in AWS quota set.
func New(clock config.Clock) *Registry {
	if clock == nil {
		clock = config.RealClock{}
	}

	return &Registry{quotas: make(map[string]*Quota), clock: clock}
}

func key(serviceCode, quotaCode string) string {
	return serviceCode + "\x00" + quotaCode
}

// Set inserts or replaces a quota. The applied Value is initialized to
// DefaultValue when it is zero (i.e. not explicitly set by the caller).
func (r *Registry) Set(q *Quota) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored := *q
	if stored.Value == 0 {
		stored.Value = stored.DefaultValue
	}

	r.quotas[key(stored.ServiceCode, stored.QuotaCode)] = &stored
}

// Get returns the applied quota for a service-code + quota-code.
func (r *Registry) Get(serviceCode, quotaCode string) (Quota, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	q, ok := r.quotas[key(serviceCode, quotaCode)]
	if !ok {
		return Quota{}, notFound(serviceCode, quotaCode)
	}

	return *q, nil
}

// Default returns the AWS default quota (ignoring any applied override).
func (r *Registry) Default(serviceCode, quotaCode string) (Quota, error) {
	q, err := r.Get(serviceCode, quotaCode)
	if err != nil {
		return Quota{}, err
	}

	q.Value = q.DefaultValue

	return q, nil
}

// List returns every applied quota for serviceCode, sorted by quota code. An
// empty serviceCode lists all quotas across every service.
func (r *Registry) List(serviceCode string) []Quota {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Quota, 0, len(r.quotas))

	for _, q := range r.quotas {
		if serviceCode == "" || q.ServiceCode == serviceCode {
			out = append(out, *q)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].ServiceCode != out[j].ServiceCode {
			return out[i].ServiceCode < out[j].ServiceCode
		}

		return out[i].QuotaCode < out[j].QuotaCode
	})

	return out
}

// SetOverride sets the applied value of an existing quota, modeling an approved
// quota change. It fails if the quota is unknown or not adjustable.
func (r *Registry) SetOverride(serviceCode, quotaCode string, value float64) (Quota, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	q, ok := r.quotas[key(serviceCode, quotaCode)]
	if !ok {
		return Quota{}, notFound(serviceCode, quotaCode)
	}

	if !q.Adjustable {
		return Quota{}, cerrors.Newf(cerrors.InvalidArgument,
			"quota %s/%s is not adjustable", serviceCode, quotaCode)
	}

	q.Value = value

	return *q, nil
}

// RequestIncrease records a requested quota increase and returns it. The desired
// value must exceed the currently applied value, matching AWS behavior.
func (r *Registry) RequestIncrease(serviceCode, quotaCode string, desired float64) (ChangeRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	q, ok := r.quotas[key(serviceCode, quotaCode)]
	if !ok {
		return ChangeRequest{}, notFound(serviceCode, quotaCode)
	}

	if !q.Adjustable {
		return ChangeRequest{}, cerrors.Newf(cerrors.InvalidArgument,
			"quota %s/%s is not adjustable", serviceCode, quotaCode)
	}

	if desired <= q.Value {
		return ChangeRequest{}, cerrors.Newf(cerrors.InvalidArgument,
			"desired value %v must exceed the applied quota value %v", desired, q.Value)
	}

	now := r.clock.Now().UTC()
	r.seq++

	cr := ChangeRequest{
		ID:           strconv.Itoa(r.seq),
		ServiceCode:  q.ServiceCode,
		ServiceName:  q.ServiceName,
		QuotaCode:    q.QuotaCode,
		QuotaName:    q.QuotaName,
		Unit:         q.Unit,
		DesiredValue: desired,
		Status:       StatusPending,
		GlobalQuota:  q.GlobalQuota,
		Created:      now,
		LastUpdated:  now,
	}

	// Newest first, matching ListRequestedServiceQuotaChangeHistory ordering.
	r.history = append([]ChangeRequest{cr}, r.history...)

	return cr, nil
}

// History returns the requested quota changes, newest first. A non-empty
// serviceCode filters to that service; a non-empty quotaCode filters further.
func (r *Registry) History(serviceCode, quotaCode string) []ChangeRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ChangeRequest, 0, len(r.history))

	for i := range r.history {
		cr := &r.history[i]

		if serviceCode != "" && cr.ServiceCode != serviceCode {
			continue
		}

		if quotaCode != "" && cr.QuotaCode != quotaCode {
			continue
		}

		out = append(out, *cr)
	}

	return out
}

func notFound(serviceCode, quotaCode string) error {
	return cerrors.Newf(cerrors.NotFound, "no quota %s for service %s", quotaCode, serviceCode)
}
