package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// fullWarmup is the warmup percentage of a fully warmed dedicated IP.
const fullWarmup = 100

// CreateDedicatedIPPool registers a dedicated IP pool.
func (m *Mock) CreateDedicatedIPPool(_ context.Context, name, scalingMode string, tags map[string]string) error {
	if name == "" {
		return cerrors.New(cerrors.InvalidArgument, "PoolName is required")
	}

	if scalingMode == "" {
		scalingMode = driver.ScalingModeStandard
	}

	pool := &driver.DedicatedIPPool{
		Name:        name,
		ScalingMode: scalingMode,
		CreatedAt:   m.now(),
		Tags:        copyTags(tags),
	}

	if !m.ipPools.SetIfAbsent(name, pool) {
		return cerrors.Newf(cerrors.AlreadyExists, "dedicated IP pool %q already exists", name)
	}

	return nil
}

// DeleteDedicatedIPPool removes a dedicated IP pool.
func (m *Mock) DeleteDedicatedIPPool(_ context.Context, name string) error {
	if !m.ipPools.Delete(name) {
		return errIPPoolNotFound(name)
	}

	return nil
}

// GetDedicatedIPPool returns a dedicated IP pool by name.
func (m *Mock) GetDedicatedIPPool(_ context.Context, name string) (*driver.DedicatedIPPool, error) {
	p, ok := m.ipPools.Get(name)
	if !ok {
		return nil, errIPPoolNotFound(name)
	}

	out := *p
	out.Tags = copyTags(p.Tags)

	return &out, nil
}

// ListDedicatedIPPools returns all pool names ordered.
func (m *Mock) ListDedicatedIPPools(_ context.Context) ([]string, error) {
	all := m.ipPools.SortedValues()
	out := make([]string, 0, len(all))

	for _, p := range all {
		out = append(out, p.Name)
	}

	return out, nil
}

// GetDedicatedIP returns a dedicated IP by address.
func (m *Mock) GetDedicatedIP(_ context.Context, ip string) (*driver.DedicatedIP, error) {
	d, ok := m.dedicatedIps.Get(ip)
	if !ok {
		return nil, errDedicatedIPNotFound(ip)
	}

	out := *d

	return &out, nil
}

// GetDedicatedIPs returns dedicated IPs, optionally filtered by pool.
func (m *Mock) GetDedicatedIPs(_ context.Context, poolName string) ([]driver.DedicatedIP, error) {
	all := m.dedicatedIps.SortedValues()
	out := make([]driver.DedicatedIP, 0, len(all))

	for _, d := range all {
		if poolName != "" && d.PoolName != poolName {
			continue
		}

		out = append(out, *d)
	}

	return out, nil
}

// PutDedicatedIPInPool assigns an IP to a pool, creating the IP record if new.
func (m *Mock) PutDedicatedIPInPool(_ context.Context, ip, destinationPool string) error {
	if !m.ipPools.Has(destinationPool) {
		return errIPPoolNotFound(destinationPool)
	}

	if m.dedicatedIps.Update(ip, func(d *driver.DedicatedIP) *driver.DedicatedIP {
		d.PoolName = destinationPool

		return d
	}) {
		return nil
	}

	m.dedicatedIps.Set(ip, &driver.DedicatedIP{
		IP:           ip,
		PoolName:     destinationPool,
		WarmupStatus: driver.WarmupStatusDone,
		WarmupPct:    fullWarmup,
	})

	return nil
}

// PutDedicatedIPPoolScalingAttributes sets a pool's scaling mode.
func (m *Mock) PutDedicatedIPPoolScalingAttributes(_ context.Context, poolName, scalingMode string) error {
	if !m.ipPools.Update(poolName, func(p *driver.DedicatedIPPool) *driver.DedicatedIPPool {
		p.ScalingMode = scalingMode

		return p
	}) {
		return errIPPoolNotFound(poolName)
	}

	return nil
}

// PutDedicatedIPWarmupAttributes sets an IP's warmup percentage.
func (m *Mock) PutDedicatedIPWarmupAttributes(_ context.Context, ip string, warmupPct int32) error {
	status := driver.WarmupStatusInProgress
	if warmupPct >= fullWarmup {
		status = driver.WarmupStatusDone
	}

	if m.dedicatedIps.Update(ip, func(d *driver.DedicatedIP) *driver.DedicatedIP {
		d.WarmupPct = warmupPct
		d.WarmupStatus = status

		return d
	}) {
		return nil
	}

	m.dedicatedIps.Set(ip, &driver.DedicatedIP{IP: ip, WarmupPct: warmupPct, WarmupStatus: status})

	return nil
}

// PutAccountDedicatedIPWarmupAttributes toggles account-wide auto-warmup.
func (m *Mock) PutAccountDedicatedIPWarmupAttributes(_ context.Context, autoWarmupEnabled bool) error {
	m.dashMu.Lock()
	defer m.dashMu.Unlock()

	m.autoWarmupEnabled = autoWarmupEnabled

	return nil
}

func errIPPoolNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "dedicated IP pool %q does not exist", name)
}

func errDedicatedIPNotFound(ip string) error {
	return cerrors.Newf(cerrors.NotFound, "dedicated IP %q does not exist", ip)
}
