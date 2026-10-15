package logging

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// CreateLog creates a log inside a log group.
func (m *Mock) CreateLog(_ context.Context, groupID string, spec LogSpec) (*Log, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createLog(groupID, spec)
}

// createLog is CreateLog with mu already held.
func (m *Mock) createLog(groupID string, spec LogSpec) (*Log, error) {
	g, ok := m.groups.Get(groupID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", groupID)
	}

	if err := requireName(spec.DisplayName, "log displayName"); err != nil {
		return nil, err
	}

	logType, err := normalizeLogType(spec.LogType)
	if err != nil {
		return nil, err
	}

	if _, taken := m.logByName(groupID, spec.DisplayName); taken {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"log %q already exists in log group %q", spec.DisplayName, groupID)
	}

	cfg, err := normalizeConfiguration(logType, spec.Configuration, g.CompartmentID)
	if err != nil {
		return nil, err
	}

	retention := spec.RetentionDuration
	if retention == 0 {
		retention = g.RetentionDays
	} else if err := validateRetention(retention); err != nil {
		return nil, err
	}

	now := m.now()
	l := Log{
		ID:                m.newOCID(typeLog),
		TenancyID:         m.opts.TenancyOCID,
		LogGroupID:        groupID,
		CompartmentID:     g.CompartmentID,
		DisplayName:       spec.DisplayName,
		LogType:           logType,
		IsEnabled:         spec.IsEnabled,
		RetentionDuration: retention,
		Configuration:     cfg,
		LifecycleState:    StateActive,
		TimeCreated:       now,
		TimeLastModified:  now,
		FreeformTags:      copyTags(spec.FreeformTags),
	}

	m.logs.Set(l.ID, &logRecord{Log: l})

	out := l.clone()

	return &out, nil
}

// normalizeLogType defaults an unset log type to CUSTOM and rejects anything
// OCI does not define.
func normalizeLogType(logType string) (string, error) {
	switch logType {
	case "":
		return LogTypeCustom, nil
	case LogTypeCustom, LogTypeService:
		return logType, nil
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"logType %q is not valid; OCI defines %s and %s", logType, LogTypeCustom, LogTypeService)
	}
}

// normalizeConfiguration validates a log's source against its type. A SERVICE
// log must name the service and resource feeding it; a CUSTOM log takes its
// entries from PutLogs and names no source.
func normalizeConfiguration(logType string, cfg *LogConfiguration, compartmentID string) (*LogConfiguration, error) {
	if logType == LogTypeService {
		if cfg == nil || cfg.Source.Service == "" || cfg.Source.Resource == "" {
			return nil, cerrors.New(cerrors.InvalidArgument,
				"a SERVICE log requires configuration.source with a service and a resource")
		}
	}

	// A CUSTOM log has no service source: it carries a configuration only
	// when the caller supplied one, and never a synthesized source type.
	if cfg == nil {
		return nil, nil //nolint:nilnil // no configuration is a valid CUSTOM log
	}

	out := *cfg
	out.Source.Parameters = copyTags(cfg.Source.Parameters)

	if out.CompartmentID == "" {
		out.CompartmentID = compartmentID
	}

	if logType == LogTypeService && out.Source.SourceType == "" {
		out.Source.SourceType = sourceTypeOCIService
	}

	return &out, nil
}

// GetLog returns a log by OCID within its group.
func (m *Mock) GetLog(_ context.Context, groupID, logID string) (*Log, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, err := m.findLog(groupID, logID)
	if err != nil {
		return nil, err
	}

	out := rec.Log.clone()

	return &out, nil
}

// ListLogs returns the logs in a group. OCI takes no compartmentId here — the
// group determines the compartment — so the group OCID is what is required.
//
//nolint:gocritic // hugeParam: LogFilter mirrors the query parameters and reads better by value.
func (m *Mock) ListLogs(_ context.Context, groupID string, f LogFilter) ([]Log, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.groups.Has(groupID) {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", groupID)
	}

	recs := m.logsIn(groupID)
	out := make([]Log, 0, len(recs))

	for _, rec := range recs {
		if matchesLogFilter(&rec.Log, f) {
			out = append(out, rec.Log.clone())
		}
	}

	return out, nil
}

// matchesLogFilter reports whether a log passes every named filter.
//
//nolint:gocritic // hugeParam: LogFilter reads better by value alongside ListLogs.
func matchesLogFilter(l *Log, f LogFilter) bool {
	if !matchesAll(l.DisplayName, f.DisplayName, l.LogType, f.LogType, l.LifecycleState, f.LifecycleState) {
		return false
	}

	var service, resource string
	if l.Configuration != nil {
		service, resource = l.Configuration.Source.Service, l.Configuration.Source.Resource
	}

	return matchesAll(service, f.SourceService, resource, f.SourceResource)
}

