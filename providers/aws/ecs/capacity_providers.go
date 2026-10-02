package ecs

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// Capacity-provider types and update states, from the ECS CapacityProvider
// data type.
const (
	cpTypeASG              = "EC2_AUTOSCALING"
	cpTypeManagedInstances = "MANAGED_INSTANCES"
	cpFargate              = "FARGATE"
	cpFargateSpot          = "FARGATE_SPOT"

	// failureMissing is the Failure reason for an id that resolved to nothing.
	failureMissing = "MISSING"

	cpUpdateComplete = "UPDATE_COMPLETE"
	cpDeleteComplete = "DELETE_COMPLETE"

	msEnabledDisabledDefault = "DISABLED"

	// ManagedScaling documented defaults and bounds.
	msDefaultTargetCapacity = 100
	msDefaultMinStep        = 1
	msDefaultMaxStep        = 10000
	msDefaultWarmup         = 300
	msMaxStep               = 10000
	msMaxWarmup             = 10000
	msMaxTargetCapacity     = 100
)

// capacityProviderName is the documented name shape: up to 255 letters,
// numbers, underscores and hyphens.
var capacityProviderName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)

// isBuiltinCapacityProvider reports whether name is one of the predefined
// Fargate capacity providers, which cannot be created, updated, deleted or
// tagged.
func isBuiltinCapacityProvider(name string) bool {
	return name == cpFargate || name == cpFargateSpot
}

// capacityProviderNameOf returns the bare name from a capacity-provider name or
// ARN (…:capacity-provider/name).
func capacityProviderNameOf(id string) string {
	if i := strings.LastIndex(id, "capacity-provider/"); i >= 0 {
		return id[i+len("capacity-provider/"):]
	}

	return id
}

// builtinCapacityProvider renders a predefined Fargate capacity provider.
func (m *Mock) builtinCapacityProvider(region, name string) driver.CapacityProvider {
	return driver.CapacityProvider{
		ARN:    m.arnIn(region, "capacity-provider/"+name),
		Name:   name,
		Status: statusActive,
		Type:   name,
	}
}

// CreateCapacityProvider creates an Auto Scaling group (EC2_AUTOSCALING) or
// Managed Instances (MANAGED_INSTANCES) capacity provider.
//
//nolint:gocritic // in is passed by value to satisfy the driver.ECS interface; the copy is cheap for a mock.
func (m *Mock) CreateCapacityProvider(
	ctx context.Context, in driver.CreateCapacityProviderInput,
) (*driver.CapacityProvider, error) {
	if err := validateCapacityProviderName(in.Name); err != nil {
		return nil, err
	}

	if err := validateTagSet(in.Tags); err != nil {
		return nil, err
	}

	cp, err := m.newCapacityProvider(ctx, &in)
	if err != nil {
		return nil, err
	}

	// A deleted (INACTIVE) provider's name may be reused; an ACTIVE one may not.
	stored := false

	m.capacityProviders.Update(cp.Name, func(existing *driver.CapacityProvider) *driver.CapacityProvider {
		if existing.Status == statusActive {
			return existing
		}

		stored = true

		return cp
	})

	if !stored && !m.capacityProviders.SetIfAbsent(cp.Name, cp) {
		return nil, apiErrf(errors.AlreadyExists, excInvalidParameter,
			"The specified capacity provider %q already exists.", in.Name)
	}

	m.recordTags(cp.ARN, in.Tags)

	out := m.describeCapacityProvider(cp)

	return &out, nil
}

// validateCapacityProviderName enforces the documented name rules.
func validateCapacityProviderName(name string) error {
	if !capacityProviderName.MatchString(name) {
		return apiErrf(errors.InvalidArgument, excInvalidParameter,
			"The capacity provider name must be 1-255 letters, numbers, underscores or hyphens.")
	}

	lower := strings.ToLower(name)
	for _, reserved := range []string{"aws", arnServiceECS, "fargate"} {
		if strings.HasPrefix(lower, reserved) {
			return apiErrf(errors.InvalidArgument, excInvalidParameter,
				"The capacity provider name can't be prefixed with %q.", reserved)
		}
	}

	return nil
}

