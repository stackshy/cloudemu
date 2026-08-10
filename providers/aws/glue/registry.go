package glue

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// defaultRegistryName is the registry Glue uses when a schema call omits one.
const defaultRegistryName = "default-registry"

// parseVersionNumbers parses a comma-separated list of positive version numbers,
// each token being either a single number ("2") or an inclusive range ("1-3"),
// matching Glue's DeleteSchemaVersions "Versions" grammar.
func parseVersionNumbers(spec string) ([]int64, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}

	parts := strings.Split(spec, ",")
	out := make([]int64, 0, len(parts))

	for _, part := range parts {
		nums, err := parseVersionToken(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}

		out = append(out, nums...)
	}

	return out, nil
}

// parseVersionToken parses a single "N" or inclusive "LO-HI" range token.
func parseVersionToken(token string) ([]int64, error) {
	lo, hi, isRange := strings.Cut(token, "-")
	if !isRange {
		n, err := strconv.ParseInt(token, 10, 64)
		if err != nil || n <= 0 {
			return nil, invalidInput("invalid schema version number %q", token)
		}

		return []int64{n}, nil
	}

	start, err1 := strconv.ParseInt(strings.TrimSpace(lo), 10, 64)
	end, err2 := strconv.ParseInt(strings.TrimSpace(hi), 10, 64)

	if err1 != nil || err2 != nil || start <= 0 || end < start {
		return nil, invalidInput("invalid schema version range %q", token)
	}

	nums := make([]int64, 0, end-start+1)
	for n := start; n <= end; n++ {
		nums = append(nums, n)
	}

	return nums, nil
}

// registryData is a schema registry plus its own lock.
type registryData struct {
	registry driver.Registry
	mu       sync.RWMutex
}

// schemaData is a schema plus its version history and its own lock. Schemas are
// keyed "<registryName>/<schemaName>".
type schemaData struct {
	schema   driver.Schema
	versions []driver.SchemaVersion
	mu       sync.RWMutex
}

// CreateRegistry creates a schema registry, atomically.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateRegistry(_ context.Context, r driver.Registry) (*driver.Registry, error) {
	if !validName(r.Name) {
		return nil, invalidInput("registry name %q is invalid", r.Name)
	}

	now := m.now()
	r.ARN = m.arn("registry/" + r.Name)
	r.Status = driver.RegistryAvailable
	r.CreatedTime = now
	r.UpdatedTime = now

	if !m.registries.SetIfAbsent(r.Name, &registryData{registry: r}) {
		return nil, alreadyExists("Registry already exists: %s", r.Name)
	}

	out := r

	return &out, nil
}

func (m *Mock) getRegistryData(name string) (*registryData, error) {
	if !validName(name) {
		return nil, invalidInput("registry name %q is invalid", name)
	}

	rd, ok := m.registries.Get(name)
	if !ok {
		return nil, entityNotFound("Registry not found: %s", name)
	}

	return rd, nil
}

// GetRegistry returns a copy of a registry.
func (m *Mock) GetRegistry(_ context.Context, name string) (*driver.Registry, error) {
	rd, err := m.getRegistryData(name)
	if err != nil {
		return nil, err
	}

	rd.mu.RLock()
	defer rd.mu.RUnlock()

	out := rd.registry

	return &out, nil
}

// UpdateRegistry updates a registry's description.
func (m *Mock) UpdateRegistry(_ context.Context, name, description string) (*driver.Registry, error) {
	rd, err := m.getRegistryData(name)
	if err != nil {
		return nil, err
	}

	rd.mu.Lock()
	defer rd.mu.Unlock()

	rd.registry.Description = description
	rd.registry.UpdatedTime = m.now()
	out := rd.registry

	return &out, nil
}

// DeleteRegistry removes a registry and every schema it contains.
func (m *Mock) DeleteRegistry(_ context.Context, name string) (*driver.Registry, error) {
	rd, err := m.getRegistryData(name)
	if err != nil {
		return nil, err
	}

	rd.mu.RLock()
	out := rd.registry
	rd.mu.RUnlock()

	m.registries.Delete(name)

	prefix := name + keySep
	for _, key := range m.schemas.Keys() {
		if strings.HasPrefix(key, prefix) {
			m.schemas.Delete(key)
		}
	}

	return &out, nil
}

// ListRegistries lists registries with pagination.
func (m *Mock) ListRegistries(
	_ context.Context, page driver.TablePagination,
) ([]driver.Registry, string, error) {
	keys := sortedKeys(m.registries.Keys())
	all := make([]driver.Registry, 0, len(keys))

	for _, key := range keys {
		rd, ok := m.registries.Get(key)
		if !ok {
			continue
		}

		rd.mu.RLock()
		all = append(all, rd.registry)
		rd.mu.RUnlock()
	}

	return paginate(all, page)
}

