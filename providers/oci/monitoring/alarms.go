package monitoring

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// CreateOCIAlarm creates a compartment-scoped alarm and returns it.
//
//nolint:gocritic // hugeParam: spec is the alarm's full definition.
func (m *Mock) CreateOCIAlarm(_ context.Context, spec driver.OCIAlarmSpec) (*driver.OCIAlarm, error) {
	if err := validateSpec(&spec); err != nil {
		return nil, err
	}

	// Real OCI allows duplicate display names; the portable driver addresses
	// alarms by name, so this mock requires them unique per compartment.
	if a := m.lookupByName(spec.CompartmentID, spec.DisplayName); a != nil {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "alarm %q already exists in compartment %s",
			spec.DisplayName, spec.CompartmentID)
	}

	now := m.opts.Clock.Now().UTC()
	rec := &alarmRecord{
		id:             idgen.OCID("alarm", m.opts.Realm, m.opts.OCIRegion()),
		place:          scope.Scope{Compartment: spec.CompartmentID},
		spec:           spec,
		status:         StatusOK,
		lifecycleState: lifecycleActive,
		timeCreated:    now,
		timeUpdated:    now,
	}

	m.alarms.Set(rec.id, rec)
	m.evaluateAlarm(rec)

	return m.snapshot(rec), nil
}

// GetOCIAlarm returns the alarm with the given OCID.
func (m *Mock) GetOCIAlarm(_ context.Context, id string) (*driver.OCIAlarm, error) {
	rec, ok := m.alarms.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "alarm %q not found", id)
	}

	return m.snapshot(rec), nil
}

// ListOCIAlarms returns the alarms in a compartment, ordered by OCID.
func (m *Mock) ListOCIAlarms(_ context.Context, compartmentID string) ([]*driver.OCIAlarm, error) {
	if compartmentID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	want := scope.Scope{Compartment: compartmentID}
	out := make([]*driver.OCIAlarm, 0)

	for _, rec := range m.alarms.SortedValues() {
		if !rec.place.Matches(want) {
			continue
		}

		out = append(out, m.snapshot(rec))
	}

	return out, nil
}

// UpdateOCIAlarm replaces an alarm's definition. The compartment is fixed at
// create time, as it is in real OCI.
//
//nolint:gocritic // hugeParam: spec is the alarm's full definition.
func (m *Mock) UpdateOCIAlarm(_ context.Context, id string, spec driver.OCIAlarmSpec) (*driver.OCIAlarm, error) {
	rec, ok := m.alarms.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "alarm %q not found", id)
	}

	spec.CompartmentID = m.specOf(rec).CompartmentID
	if err := validateSpec(&spec); err != nil {
		return nil, err
	}

	m.mu.Lock()
	rec.spec = spec
	rec.timeUpdated = m.opts.Clock.Now().UTC()
	m.mu.Unlock()

	m.evaluateAlarm(rec)

	return m.snapshot(rec), nil
}

// DeleteOCIAlarm deletes the alarm with the given OCID.
func (m *Mock) DeleteOCIAlarm(_ context.Context, id string) error {
	if !m.alarms.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "alarm %q not found", id)
	}

	return nil
}

// OCIAlarmHistory returns an alarm's state transitions, most recent last.
func (m *Mock) OCIAlarmHistory(_ context.Context, id string, limit int) ([]driver.AlarmHistoryEntry, error) {
	rec, ok := m.alarms.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "alarm %q not found", id)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	entries := append([]driver.AlarmHistoryEntry(nil), rec.history...)
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	return entries, nil
}

// evaluateAlarms re-evaluates every alarm watching metrics in a compartment.
func (m *Mock) evaluateAlarms(compartmentID string) {
	for _, rec := range m.alarms.SortedValues() {
		spec := m.specOf(rec)
		if spec.MetricCompartmentID == compartmentID {
			m.evaluate(rec, &spec)
		}
	}
}

