// Package composer provides an in-memory mock of the Google Cloud Composer
// environment control plane (composer.googleapis.com/v1). It models managed
// Apache Airflow environments and the long-running operations their mutating
// RPCs return. It is control-plane only: real Airflow, DAG execution, the GKE
// cluster, snapshots, and user-workload surfaces are out of scope.
package composer

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	cdriver "github.com/stackshy/cloudemu/v2/services/composer/driver"
)

var _ cdriver.Composer = (*Mock)(nil)

// Mock is the in-memory Composer control-plane implementation. Environments are
// keyed by their full resource name
// (projects/{p}/locations/{loc}/environments/{env}).
type Mock struct {
	mu sync.RWMutex

	environments *memstore.Store[cdriver.Environment]
	operations   *memstore.Store[cdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Composer mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		environments: memstore.New[cdriver.Environment](),
		operations:   memstore.New[cdriver.Operation](),
		opts:         opts,
	}
}

// environmentName builds the full environment resource name.
func environmentName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/environments/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *cdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := cdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/composer-%s-%d", scope, opType, m.opSeq.Add(1)),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// CreateEnvironment provisions a new environment, RUNNING immediately, with all
// computed output fields derived deterministically.
func (m *Mock) CreateEnvironment(
	_ context.Context, cfg *cdriver.CreateEnvironmentConfig,
) (*cdriver.Environment, *cdriver.Operation, error) {
	if cfg.EnvironmentID == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "environment name is required")
	}

	if cfg.Location == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := environmentName(cfg.Project, cfg.Location, cfg.EnvironmentID)
	if m.environments.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "environment %q already exists", cfg.EnvironmentID)
	}

	now := m.opts.Clock.Now().UTC()

	env := cdriver.Environment{
		Project:       cfg.Project,
		Location:      cfg.Location,
		EnvironmentID: cfg.EnvironmentID,
		UUID:          idgen.UUID(),
		State:         cdriver.StateRunning,
		CreateTime:    now,
		UpdateTime:    now,
		Labels:        copyLabels(cfg.Labels),
		Config:        cloneConfig(&cfg.Config),
		StorageBucket: generatedBucket(cfg.Location, cfg.EnvironmentID, cfg.Project),
	}
	deriveComputed(&env)
	m.environments.Set(key, env)

	op := m.newOp(cfg.Project, cfg.Location, "create", key)
	out := cloneEnvironment(&env)

	return &out, op, nil
}

// GetEnvironment returns an environment by project/location/id.
func (m *Mock) GetEnvironment(_ context.Context, project, location, id string) (*cdriver.Environment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	e, ok := m.environments.Get(environmentName(project, location, id))
	if !ok {
		return nil, notFoundErr(project, location, id)
	}

	out := cloneEnvironment(&e)

	return &out, nil
}

// ListEnvironments returns every environment in a project+location, ordered by
// resource name.
func (m *Mock) ListEnvironments(_ context.Context, project, location string) ([]cdriver.Environment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/environments/"
	all := m.environments.SortedValues()
	out := make([]cdriver.Environment, 0, len(all))

	for i := range all {
		key := environmentName(all[i].Project, all[i].Location, all[i].EnvironmentID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneEnvironment(&all[i]))
		}
	}

	return out, nil
}

// UpdateEnvironment applies a masked update and returns the completed LRO. Only
// the fields named in mask are written, matching real Composer, which requires
// an explicit updateMask.
func (m *Mock) UpdateEnvironment(
	_ context.Context, project, location, id string, desired *cdriver.EnvironmentConfig,
	labels map[string]string, mask []string,
) (*cdriver.Environment, *cdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := environmentName(project, location, id)

	e, ok := m.environments.Get(key)
	if !ok {
		return nil, nil, notFoundErr(project, location, id)
	}

	applyUpdate(&e, desired, labels, mask)
	e.UpdateTime = m.opts.Clock.Now().UTC()
	e.State = cdriver.StateRunning
	deriveComputed(&e)
	m.environments.Set(key, e)

	op := m.newOp(project, location, "update", key)
	out := cloneEnvironment(&e)

	return &out, op, nil
}

// DeleteEnvironment removes an environment and returns the completed LRO.
func (m *Mock) DeleteEnvironment(_ context.Context, project, location, id string) (*cdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := environmentName(project, location, id)
	if !m.environments.Has(key) {
		return nil, notFoundErr(project, location, id)
	}

	m.environments.Delete(key)

	return m.newOp(project, location, "delete", key), nil
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*cdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &cdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real Composer does for a Get/Patch/Delete of a missing environment.
func notFoundErr(project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "environment %q not found", environmentName(project, location, id))
}

// deriveComputed fills the service-computed OUTPUT-only fields and the default
// image version. They are derived from the stable identity (project/location/
// environment id) so a Terraform refresh reads the same values every time.
func deriveComputed(env *cdriver.Environment) {
	if env.Config.SoftwareConfig == nil {
		env.Config.SoftwareConfig = &cdriver.SoftwareConfig{}
	}

	if env.Config.SoftwareConfig.ImageVersion == "" {
		env.Config.SoftwareConfig.ImageVersion = cdriver.DefaultImageVersion
	}

	h := hash8(env.Project, env.Location, env.EnvironmentID)

	bucket := env.StorageBucket
	if bucket == "" {
		bucket = generatedBucket(env.Location, env.EnvironmentID, env.Project)
		env.StorageBucket = bucket
	}

	env.Config.GkeCluster = "projects/" + env.Project + "/locations/" + env.Location +
		"/clusters/" + env.EnvironmentID + "-gke-" + h
	env.Config.DagGcsPrefix = "gs://" + bucket + "/dags"
	env.Config.AirflowURI = "https://" + h + "-dot-" + env.Location + ".composer.googleusercontent.com"
}

