package ec2

import (
	"context"

	"github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/memstore"
)

const (
	asgStatusActive = "active"
	percentDivisor  = 100
)

// CreateAutoScalingGroup creates an auto-scaling group and launches desired instances.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateAutoScalingGroup(
	ctx context.Context, cfg driver.AutoScalingGroupConfig,
) (*driver.AutoScalingGroup, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "ASG name is required")
	}

	if m.asgs.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "ASG %q already exists", cfg.Name)
	}

	if err := validateASGBounds(cfg.DesiredCapacity, cfg.MinSize, cfg.MaxSize); err != nil {
		return nil, err
	}

	instances, err := m.RunInstances(ctx, cfg.InstanceConfig, cfg.DesiredCapacity)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "failed to launch instances: %v", err)
	}

	ids := extractInstanceIDs(instances)

	asg := &asgData{
		config: driver.AutoScalingGroup{
			Name:              cfg.Name,
			MinSize:           cfg.MinSize,
			MaxSize:           cfg.MaxSize,
			DesiredCapacity:   cfg.DesiredCapacity,
			CurrentSize:       len(ids),
			InstanceIDs:       ids,
			Status:            asgStatusActive,
			HealthCheckType:   cfg.HealthCheckType,
			CreatedAt:         m.opts.Clock.Now().UTC().Format(timeFormat),
			Tags:              copyTags(cfg.Tags),
			AvailabilityZones: copyStrings(cfg.AvailabilityZones),
		},
		policies: memstore.New[driver.ScalingPolicy](),
	}

	m.asgs.Set(cfg.Name, asg)

	result := asg.config

	return &result, nil
}

// DeleteAutoScalingGroup deletes an ASG, optionally force-terminating its instances.
func (m *Mock) DeleteAutoScalingGroup(ctx context.Context, name string, forceDelete bool) error {
	asg, ok := m.asgs.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "ASG %q not found", name)
	}

	if !forceDelete && len(asg.config.InstanceIDs) > 0 {
		return cerrors.Newf(cerrors.FailedPrecondition, "ASG %q has instances; use forceDelete", name)
	}

	if forceDelete && len(asg.config.InstanceIDs) > 0 {
		if err := m.TerminateInstances(ctx, asg.config.InstanceIDs); err != nil {
			return err
		}
	}

	m.asgs.Delete(name)

	return nil
}

// GetAutoScalingGroup returns details of an ASG.
func (m *Mock) GetAutoScalingGroup(_ context.Context, name string) (*driver.AutoScalingGroup, error) {
	asg, ok := m.asgs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "ASG %q not found", name)
	}

	result := asg.config

	return &result, nil
}

// ListAutoScalingGroups returns all ASGs.
func (m *Mock) ListAutoScalingGroups(_ context.Context) ([]driver.AutoScalingGroup, error) {
	all := m.asgs.All()
	results := make([]driver.AutoScalingGroup, 0, len(all))

	for _, asg := range all {
		results = append(results, asg.config)
	}

	return results, nil
}

// UpdateAutoScalingGroup updates the capacity settings of an ASG.
func (m *Mock) UpdateAutoScalingGroup(
	ctx context.Context, name string, desired, minSize, maxSize int,
) error {
	if err := validateASGBounds(desired, minSize, maxSize); err != nil {
		return err
	}

	asg, ok := m.asgs.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "ASG %q not found", name)
	}

	asg.config.MinSize = minSize
	asg.config.MaxSize = maxSize

	return m.reconcileASG(ctx, asg, desired)
}

// SetDesiredCapacity sets the desired capacity of an ASG.
func (m *Mock) SetDesiredCapacity(ctx context.Context, name string, desired int) error {
	asg, ok := m.asgs.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "ASG %q not found", name)
	}

	if desired < asg.config.MinSize || desired > asg.config.MaxSize {
		return cerrors.Newf(
			cerrors.InvalidArgument,
			"desired %d outside bounds [%d, %d]", desired, asg.config.MinSize, asg.config.MaxSize,
		)
	}

	return m.reconcileASG(ctx, asg, desired)
}

// reconcileASG scales instances up or down to match the desired capacity.
func (m *Mock) reconcileASG(ctx context.Context, asg *asgData, desired int) error {
	current := len(asg.config.InstanceIDs)

	if desired > current {
		return m.scaleUp(ctx, asg, desired-current)
	}

	if desired < current {
		return m.scaleDown(ctx, asg, current-desired)
	}

	return nil
}

