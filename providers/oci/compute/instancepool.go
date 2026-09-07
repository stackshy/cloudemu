package compute

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Instance pool lifecycle states.
const (
	poolRunning = "RUNNING"
	poolStopped = "STOPPED"
)

// Actions an instance pool accepts.
const (
	PoolActionStart = "START"
	PoolActionStop  = "STOP"
	PoolActionReset = "RESET"
)

// percent is the divisor a PercentChangeInCapacity adjustment scales by.
const percent = 100

// maxPoolSize bounds a pool, so an oversized size cannot drive an unbounded
// number of launches.
const maxPoolSize = 1000

type poolData struct {
	ID              string
	Name            string
	Size            int
	MinSize         int
	MaxSize         int
	State           string
	CreatedAt       string
	Tags            map[string]string
	ConfigurationID string
	Placements      []PoolPlacement
	LoadBalancers   []PoolLoadBalancer
	InstanceIDs     []string
	HealthCheckType string
	HealthGrace     int
	Launch          driver.InstanceConfig
	policies        *memstore.Store[driver.ScalingPolicy]
}

// CreateAutoScalingGroup creates an instance pool and launches its instances.
//
//nolint:gocritic // hugeParam: the driver interface fixes the signature.
func (m *Mock) CreateAutoScalingGroup(
	ctx context.Context, cfg driver.AutoScalingGroupConfig,
) (*driver.AutoScalingGroup, error) {
	if err := m.reservePool(cfg); err != nil {
		return nil, err
	}

	// The instances are launched with m.mu released: a launch reaches into
	// the VCN service for a VNIC, and Compute's lock must not span that.
	if err := m.syncPool(ctx, cfg.Name); err != nil {
		return nil, err
	}

	return m.GetAutoScalingGroup(ctx, cfg.Name)
}

// reservePool stores an empty instance pool, validating the request first.
//
//nolint:gocritic // hugeParam: mirrors CreateAutoScalingGroup.
func (m *Mock) reservePool(cfg driver.AutoScalingGroupConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "instance pool name is required")
	}

	if m.poolByName(cfg.Name) != nil {
		return cerrors.Newf(cerrors.AlreadyExists, "instance pool %q already exists", cfg.Name)
	}

	if err := validateSize(cfg.DesiredCapacity, cfg.MinSize, cfg.MaxSize); err != nil {
		return err
	}

	id := m.newOCID(typeInstancePool)
	p := &poolData{
		ID:              id,
		Name:            cfg.Name,
		Size:            cfg.DesiredCapacity,
		MinSize:         cfg.MinSize,
		MaxSize:         cfg.MaxSize,
		State:           poolRunning,
		CreatedAt:       m.now(),
		Tags:            copyTags(cfg.Tags),
		HealthCheckType: cfg.HealthCheckType,
		HealthGrace:     cfg.HealthCheckGrace,
		Launch:          cfg.InstanceConfig,
		InstanceIDs:     []string{},
		policies:        memstore.New[driver.ScalingPolicy](),
	}

	if len(cfg.AvailabilityZones) > 0 {
		p.Placements = make([]PoolPlacement, 0, len(cfg.AvailabilityZones))
		for _, ad := range cfg.AvailabilityZones {
			p.Placements = append(p.Placements, PoolPlacement{
				AvailabilityDomain: ad,
				PrimarySubnetID:    cfg.InstanceConfig.SubnetID,
			})
		}
	}

	m.pools.Set(id, p)
	m.record(id)

	return nil
}

