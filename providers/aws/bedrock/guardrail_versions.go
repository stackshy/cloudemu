package bedrock

import (
	"context"
	"strconv"
	"sync"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// guardrailRecord holds a guardrail's mutable working copy (the "DRAFT"
// version) alongside its immutable, numbered version snapshots. Records are
// stored in m.guardrails keyed by the guardrail name.
//
// mu guards draft, versions and nextVer. memstore only serializes access to
// its map, not to the record it points at, so every reader and mutator of a
// *guardrailRecord must hold mu. The record therefore contains a sync.RWMutex
// and MUST NOT be copied by value — always pass and store it as a pointer.
type guardrailRecord struct {
	mu       sync.RWMutex
	draft    *driver.Guardrail
	versions []*driver.Guardrail // numbered snapshots ("1", "2", ...) in creation order
	nextVer  int                 // monotonic next version number; never reused after delete
}

// version returns the snapshot for the given numbered version, or nil. Callers
// must hold rec.mu (read or write); it performs no locking of its own.
func (rec *guardrailRecord) version(v string) *driver.Guardrail {
	for _, g := range rec.versions {
		if g.Version == v {
			return g
		}
	}

	return nil
}

// draftSnapshot returns a deep, aliasing-free copy of the DRAFT working copy.
func (rec *guardrailRecord) draftSnapshot() driver.Guardrail {
	rec.mu.RLock()
	defer rec.mu.RUnlock()

	return cloneGuardrail(rec.draft)
}

// versionSnapshot returns a deep copy of the given numbered version and true,
// or the zero value and false if no such version exists.
func (rec *guardrailRecord) versionSnapshot(v string) (driver.Guardrail, bool) {
	rec.mu.RLock()
	defer rec.mu.RUnlock()

	g := rec.version(v)
	if g == nil {
		return driver.Guardrail{}, false
	}

	return cloneGuardrail(g), true
}

// allSnapshots returns deep copies of the DRAFT plus every numbered version.
func (rec *guardrailRecord) allSnapshots() []driver.Guardrail {
	rec.mu.RLock()
	defer rec.mu.RUnlock()

	out := make([]driver.Guardrail, 0, len(rec.versions)+1)
	out = append(out, cloneGuardrail(rec.draft))

	for _, v := range rec.versions {
		out = append(out, cloneGuardrail(v))
	}

	return out
}

// cloneGuardrail returns a value copy of g with its policy object graph
// deep-copied, so the result shares no mutable state with the stored record.
func cloneGuardrail(g *driver.Guardrail) driver.Guardrail {
	result := *g
	result.GuardrailPolicies = deepCopyGuardrailPolicies(g.GuardrailPolicies)

	return result
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

	rec.mu.Lock()
	defer rec.mu.Unlock()

	ver := strconv.Itoa(rec.nextVer)
	rec.nextVer++

	// Deep-copy the draft so the snapshot is immutable against later draft
	// edits (UpdateGuardrail mutates the same *draft in place).
	snapshot := cloneGuardrail(rec.draft)
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
