package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/projectctx"
)

// ProjectTag is the internal tag holding the GCP project that owns a network,
// subnetwork or firewall. Its "cloudemu:gcp" prefix keeps it out of labels.
const ProjectTag = "cloudemu:gcp:project"

// project returns the project the request addressed, or the configured
// default project when nothing was stamped.
func (m *Mock) project(ctx context.Context) string {
	return projectctx.ProjectOr(ctx, m.opts.ProjectID)
}

// stampProject returns a copy of tags with the owning project set.
func stampProject(tags map[string]string, project string) map[string]string {
	out := make(map[string]string, len(tags)+1)
	for k, v := range tags {
		out[k] = v
	}

	out[ProjectTag] = project

	return out
}

// describeScoped lists records by id, or, with no ids, every record of the
// project ctx addresses. A record with no project tag belongs to the default
// project; projectctx.AllProjects sees every record. Ids embed their project,
// so an explicit id lookup is not filtered.
func describeScoped[T any, R any](
	ctx context.Context, m *Mock, store *memstore.Store[T], ids []string,
	tags func(T) map[string]string, toInfo func(T) R,
) []R {
	if len(ids) > 0 {
		return describeResources(store, ids, toInfo)
	}

	every := projectctx.IsAllProjects(ctx)
	want := m.project(ctx)
	all := store.All()
	out := make([]R, 0, len(all))

	for _, item := range all {
		owner := tags(item)[ProjectTag]
		if owner == "" {
			owner = m.opts.ProjectID
		}

		if every || owner == want {
			out = append(out, toInfo(item))
		}
	}

	return out
}

// adoptLegacy tags every record of store with no project with the default
// project and returns how many it adopted.
func adoptLegacy[T any](store *memstore.Store[*T], project string, tags func(*T) *map[string]string) int {
	n := 0

	for key, v := range store.All() {
		if (*tags(v))[ProjectTag] != "" {
			continue
		}

		store.Update(key, func(cur *T) *T {
			cp := *cur
			t := tags(&cp)
			*t = stampProject(*t, project)

			return &cp
		})

		n++
	}

	return n
}

// adoptLegacyRecords places networks, subnetworks and firewalls restored from
// a snapshot taken before project scoping in the default project.
func (m *Mock) adoptLegacyRecords() {
	p := m.opts.ProjectID
	n := adoptLegacy(m.vpcs, p, func(v *vpcData) *map[string]string { return &v.Tags })
	n += adoptLegacy(m.subnets, p, func(s *subnetData) *map[string]string { return &s.Tags })
	n += adoptLegacy(m.securityGroups, p, func(s *sgData) *map[string]string { return &s.Tags })

	projectctx.WarnAdopted("vpc", n, p)
}