// validateSize rejects a desired capacity outside its bounds.
func validateSize(desired, minSize, maxSize int) error {
	if desired < 0 || minSize < 0 || maxSize < 0 {
		return cerrors.New(cerrors.InvalidArgument, "instance pool sizes cannot be negative")
	}

	if maxSize > 0 && minSize > maxSize {
		return cerrors.Newf(cerrors.InvalidArgument, "min size %d exceeds max size %d", minSize, maxSize)
	}

	if desired > maxPoolSize {
		return cerrors.Newf(cerrors.InvalidArgument,
			"instance pool size %d exceeds the maximum of %d", desired, maxPoolSize)
	}

	if maxSize > 0 && desired > maxSize {
		return cerrors.Newf(cerrors.InvalidArgument, "desired capacity %d exceeds max size %d", desired, maxSize)
	}

	if desired < minSize {
		return cerrors.Newf(cerrors.InvalidArgument, "desired capacity %d is below min size %d", desired, minSize)
	}

	return nil
}

// poolDelta is what a pool still has to do to reach its size.
type poolDelta struct {
	PoolID string
	Launch driver.InstanceConfig
	Want   int
	Have   int
	// Newest is the instance a shrink terminates next.
	Newest string
}

// syncPool brings a pool's membership to its size, one instance at a time. It
// must be called without m.mu held: launching and terminating both take it.
func (m *Mock) syncPool(ctx context.Context, name string) error {
	for {
		delta, ok := m.poolDelta(name)
		if !ok {
			return poolNotFound(name)
		}

		switch {
		case delta.Have < delta.Want:
			instances, err := m.RunInstances(ctx, delta.Launch, 1)
			if err != nil {
				return err
			}

			m.addPoolMember(delta.PoolID, instances[0].ID)
		case delta.Have > delta.Want:
			if err := m.TerminateInstance(ctx, delta.Newest, false); err != nil {
				return err
			}

			m.removePoolMember(delta.PoolID, delta.Newest)
		default:
			return nil
		}
	}
}

// poolDelta reports what a pool still has to do to reach its size.
func (m *Mock) poolDelta(name string) (poolDelta, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p := m.poolByName(name)
	if p == nil {
		return poolDelta{}, false
	}

	delta := poolDelta{PoolID: p.ID, Launch: p.Launch, Want: p.Size, Have: len(p.InstanceIDs)}
	if len(p.InstanceIDs) > 0 {
		delta.Newest = p.InstanceIDs[len(p.InstanceIDs)-1]
	}

	return delta, true
}

func (m *Mock) addPoolMember(poolID, instanceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pools.Update(poolID, func(p *poolData) *poolData {
		p.InstanceIDs = appendItem(p.InstanceIDs, instanceID)

		return p
	})

	m.details.Update(instanceID, func(d InstanceDetails) InstanceDetails {
		d.InstancePoolID = poolID

		return d
	})
}

func (m *Mock) removePoolMember(poolID, instanceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pools.Update(poolID, func(p *poolData) *poolData {
		out := make([]string, 0, len(p.InstanceIDs))

		for _, id := range p.InstanceIDs {
			if id != instanceID {
				out = append(out, id)
			}
		}

		p.InstanceIDs = out

		return p
	})
}

// DeleteAutoScalingGroup terminates an instance pool and its instances. Real
// OCI refuses a non-empty pool unless the caller forces it.
func (m *Mock) DeleteAutoScalingGroup(ctx context.Context, name string, forceDelete bool) error {
	id, members, err := m.poolMembers(name)
	if err != nil {
		return err
	}

	if len(members) > 0 && !forceDelete {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"instance pool %q still has %d instances", name, len(members))
	}

	for _, instanceID := range members {
		if err := m.TerminateInstance(ctx, instanceID, false); err != nil {
			return err
		}
	}

	return m.dropPool(id)
}

// TerminateInstancePool terminates a pool by OCID, which is how OCI addresses
// it. Its instances go with it.
func (m *Mock) TerminateInstancePool(ctx context.Context, id string) error {
	name, err := m.poolName(id)
	if err != nil {
		return err
	}

	return m.DeleteAutoScalingGroup(ctx, name, true)
}

func (m *Mock) poolMembers(name string) (id string, members []string, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p := m.poolByName(name)
	if p == nil {
		return "", nil, poolNotFound(name)
	}

	return p.ID, copyStrings(p.InstanceIDs), nil
}