// generatedBucket derives the deterministic Cloud Storage bucket real Composer
// auto-creates for an environment. It is stable across reads so a Terraform
// refresh of the computed dagGcsPrefix / storageConfig.bucket does not drift.
func generatedBucket(location, id, project string) string {
	return location + "-" + id + "-" + hash8(project, location, id) + "-bucket"
}

// hash8 is a stable 8-hex FNV-1a hash of the environment identity.
func hash8(project, location, id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(project + "/" + location + "/" + id))

	return fmt.Sprintf("%08x", h.Sum32())
}

// applyUpdate mutates e per the update mask. Only the masked paths are written;
// a field outside the mask is left untouched.
func applyUpdate(
	e *cdriver.Environment, desired *cdriver.EnvironmentConfig, labels map[string]string, mask []string,
) {
	if maskHas(mask, "labels") {
		e.Labels = copyLabels(labels)
	}

	if maskHas(mask, "config.nodecount") {
		e.Config.NodeCount = desired.NodeCount
	}

	if maskHas(mask, "config.environmentsize") {
		e.Config.EnvironmentSize = desired.EnvironmentSize
	}

	applySoftwareUpdate(&e.Config, desired, mask)
	applyOtherUpdate(&e.Config, desired, mask)
}

// applySoftwareUpdate folds the masked softwareConfig fields from desired into
// the environment's config, creating the software block on demand.
func applySoftwareUpdate(cfg, desired *cdriver.EnvironmentConfig, mask []string) {
	if !maskHasPrefix(mask, "config.softwareconfig") {
		return
	}

	if cfg.SoftwareConfig == nil {
		cfg.SoftwareConfig = &cdriver.SoftwareConfig{}
	}

	if desired.SoftwareConfig == nil {
		desired.SoftwareConfig = &cdriver.SoftwareConfig{}
	}

	sc, dc := cfg.SoftwareConfig, desired.SoftwareConfig

	if maskHas(mask, "config.softwareconfig.imageversion") {
		sc.ImageVersion = dc.ImageVersion
	}

	if maskHas(mask, "config.softwareconfig.schedulercount") {
		sc.SchedulerCount = dc.SchedulerCount
	}

	if maskHasPrefix(mask, "config.softwareconfig.airflowconfigoverrides") {
		sc.AirflowConfigOverrides = copyLabels(dc.AirflowConfigOverrides)
	}

	if maskHasPrefix(mask, "config.softwareconfig.pypipackages") {
		sc.PypiPackages = copyLabels(dc.PypiPackages)
	}

	if maskHasPrefix(mask, "config.softwareconfig.envvariables") {
		sc.EnvVariables = copyLabels(dc.EnvVariables)
	}
}

// modeledConfigTokens are the lowercased config field names CloudEmu models
// explicitly; every other config.<block> mask path targets an opaque block
// carried in EnvironmentConfig.Other.
func modeledConfigTokens() map[string]bool {
	return map[string]bool{
		"softwareconfig": true, "nodeconfig": true, "nodecount": true,
		"environmentsize": true, "resiliencemode": true,
	}
}

// otherMaskKeys returns the lowercased top-level config block tokens named in
// mask that target an opaque (unmodeled) sub-block.
func otherMaskKeys(mask []string) []string {
	modeled := modeledConfigTokens()

	var out []string

	for _, p := range mask {
		rest, ok := strings.CutPrefix(p, "config.")
		if !ok {
			continue
		}

		token := rest
		if i := strings.Index(rest, "."); i >= 0 {
			token = rest[:i]
		}

		if !modeled[token] {
			out = append(out, token)
		}
	}

	return out
}

// applyOtherUpdate replaces any masked opaque config sub-block (workloadsConfig,
// webServerNetworkAccessControl, …) with the desired value, verbatim. Keys are
// matched case-insensitively against the canonical JSON field name the wire
// layer stored, so the update honors the mask regardless of its casing.
func applyOtherUpdate(cfg, desired *cdriver.EnvironmentConfig, mask []string) {
	for _, token := range otherMaskKeys(mask) {
		if canon, v, ok := lookupOther(desired.Other, token); ok {
			if cfg.Other == nil {
				cfg.Other = map[string]json.RawMessage{}
			}

			cfg.Other[canon] = append(json.RawMessage(nil), v...)

			continue
		}

		if canon, _, ok := lookupOther(cfg.Other, token); ok {
			delete(cfg.Other, canon)
		}
	}
}

// lookupOther finds the entry in m whose canonical key lowercases to token.
func lookupOther(m map[string]json.RawMessage, token string) (canon string, val json.RawMessage, ok bool) {
	for k, v := range m {
		if strings.EqualFold(k, token) {
			return k, v, true
		}
	}

	return "", nil, false
}

func maskHas(mask []string, field string) bool {
	for _, p := range mask {
		if p == field {
			return true
		}
	}

	return false
}

// maskHasPrefix reports whether any mask path equals prefix or is nested under
// it (so "config.softwareConfig.airflowConfigOverrides.core-foo" selects the
// whole airflowConfigOverrides map — TF sends the full map anyway).
func maskHasPrefix(mask []string, prefix string) bool {
	for _, p := range mask {
		if p == prefix || strings.HasPrefix(p, prefix+".") {
			return true
		}
	}

	return false
}

func copyLabels(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}