// matchesAll reports whether each value matches its filter, an empty filter
// matching anything. Arguments are read in value, filter pairs.
func matchesAll(pairs ...string) bool {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" && pairs[i] != pairs[i+1] {
			return false
		}
	}

	return true
}

// UpdateLog replaces the mutable fields of a log.
func (m *Mock) UpdateLog(_ context.Context, groupID, logID string, u LogUpdate) (*Log, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, err := m.findLog(groupID, logID)
	if err != nil {
		return nil, err
	}

	// Validate every field before applying any, so a rejected update leaves
	// the log as it was.
	rename, cfg, err := m.validateLogUpdate(rec, groupID, &u)
	if err != nil {
		return nil, err
	}

	if rename {
		rec.Log.DisplayName = *u.DisplayName
	}

	if u.IsEnabled != nil {
		rec.Log.IsEnabled = *u.IsEnabled
	}

	if u.RetentionDuration != nil {
		rec.Log.RetentionDuration = *u.RetentionDuration
	}

	if cfg != nil {
		rec.Log.Configuration = cfg
	}

	if u.FreeformTags != nil {
		rec.Log.FreeformTags = copyTags(u.FreeformTags)
	}

	rec.Log.TimeLastModified = m.now()

	out := rec.Log.clone()

	return &out, nil
}

// validateLogUpdate checks an update against the log it applies to, reporting
// whether it renames the log and the normalized configuration it sets, if any.
// The caller holds mu.
func (m *Mock) validateLogUpdate(
	rec *logRecord, groupID string, u *LogUpdate,
) (rename bool, cfg *LogConfiguration, err error) {
	rename = u.DisplayName != nil && *u.DisplayName != rec.Log.DisplayName
	if rename {
		if _, taken := m.logByName(groupID, *u.DisplayName); taken {
			return false, nil, cerrors.Newf(cerrors.AlreadyExists,
				"log %q already exists in log group %q", *u.DisplayName, groupID)
		}
	}

	if u.RetentionDuration != nil {
		if retErr := validateRetention(*u.RetentionDuration); retErr != nil {
			return false, nil, retErr
		}
	}

	if u.Configuration != nil {
		cfg, err = normalizeConfiguration(rec.Log.LogType, u.Configuration, rec.Log.CompartmentID)
		if err != nil {
			return false, nil, err
		}
	}

	return rename, cfg, nil
}

// MoveLog moves a log, with its entries, into another log group. The log takes
// the target group's compartment.
func (m *Mock) MoveLog(_ context.Context, groupID, logID, targetGroupID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := requireName(targetGroupID, "targetLogGroupId"); err != nil {
		return err
	}

	rec, err := m.findLog(groupID, logID)
	if err != nil {
		return err
	}

	target, ok := m.groups.Get(targetGroupID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "log group %q not found", targetGroupID)
	}

	if targetGroupID == groupID {
		return nil
	}

	if _, taken := m.logByName(targetGroupID, rec.Log.DisplayName); taken {
		return cerrors.Newf(cerrors.AlreadyExists,
			"log %q already exists in log group %q", rec.Log.DisplayName, targetGroupID)
	}

	rec.Log.LogGroupID = targetGroupID
	rec.Log.CompartmentID = target.CompartmentID
	rec.Log.TimeLastModified = m.now()

	if rec.Log.Configuration != nil {
		cfg := *rec.Log.Configuration
		cfg.CompartmentID = target.CompartmentID
		rec.Log.Configuration = &cfg
	}

	return nil
}

// DeleteLog deletes a log and the entries ingested into it.
func (m *Mock) DeleteLog(_ context.Context, groupID, logID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.findLog(groupID, logID); err != nil {
		return err
	}

	m.logs.Delete(logID)

	return nil
}

// findLog resolves a log by OCID and checks it belongs to the named group.
// A log addressed through the wrong group is a 404, as it is in real OCI.
// The caller holds mu.
func (m *Mock) findLog(groupID, logID string) (*logRecord, error) {
	rec, ok := m.logs.Get(logID)
	if !ok || rec.Log.LogGroupID != groupID {
		return nil, cerrors.Newf(cerrors.NotFound, "log %q not found in log group %q", logID, groupID)
	}

	return rec, nil
}

// Retention bounds OCI accepts for a log, in days.
const (
	minRetentionDays  = 30
	maxRetentionDays  = 180
	retentionStepDays = 30
)

// validateRetention rejects a retentionDuration OCI does not accept: 30 to 180
// days, in 30-day steps.
func validateRetention(days int) error {
	if days < minRetentionDays || days > maxRetentionDays || days%retentionStepDays != 0 {
		return cerrors.Newf(cerrors.InvalidArgument,
			"retentionDuration %d is not valid; OCI accepts %d to %d days in steps of %d",
			days, minRetentionDays, maxRetentionDays, retentionStepDays)
	}

	return nil
}
