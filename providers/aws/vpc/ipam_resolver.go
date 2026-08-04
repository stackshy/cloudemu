package vpc

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// CreateIpamPrefixListResolver creates a prefix-list resolver in an IPAM.
func (m *Mock) CreateIpamPrefixListResolver(
	_ context.Context, ipamID, addressFamily, description string, tags map[string]string,
) (*driver.IpamPrefixListResolver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ipam, ok := m.ipams.Get(ipamID)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "ipam %q not found", ipamID)
	}

	id := idgen.GenerateID("ipam-pl-res-")
	r := &driver.IpamPrefixListResolver{
		ID: id, ARN: m.ipamARN("ipam-prefix-list-resolver/" + id), IpamID: ipamID, IpamARN: ipam.ARN,
		IpamRegion: ipam.Region, OwnerID: m.opts.AccountID, AddressFamily: orDefaultStr(addressFamily, "ipv4"),
		Description: description, State: "create-complete", LastVersionCreationStatus: "success", Tags: copyTags(tags),
	}
	m.ipamResolvers.Set(id, r)

	out := cloneResolver(r)

	return &out, nil
}

// DescribeIpamPrefixListResolvers returns resolvers matching ids.
func (m *Mock) DescribeIpamPrefixListResolvers(_ context.Context, ids []string) ([]driver.IpamPrefixListResolver, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.ipamResolvers, ids, cloneResolver), nil
}

// ModifyIpamPrefixListResolver updates a resolver's description.
func (m *Mock) ModifyIpamPrefixListResolver(_ context.Context, id, description string) (*driver.IpamPrefixListResolver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.ipamResolvers.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam prefix list resolver %q not found", id)
	}

	r.Description = description

	out := cloneResolver(r)

	return &out, nil
}

// DeleteIpamPrefixListResolver deletes a resolver with no targets.
func (m *Mock) DeleteIpamPrefixListResolver(_ context.Context, id string) (*driver.IpamPrefixListResolver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.ipamResolvers.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam prefix list resolver %q not found", id)
	}

	for _, t := range m.ipamResolverTargets.SortedValues() {
		if t.ResolverID == id {
			return nil, errors.Newf(errors.FailedPrecondition, "resolver %q has targets", id)
		}
	}

	r.State = ipamStateDeleteComplete

	m.ipamResolvers.Delete(id)

	out := cloneResolver(r)

	return &out, nil
}

// CreateIpamPrefixListResolverTarget adds a managed prefix list as a sync target.
func (m *Mock) CreateIpamPrefixListResolverTarget(
	_ context.Context, resolverID, prefixListID, prefixListRegion string,
	desiredVersion int, trackLatest bool, tags map[string]string,
) (*driver.IpamPrefixListResolverTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.ipamResolvers.Has(resolverID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam prefix list resolver %q not found", resolverID)
	}

	id := idgen.GenerateID("ipam-pl-res-target-")
	t := &driver.IpamPrefixListResolverTarget{
		ID: id, ARN: m.ipamARN("ipam-prefix-list-resolver-target/" + id), ResolverID: resolverID,
		OwnerID: m.opts.AccountID, PrefixListID: prefixListID, PrefixListRegion: orDefaultStr(prefixListRegion, m.opts.Region),
		DesiredVersion: desiredVersion, LastSyncedVersion: desiredVersion, TrackLatestVersion: trackLatest,
		State: "create-complete", Tags: copyTags(tags),
	}
	m.ipamResolverTargets.Set(id, t)

	out := cloneResolverTarget(t)

	return &out, nil
}

// DescribeIpamPrefixListResolverTargets returns targets matching ids.
func (m *Mock) DescribeIpamPrefixListResolverTargets(_ context.Context, ids []string) ([]driver.IpamPrefixListResolverTarget, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.ipamResolverTargets, ids, cloneResolverTarget), nil
}

// ModifyIpamPrefixListResolverTarget updates a target's version tracking.
func (m *Mock) ModifyIpamPrefixListResolverTarget(
	_ context.Context, id string, desiredVersion int, trackLatest bool,
) (*driver.IpamPrefixListResolverTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.ipamResolverTargets.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam prefix list resolver target %q not found", id)
	}

	if desiredVersion > 0 {
		t.DesiredVersion = desiredVersion
		t.LastSyncedVersion = desiredVersion
	}

	t.TrackLatestVersion = trackLatest

	out := cloneResolverTarget(t)

	return &out, nil
}