func (m *Mock) poolName(id string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.pools.Get(id)
	if !ok {
		return "", poolNotFound(id)
	}

	return p.Name, nil
}

func (m *Mock) dropPool(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.pools.Has(id) {
		return poolNotFound(id)
	}

	m.pools.Delete(id)
	m.forget(id)

	return nil
}

// GetAutoScalingGroup returns an instance pool by display name.
func (m *Mock) GetAutoScalingGroup(_ context.Context, name string) (*driver.AutoScalingGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p := m.poolByName(name)
	if p == nil {
		return nil, poolNotFound(name)
	}

	out := toASG(p)

	return &out, nil
}

// ListAutoScalingGroups returns every instance pool.
func (m *Mock) ListAutoScalingGroups(_ context.Context) ([]driver.AutoScalingGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.pools, nil, toASG), nil
}

// UpdateAutoScalingGroup resizes an instance pool.
func (m *Mock) UpdateAutoScalingGroup(ctx context.Context, name string, desired, minSize, maxSize int) error {
	if err := m.resizePool(name, desired, minSize, maxSize); err != nil {
		return err
	}

	return m.syncPool(ctx, name)
}

// SetDesiredCapacity resizes an instance pool to a new instance count.
func (m *Mock) SetDesiredCapacity(ctx context.Context, name string, desired int) error {
	if err := m.resizePool(name, desired, -1, -1); err != nil {
		return err
	}

	return m.syncPool(ctx, name)
}

// resizePool records a pool's new bounds. A negative bound leaves it alone.
func (m *Mock) resizePool(name string, desired, minSize, maxSize int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p := m.poolByName(name)
	if p == nil {
		return poolNotFound(name)
	}

	wantMin, wantMax := p.MinSize, p.MaxSize
	if minSize >= 0 {
		wantMin = minSize
	}

	if maxSize >= 0 {
		wantMax = maxSize
	}

	if err := validateSize(desired, wantMin, wantMax); err != nil {
		return err
	}

	m.pools.Update(p.ID, func(p *poolData) *poolData {
		p.Size, p.MinSize, p.MaxSize = desired, wantMin, wantMax

		return p
	})

	return nil
}

// PutScalingPolicy records a scaling policy on an instance pool. Real OCI
// keeps these in the separate Autoscaling API (/20181001), which this handler
// does not serve; the policy is stored so the portable API round-trips it.
//
//nolint:gocritic // hugeParam: the driver interface fixes the signature.
func (m *Mock) PutScalingPolicy(_ context.Context, policy driver.ScalingPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p := m.poolByName(policy.AutoScalingGroup)
	if p == nil {
		return poolNotFound(policy.AutoScalingGroup)
	}

	if policy.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "scaling policy name is required")
	}

	p.policies.Set(policy.Name, policy)

	return nil
}

// DeleteScalingPolicy removes a scaling policy from an instance pool.
func (m *Mock) DeleteScalingPolicy(_ context.Context, asgName, policyName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p := m.poolByName(asgName)
	if p == nil {
		return poolNotFound(asgName)
	}

	if !p.policies.Delete(policyName) {
		return cerrors.Newf(cerrors.NotFound, "scaling policy %q not found", policyName)
	}

	return nil
}

// ExecuteScalingPolicy applies a scaling policy's adjustment to its pool.
func (m *Mock) ExecuteScalingPolicy(ctx context.Context, asgName, policyName string) error {
	desired, err := m.policyTarget(asgName, policyName)
	if err != nil {
		return err
	}

	return m.SetDesiredCapacity(ctx, asgName, desired)
}