// CreateSchema creates a schema in a registry and registers its first version.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateSchema(_ context.Context, s driver.Schema, initialDefinition string) (*driver.Schema, error) {
	if !validName(s.Name) {
		return nil, invalidInput("schema name %q is invalid", s.Name)
	}

	registry := s.RegistryName
	if registry == "" {
		registry = defaultRegistryName
	}

	s.RegistryName = registry
	now := m.now()
	s.ARN = m.arn("schema/" + registry + "/" + s.Name)
	s.Status = driver.SchemaStatusAvailable
	s.CreatedTime = now
	s.UpdatedTime = now

	sd := &schemaData{schema: s}

	if initialDefinition != "" {
		s.LatestVersion = 1
		s.NextVersion = 2
		sd.schema = s
		sd.versions = []driver.SchemaVersion{{
			SchemaName: s.Name, RegistryName: registry, VersionID: idgen.GenerateID("sv_"),
			VersionNumber: 1, Definition: initialDefinition, Status: driver.SchemaVersionAvailable, CreatedTime: now,
		}}
	}

	if !m.schemas.SetIfAbsent(nameKey(registry, s.Name), sd) {
		return nil, alreadyExists("Schema already exists: %s", s.Name)
	}

	out := sd.schema

	return &out, nil
}

func (m *Mock) getSchemaData(registryName, schemaName string) (*schemaData, error) {
	if !validName(schemaName) {
		return nil, invalidInput("schema name %q is invalid", schemaName)
	}

	if registryName == "" {
		registryName = defaultRegistryName
	}

	sd, ok := m.schemas.Get(nameKey(registryName, schemaName))
	if !ok {
		return nil, entityNotFound("Schema not found: %s", schemaName)
	}

	return sd, nil
}

// GetSchema returns a copy of a schema.
func (m *Mock) GetSchema(_ context.Context, registryName, schemaName string) (*driver.Schema, error) {
	sd, err := m.getSchemaData(registryName, schemaName)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := sd.schema

	return &out, nil
}

// UpdateSchema updates a schema's compatibility and/or description.
func (m *Mock) UpdateSchema(
	_ context.Context, registryName, schemaName, compatibility, description string,
) (*driver.Schema, error) {
	sd, err := m.getSchemaData(registryName, schemaName)
	if err != nil {
		return nil, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if compatibility != "" {
		sd.schema.Compatibility = compatibility
	}

	if description != "" {
		sd.schema.Description = description
	}

	sd.schema.UpdatedTime = m.now()
	out := sd.schema

	return &out, nil
}

// DeleteSchema removes a schema and its versions.
func (m *Mock) DeleteSchema(_ context.Context, registryName, schemaName string) (*driver.Schema, error) {
	sd, err := m.getSchemaData(registryName, schemaName)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	out := sd.schema
	sd.mu.RUnlock()

	if registryName == "" {
		registryName = defaultRegistryName
	}

	m.schemas.Delete(nameKey(registryName, schemaName))

	return &out, nil
}

// ListSchemas lists a registry's schemas with pagination.
func (m *Mock) ListSchemas(
	_ context.Context, registryName string, page driver.TablePagination,
) ([]driver.Schema, string, error) {
	prefix := ""
	if registryName != "" {
		prefix = registryName + keySep
	}

	keys := sortedKeys(m.schemas.Keys())
	all := make([]driver.Schema, 0, len(keys))

	for _, key := range keys {
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}

		sd, ok := m.schemas.Get(key)
		if !ok {
			continue
		}

		sd.mu.RLock()
		all = append(all, sd.schema)
		sd.mu.RUnlock()
	}

	return paginate(all, page)
}

