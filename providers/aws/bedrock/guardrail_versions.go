package bedrock

import (
	"context"
	"strconv"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// guardrailRecord holds a guardrail's mutable working copy (the "DRAFT"
// version) alongside its immutable, numbered version snapshots. Records are
// stored in m.guardrails keyed by the guardrail name.
type guardrailRecord struct {
	draft    *driver.Guardrail
	versions []*driver.Guardrail // numbered snapshots ("1", "2", ...) in creation order
	nextVer  int                 // monotonic next version number; never reused after delete
}

// version returns the snapshot for the given numbered version, or nil.
func (rec *guardrailRecord) version(v string) *driver.Guardrail {
	for _, g := range rec.versions {
		if g.Version == v {
			return g
		}
	}

	return nil
}

// CreateGuardrailVersion snapshots the current DRAFT into a new immutable,
// numbered version and returns the guardrail ID and the assigned version.
func (m *Mock) CreateGuardrailVersion(
	_ context.Context, identifier, description string,
) (guardrailID, version string, err error) {
	rec := m.findGuardrailRecord(identifier)
	if rec == nil {
		return "", "", errors.Newf(errors.NotFound, "guardrail %q not found", identifier)
	}

	ver := strconv.Itoa(rec.nextVer)
	rec.nextVer++

	snapshot := *rec.draft
	snapshot.Version = ver

	if description != "" {
		snapshot.Description = description
	}

	now := m.now()
	snapshot.CreatedAt = now
	snapshot.UpdatedAt = now

	rec.versions = append(rec.versions, &snapshot)

	return snapshot.ID, ver, nil
}
