package ecs

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// sortDesc is the ListTaskDefinitions sort order that reverses the default
// (family, ascending numeric revision) ordering.
const sortDesc = "DESC"

// RegisterTaskDefinition registers a new revision for a family, auto-incrementing
// the revision number (starting at 1).
//
//nolint:gocritic // in is passed by value to satisfy the driver.ECS interface; the copy is cheap for a mock.
func (m *Mock) RegisterTaskDefinition(_ context.Context, in driver.RegisterTaskDefinitionInput) (*driver.TaskDefinition, error) {
	if in.Family == "" {
		return nil, errors.New(errors.InvalidArgument, "family is required")
	}

	if err := validateTaskDef(in); err != nil {
		return nil, err
	}

	m.regMu.Lock()
	defer m.regMu.Unlock()

	revision := m.nextRevision(in.Family)
	td := &driver.TaskDefinition{
		ARN:                     m.arn(fmt.Sprintf("task-definition/%s:%d", in.Family, revision)),
		Family:                  in.Family,
		Revision:                revision,
		Status:                  statusActive,
		ContainerDefinitions:    cloneContainerDefs(in.ContainerDefinitions),
		CPU:                     in.CPU,
		Memory:                  in.Memory,
		NetworkMode:             in.NetworkMode,
		RequiresCompatibilities: append([]string(nil), in.RequiresCompatibilities...),
		TaskRoleARN:             in.TaskRoleARN,
		ExecutionRoleARN:        in.ExecutionRoleARN,
		Volumes:                 in.Volumes,
		EphemeralStorage:        in.EphemeralStorage,
		PidMode:                 in.PidMode,
		IpcMode:                 in.IpcMode,
		RuntimePlatform:         in.RuntimePlatform,
		ProxyConfiguration:      in.ProxyConfiguration,
		PlacementConstraints:    in.PlacementConstraints,
		InferenceAccelerators:   in.InferenceAccelerators,
		EnableFaultInjection:    in.EnableFaultInjection,
		RegisteredAt:            m.now(),
		Tags:                    copyTags(in.Tags),
	}
	// Store a deep clone so the stored record never aliases the caller's input:
	// the ContainerDefinitions slice above is cloned, but the remaining task-level
	// reference fields (Volumes, PlacementConstraints, InferenceAccelerators,
	// EphemeralStorage, RuntimePlatform, ProxyConfiguration) would otherwise be
	// held by reference, so a caller mutating its input after register could
	// corrupt the store. cloneTaskDef deep-copies every such field.
	stored := cloneTaskDef(td)
	m.taskDefs.Set(fmt.Sprintf("%s:%d", in.Family, revision), &stored)
	m.recordTags(stored.ARN, in.Tags)

	out := cloneTaskDef(&stored)

	return &out, nil
}

// validateTaskDef enforces the registration rules ECS applies synchronously: a
// task definition must declare at least one container, and a Fargate-compatible
// definition must carry task-level cpu and memory and use the awsvpc network
// mode. Violations surface as ClientException, matching AWS.
//
//nolint:gocritic // in mirrors the RegisterTaskDefinition signature; the copy is cheap for a mock.
func validateTaskDef(in driver.RegisterTaskDefinitionInput) error {
	if len(in.ContainerDefinitions) == 0 {
		return apiErrf(errors.InvalidArgument, excClient, "containerDefinitions must contain at least one container")
	}

	if err := validateContainerNames(in.ContainerDefinitions); err != nil {
		return err
	}

	if !containsLaunchType(in.RequiresCompatibilities, launchFargate) {
		return nil
	}

	if in.CPU == "" || in.Memory == "" {
		return apiErrf(errors.InvalidArgument, excClient,
			"Fargate requires task-level cpu and memory to be specified")
	}

	if in.NetworkMode != networkModeAwsvpc {
		return apiErrf(errors.InvalidArgument, excClient, "Fargate requires the awsvpc network mode")
	}

	return nil
}

// validateContainerNames enforces that every container definition carries a
// non-empty name and that names are unique within the task definition, matching
// the ClientException AWS raises for a missing or duplicate container name.
func validateContainerNames(defs []driver.ContainerDefinition) error {
	seen := make(map[string]bool, len(defs))

	for i := range defs {
		name := defs[i].Name
		if name == "" {
			return apiErrf(errors.InvalidArgument, excClient, "every container definition must have a name")
		}

		if seen[name] {
			return apiErrf(errors.InvalidArgument, excClient, "duplicate container name %q", name)
		}

		seen[name] = true
	}

	return nil
}

// nextRevision returns the next revision number for a family. Callers must hold
// regMu.
func (m *Mock) nextRevision(family string) int {
	maxRev := 0

	for _, td := range m.taskDefs.All() {
		if td.Family == family && td.Revision > maxRev {
			maxRev = td.Revision
		}
	}

	return maxRev + 1
}

