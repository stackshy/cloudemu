package loadbalancer

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/projectctx"
)

// keySep separates the parts of an opaque GCP resource key. NUL can't collide
// with a project, collection, region or name segment.
const keySep = "\x00"

// project returns the project the request addressed, or the configured
// default project when nothing was stamped.
func (m *Mock) project(ctx context.Context) string {
	return projectctx.ProjectOr(ctx, m.opts.ProjectID)
}

// gcpResourceKey builds the store key for an opaque GCP resource.
func gcpResourceKey(project, collection, scope, name string) string {
	return project + keySep + collection + keySep + scope + keySep + name
}

// resourceKey is gcpResourceKey in the project ctx addresses.
func (m *Mock) resourceKey(ctx context.Context, collection, scope, name string) string {
	return gcpResourceKey(m.project(ctx), collection, scope, name)
}

// inProject reports whether a record owned by keyProject is visible to ctx:
// it is in the project ctx addresses, or ctx is marked AllProjects.
func (m *Mock) inProject(ctx context.Context, keyProject string) bool {
	return projectctx.IsAllProjects(ctx) || keyProject == m.project(ctx)
}

// describeScoped returns the records named by arns, or, with no arns, every
// record of the project ctx addresses. ARNs embed their project
// ("projects/<p>/..."), so an explicit lookup is not filtered.
func describeScoped[T any](ctx context.Context, m *Mock, store *memstore.Store[T], arns []string) []T {
	if len(arns) > 0 {
		return describeResources(store, arns)
	}

	return filterToSlice(store, func(arn string, _ T) bool {
		return m.inProject(ctx, projectctx.FromPath(arn))
	})
}

// adoptLegacyResources moves opaque resources restored from a snapshot taken
// before project scoping (keys with no project part) into the default project.
// Forwarding rule, backend service and URL map ARNs already carry the default
// project, so they need no change.
func (m *Mock) adoptLegacyResources() {
	n := 0

	for key, res := range m.gcpResources.All() {
		if strings.Count(key, keySep) != 2 { //nolint:mnd // collection, scope, name
			continue
		}

		m.gcpResources.Delete(key)
		m.gcpResources.Set(gcpResourceKey(m.opts.ProjectID, res.Collection, res.Scope, res.Name), res)

		n++
	}

	projectctx.WarnAdopted("loadbalancer", n, m.opts.ProjectID)
}