// policyTarget resolves the size a policy's adjustment asks for.
func (m *Mock) policyTarget(asgName, policyName string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p := m.poolByName(asgName)
	if p == nil {
		return 0, poolNotFound(asgName)
	}

	policy, ok := p.policies.Get(policyName)
	if !ok {
		return 0, cerrors.Newf(cerrors.NotFound, "scaling policy %q not found", policyName)
	}

	var desired int

	switch policy.AdjustmentType {
	case "ExactCapacity":
		desired = policy.ScalingAdjustment
	case "PercentChangeInCapacity":
		desired = p.Size + p.Size*policy.ScalingAdjustment/percent
	default:
		desired = p.Size + policy.ScalingAdjustment
	}

	if desired < p.MinSize {
		desired = p.MinSize
	}

	if p.MaxSize > 0 && desired > p.MaxSize {
		desired = p.MaxSize
	}

	return desired, nil
}

// CreateInstancePool creates a pool from an instance configuration, which is
// how OCI creates one.
func (m *Mock) CreateInstancePool(
	ctx context.Context, displayName, configurationID string, size int,
	placements []PoolPlacement, tags map[string]string,
) (*InstancePool, error) {
	launch, err := m.launchConfigOf(configurationID, poolOverrides(placements))
	if err != nil {
		return nil, err
	}

	name := orDefault(displayName, configurationID)

	cfg := driver.AutoScalingGroupConfig{
		Name:            name,
		MinSize:         0,
		MaxSize:         maxPoolSize,
		DesiredCapacity: size,
		InstanceConfig:  launch,
		Tags:            tags,
	}

	for _, p := range placements {
		cfg.AvailabilityZones = append(cfg.AvailabilityZones, p.AvailabilityDomain)
	}

	if err := m.reservePool(cfg); err != nil {
		return nil, err
	}

	if err := m.bindPoolConfiguration(name, configurationID, placements); err != nil {
		return nil, err
	}

	if err := m.syncPool(ctx, name); err != nil {
		return nil, err
	}

	return m.instancePoolByName(name)
}

// poolOverrides turns a pool's placement into the launch overrides its
// instances take.
func poolOverrides(placements []PoolPlacement) *LaunchSpec {
	if len(placements) == 0 {
		return nil
	}

	return &LaunchSpec{
		AvailabilityDomain: placements[0].AvailabilityDomain,
		SubnetID:           placements[0].PrimarySubnetID,
	}
}

func (m *Mock) bindPoolConfiguration(name, configurationID string, placements []PoolPlacement) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p := m.poolByName(name)
	if p == nil {
		return poolNotFound(name)
	}

	m.pools.Update(p.ID, func(p *poolData) *poolData {
		p.ConfigurationID = configurationID
		p.Placements = placements

		return p
	})

	return nil
}

// GetInstancePool returns one instance pool by OCID.
func (m *Mock) GetInstancePool(_ context.Context, id string) (*InstancePool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.pools.Get(id)
	if !ok {
		return nil, poolNotFound(id)
	}

	out := toInstancePool(p)

	return &out, nil
}

func (m *Mock) instancePoolByName(name string) (*InstancePool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p := m.poolByName(name)
	if p == nil {
		return nil, poolNotFound(name)
	}

	out := toInstancePool(p)

	return &out, nil
}

// ListInstancePools returns the instance pools in a compartment.
func (m *Mock) ListInstancePools(_ context.Context, compartmentID string) ([]InstancePool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]InstancePool, 0)

	for _, p := range m.pools.SortedValues() {
		if s, _ := m.scopes.Get(p.ID); s.Compartment != compartmentID {
			continue
		}

		out = append(out, toInstancePool(p))
	}

	return out, nil
}

// UpdateInstancePool changes a pool's display name, size and tags by OCID.
func (m *Mock) UpdateInstancePool(ctx context.Context, id string, upd Update, size int) (*InstancePool, error) {
	name, err := m.poolName(id)
	if err != nil {
		return nil, err
	}

	if size >= 0 {
		if err := m.resizePool(name, size, -1, -1); err != nil {
			return nil, err
		}

		if err := m.syncPool(ctx, name); err != nil {
			return nil, err
		}
	}

	if err := m.renamePool(id, upd); err != nil {
		return nil, err
	}

	return m.GetInstancePool(ctx, id)
}

