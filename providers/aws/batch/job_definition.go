package batch

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

//nolint:gocritic // clone intentionally takes a value to return an independent copy-on-write copy
func cloneJobDefinition(jd driver.JobDefinition) driver.JobDefinition {
	jd.ContainerProperties = copyRaw(jd.ContainerProperties)
	jd.NodeProperties = copyRaw(jd.NodeProperties)
	jd.EcsProperties = copyRaw(jd.EcsProperties)
	jd.EksProperties = copyRaw(jd.EksProperties)
	jd.RetryStrategy = copyRaw(jd.RetryStrategy)
	jd.Timeout = copyRaw(jd.Timeout)
	jd.Parameters = copyTags(jd.Parameters)
	jd.PlatformCapabilities = copyStrings(jd.PlatformCapabilities)
	jd.PropagateTags = copyBool(jd.PropagateTags)
	jd.SchedulingPriority = copyInt32(jd.SchedulingPriority)
	jd.Tags = copyTags(jd.Tags)

	return jd
}

// RegisterJobDefinition registers a new revision. A repeated name yields the
// next revision (N+1) with a new ARN; prior revisions remain ACTIVE until
// deregistered. platformCapabilities defaults to [EC2].
//
//nolint:gocritic // in is the driver RegisterJobDefinitionInput, taken by value to match the interface
func (m *Mock) RegisterJobDefinition(
	_ context.Context, in driver.RegisterJobDefinitionInput,
) (*driver.JobDefinition, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "jobDefinitionName is required")
	}

	if in.Type == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "type is required (container or multinode)")
	}

	caps := in.PlatformCapabilities
	if len(caps) == 0 {
		caps = []string{"EC2"}
	}

	m.registerMu.Lock()

	cur, _ := m.jobDefMaxRev.Get(in.Name)
	rev := cur + 1

	jd := driver.JobDefinition{
		Name:                 in.Name,
		ARN:                  m.arn(kindJobDefinition, jobDefKey(in.Name, rev)),
		Revision:             rev,
		Status:               driver.JDStatusActive,
		Type:                 in.Type,
		ContainerProperties:  copyRaw(in.ContainerProperties),
		NodeProperties:       copyRaw(in.NodeProperties),
		EcsProperties:        copyRaw(in.EcsProperties),
		EksProperties:        copyRaw(in.EksProperties),
		RetryStrategy:        copyRaw(in.RetryStrategy),
		Timeout:              copyRaw(in.Timeout),
		Parameters:           copyTags(in.Parameters),
		PlatformCapabilities: copyStrings(caps),
		PropagateTags:        copyBool(in.PropagateTags),
		SchedulingPriority:   copyInt32(in.SchedulingPriority),
		Tags:                 copyTags(in.Tags),
	}

	m.jobDefs.Set(jobDefKey(in.Name, rev), jd)
	m.jobDefMaxRev.Set(in.Name, rev)

	m.registerMu.Unlock()

	out := cloneJobDefinition(jd)

	return &out, nil
}

// DescribeJobDefinitions returns the matching revisions. Explicit jobDefinitions
// (ARN or name:revision) are returned regardless of status; otherwise Name and
// Status (default ACTIVE) filter the set. Results are ordered by name then
// revision.
func (m *Mock) DescribeJobDefinitions(
	_ context.Context, in driver.DescribeJobDefinitionsInput,
) ([]driver.JobDefinition, error) {
	if len(in.JobDefinitions) > 0 {
		return m.describeByRef(in.JobDefinitions), nil
	}

	status := in.Status
	if status == "" {
		status = driver.JDStatusActive
	}

	out := make([]driver.JobDefinition, 0)

	defs := m.sortedJobDefs()
	for i := range defs {
		if in.Name != "" && defs[i].Name != in.Name {
			continue
		}

		if defs[i].Status != status {
			continue
		}

		out = append(out, cloneJobDefinition(defs[i]))
	}

	return out, nil
}

// describeByRef resolves explicit references. A ref carrying a revision
// (name:revision or a full ARN) selects one revision; a bare name selects every
// revision of that name.
func (m *Mock) describeByRef(refs []string) []driver.JobDefinition {
	out := make([]driver.JobDefinition, 0, len(refs))

	for _, ref := range refs {
		key := nameFromARN(kindJobDefinition, ref)
		if strings.Contains(key, ":") {
			if jd, ok := m.jobDefs.Get(key); ok {
				out = append(out, cloneJobDefinition(jd))
			}

			continue
		}

		defs := m.sortedJobDefs()
		for i := range defs {
			if defs[i].Name == key {
				out = append(out, cloneJobDefinition(defs[i]))
			}
		}
	}

	return out
}

// DeregisterJobDefinition marks a specific revision (name:revision or ARN)
// INACTIVE. Deregistering a missing or already-inactive revision is a no-op.
func (m *Mock) DeregisterJobDefinition(_ context.Context, nameRevisionOrARN string) error {
	key := nameFromARN(kindJobDefinition, nameRevisionOrARN)
	if !strings.Contains(key, ":") {
		return cerrors.Newf(cerrors.InvalidArgument,
			"job definition reference %q must include a revision (name:revision)", nameRevisionOrARN)
	}

	m.jobDefs.Update(key, func(jd driver.JobDefinition) driver.JobDefinition {
		next := cloneJobDefinition(jd)
		next.Status = driver.JDStatusInactive

		return next
	})

	return nil
}

// sortedJobDefs returns every stored revision ordered by name then revision.
func (m *Mock) sortedJobDefs() []driver.JobDefinition {
	all := m.jobDefs.All()
	out := make([]driver.JobDefinition, 0, len(all))

	for k := range all {
		out = append(out, all[k])
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}

		return out[i].Revision < out[j].Revision
	})

	return out
}
