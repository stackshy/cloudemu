package logging

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// PutLogs ingests batches of entries into a custom log — the loggingingestion
// data plane. A SERVICE log is fed by the service that owns it, so ingesting
// into one is refused rather than silently accepted.
func (m *Mock) PutLogs(ctx context.Context, logID string, batches []LogEntryBatch) error {
	m.mu.Lock()

	rec, ok := m.logs.Get(logID)
	if !ok {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.NotFound, "log %q not found", logID)
	}

	if rec.log.LogType != LogTypeCustom {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.InvalidArgument,
			"log %q is a %s log; only a %s log accepts PutLogs", logID, rec.log.LogType, LogTypeCustom)
	}

	if !rec.log.IsEnabled {
		m.mu.Unlock()
		return cerrors.Newf(cerrors.FailedPrecondition, "log %q is disabled and accepts no entries", logID)
	}

	count, bytes := m.ingest(rec, batches)

	compartmentID, groupID := rec.log.CompartmentID, rec.log.LogGroupID
	mon := m.monitoring
	m.mu.Unlock()

	dims := map[string]string{"logId": logID, "logGroupId": groupID, "compartmentId": compartmentID}
	m.emitMetric(ctx, mon, "IngestedLogEntries", float64(count), dims)
	m.emitMetric(ctx, mon, "IngestedLogBytes", float64(bytes), dims)

	return nil
}

// ingest appends every batch's entries to a log, returning how many entries
// and payload bytes landed. The caller holds mu.
func (m *Mock) ingest(rec *logRecord, batches []LogEntryBatch) (count, bytes int) {
	ingested := m.opts.Clock.Now().UTC()

	for i := range batches {
		batch := &batches[i]

		for j := range batch.Entries {
			entry := buildEntry(rec.log.ID, batch, &batch.Entries[j], ingested)
			rec.entries = append(rec.entries, entry)
			count++
			bytes += len(entry.Data)
		}
	}

	return count, bytes
}

// buildEntry stamps one ingested entry, filling in the batch defaults an
// entry leaves unset.
func buildEntry(logID string, batch *LogEntryBatch, item *LogEntryItem, ingested time.Time) LogEntry {
	id := item.ID
	if id == "" {
		id = idgen.GenerateID("logentry")
	}

	when := item.Time
	if when.IsZero() {
		when = batch.DefaultLogEntryTime
	}

	if when.IsZero() {
		when = ingested
	}

	return LogEntry{
		ID:           id,
		LogID:        logID,
		Time:         when.UTC(),
		IngestedTime: ingested,
		Data:         item.Data,
		Source:       batch.Source,
		Subject:      batch.Subject,
		Type:         batch.Type,
	}
}

// Entries returns the entries ingested into a log, ordered as they arrived.
func (m *Mock) Entries(_ context.Context, logID string) ([]LogEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, ok := m.logs.Get(logID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "log %q not found", logID)
	}

	out := make([]LogEntry, len(rec.entries))
	copy(out, rec.entries)

	return out, nil
}
