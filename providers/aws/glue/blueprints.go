package glue

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// blueprintData is a blueprint plus its own lock.
type blueprintData struct {
	blueprint driver.Blueprint
	mu        sync.RWMutex
}

// blueprintRunData is a single blueprint run plus its own lock, keyed
// "<blueprintName>/<runID>".
type blueprintRunData struct {
	run driver.BlueprintRun
	mu  sync.RWMutex
}

// CreateBlueprint creates a blueprint, atomically, returning its name.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateBlueprint(_ context.Context, b driver.Blueprint) (string, error) {
	if !validName(b.Name) {
		return "", invalidInput("blueprint name %q is invalid", b.Name)
	}

	now := m.now()
	b.CreatedOn = now
	b.LastModifiedOn = now
	b.Status = driver.SchemaStatusAvailable
	stored := b

	if !m.blueprints.SetIfAbsent(b.Name, &blueprintData{blueprint: stored}) {
		return "", alreadyExists("Blueprint already exists: %s", b.Name)
	}

	return b.Name, nil
}

func (m *Mock) getBlueprintData(name string) (*blueprintData, error) {
	if !validName(name) {
		return nil, invalidInput("blueprint name %q is invalid", name)
	}

	bd, ok := m.blueprints.Get(name)
	if !ok {
		return nil, entityNotFound("Blueprint not found: %s", name)
	}

	return bd, nil
}

// GetBlueprint returns a copy of a blueprint.
func (m *Mock) GetBlueprint(_ context.Context, name string) (*driver.Blueprint, error) {
	bd, err := m.getBlueprintData(name)
	if err != nil {
		return nil, err
	}

	bd.mu.RLock()
	defer bd.mu.RUnlock()

	out := bd.blueprint

	return &out, nil
}

// UpdateBlueprint replaces a blueprint's mutable fields, returning its name.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateBlueprint(_ context.Context, name string, b driver.Blueprint) (string, error) {
	bd, err := m.getBlueprintData(name)
	if err != nil {
		return "", err
	}

	bd.mu.Lock()
	defer bd.mu.Unlock()

	created := bd.blueprint.CreatedOn
	bd.blueprint = b
	bd.blueprint.Name = name
	bd.blueprint.CreatedOn = created
	bd.blueprint.LastModifiedOn = m.now()
	bd.blueprint.Status = driver.SchemaStatusAvailable

	return name, nil
}

// DeleteBlueprint removes a blueprint, returning its name.
func (m *Mock) DeleteBlueprint(_ context.Context, name string) (string, error) {
	if _, err := m.getBlueprintData(name); err != nil {
		return "", err
	}

	m.blueprints.Delete(name)

	return name, nil
}

// ListBlueprints returns blueprint names with pagination.
//
//nolint:gocritic // unnamedResult: thin pass-through to paginate; names add no clarity
func (m *Mock) ListBlueprints(_ context.Context, page driver.TablePagination) ([]string, string, error) {
	return paginate(sortedKeys(m.blueprints.Keys()), page)
}

// BatchGetBlueprints returns the found blueprints and the missing names.
//
//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (m *Mock) BatchGetBlueprints(_ context.Context, names []string) ([]driver.Blueprint, []string, error) {
	if len(names) > maxBatchGet {
		return nil, nil, invalidInput("cannot request more than %d blueprints", maxBatchGet)
	}

	found := make([]driver.Blueprint, 0, len(names))

	var notFound []string

	for _, n := range names {
		b, err := m.GetBlueprint(context.Background(), n)
		if err != nil {
			notFound = append(notFound, n)

			continue
		}

		found = append(found, *b)
	}

	return found, notFound, nil
}

// StartBlueprintRun starts a blueprint run that completes SUCCEEDED
// synchronously (no real workflow provisioning). Returns the run ID.
func (m *Mock) StartBlueprintRun(_ context.Context, name, roleARN, parameters string) (string, error) {
	if _, err := m.getBlueprintData(name); err != nil {
		return "", err
	}

	now := m.now()
	runID := idgen.GenerateID("br_")
	run := driver.BlueprintRun{
		RunID:         runID,
		BlueprintName: name,
		State:         driver.BlueprintRunSucceeded,
		StartedOn:     now,
		CompletedOn:   now,
		Parameters:    parameters,
		RoleARN:       roleARN,
	}

	m.blueprintRuns.Set(nameKey(name, runID), &blueprintRunData{run: run})

	return runID, nil
}

// GetBlueprintRun returns a copy of a blueprint run.
func (m *Mock) GetBlueprintRun(_ context.Context, name, runID string) (*driver.BlueprintRun, error) {
	rd, ok := m.blueprintRuns.Get(nameKey(name, runID))
	if !ok {
		return nil, entityNotFound("BlueprintRun not found: %s", runID)
	}

	rd.mu.RLock()
	defer rd.mu.RUnlock()

	out := rd.run

	return &out, nil
}

// GetBlueprintRuns lists a blueprint's runs with pagination.
func (m *Mock) GetBlueprintRuns(
	_ context.Context, name string, page driver.TablePagination,
) ([]driver.BlueprintRun, string, error) {
	if _, err := m.getBlueprintData(name); err != nil {
		return nil, "", err
	}

	prefix := name + keySep
	keys := sortedKeys(m.blueprintRuns.Keys())
	all := make([]driver.BlueprintRun, 0, len(keys))

	for _, key := range keys {
		if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
			continue
		}

		rd, ok := m.blueprintRuns.Get(key)
		if !ok {
			continue
		}

		rd.mu.RLock()
		all = append(all, rd.run)
		rd.mu.RUnlock()
	}

	return paginate(all, page)
}