func (m *Mock) scaleUp(ctx context.Context, asg *asgData, count int) error {
	cfg := instanceConfigFromASG(asg)

	instances, err := m.RunInstances(ctx, cfg, count)
	if err != nil {
		return err
	}

	asg.config.InstanceIDs = append(asg.config.InstanceIDs, extractInstanceIDs(instances)...)
	asg.config.CurrentSize = len(asg.config.InstanceIDs)
	asg.config.DesiredCapacity = asg.config.CurrentSize

	return nil
}

func (m *Mock) scaleDown(ctx context.Context, asg *asgData, count int) error {
	ids := asg.config.InstanceIDs

	// Terminate newest first (last added).
	toTerminate := ids[len(ids)-count:]
	remaining := make([]string, len(ids)-count)
	copy(remaining, ids[:len(ids)-count])

	if err := m.TerminateInstances(ctx, toTerminate); err != nil {
		return err
	}

	asg.config.InstanceIDs = remaining
	asg.config.CurrentSize = len(remaining)
	asg.config.DesiredCapacity = asg.config.CurrentSize

	return nil
}

func instanceConfigFromASG(asg *asgData) driver.InstanceConfig {
	return driver.InstanceConfig{
		Tags: copyTags(asg.config.Tags),
	}
}

func validateASGBounds(desired, minSize, maxSize int) error {
	if minSize < 0 {
		return cerrors.New(cerrors.InvalidArgument, "min size must be >= 0")
	}

	if maxSize < minSize {
		return cerrors.New(cerrors.InvalidArgument, "max size must be >= min size")
	}

	if desired < minSize || desired > maxSize {
		return cerrors.Newf(
			cerrors.InvalidArgument,
			"desired capacity %d outside bounds [%d, %d]", desired, minSize, maxSize,
		)
	}

	return nil
}

func extractInstanceIDs(instances []driver.Instance) []string {
	ids := make([]string, len(instances))

	for i := range instances {
		ids[i] = instances[i].ID
	}

	return ids
}

// PutScalingPolicy attaches a scaling policy to an ASG.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) PutScalingPolicy(_ context.Context, policy driver.ScalingPolicy) error {
	asg, ok := m.asgs.Get(policy.AutoScalingGroup)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "ASG %q not found", policy.AutoScalingGroup)
	}

	asg.policies.Set(policy.Name, policy)

	return nil
}

// DeleteScalingPolicy removes a scaling policy from an ASG.
func (m *Mock) DeleteScalingPolicy(_ context.Context, asgName, policyName string) error {
	asg, ok := m.asgs.Get(asgName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "ASG %q not found", asgName)
	}

	if !asg.policies.Delete(policyName) {
		return cerrors.Newf(cerrors.NotFound, "policy %q not found in ASG %q", policyName, asgName)
	}

	return nil
}

// ExecuteScalingPolicy executes a scaling policy on an ASG.
func (m *Mock) ExecuteScalingPolicy(ctx context.Context, asgName, policyName string) error {
	asg, ok := m.asgs.Get(asgName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "ASG %q not found", asgName)
	}

	policy, ok := asg.policies.Get(policyName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "policy %q not found in ASG %q", policyName, asgName)
	}

	newDesired := computeNewDesired(&asg.config, &policy)

	return m.reconcileASG(ctx, asg, newDesired)
}

func computeNewDesired(cfg *driver.AutoScalingGroup, policy *driver.ScalingPolicy) int {
	desired := cfg.DesiredCapacity

	switch policy.AdjustmentType {
	case "ExactCapacity":
		desired = policy.ScalingAdjustment
	case "ChangeInCapacity":
		desired += policy.ScalingAdjustment
	case "PercentChangeInCapacity":
		delta := (desired * policy.ScalingAdjustment) / percentDivisor
		desired += delta
	}

	return clampDesired(desired, cfg.MinSize, cfg.MaxSize)
}

func clampDesired(desired, minSize, maxSize int) int {
	if desired < minSize {
		return minSize
	}

	if desired > maxSize {
		return maxSize
	}

	return desired
}

func copyTags(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}

	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}

	return dst
}

func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}

	dst := make([]string, len(src))
	copy(dst, src)

	return dst
}