// RegisterSchemaVersion appends a new version to a schema.
func (m *Mock) RegisterSchemaVersion(
	_ context.Context, registryName, schemaName, definition string,
) (*driver.SchemaVersion, error) {
	sd, err := m.getSchemaData(registryName, schemaName)
	if err != nil {
		return nil, err
	}

	if definition == "" {
		return nil, invalidInput("schema definition must not be empty")
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	// Dedup: an identical definition returns the existing version rather than
	// creating a duplicate, matching real Glue's RegisterSchemaVersion.
	for i := range sd.versions {
		if sd.versions[i].Definition == definition {
			out := sd.versions[i]

			return &out, nil
		}
	}

	// Compatibility gate. The emulator applies a pragmatic check rather than
	// parsing schema grammars: DISABLED rejects any new (non-identical) version;
	// NONE (or unset) always accepts. Other modes accept, since a structural
	// diff is out of scope (documented in docs/services.md).
	if strings.EqualFold(sd.schema.Compatibility, "DISABLED") {
		return nil, invalidInput(
			"schema %s has compatibility DISABLED; new versions are not allowed", schemaName,
		)
	}

	next := sd.schema.NextVersion
	if next == 0 {
		next = 1
	}

	ver := driver.SchemaVersion{
		SchemaName: sd.schema.Name, RegistryName: sd.schema.RegistryName, VersionID: idgen.GenerateID("sv_"),
		VersionNumber: next, Definition: definition, Status: driver.SchemaVersionAvailable, CreatedTime: m.now(),
	}
	sd.versions = append(sd.versions, ver)
	sd.schema.LatestVersion = next
	sd.schema.NextVersion = next + 1

	out := ver

	return &out, nil
}

// GetSchemaVersion returns a version by ID or by number ("" versionID + 0
// versionNumber selects the latest).
func (m *Mock) GetSchemaVersion(
	_ context.Context, registryName, schemaName, versionID string, versionNumber int64,
) (*driver.SchemaVersion, error) {
	sd, err := m.getSchemaData(registryName, schemaName)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	if len(sd.versions) == 0 {
		return nil, schemaVersionNotFound("no schema versions for %s", schemaName)
	}

	for i := range sd.versions {
		v := sd.versions[i]
		if (versionID != "" && v.VersionID == versionID) || (versionNumber != 0 && v.VersionNumber == versionNumber) {
			out := v

			return &out, nil
		}
	}

	if versionID == "" && versionNumber == 0 {
		out := sd.versions[len(sd.versions)-1]

		return &out, nil
	}

	return nil, schemaVersionNotFound("schema version not found for %s", schemaName)
}

// GetSchemaByDefinition returns the version whose definition matches exactly.
func (m *Mock) GetSchemaByDefinition(
	_ context.Context, registryName, schemaName, definition string,
) (*driver.SchemaVersion, error) {
	sd, err := m.getSchemaData(registryName, schemaName)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	for i := range sd.versions {
		if sd.versions[i].Definition == definition {
			out := sd.versions[i]

			return &out, nil
		}
	}

	return nil, entityNotFound("no schema version matches the given definition")
}

// ListSchemaVersions lists a schema's versions with pagination.
func (m *Mock) ListSchemaVersions(
	_ context.Context, registryName, schemaName string, page driver.TablePagination,
) ([]driver.SchemaVersion, string, error) {
	sd, err := m.getSchemaData(registryName, schemaName)
	if err != nil {
		return nil, "", err
	}

	sd.mu.RLock()
	all := append([]driver.SchemaVersion(nil), sd.versions...)
	sd.mu.RUnlock()

	return paginate(all, page)
}

// DeleteSchemaVersions deletes versions given a comma-separated numeric spec.
// Version numbers are validated before any delete so a bad token can't leave a
// partial delete committed.
func (m *Mock) DeleteSchemaVersions(
	_ context.Context, registryName, schemaName, versions string,
) ([]driver.BatchError, error) {
	sd, err := m.getSchemaData(registryName, schemaName)
	if err != nil {
		return nil, err
	}

	wanted, err := parseVersionNumbers(versions)
	if err != nil {
		return nil, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	present := map[int64]bool{}
	for i := range sd.versions {
		present[sd.versions[i].VersionNumber] = true
	}

	del := map[int64]bool{}

	var errs []driver.BatchError

	// Report any requested version number that the schema doesn't have.
	for _, n := range wanted {
		if !present[n] {
			errs = append(errs, driver.BatchError{
				Values:       []string{strconv.FormatInt(n, 10)},
				ErrorCode:    driver.ExEntityNotFound,
				ErrorMessage: entityNotFound("schema version %d not found for %s", n, schemaName).Error(),
			})

			continue
		}

		del[n] = true
	}

	kept := sd.versions[:0]

	for i := range sd.versions {
		if del[sd.versions[i].VersionNumber] {
			continue
		}

		kept = append(kept, sd.versions[i])
	}

	sd.versions = kept

	return errs, nil
}

// CheckSchemaVersionValidity reports whether a definition is non-empty for the
// data format. The emulator does not parse Avro/JSON/Protobuf grammars.
//
//nolint:gocritic // unnamedResult: (valid, errorMessage) is self-explanatory
func (*Mock) CheckSchemaVersionValidity(_ context.Context, _, definition string) (bool, string) {
	if strings.TrimSpace(definition) == "" {
		return false, "definition must not be empty"
	}

	return true, ""
}

// GetSchemaVersionsDiff returns a synthesized (empty) diff; the emulator does
// not compute structural schema diffs.
func (*Mock) GetSchemaVersionsDiff(_ context.Context, _, _, _, _ string) (string, error) {
	return "[]", nil
}