// newCapacityProvider validates the provider block and builds the stored record.
func (m *Mock) newCapacityProvider(
	ctx context.Context, in *driver.CreateCapacityProviderInput,
) (*driver.CapacityProvider, error) {
	hasASG := in.AutoScalingGroupProvider != nil
	hasMI := len(in.ManagedInstancesProvider) > 0

	if hasASG == hasMI {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"Exactly one of autoScalingGroupProvider or managedInstancesProvider must be specified.")
	}

	cp := &driver.CapacityProvider{
		ARN:    m.arnIn(regionctx.RegionOr(ctx, m.opts.Region), "capacity-provider/"+in.Name),
		Name:   in.Name,
		Status: statusActive,
		Tags:   copyTags(in.Tags),
	}

	if hasMI {
		// Managed Instances capacity providers are cluster-scoped; the cluster
		// is required for them.
		cluster, err := m.capacityProviderCluster(in.Cluster, true)
		if err != nil {
			return nil, err
		}

		cp.Type = cpTypeManagedInstances
		cp.Cluster = cluster

		cp.ManagedInstancesProvider = append(json.RawMessage(nil), in.ManagedInstancesProvider...)

		return cp, nil
	}

	cluster, err := m.capacityProviderCluster(in.Cluster, false)
	if err != nil {
		return nil, err
	}

	asg, err := normalizeASGProvider(in.AutoScalingGroupProvider)
	if err != nil {
		return nil, err
	}

	cp.Type = cpTypeASG
	cp.Cluster = cluster
	cp.AutoScalingGroupProvider = asg

	return cp, nil
}

// capacityProviderCluster resolves the optional cluster a capacity provider is
// scoped to, returning its bare name. required makes an empty value an error.
func (m *Mock) capacityProviderCluster(id string, required bool) (string, error) {
	if id == "" {
		if required {
			return "", apiErrf(errors.InvalidArgument, excInvalidParameter,
				"cluster is required for a Managed Instances capacity provider.")
		}

		return "", nil
	}

	name := resolveClusterName(id)
	if !m.clusterActive(name) {
		return "", apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", name)
	}

	return name, nil
}