// evaluateAlarm re-evaluates one alarm against its current definition.
func (m *Mock) evaluateAlarm(rec *alarmRecord) {
	spec := m.specOf(rec)
	m.evaluate(rec, &spec)
}

// evaluate compares a spec's query against the samples in its window and records
// any status change. The spec is passed in rather than read off rec because
// PostMetricData evaluates outside m.mu, where a concurrent update races it.
func (m *Mock) evaluate(rec *alarmRecord, spec *driver.OCIAlarmSpec) {
	cond, ok := parseQuery(spec.Query)
	if !ok {
		return
	}

	if !spec.IsEnabled {
		m.transition(rec, StatusSuspended, "Alarm is disabled")
		return
	}

	end := m.opts.Clock.Now().UTC()
	filter := driver.OCIMetricFilter{
		Namespace:     spec.Namespace,
		ResourceGroup: spec.ResourceGroup,
		Name:          cond.metricName,
		Dimensions:    cond.dimensions,
	}

	var values []float64

	for _, s := range m.selectSeries(spec.MetricCompartmentID, filter) {
		values = append(values, valuesIn(m.pointsOf(s), end.Add(-cond.interval), end, true)...)
	}

	if len(values) == 0 {
		return
	}

	if compare(computeStat(values, cond.stat), cond.threshold, cond.operator) {
		m.transition(rec, StatusFiring, "Alarm query matched")
		return
	}

	m.transition(rec, StatusOK, "Alarm query did not match")
}

// transition records a status change and its history entry.
func (m *Mock) transition(rec *alarmRecord, status, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec.status == status {
		return
	}

	now := m.opts.Clock.Now().UTC()
	rec.history = append(rec.history, driver.AlarmHistoryEntry{
		AlarmName: rec.spec.DisplayName,
		Timestamp: now,
		OldState:  rec.status,
		NewState:  status,
		Reason:    reason,
	})

	rec.status = status
	rec.timeUpdated = now

	if status == StatusFiring {
		rec.timeTriggered = now
	}
}

// lookupByName returns the alarm with a display name in a compartment.
func (m *Mock) lookupByName(compartmentID, name string) *alarmRecord {
	for _, rec := range m.alarms.SortedValues() {
		if rec.place.Compartment == compartmentID && m.specOf(rec).DisplayName == name {
			return rec
		}
	}

	return nil
}

// specOf copies an alarm's definition out from under the mutation lock.
func (m *Mock) specOf(rec *alarmRecord) driver.OCIAlarmSpec {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return rec.spec
}

// snapshot copies an alarm out from under the mutation lock.
func (m *Mock) snapshot(rec *alarmRecord) *driver.OCIAlarm {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return &driver.OCIAlarm{
		ID:             rec.id,
		Spec:           rec.spec,
		Status:         rec.status,
		LifecycleState: rec.lifecycleState,
		TimeCreated:    rec.timeCreated,
		TimeUpdated:    rec.timeUpdated,
		TimeTriggered:  rec.timeTriggered,
	}
}

// validateSpec rejects an alarm OCI would reject and fills its defaults.
func validateSpec(spec *driver.OCIAlarmSpec) error {
	switch {
	case spec.DisplayName == "":
		return cerrors.New(cerrors.InvalidArgument, "displayName is required")
	case spec.CompartmentID == "":
		return cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	case spec.Query == "":
		return cerrors.New(cerrors.InvalidArgument, "query is required")
	}

	if spec.MetricCompartmentID == "" {
		spec.MetricCompartmentID = spec.CompartmentID
	}

	if spec.Resolution == "" {
		spec.Resolution = defaultResolution
	}

	if spec.Severity == "" {
		spec.Severity = "WARNING"
	}

	if spec.Destinations == nil {
		spec.Destinations = []string{}
	}

	return nil
}

// alarmDuration parses an alarm interval, falling back to the default period.
func alarmDuration(raw string) time.Duration {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}

	return defaultPeriod * time.Second
}
