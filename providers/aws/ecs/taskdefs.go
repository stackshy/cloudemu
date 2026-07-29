package ecs

import (
	"context"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// RegisterTaskDefinition registers a new revision for a family, auto-incrementing
// the revision number (starting at 1).
//
//nolint:gocritic // in is passed by value to satisfy the driver.ECS interface; the copy is cheap for a mock.
func (m *Mock) RegisterTaskDefinition(_ context.Context, in driver.RegisterTaskDefinitionInput) (*driver.TaskDefinition, error) {
	if in.Family == "" {
		return nil, errors.New(errors.InvalidArgument, "family is required")
	}

	m.regMu.Lock()
	defer m.regMu.Unlock()

	revision := m.nextRevision(in.Family)
	td := &driver.TaskDefinition{
		ARN:                     m.arn(fmt.Sprintf("task-definition/%s:%d", in.Family, revision)),
		Family:                  in.Family,
		Revision:                revision,
		Status:                  statusActive,
		ContainerDefinitions:    append([]driver.ContainerDefinition(nil), in.ContainerDefinitions...),
		CPU:                     in.CPU,
		Memory:                  in.Memory,
		NetworkMode:             in.NetworkMode,
		RequiresCompatibilities: append([]string(nil), in.RequiresCompatibilities...),
		TaskRoleARN:             in.TaskRoleARN,
		ExecutionRoleARN:        in.ExecutionRoleARN,
		RegisteredAt:            m.now(),
		Tags:                    copyTags(in.Tags),
	}
	m.taskDefs.Set(fmt.Sprintf("%s:%d", in.Family, revision), td)

	out := *td

	return &out, nil
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

// ListTaskDefinitions returns task-definition ARNs filtered by family prefix and
// status, in deterministic order.
func (m *Mock) ListTaskDefinitions(_ context.Context, familyPrefix, status string) ([]driver.TaskDefinition, error) {
	all := m.taskDefs.SortedValues()

	out := make([]driver.TaskDefinition, 0, len(all))

	for _, td := range all {
		if familyPrefix != "" && !strings.HasPrefix(td.Family, familyPrefix) {
			continue
		}

		if status != "" && td.Status != status {
			continue
		}

		out = append(out, *td)
	}

	return out, nil
}

// DescribeTaskDefinition resolves a task definition by "family", "family:revision",
// or full ARN. A bare family resolves to the latest ACTIVE revision.
func (m *Mock) DescribeTaskDefinition(_ context.Context, id string) (*driver.TaskDefinition, error) {
	td, ok := m.resolveTaskDef(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "task definition %q not found", id)
	}

	out := *td

	return &out, nil
}

// DeregisterTaskDefinition marks a task-definition revision INACTIVE.
func (m *Mock) DeregisterTaskDefinition(_ context.Context, id string) (*driver.TaskDefinition, error) {
	td, ok := m.resolveTaskDef(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "task definition %q not found", id)
	}

	td.Status = statusInactive
	td.DeregisteredAt = m.now()

	out := *td

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