// DeleteIpamPrefixListResolverTarget removes a sync target.
func (m *Mock) DeleteIpamPrefixListResolverTarget(_ context.Context, id string) (*driver.IpamPrefixListResolverTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.ipamResolverTargets.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam prefix list resolver target %q not found", id)
	}

	t.State = ipamStateDeleteComplete

	m.ipamResolverTargets.Delete(id)

	out := cloneResolverTarget(t)

	return &out, nil
}

// GetIpamPrefixListResolverRules returns a resolver's rules. The emulator does
// not model a rule engine, so it derives one rule per pool in the same IPAM.
func (m *Mock) GetIpamPrefixListResolverRules(_ context.Context, resolverID string) ([]driver.IpamPrefixListResolverRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.ipamResolvers.Get(resolverID)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "ipam prefix list resolver %q not found", resolverID)
	}

	var out []driver.IpamPrefixListResolverRule

	for _, p := range m.ipamPools.SortedValues() {
		if scope := m.scopeByARN(p.IpamScopeARN); scope != nil && scope.IpamARN == r.IpamARN {
			out = append(out, driver.IpamPrefixListResolverRule{IpamPoolID: p.ID})
		}
	}

	return out, nil
}

// GetIpamPrefixListResolverVersions returns the resolver's published versions.
func (m *Mock) GetIpamPrefixListResolverVersions(_ context.Context, resolverID string) ([]driver.IpamPrefixListResolverVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.ipamResolvers.Has(resolverID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam prefix list resolver %q not found", resolverID)
	}

	return []driver.IpamPrefixListResolverVersion{{Version: 1, CreatedAt: time.Unix(0, 0).UTC()}}, nil
}

// GetIpamPrefixListResolverVersionEntries returns the CIDR entries of a version.
func (m *Mock) GetIpamPrefixListResolverVersionEntries(_ context.Context, resolverID string, _ int) ([]driver.PrefixListEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.ipamResolvers.Has(resolverID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam prefix list resolver %q not found", resolverID)
	}

	return nil, nil
}

// CreateIpamExternalResourceVerificationToken issues a verification token.
func (m *Mock) CreateIpamExternalResourceVerificationToken(
	_ context.Context, ipamID, tokenName string, tags map[string]string,
) (*driver.IpamExternalResourceVerificationToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ipam, ok := m.ipams.Get(ipamID)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "ipam %q not found", ipamID)
	}

	id := idgen.GenerateID("ipam-ext-verify-token-")
	t := &driver.IpamExternalResourceVerificationToken{
		ID: id, ARN: m.ipamARN("ipam-external-resource-verification-token/" + id), IpamID: ipamID, IpamARN: ipam.ARN,
		IpamRegion: ipam.Region, OwnerID: m.opts.AccountID, TokenName: tokenName,
		TokenValue: idgen.GenerateID("token-"), NotAfter: time.Unix(0, 0).UTC(),
		State: "create-complete", Status: "valid", Tags: copyTags(tags),
	}
	m.ipamTokens.Set(id, t)

	out := cloneToken(t)

	return &out, nil
}

// DeleteIpamExternalResourceVerificationToken deletes a verification token.
func (m *Mock) DeleteIpamExternalResourceVerificationToken(
	_ context.Context, id string,
) (*driver.IpamExternalResourceVerificationToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.ipamTokens.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam verification token %q not found", id)
	}

	t.State = ipamStateDeleteComplete

	m.ipamTokens.Delete(id)

	out := cloneToken(t)

	return &out, nil
}

// DescribeIpamExternalResourceVerificationTokens returns tokens matching ids.
func (m *Mock) DescribeIpamExternalResourceVerificationTokens(
	_ context.Context, ids []string,
) ([]driver.IpamExternalResourceVerificationToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.ipamTokens, ids, cloneToken), nil
}

func cloneResolver(r *driver.IpamPrefixListResolver) driver.IpamPrefixListResolver {
	out := *r
	out.Tags = copyTags(r.Tags)

	return out
}

func cloneResolverTarget(t *driver.IpamPrefixListResolverTarget) driver.IpamPrefixListResolverTarget {
	out := *t
	out.Tags = copyTags(t.Tags)

	return out
}

func cloneToken(t *driver.IpamExternalResourceVerificationToken) driver.IpamExternalResourceVerificationToken {
	out := *t
	out.Tags = copyTags(t.Tags)

	return out
}