// normalizeASGProvider validates a create-time Auto Scaling group provider and
// applies the documented defaults: managed termination protection is off, and
// an omitted managedScaling field takes its documented default value.
func normalizeASGProvider(in *driver.AutoScalingGroupProvider) (*driver.AutoScalingGroupProvider, error) {
	if in.AutoScalingGroupARN == "" {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"autoScalingGroupProvider.autoScalingGroupArn is required.")
	}

	out := cloneASGProvider(in)
	if out.ManagedTerminationProtection == "" {
		out.ManagedTerminationProtection = msEnabledDisabledDefault
	}

	if out.ManagedScaling != nil {
		applyManagedScalingDefaults(out.ManagedScaling)

		if err := validateManagedScaling(out.ManagedScaling); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func applyManagedScalingDefaults(ms *driver.ManagedScaling) {
	defaults := []struct {
		dst *(*int)
		v   int
	}{
		{&ms.TargetCapacity, msDefaultTargetCapacity},
		{&ms.MinimumScalingStepSize, msDefaultMinStep},
		{&ms.MaximumScalingStepSize, msDefaultMaxStep},
		{&ms.InstanceWarmupPeriod, msDefaultWarmup},
	}

	for _, d := range defaults {
		if *d.dst == nil {
			*d.dst = ptrInt(d.v)
		}
	}
}

// validateManagedScaling enforces the documented ManagedScaling ranges on the
// fields that are set.
func validateManagedScaling(ms *driver.ManagedScaling) error {
	checks := []struct {
		name     string
		v        *int
		min, max int
	}{
		{"targetCapacity", ms.TargetCapacity, 1, msMaxTargetCapacity},
		{"minimumScalingStepSize", ms.MinimumScalingStepSize, 1, msMaxStep},
		{"maximumScalingStepSize", ms.MaximumScalingStepSize, 1, msMaxStep},
		{"instanceWarmupPeriod", ms.InstanceWarmupPeriod, 0, msMaxWarmup},
	}

	for _, c := range checks {
		if c.v != nil && (*c.v < c.min || *c.v > c.max) {
			return apiErrf(errors.InvalidArgument, excInvalidParameter,
				"managedScaling.%s must be between %d and %d.", c.name, c.min, c.max)
		}
	}

	if ms.Status != "" && ms.Status != "ENABLED" && ms.Status != "DISABLED" {
		return apiErrf(errors.InvalidArgument, excInvalidParameter,
			"managedScaling.status must be ENABLED or DISABLED.")
	}

	return nil
}

// DescribeCapacityProviders resolves capacity providers by name or ARN. With no
// ids it returns every provider, starting with the predefined FARGATE and
// FARGATE_SPOT. A cluster narrows the result to the providers associated with
// that cluster (through PutClusterCapacityProviders or, for a cluster-scoped
// provider, its own cluster).
func (m *Mock) DescribeCapacityProviders(ctx context.Context, cluster string, ids []string) (
	[]driver.CapacityProvider, []driver.Failure, error,
) {
	region := regionctx.RegionOr(ctx, m.opts.Region)

	inCluster := func(string, string) bool { return true }

	if cluster != "" {
		name := resolveClusterName(cluster)

		c, ok := m.clusters.Get(name)
		if !ok {
			return nil, nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", name)
		}

		associated := make(map[string]bool, len(c.CapacityProviders))
		for _, p := range c.CapacityProviders {
			associated[p] = true
		}

		inCluster = func(cpName, cpCluster string) bool { return associated[cpName] || cpCluster == name }
	}

	if len(ids) == 0 {
		return m.allCapacityProviders(region, inCluster), nil, nil
	}

	found := make([]driver.CapacityProvider, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		cp, ok := m.resolveCapacityProvider(region, id)
		if !ok || !inCluster(cp.Name, cp.Cluster) {
			failures = append(failures, driver.Failure{ARN: id, Reason: failureMissing})
			continue
		}

		found = append(found, cp)
	}

	return found, failures, nil
}

// allCapacityProviders lists the predefined Fargate providers followed by the
// stored ones in name order, filtered by keep.
func (m *Mock) allCapacityProviders(region string, keep func(name, cluster string) bool) []driver.CapacityProvider {
	out := make([]driver.CapacityProvider, 0, m.capacityProviders.Len()+2) //nolint:mnd // the two Fargate providers

	for _, name := range []string{cpFargate, cpFargateSpot} {
		if keep(name, "") {
			out = append(out, m.builtinCapacityProvider(region, name))
		}
	}

	stored := m.capacityProviders.SortedValues()
	sort.SliceStable(stored, func(i, j int) bool { return stored[i].Name < stored[j].Name })

	for _, cp := range stored {
		if keep(cp.Name, cp.Cluster) {
			out = append(out, m.describeCapacityProvider(cp))
		}
	}

	return out
}

// resolveCapacityProvider looks a provider up by name or ARN, including the
// predefined Fargate providers, and returns it described (live tags applied).
func (m *Mock) resolveCapacityProvider(region, id string) (driver.CapacityProvider, bool) {
	name := capacityProviderNameOf(id)
	if isBuiltinCapacityProvider(name) {
		return m.builtinCapacityProvider(region, name), true
	}

	cp, ok := m.capacityProviders.Get(name)
	if !ok {
		return driver.CapacityProvider{}, false
	}

	return m.describeCapacityProvider(cp), true
}

// describeCapacityProvider deep-copies a stored provider and overlays its live
// ARN-keyed tags.
func (m *Mock) describeCapacityProvider(cp *driver.CapacityProvider) driver.CapacityProvider {
	out := cloneCapacityProvider(cp)
	out.Tags = m.liveTags(cp.ARN, cp.Tags)

	return out
}

// UpdateCapacityProvider modifies a provider's Auto Scaling group settings or
// replaces its Managed Instances configuration. The predefined Fargate
// providers cannot be updated.
func (m *Mock) UpdateCapacityProvider(
	_ context.Context, in driver.UpdateCapacityProviderInput,
) (*driver.CapacityProvider, error) {
	name := capacityProviderNameOf(in.Name)
	if isBuiltinCapacityProvider(name) {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"The %s capacity provider is reserved and can't be updated.", name)
	}

	var (
		updated driver.CapacityProvider
		uerr    error
	)

	ok := m.capacityProviders.Update(name, func(cp *driver.CapacityProvider) *driver.CapacityProvider {
		if cp.Status != statusActive {
			uerr = capacityProviderNotFound(in.Name)
			return cp
		}

		updated = cloneCapacityProvider(cp)
		if uerr = applyCapacityProviderUpdate(&updated, &in); uerr != nil {
			return cp
		}

		updated.UpdateStatus = cpUpdateComplete

		return &updated
	})
	if !ok {
		return nil, capacityProviderNotFound(in.Name)
	}

	if uerr != nil {
		return nil, uerr
	}

	out := m.describeCapacityProvider(&updated)

	return &out, nil
}

