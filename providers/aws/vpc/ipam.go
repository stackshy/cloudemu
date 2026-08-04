package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	ipamStateDeleteComplete = "delete-complete"
	ipamStateDeprovisioned  = "deprovisioned"
)

// ipamARN builds an IPAM-family ARN. IPAM ARNs carry no region segment
// (arn:aws:ec2::<account>:<resource>), matching the real service.
func (m *Mock) ipamARN(resource string) string {
	return idgen.AWSARN("ec2", "", m.opts.AccountID, resource)
}

// CreateIpam creates an IPAM plus its public and private default scopes.
func (m *Mock) CreateIpam(_ context.Context, cfg driver.IpamConfig) (*driver.Ipam, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := idgen.GenerateID("ipam-")
	ipamARN := m.ipamARN("ipam/" + id)

	pub := m.newDefaultScope(ipamARN, "public")
	priv := m.newDefaultScope(ipamARN, "private")

	ipam := &driver.Ipam{
		ID:                    id,
		ARN:                   ipamARN,
		Region:                m.opts.Region,
		PublicDefaultScopeID:  pub.ID,
		PrivateDefaultScopeID: priv.ID,
		ScopeCount:            2,
		OperatingRegions:      cfg.OperatingRegions,
		Description:           cfg.Description,
		Tier:                  orDefaultStr(cfg.Tier, "advanced"),
		State:                 "create-complete",
		Tags:                  copyTags(cfg.Tags),
	}

	// A new IPAM gets a default resource discovery associated with it.
	rd := m.newResourceDiscovery(true, "", nil)
	assoc := m.newRDAssociation(ipam, rd.ID, true, nil)
	ipam.DefaultResourceDiscoveryID = rd.ID
	ipam.DefaultResourceDiscoveryAssociationID = assoc.ID
	ipam.ResourceDiscoveryAssociationCount = 1

	m.ipams.Set(id, ipam)

	out := cloneIpam(ipam)

	return &out, nil
}

// newDefaultScope creates and stores a default scope for an IPAM. Caller holds mu.
func (m *Mock) newDefaultScope(ipamARN, scopeType string) *driver.IpamScope {
	id := idgen.GenerateID("ipam-scope-")
	scope := &driver.IpamScope{
		ID:        id,
		ARN:       m.ipamARN("ipam-scope/" + id),
		IpamARN:   ipamARN,
		ScopeType: scopeType,
		IsDefault: true,
		State:     "create-complete",
	}
	m.ipamScopes.Set(id, scope)

	return scope
}

// DescribeIpams returns IPAMs matching ids (all if empty).
func (m *Mock) DescribeIpams(_ context.Context, ids []string) ([]driver.Ipam, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.ipams, ids, cloneIpam), nil
}

// ModifyIpam updates an IPAM's description.
func (m *Mock) ModifyIpam(_ context.Context, id, description string) (*driver.Ipam, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ipam, ok := m.ipams.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam %q not found", id)
	}

	ipam.Description = description

	out := cloneIpam(ipam)

	return &out, nil
}

// DeleteIpam deletes an IPAM and its default scopes. Non-default scopes or
// pools referencing the IPAM block deletion.
//
//nolint:gocyclo // sequential precondition checks; flattening hurts readability
func (m *Mock) DeleteIpam(_ context.Context, id string) (*driver.Ipam, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ipam, ok := m.ipams.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam %q not found", id)
	}

	for _, s := range m.ipamScopes.SortedValues() {
		if s.IpamARN == ipam.ARN && !s.IsDefault {
			return nil, errors.Newf(errors.FailedPrecondition, "ipam %q has non-default scopes", id)
		}
	}

	if m.poolsInScopes(ipam.ARN) {
		return nil, errors.Newf(errors.FailedPrecondition, "ipam %q has pools", id)
	}

	for _, a := range m.ipamRDAssociations.SortedValues() {
		if a.IpamID == id && !a.IsDefault {
			return nil, errors.Newf(errors.FailedPrecondition, "ipam %q has resource-discovery associations", id)
		}
	}

	for _, s := range m.ipamScopes.SortedValues() {
		if s.IpamARN == ipam.ARN {
			m.ipamScopes.Delete(s.ID)
		}
	}

	// Tear down the default resource discovery + association.
	for _, a := range m.ipamRDAssociations.SortedValues() {
		if a.IpamID == id {
			m.ipamRDAssociations.Delete(a.ID)
		}
	}

	m.ipamDiscoveries.Delete(ipam.DefaultResourceDiscoveryID)

	ipam.State = ipamStateDeleteComplete

	m.ipams.Delete(id)

	out := cloneIpam(ipam)

	return &out, nil
}