func (m *Mock) renamePool(id string, upd Update) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.pools.Has(id) {
		return poolNotFound(id)
	}

	m.pools.Update(id, func(p *poolData) *poolData {
		if upd.DisplayName != nil {
			p.Name = *upd.DisplayName
		}

		if upd.Tags != nil {
			p.Tags = mergeTags(p.Tags, upd.Tags)
		}

		return p
	})

	return nil
}

// InstancePoolAction starts, stops or resets every instance in a pool.
func (m *Mock) InstancePoolAction(ctx context.Context, id, action string) (*InstancePool, error) {
	members, err := m.poolMemberIDs(id)
	if err != nil {
		return nil, err
	}

	var state string

	switch action {
	case PoolActionStart:
		err, state = m.StartInstances(ctx, members), poolRunning
	case PoolActionStop:
		err, state = m.StopInstances(ctx, members), poolStopped
	case PoolActionReset:
		err, state = m.RebootInstances(ctx, members), poolRunning
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"unsupported instance pool action %q: use START, STOP or RESET", action)
	}

	if err != nil {
		return nil, err
	}

	m.setPoolState(id, state)

	return m.GetInstancePool(ctx, id)
}

func (m *Mock) poolMemberIDs(id string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.pools.Get(id)
	if !ok {
		return nil, poolNotFound(id)
	}

	return copyStrings(p.InstanceIDs), nil
}

func (m *Mock) setPoolState(id, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pools.Update(id, func(p *poolData) *poolData {
		p.State = state

		return p
	})
}

// ListInstancePoolInstances returns a pool's member instances.
func (m *Mock) ListInstancePoolInstances(_ context.Context, id string) ([]PoolInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.pools.Get(id)
	if !ok {
		return nil, poolNotFound(id)
	}

	out := make([]PoolInstance, 0, len(p.InstanceIDs))

	for _, instanceID := range p.InstanceIDs {
		inst, held := m.instances.Get(instanceID)
		if !held {
			continue
		}

		d, _ := m.details.Get(instanceID)
		out = append(out, PoolInstance{
			ID:                 instanceID,
			InstanceID:         instanceID,
			AvailabilityDomain: inst.AD,
			DisplayName:        d.DisplayName,
			Shape:              inst.Shape,
			State:              inst.State,
			TimeCreated:        inst.LaunchTime,
		})
	}

	return out, nil
}

// poolByName finds an instance pool by display name. The caller holds m.mu.
func (m *Mock) poolByName(name string) *poolData {
	for _, p := range m.pools.SortedValues() {
		if p.Name == name {
			return p
		}
	}

	return nil
}

func toASG(p *poolData) driver.AutoScalingGroup {
	zones := make([]string, 0, len(p.Placements))
	for _, pl := range p.Placements {
		zones = append(zones, pl.AvailabilityDomain)
	}

	status := "Active"
	if p.State == poolStopped {
		status = "Stopped"
	}

	return driver.AutoScalingGroup{
		Name:              p.Name,
		MinSize:           p.MinSize,
		MaxSize:           p.MaxSize,
		DesiredCapacity:   p.Size,
		CurrentSize:       len(p.InstanceIDs),
		InstanceIDs:       copyStrings(p.InstanceIDs),
		Status:            status,
		HealthCheckType:   p.HealthCheckType,
		CreatedAt:         p.CreatedAt,
		Tags:              copyTags(p.Tags),
		AvailabilityZones: zones,
	}
}

func toInstancePool(p *poolData) InstancePool {
	return InstancePool{
		ID:                      p.ID,
		DisplayName:             p.Name,
		InstanceConfigurationID: p.ConfigurationID,
		Size:                    p.Size,
		LifecycleState:          p.State,
		Placements:              p.Placements,
		LoadBalancers:           p.LoadBalancers,
		InstanceIDs:             copyStrings(p.InstanceIDs),
		TimeCreated:             p.CreatedAt,
		Tags:                    copyTags(p.Tags),
	}
}

func poolNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "instance pool %q not found", id)
}
