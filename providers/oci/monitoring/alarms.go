package monitoring

import (
	"context"
	"slices"
	"strconv"
	"strings"
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

	if err := m.insertAlarm(rec); err != nil {
		return nil, err
	}

	m.evaluateAlarm(rec)

	return m.snapshot(rec), nil
}

// insertAlarm stores an alarm unless its compartment already holds its display
// name. Real OCI allows duplicate display names; the portable driver addresses
// alarms by name, so this mock requires them unique per compartment. The scan
// and the insert share one lock, so two concurrent creates cannot both win.
func (m *Mock) insertAlarm(rec *alarmRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkNameFree(rec, rec.spec.DisplayName); err != nil {
		return err
	}

	m.alarms.Set(rec.id, rec)

	return nil
}

// checkNameFree reports a display name an alarm other than rec already holds in
// rec's compartment. The caller holds m.mu, so specs are read directly.
func (m *Mock) checkNameFree(rec *alarmRecord, name string) error {
	for _, other := range m.alarms.SortedValues() {
		if other.id == rec.id || other.place.Compartment != rec.place.Compartment {
			continue
		}

		if other.spec.DisplayName == name {
			return cerrors.Newf(cerrors.AlreadyExists, "alarm %q already exists in compartment %s",
				name, rec.place.Compartment)
		}
	}

	return nil
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

	if err := m.applySpec(rec, &spec); err != nil {
		return nil, err
	}

	m.evaluateAlarm(rec)

	return m.snapshot(rec), nil
}

// applySpec replaces an alarm's definition under one lock, refusing a display
// name another alarm in the compartment holds. That is the rule create
// enforces, so an update cannot make the duplicate create refuses.
func (m *Mock) applySpec(rec *alarmRecord, spec *driver.OCIAlarmSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkNameFree(rec, spec.DisplayName); err != nil {
		return err
	}

	rec.spec = *spec
	rec.timeUpdated = m.opts.Clock.Now().UTC()
	rec.breachSince = time.Time{} // A new condition starts a new pending window.

	return nil
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
	cond, err := parseQuery(spec.Query)
	if err != nil {
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
	}

	var values []float64

	for _, s := range m.selectSeries(spec.MetricCompartmentID, filter, cond.dimensions) {
		values = append(values, valuesIn(m.pointsOf(s), end.Add(-cond.interval), end, true)...)
	}

	if len(values) == 0 {
		return
	}

	if compare(computeStat(values, cond.stat), cond.threshold, cond.operator) {
		if m.breaching(rec, end, pendingOf(spec.PendingDuration)) {
			m.transition(rec, StatusFiring, "Alarm query matched")
		}

		return
	}

	m.clearBreach(rec)
	m.transition(rec, StatusOK, "Alarm query did not match")
}

// breaching records when a breach was first seen and reports whether it has
// since persisted for the alarm's pending duration, which real OCI requires
// before an alarm fires.
func (m *Mock) breaching(rec *alarmRecord, now time.Time, pending time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec.breachSince.IsZero() {
		rec.breachSince = now
	}

	return now.Sub(rec.breachSince) >= pending
}

// clearBreach forgets a breach the alarm has recovered from.
func (m *Mock) clearBreach(rec *alarmRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec.breachSince = time.Time{}
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

	// The query is parsed here rather than only at evaluation time: one evaluate
	// cannot read would otherwise be stored ACTIVE and never fire.
	if _, err := parseQuery(spec.Query); err != nil {
		return err
	}

	applyDefaults(spec)

	return validateFields(spec)
}

// applyDefaults fills the alarm fields OCI supplies when a caller omits them.
func applyDefaults(spec *driver.OCIAlarmSpec) {
	if spec.MetricCompartmentID == "" {
		spec.MetricCompartmentID = spec.CompartmentID
	}

	if spec.Resolution == "" {
		spec.Resolution = defaultResolution
	}

	if spec.Severity == "" {
		spec.Severity = severityWarning
	}

	if spec.Destinations == nil {
		spec.Destinations = []string{}
	}
}

// validateFields checks the alarm values OCI constrains to an enum or a format.
func validateFields(spec *driver.OCIAlarmSpec) error {
	if !slices.Contains(severities(), spec.Severity) {
		return cerrors.Newf(cerrors.InvalidArgument, "severity %q must be one of %s",
			spec.Severity, strings.Join(severities(), ", "))
	}

	if _, ok := parsePending(spec.PendingDuration); !ok {
		return cerrors.Newf(cerrors.InvalidArgument,
			"pendingDuration %q is not an ISO-8601 duration such as PT5M", spec.PendingDuration)
	}

	if step, err := time.ParseDuration(spec.Resolution); err != nil || step < minResolution {
		return cerrors.Newf(cerrors.InvalidArgument, "resolution %q must be an interval of at least %s",
			spec.Resolution, resolutionLabel(minResolution))
	}

	return nil
}

// severities lists the alarm severities OCI accepts, most severe first.
func severities() []string {
	return []string{severityCritical, severityError, severityWarning, severityInfo}
}

// alarmDuration parses an alarm interval, falling back to the default period.
func alarmDuration(raw string) time.Duration {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}

	return defaultPeriod * time.Second
}

// pendingOf reads OCI's pendingDuration, an ISO-8601 duration such as PT5M. An
// absent one fires on the first breach: this mock evaluates on PostMetricData
// rather than on a timer, so OCI's PT1M default would never elapse on its own.
// An unreadable one never reaches here; validateFields rejects it at create.
func pendingOf(raw string) time.Duration {
	pending, _ := parsePending(raw)

	return pending
}

// parsePending parses an ISO-8601 duration, reporting whether it is one. An
// empty duration is no duration, which the field permits.
func parsePending(raw string) (pending time.Duration, ok bool) {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	if trimmed == "" {
		return 0, true
	}

	body, found := strings.CutPrefix(trimmed, "P")
	if !found || !strings.ContainsAny(body, "0123456789") {
		return 0, false
	}

	date, clock, _ := strings.Cut(body, "T")

	days, ok := isoParts(date, map[byte]time.Duration{'D': 24 * time.Hour})
	if !ok {
		return 0, false
	}

	rest, ok := isoParts(clock, map[byte]time.Duration{'H': time.Hour, 'M': time.Minute, 'S': time.Second})
	if !ok {
		return 0, false
	}

	return days + rest, true
}

// isoParts sums the <number><unit> pairs of one half of an ISO-8601 duration.
func isoParts(s string, units map[byte]time.Duration) (total time.Duration, ok bool) {
	digits := 0

	for i := range len(s) {
		if s[i] >= '0' && s[i] <= '9' {
			digits++
			continue
		}

		unit, known := units[s[i]]
		if !known || digits == 0 {
			return 0, false
		}

		n, err := strconv.Atoi(s[i-digits : i])
		if err != nil {
			return 0, false
		}

		total += time.Duration(n) * unit
		digits = 0
	}

	return total, digits == 0
}