// CreateIpamScope creates a non-default (private) scope within an IPAM.
func (m *Mock) CreateIpamScope(_ context.Context, cfg driver.IpamScopeConfig) (*driver.IpamScope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ipam, ok := m.ipams.Get(cfg.IpamID)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "ipam %q not found", cfg.IpamID)
	}

	id := idgen.GenerateID("ipam-scope-")
	scope := &driver.IpamScope{
		ID:          id,
		ARN:         m.ipamARN("ipam-scope/" + id),
		IpamARN:     ipam.ARN,
		ScopeType:   "private",
		IsDefault:   false,
		Description: cfg.Description,
		State:       "create-complete",
		Tags:        copyTags(cfg.Tags),
	}
	m.ipamScopes.Set(id, scope)

	ipam.ScopeCount++

	out := cloneIpamScope(scope)

	return &out, nil
}

// DescribeIpamScopes returns scopes matching ids (all if empty).
func (m *Mock) DescribeIpamScopes(_ context.Context, ids []string) ([]driver.IpamScope, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.ipamScopes, ids, cloneIpamScope), nil
}

// ModifyIpamScope updates a scope's description.
func (m *Mock) ModifyIpamScope(_ context.Context, id, description string) (*driver.IpamScope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	scope, ok := m.ipamScopes.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam scope %q not found", id)
	}

	scope.Description = description

	out := cloneIpamScope(scope)

	return &out, nil
}

// DeleteIpamScope deletes a non-default scope with no pools.
func (m *Mock) DeleteIpamScope(_ context.Context, id string) (*driver.IpamScope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	scope, ok := m.ipamScopes.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam scope %q not found", id)
	}

	if scope.IsDefault {
		return nil, errors.Newf(errors.FailedPrecondition, "cannot delete default scope %q", id)
	}

	for _, p := range m.ipamPools.SortedValues() {
		if p.IpamScopeARN == scope.ARN {
			return nil, errors.Newf(errors.FailedPrecondition, "ipam scope %q has pools", id)
		}
	}

	scope.State = ipamStateDeleteComplete

	m.ipamScopes.Delete(id)

	if ipam := m.ipamByARN(scope.IpamARN); ipam != nil {
		ipam.ScopeCount--
	}

	out := cloneIpamScope(scope)

	return &out, nil
}

// CreateIpamPool creates a CIDR pool in a scope.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateIpamPool(_ context.Context, cfg driver.IpamPoolConfig) (*driver.IpamPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	scope, ok := m.ipamScopes.Get(cfg.IpamScopeID)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "ipam scope %q not found", cfg.IpamScopeID)
	}

	id := idgen.GenerateID("ipam-pool-")
	pool := &driver.IpamPool{
		ID:                             id,
		ARN:                            m.ipamARN("ipam-pool/" + id),
		IpamScopeARN:                   scope.ARN,
		IpamScopeType:                  scope.ScopeType,
		AddressFamily:                  orDefaultStr(cfg.AddressFamily, "ipv4"),
		Locale:                         orDefaultStr(cfg.Locale, "None"),
		PoolDepth:                      1,
		Description:                    cfg.Description,
		State:                          "create-complete",
		AllocationMinNetmaskLength:     cfg.AllocationMinNetmaskLength,
		AllocationMaxNetmaskLength:     cfg.AllocationMaxNetmaskLength,
		AllocationDefaultNetmaskLength: cfg.AllocationDefaultNetmaskLength,
		Tags:                           copyTags(cfg.Tags),
	}
	m.ipamPools.Set(id, pool)

	scope.PoolCount++

	out := cloneIpamPool(pool)

	return &out, nil
}

