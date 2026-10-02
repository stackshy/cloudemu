package loadbalancer

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Compile-time checks that Mock implements the GCP-specific optional interfaces.
var (
	_ driver.GCPComputeResourceStore  = (*Mock)(nil)
	_ driver.GCPBackendServicePatcher = (*Mock)(nil)
	_ driver.GCPForwardingRulePatcher = (*Mock)(nil)
)

// gcpResourceKey builds the store key for an opaque GCP resource. The NUL
// separator can't collide with a project/region/name segment.
func gcpResourceKey(collection, scope, name string) string {
	return collection + "\x00" + scope + "\x00" + name
}

// PutGCPResource stores an opaque GCP Compute Load Balancing resource, returning
// AlreadyExists when the (collection, scope, name) triple is already taken.
//
//nolint:gocritic // hugeParam: interface method signature is fixed.
func (m *Mock) PutGCPResource(_ context.Context, res driver.GCPResource) error {
	key := gcpResourceKey(res.Collection, res.Scope, res.Name)
	if !m.gcpResources.SetIfAbsent(key, res) {
		return cerrors.Newf(cerrors.AlreadyExists, "%s %q already exists", res.Collection, res.Name)
	}

	return nil
}

// GetGCPResource returns the stored opaque resource or NotFound.
func (m *Mock) GetGCPResource(_ context.Context, collection, scope, name string) (*driver.GCPResource, error) {
	res, ok := m.gcpResources.Get(gcpResourceKey(collection, scope, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "%s %q not found", collection, name)
	}

	result := res

	return &result, nil
}

// ListGCPResources returns every opaque resource in a (collection, scope) bucket.
func (m *Mock) ListGCPResources(_ context.Context, collection, scope string) ([]driver.GCPResource, error) {
	return filterToSlice(m.gcpResources, func(_ string, res driver.GCPResource) bool {
		return res.Collection == collection && res.Scope == scope
	}), nil
}

// DeleteGCPResource removes an opaque resource, returning NotFound when absent.
func (m *Mock) DeleteGCPResource(_ context.Context, collection, scope, name string) error {
	if !m.gcpResources.Delete(gcpResourceKey(collection, scope, name)) {
		return cerrors.Newf(cerrors.NotFound, "%s %q not found", collection, name)
	}

	return nil
}

// UpdateGCPResource applies mutate to the stored opaque resource in place,
// holding the store lock across the read-modify-write. Returns NotFound when no
// resource with the (collection, scope, name) triple exists.
func (m *Mock) UpdateGCPResource(_ context.Context, collection, scope, name string, mutate func(*driver.GCPResource)) error {
	key := gcpResourceKey(collection, scope, name)

	updated := m.gcpResources.Update(key, func(res driver.GCPResource) driver.GCPResource {
		mutate(&res)
		return res
	})
	if !updated {
		return cerrors.Newf(cerrors.NotFound, "%s %q not found", collection, name)
	}

	return nil
}

// PatchGCPForwardingRule applies mutate to the forwarding rule named name (its
// driver name) under the store lock. mutate works on a copy with its own Tags
// map, so an error leaves the stored record untouched. Returns NotFound when no
// such rule exists.
func (m *Mock) PatchGCPForwardingRule(_ context.Context, name string, mutate func(*driver.LBInfo) error) error {
	arn := idgen.GCPID(m.opts.ProjectID, "forwardingRules", name)

	var mutErr error

	updated := m.lbs.Update(arn, func(lb driver.LBInfo) driver.LBInfo {
		next := lb
		next.Tags = make(map[string]string, len(lb.Tags))

		for k, v := range lb.Tags {
			next.Tags[k] = v
		}

		if mutErr = mutate(&next); mutErr != nil {
			return lb
		}

		return next
	})
	if !updated {
		return cerrors.Newf(cerrors.NotFound, "forwarding rule %q not found", name)
	}

	return mutErr
}

// PatchGCPBackendService applies mutate to the target group named name, holding
// the store lock across the read-modify-write. mutate works on a copy with its
// own Tags map, so readers holding the stored map never see it change. Returns
// NotFound when no backend service with that name exists.
func (m *Mock) PatchGCPBackendService(_ context.Context, name string, mutate func(*driver.TargetGroupInfo)) error {
	// CreateTargetGroup keys the store by GCPID(project, "backendServices", name),
	// so the ARN is derivable from the name without scanning.
	arn := idgen.GCPID(m.opts.ProjectID, "backendServices", name)

	updated := m.tgs.Update(arn, func(tg driver.TargetGroupInfo) driver.TargetGroupInfo {
		next := tg
		next.Tags = make(map[string]string, len(tg.Tags))

		for k, v := range tg.Tags {
			next.Tags[k] = v
		}

		mutate(&next)

		return next
	})
	if !updated {
		return cerrors.Newf(cerrors.NotFound, "backend service %q not found", name)
	}

	return nil
}
