package compute

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/projectctx"
)

// ProjectTag is the internal tag holding the GCP project that owns an
// instance, disk, image or snapshot. Its "cloudemu:gcp" prefix keeps it out of
// the labels the wire layer emits.
const ProjectTag = "cloudemu:gcp:project"

// project returns the project the request addressed, or the configured
// default project when nothing was stamped.
func (m *Mock) project(ctx context.Context) string {
	return projectctx.ProjectOr(ctx, m.opts.ProjectID)
}

// visible reports whether a record with tags belongs to the project ctx
// addresses. A record with no project tag (created before project scoping)
// belongs to the default project. projectctx.AllProjects sees every record.
func (m *Mock) visible(ctx context.Context, tags map[string]string) bool {
	if projectctx.IsAllProjects(ctx) {
		return true
	}

	owner := tags[ProjectTag]
	if owner == "" {
		owner = m.opts.ProjectID
	}

	return owner == m.project(ctx)
}

// stampProject returns tags with the owning project set, allocating the map
// when tags is nil.
func stampProject(tags map[string]string, project string) map[string]string {
	if tags == nil {
		tags = make(map[string]string, 1)
	}

	tags[ProjectTag] = project

	return tags
}

// describeScoped lists records by id, or, with no ids, every record of the
// project ctx addresses. Ids embed their project, so an explicit id lookup is
// not filtered.
func describeScoped[T any](
	ctx context.Context, m *Mock, store *memstore.Store[*T], ids []string, tags func(*T) map[string]string,
) []T {
	if len(ids) > 0 {
		return describeResources(store, ids)
	}

	all := store.All()
	out := make([]T, 0, len(all))

	for _, v := range all {
		if m.visible(ctx, tags(v)) {
			out = append(out, *v)
		}
	}

	return out
}

// adoptLegacy tags every record of store that has no project with the default
// project and returns how many it adopted. Snapshots taken before project
// scoping carry no project tag.
func adoptLegacy[T any](store *memstore.Store[*T], project string, tags func(*T) *map[string]string) int {
	n := 0

	for key, v := range store.All() {
		if (*tags(v))[ProjectTag] != "" {
			continue
		}

		store.Update(key, func(cur *T) *T {
			cp := *cur
			t := tags(&cp)
			*t = stampProject(copyTags(*t), project)

			return &cp
		})

		n++
	}

	return n
}
