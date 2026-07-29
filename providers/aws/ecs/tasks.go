package ecs

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// RunTask creates count tasks (default 1) from a task definition. An unresolved
// task definition yields a single failure and no tasks.
//
//nolint:gocritic // in is passed by value to satisfy the driver.ECS interface; the copy is cheap for a mock.
func (m *Mock) RunTask(_ context.Context, in driver.RunTaskInput) ([]driver.Task, []driver.Failure, error) {
	cluster := resolveClusterName(in.Cluster)
	clusterARN := m.arn("cluster/" + cluster)

	td, ok := m.resolveTaskDef(in.TaskDefinition)
	if !ok {
		return nil, []driver.Failure{{ARN: in.TaskDefinition, Reason: "MISSING"}}, nil
	}

	count := in.Count
	if count <= 0 {
		count = 1
	}

	tasks := make([]driver.Task, 0, count)

	for range count {
		task := &driver.Task{
			ARN:               m.arn("task/" + cluster + "/" + m.hexID()),
			ClusterARN:        clusterARN,
			TaskDefinitionARN: td.ARN,
			LastStatus:        statusRunning,
			DesiredStatus:     statusRunning,
			LaunchType:        in.LaunchType,
			Group:             in.Group,
			StartedBy:         in.StartedBy,
			CreatedAt:         m.now(),
			Containers:        containersFor(td),
			Tags:              copyTags(in.Tags),
		}
		m.tasks.Set(task.ARN, task)
		tasks = append(tasks, *task)
	}

	return tasks, nil, nil
}

func containersFor(td *driver.TaskDefinition) []driver.Container {
	out := make([]driver.Container, 0, len(td.ContainerDefinitions))

	for i := range td.ContainerDefinitions {
		cd := &td.ContainerDefinitions[i]
		out = append(out, driver.Container{Name: cd.Name, Image: cd.Image, LastStatus: statusRunning})
	}

	return out
}

// StopTask marks a task STOPPED.
func (m *Mock) StopTask(_ context.Context, _, task, reason string) (*driver.Task, error) {
	t, ok := m.resolveTask(task)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "task %q not found", task)
	}

	t.LastStatus = statusStopped
	t.DesiredStatus = statusStopped
	t.StoppedReason = reason
	t.StopCode = "UserInitiated"

	for i := range t.Containers {
		t.Containers[i].LastStatus = statusStopped
	}

	out := *t

	return &out, nil
}

// ListTasks returns tasks in a cluster, optionally filtered by family and
// desired status.
func (m *Mock) ListTasks(_ context.Context, cluster, family, desiredStatus string) ([]driver.Task, error) {
	want := resolveClusterName(cluster)

	all := m.tasks.SortedValues()

	out := make([]driver.Task, 0, len(all))

	for _, t := range all {
		if clusterNameFromARN(t.ClusterARN) != want {
			continue
		}

		if family != "" && familyFromTaskDefARN(t.TaskDefinitionARN) != family {
			continue
		}

		if desiredStatus != "" && t.DesiredStatus != desiredStatus {
			continue
		}

		out = append(out, *t)
	}

	return out, nil
}

// DescribeTasks resolves tasks by id or ARN; unresolved ids become failures.
//
//nolint:dupl // batch resolve-or-fail loop; each Describe binds a distinct resolver and type.
func (m *Mock) DescribeTasks(_ context.Context, _ string, ids []string) ([]driver.Task, []driver.Failure, error) {
	found := make([]driver.Task, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		if t, ok := m.resolveTask(id); ok {
			found = append(found, *t)
			continue
		}

		failures = append(failures, driver.Failure{ARN: id, Reason: "MISSING"})
	}

	return found, failures, nil
}

// resolveTask looks up a task by full ARN or bare 32-hex id.
func (m *Mock) resolveTask(id string) (*driver.Task, bool) {
	if t, ok := m.tasks.Get(id); ok {
		return t, true
	}

	// Match by trailing id segment when a bare id was supplied.
	for _, t := range m.tasks.All() {
		if id != "" && strings.HasSuffix(t.ARN, "/"+id) {
			return t, true
		}
	}

	return nil, false
}