// ListTaskDefinitions returns task definitions filtered by family prefix and
// status, sorted by family then NUMERIC revision (so web:2 precedes web:10).
// sortOrder is "ASC" (default) or "DESC"; DESC reverses the ordering.
func (m *Mock) ListTaskDefinitions(
	_ context.Context, familyPrefix, status, sortOrder string,
) ([]driver.TaskDefinition, error) {
	all := m.taskDefs.SortedValues()

	out := make([]driver.TaskDefinition, 0, len(all))

	for _, td := range all {
		if familyPrefix != "" && !strings.HasPrefix(td.Family, familyPrefix) {
			continue
		}

		if status != "" && td.Status != status {
			continue
		}

		out = append(out, cloneTaskDef(td))
	}

	desc := strings.EqualFold(sortOrder, sortDesc)

	sort.SliceStable(out, func(i, j int) bool {
		if desc {
			return taskDefLess(&out[j], &out[i])
		}

		return taskDefLess(&out[i], &out[j])
	})

	return out, nil
}

// ListTaskDefinitionFamilies returns the distinct task-definition family names,
// sorted, optionally filtered by family prefix. status filters by family
// activeness: ACTIVE keeps families with at least one ACTIVE revision, INACTIVE
// keeps families with none, and an empty status keeps every family.
func (m *Mock) ListTaskDefinitionFamilies(_ context.Context, familyPrefix, status string) ([]string, error) {
	hasActive := make(map[string]bool)
	seen := make(map[string]bool)

	for _, td := range m.taskDefs.All() {
		seen[td.Family] = true

		if td.Status == statusActive {
			hasActive[td.Family] = true
		}
	}

	out := make([]string, 0, len(seen))

	for family := range seen {
		if familyPrefix != "" && !strings.HasPrefix(family, familyPrefix) {
			continue
		}

		switch status {
		case statusActive:
			if !hasActive[family] {
				continue
			}
		case statusInactive:
			if hasActive[family] {
				continue
			}
		}

		out = append(out, family)
	}

	sort.Strings(out)

	return out, nil
}

// taskDefLess orders task definitions by family, then by numeric revision, so
// that revision 2 sorts before revision 10 (a lexical key sort would not).
func taskDefLess(a, b *driver.TaskDefinition) bool {
	if a.Family != b.Family {
		return a.Family < b.Family
	}

	return a.Revision < b.Revision
}

// DescribeTaskDefinition resolves a task definition by "family", "family:revision",
// or full ARN. A bare family resolves to the latest ACTIVE revision.
func (m *Mock) DescribeTaskDefinition(_ context.Context, id string) (*driver.TaskDefinition, error) {
	td, ok := m.resolveTaskDef(id)
	if !ok {
		return nil, apiErrf(errors.NotFound, excClient, "task definition %q not found", id)
	}

	out := cloneTaskDef(td)

	return &out, nil
}

// DeregisterTaskDefinition marks a task-definition revision INACTIVE.
func (m *Mock) DeregisterTaskDefinition(_ context.Context, id string) (*driver.TaskDefinition, error) {
	td, ok := m.resolveTaskDef(id)
	if !ok {
		return nil, apiErrf(errors.NotFound, excClient, "task definition %q not found", id)
	}

	// Copy-on-write: mutate a clone and Set it back under its stored key.
	updated := cloneTaskDef(td)
	updated.Status = statusInactive
	updated.DeregisteredAt = m.now()
	m.taskDefs.Set(fmt.Sprintf("%s:%d", updated.Family, updated.Revision), &updated)

	out := cloneTaskDef(&updated)

	return &out, nil
}

// resolveTaskDef looks up a task definition by family, family:revision, or ARN.
func (m *Mock) resolveTaskDef(id string) (*driver.TaskDefinition, bool) {
	key := id
	if strings.Contains(id, "task-definition/") {
		key = id[strings.LastIndex(id, "task-definition/")+len("task-definition/"):]
	}

	if strings.Contains(key, ":") {
		return m.taskDefs.Get(key)
	}

	return m.latestActive(key)
}

// resolveLaunchableTaskDef resolves a definition for launching a task or service
// and requires it to be ACTIVE. resolveTaskDef alone accepts a deregistered
// (INACTIVE) definition when referenced by an explicit family:revision or ARN;
// real ECS refuses to run new tasks from such a definition, so launch paths use
// this instead. A missing definition is a not-found ClientException; a resolved
// but INACTIVE one is an InvalidParameter ClientException (it exists, it's just
// not runnable). Bare-family lookups are unaffected — latestActive already skips
// INACTIVE revisions.
func (m *Mock) resolveLaunchableTaskDef(id string) (*driver.TaskDefinition, error) {
	td, ok := m.resolveTaskDef(id)
	if !ok {
		return nil, apiErrf(errors.NotFound, excClient, "task definition %q not found", id)
	}

	if td.Status != statusActive {
		return nil, apiErrf(errors.InvalidArgument, excClient,
			"task definition %q is not ACTIVE; it was deregistered", id)
	}

	return td, nil
}

// latestActive returns the highest ACTIVE revision for a family.
func (m *Mock) latestActive(family string) (*driver.TaskDefinition, bool) {
	var (
		best   *driver.TaskDefinition
		bestNo int
	)

	for _, td := range m.taskDefs.All() {
		if td.Family == family && td.Status == statusActive && td.Revision > bestNo {
			best = td
			bestNo = td.Revision
		}
	}

	return best, best != nil
}
