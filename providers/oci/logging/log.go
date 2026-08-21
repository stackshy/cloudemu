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
	}

	now := m.now()
	l := Log{
		ID:                m.newOCID(typeLog),
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

	m.logs.Set(l.ID, &logRecord{log: l})

	out := l

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

	if cfg == nil {
		return &LogConfiguration{
			CompartmentID: compartmentID,
			Source:        LogSource{SourceType: sourceTypeOCIService},
		}, nil
	}

	out := *cfg
	out.Source.Parameters = copyTags(cfg.Source.Parameters)

	if out.CompartmentID == "" {
		out.CompartmentID = compartmentID
	}

	if out.Source.SourceType == "" {
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

	out := rec.log

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
		if matchesLogFilter(&rec.log, f) {
			out = append(out, rec.log)
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

	if u.DisplayName != nil && *u.DisplayName != rec.log.DisplayName {
		if _, taken := m.logByName(groupID, *u.DisplayName); taken {
			return nil, cerrors.Newf(cerrors.AlreadyExists,
				"log %q already exists in log group %q", *u.DisplayName, groupID)
		}

		rec.log.DisplayName = *u.DisplayName
	}

	if u.IsEnabled != nil {
		rec.log.IsEnabled = *u.IsEnabled
	}

	if u.RetentionDuration != nil {
		rec.log.RetentionDuration = *u.RetentionDuration
	}

	if u.Configuration != nil {
		cfg, cfgErr := normalizeConfiguration(rec.log.LogType, u.Configuration, rec.log.CompartmentID)
		if cfgErr != nil {
			return nil, cfgErr
		}

		rec.log.Configuration = cfg
	}

	if u.FreeformTags != nil {
		rec.log.FreeformTags = copyTags(u.FreeformTags)
	}

	rec.log.TimeLastModified = m.now()

	out := rec.log

	return &out, nil
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
	if !ok || rec.log.LogGroupID != groupID {
		return nil, cerrors.Newf(cerrors.NotFound, "log %q not found in log group %q", logID, groupID)
	}

	return rec, nil
}