// DescribeIpamPools returns pools matching ids (all if empty).
func (m *Mock) DescribeIpamPools(_ context.Context, ids []string) ([]driver.IpamPool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.ipamPools, ids, cloneIpamPool), nil
}

// ModifyIpamPool updates a pool's description.
func (m *Mock) ModifyIpamPool(_ context.Context, id, description string) (*driver.IpamPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.ipamPools.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam pool %q not found", id)
	}

	pool.Description = description

	out := cloneIpamPool(pool)

	return &out, nil
}

// DeleteIpamPool deletes a pool with no live allocations.
func (m *Mock) DeleteIpamPool(_ context.Context, id string) (*driver.IpamPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.ipamPools.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "ipam pool %q not found", id)
	}

	for _, a := range m.ipamAllocations.SortedValues() {
		if m.ipamPoolByAllocation[a.ID] == id {
			return nil, errors.Newf(errors.FailedPrecondition, "ipam pool %q has allocations", id)
		}
	}

	for _, c := range m.ipamPoolCidrs.SortedValues() {
		if m.ipamPoolByCidr[c.ID] == id {
			m.ipamPoolCidrs.Delete(c.ID)
			delete(m.ipamPoolByCidr, c.ID)
		}
	}

	pool.State = ipamStateDeleteComplete

	m.ipamPools.Delete(id)

	if scope := m.scopeByARN(pool.IpamScopeARN); scope != nil {
		scope.PoolCount--
	}

	out := cloneIpamPool(pool)

	return &out, nil
}

// ProvisionIpamPoolCidr adds a CIDR to a pool's supply.
func (m *Mock) ProvisionIpamPoolCidr(_ context.Context, poolID, cidr string, netmaskLength int) (*driver.IpamPoolCidr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.ipamPools.Has(poolID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam pool %q not found", poolID)
	}

	if cidr == "" && netmaskLength == 0 {
		return nil, errors.New(errors.InvalidArgument, "a cidr or netmaskLength is required")
	}

	id := idgen.GenerateID("ipam-pool-cidr-")
	pc := &driver.IpamPoolCidr{
		ID:            id,
		CIDR:          cidr,
		NetmaskLength: netmaskLength,
		State:         "provisioned",
	}
	m.ipamPoolCidrs.Set(id, pc)
	m.ipamPoolByCidr[id] = poolID

	out := *pc

	return &out, nil
}

// DeprovisionIpamPoolCidr removes a provisioned CIDR from a pool.
func (m *Mock) DeprovisionIpamPoolCidr(_ context.Context, poolID, cidr string) (*driver.IpamPoolCidr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.ipamPools.Has(poolID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam pool %q not found", poolID)
	}

	for _, c := range m.ipamPoolCidrs.SortedValues() {
		if m.ipamPoolByCidr[c.ID] != poolID || c.CIDR != cidr {
			continue
		}

		c.State = ipamStateDeprovisioned

		m.ipamPoolCidrs.Delete(c.ID)
		delete(m.ipamPoolByCidr, c.ID)

		out := *c

		return &out, nil
	}

	return nil, errors.Newf(errors.NotFound, "cidr %q not provisioned in pool %q", cidr, poolID)
}

// GetIpamPoolCidrs returns the CIDRs provisioned into a pool.
func (m *Mock) GetIpamPoolCidrs(_ context.Context, poolID string) ([]driver.IpamPoolCidr, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.ipamPools.Has(poolID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam pool %q not found", poolID)
	}

	var out []driver.IpamPoolCidr

	for _, c := range m.ipamPoolCidrs.SortedValues() {
		if m.ipamPoolByCidr[c.ID] == poolID {
			out = append(out, *c)
		}
	}

	return out, nil
}