// applyCapacityProviderUpdate merges an update into a cloned provider. An
// Auto Scaling group block only applies to an EC2_AUTOSCALING provider and a
// Managed Instances block only to a MANAGED_INSTANCES one.
func applyCapacityProviderUpdate(cp *driver.CapacityProvider, in *driver.UpdateCapacityProviderInput) error {
	if in.AutoScalingGroupProvider != nil {
		if cp.Type != cpTypeASG {
			return apiErrf(errors.InvalidArgument, excInvalidParameter,
				"autoScalingGroupProvider can only be updated on an %s capacity provider.", cpTypeASG)
		}

		if err := mergeASGUpdate(cp.AutoScalingGroupProvider, in.AutoScalingGroupProvider); err != nil {
			return err
		}
	}

	if len(in.ManagedInstancesProvider) > 0 {
		if cp.Type != cpTypeManagedInstances {
			return apiErrf(errors.InvalidArgument, excInvalidParameter,
				"managedInstancesProvider can only be updated on a %s capacity provider.", cpTypeManagedInstances)
		}

		cp.ManagedInstancesProvider = append(json.RawMessage(nil), in.ManagedInstancesProvider...)
	}

	return nil
}

// mergeASGUpdate applies the set fields of an AutoScalingGroupProviderUpdate
// onto the stored provider. The Auto Scaling group itself cannot be changed.
func mergeASGUpdate(dst, upd *driver.AutoScalingGroupProvider) error {
	if upd.ManagedTerminationProtection != "" {
		dst.ManagedTerminationProtection = upd.ManagedTerminationProtection
	}

	if upd.ManagedDraining != "" {
		dst.ManagedDraining = upd.ManagedDraining
	}

	if upd.ManagedScaling == nil {
		return nil
	}

	ms := cloneManagedScaling(upd.ManagedScaling)
	applyManagedScalingDefaults(ms)

	if err := validateManagedScaling(ms); err != nil {
		return err
	}

	dst.ManagedScaling = ms

	return nil
}

// DeleteCapacityProvider deletes a capacity provider. The predefined Fargate
// providers are reserved, and a provider still associated with a cluster or
// referenced by a service's strategy must be disassociated first. The deletion
// is synchronous: the provider is returned INACTIVE / DELETE_COMPLETE, and its
// tags are deleted with it.
func (m *Mock) DeleteCapacityProvider(_ context.Context, _, capacityProvider string) (*driver.CapacityProvider, error) {
	name := capacityProviderNameOf(capacityProvider)
	if isBuiltinCapacityProvider(name) {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"The %s capacity provider is reserved and can't be deleted.", name)
	}

	cp, ok := m.capacityProviders.Get(name)
	if !ok || cp.Status != statusActive {
		return nil, capacityProviderNotFound(capacityProvider)
	}

	if err := m.checkCapacityProviderUnused(name); err != nil {
		return nil, err
	}

	deleted := cloneCapacityProvider(cp)
	deleted.Status = statusInactive
	deleted.UpdateStatus = cpDeleteComplete
	deleted.Tags = m.liveTags(cp.ARN, cp.Tags)

	stored := cloneCapacityProvider(&deleted)
	m.capacityProviders.Set(name, &stored)
	m.tags.Delete(cp.ARN)

	return &deleted, nil
}

// checkCapacityProviderUnused refuses a delete while an ACTIVE cluster lists
// the provider or an ACTIVE service's strategy references it.
func (m *Mock) checkCapacityProviderUnused(name string) error {
	for _, c := range m.clusters.All() {
		if c.Status != statusActive {
			continue
		}

		for _, p := range c.CapacityProviders {
			if p == name {
				return apiErrf(errors.FailedPrecondition, excInvalidParameter,
					"The capacity provider %q is associated with cluster %q. "+
						"Remove it with PutClusterCapacityProviders before deleting it.", name, c.Name)
			}
		}
	}

	for _, s := range m.services.All() {
		if s.Status != statusActive {
			continue
		}

		for _, item := range s.CapacityProviderStrategy {
			if item.CapacityProvider == name {
				return apiErrf(errors.FailedPrecondition, excInvalidParameter,
					"The capacity provider %q is in use by service %q.", name, s.Name)
			}
		}
	}

	return nil
}

func capacityProviderNotFound(id string) error {
	return apiErrf(errors.NotFound, excClient, "The specified capacity provider %q does not exist.", id)
}