// AllocateIpamPoolCidr hands out a CIDR from a pool.
func (m *Mock) AllocateIpamPoolCidr(_ context.Context, cfg driver.AllocateIpamPoolCidrConfig) (*driver.IpamPoolAllocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.ipamPools.Has(cfg.IpamPoolID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam pool %q not found", cfg.IpamPoolID)
	}

	if cfg.CIDR == "" && cfg.NetmaskLength == 0 {
		return nil, errors.New(errors.InvalidArgument, "a cidr or netmaskLength is required")
	}

	id := idgen.GenerateID("ipam-pool-alloc-")
	alloc := &driver.IpamPoolAllocation{
		ID:           id,
		CIDR:         cfg.CIDR,
		ResourceType: "custom",
		Description:  cfg.Description,
		Tags:         copyTags(cfg.Tags),
	}
	m.ipamAllocations.Set(id, alloc)
	m.ipamPoolByAllocation[id] = cfg.IpamPoolID

	out := cloneIpamAllocation(alloc)

	return &out, nil
}

// ReleaseIpamPoolAllocation frees an allocation.
func (m *Mock) ReleaseIpamPoolAllocation(_ context.Context, poolID, allocationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ipamPoolByAllocation[allocationID] != poolID || !m.ipamAllocations.Has(allocationID) {
		return errors.Newf(errors.NotFound, "allocation %q not found in pool %q", allocationID, poolID)
	}

	m.ipamAllocations.Delete(allocationID)
	delete(m.ipamPoolByAllocation, allocationID)

	return nil
}

// GetIpamPoolAllocations returns the allocations handed out from a pool.
func (m *Mock) GetIpamPoolAllocations(_ context.Context, poolID string) ([]driver.IpamPoolAllocation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.ipamPools.Has(poolID) {
		return nil, errors.Newf(errors.InvalidArgument, "ipam pool %q not found", poolID)
	}

	var out []driver.IpamPoolAllocation

	for _, a := range m.ipamAllocations.SortedValues() {
		if m.ipamPoolByAllocation[a.ID] == poolID {
			out = append(out, cloneIpamAllocation(a))
		}
	}

	return out, nil
}

// ModifyIpamPoolAllocation updates an allocation's description.
func (m *Mock) ModifyIpamPoolAllocation(_ context.Context, allocationID, description string) (*driver.IpamPoolAllocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	alloc, ok := m.ipamAllocations.Get(allocationID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "allocation %q not found", allocationID)
	}

	alloc.Description = description

	out := cloneIpamAllocation(alloc)

	return &out, nil
}

// ---- internal lookups (caller holds mu) ----

func (m *Mock) ipamByARN(arn string) *driver.Ipam {
	for _, i := range m.ipams.SortedValues() {
		if i.ARN == arn {
			return i
		}
	}

	return nil
}

func (m *Mock) scopeByARN(arn string) *driver.IpamScope {
	for _, s := range m.ipamScopes.SortedValues() {
		if s.ARN == arn {
			return s
		}
	}

	return nil
}

func (m *Mock) poolsInScopes(ipamARN string) bool {
	scopeARNs := make(map[string]bool)

	for _, s := range m.ipamScopes.SortedValues() {
		if s.IpamARN == ipamARN {
			scopeARNs[s.ARN] = true
		}
	}

	for _, p := range m.ipamPools.SortedValues() {
		if scopeARNs[p.IpamScopeARN] {
			return true
		}
	}

	return false
}

// ---- clones ----

func cloneIpam(i *driver.Ipam) driver.Ipam {
	out := *i
	out.OperatingRegions = append([]string(nil), i.OperatingRegions...)
	out.Tags = copyTags(i.Tags)

	return out
}

func cloneIpamScope(s *driver.IpamScope) driver.IpamScope {
	out := *s
	out.Tags = copyTags(s.Tags)

	return out
}

func cloneIpamPool(p *driver.IpamPool) driver.IpamPool {
	out := *p
	out.Tags = copyTags(p.Tags)

	return out
}

func cloneIpamAllocation(a *driver.IpamPoolAllocation) driver.IpamPoolAllocation {
	out := *a
	out.Tags = copyTags(a.Tags)

	return out
}
