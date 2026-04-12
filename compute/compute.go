package compute

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Compute is the portable compute type wrapping a driver with cross-cutting concerns.
type Compute struct {
	driver   driver.Compute
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewCompute creates a new portable Compute wrapping the given driver.
func NewCompute(d driver.Compute, opts ...Option) *Compute {
	c := &Compute{driver: d}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Option configures a portable Compute.
type Option func(*Compute)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(c *Compute) { c.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(c *Compute) { c.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(c *Compute) { c.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(c *Compute) { c.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(c *Compute) { c.latency = d } }

func (c *Compute) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if c.injector != nil {
		if err := c.injector.Check("compute", op); err != nil {
			c.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if c.limiter != nil {
		if err := c.limiter.Allow(); err != nil {
			c.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if c.latency > 0 {
		time.Sleep(c.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if c.metrics != nil {
		labels := map[string]string{"service": "compute", "operation": op}
		c.metrics.Counter("calls_total", 1, labels)
		c.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			c.metrics.Counter("errors_total", 1, labels)
		}
	}

	c.record(op, input, out, err, dur)

	return out, err
}

func (c *Compute) record(op string, input, output any, err error, dur time.Duration) {
	if c.recorder != nil {
		c.recorder.Record("compute", op, input, output, err, dur)
	}
}

// RunInstances creates new VM instances.
//
//nolint:gocritic // config passed by value to match driver.Compute interface pattern
func (c *Compute) RunInstances(ctx context.Context, config driver.InstanceConfig, count int) ([]driver.Instance, error) {
	out, err := c.do(ctx, "RunInstances", config, func() (any, error) {
		return c.driver.RunInstances(ctx, config, count)
	})

	if err != nil {
		return nil, err
	}

	return out.([]driver.Instance), nil
}

// StartInstances starts stopped instances.
func (c *Compute) StartInstances(ctx context.Context, instanceIDs []string) error {
	_, err := c.do(ctx, "StartInstances", instanceIDs, func() (any, error) {
		return nil, c.driver.StartInstances(ctx, instanceIDs)
	})

	return err
}

// StopInstances stops running instances.
func (c *Compute) StopInstances(ctx context.Context, instanceIDs []string) error {
	_, err := c.do(ctx, "StopInstances", instanceIDs, func() (any, error) {
		return nil, c.driver.StopInstances(ctx, instanceIDs)
	})

	return err
}

// RebootInstances reboots running instances.
func (c *Compute) RebootInstances(ctx context.Context, instanceIDs []string) error {
	_, err := c.do(ctx, "RebootInstances", instanceIDs, func() (any, error) {
		return nil, c.driver.RebootInstances(ctx, instanceIDs)
	})

	return err
}

// TerminateInstances terminates instances.
func (c *Compute) TerminateInstances(ctx context.Context, instanceIDs []string) error {
	_, err := c.do(ctx, "TerminateInstances", instanceIDs, func() (any, error) {
		return nil, c.driver.TerminateInstances(ctx, instanceIDs)
	})

	return err
}

// DescribeInstances describes instances.
func (c *Compute) DescribeInstances(ctx context.Context, instanceIDs []string, filters []driver.DescribeFilter) ([]driver.Instance, error) {
	out, err := c.do(ctx, "DescribeInstances", instanceIDs, func() (any, error) {
		return c.driver.DescribeInstances(ctx, instanceIDs, filters)
	})

	if err != nil {
		return nil, err
	}

	return out.([]driver.Instance), nil
}

// ModifyInstance modifies an instance.
func (c *Compute) ModifyInstance(ctx context.Context, instanceID string, input driver.ModifyInstanceInput) error {
	_, err := c.do(ctx, "ModifyInstance", input, func() (any, error) {
		return nil, c.driver.ModifyInstance(ctx, instanceID, input)
	})

	return err
}

// CreateAutoScalingGroup creates an auto-scaling group.
//
//nolint:gocritic // hugeParam: config passed by value to match driver.Compute interface pattern
func (c *Compute) CreateAutoScalingGroup(
	ctx context.Context, config driver.AutoScalingGroupConfig,
) (*driver.AutoScalingGroup, error) {
	out, err := c.do(ctx, "CreateAutoScalingGroup", config, func() (any, error) {
		return c.driver.CreateAutoScalingGroup(ctx, config)
	})

	if err != nil {
		return nil, err
	}

	return out.(*driver.AutoScalingGroup), nil
}

// DeleteAutoScalingGroup deletes an auto-scaling group.
func (c *Compute) DeleteAutoScalingGroup(ctx context.Context, name string, forceDelete bool) error {
	_, err := c.do(ctx, "DeleteAutoScalingGroup", name, func() (any, error) {
		return nil, c.driver.DeleteAutoScalingGroup(ctx, name, forceDelete)
	})

	return err
}

// GetAutoScalingGroup returns an auto-scaling group.
func (c *Compute) GetAutoScalingGroup(ctx context.Context, name string) (*driver.AutoScalingGroup, error) {
	out, err := c.do(ctx, "GetAutoScalingGroup", name, func() (any, error) {
		return c.driver.GetAutoScalingGroup(ctx, name)
	})

	if err != nil {
		return nil, err
	}

	return out.(*driver.AutoScalingGroup), nil
}

// ListAutoScalingGroups lists all auto-scaling groups.
func (c *Compute) ListAutoScalingGroups(ctx context.Context) ([]driver.AutoScalingGroup, error) {
	out, err := c.do(ctx, "ListAutoScalingGroups", nil, func() (any, error) {
		return c.driver.ListAutoScalingGroups(ctx)
	})

	if err != nil {
		return nil, err
	}

	return out.([]driver.AutoScalingGroup), nil
}

// UpdateAutoScalingGroup updates an auto-scaling group.
func (c *Compute) UpdateAutoScalingGroup(ctx context.Context, name string, desired, minSize, maxSize int) error {
	_, err := c.do(ctx, "UpdateAutoScalingGroup", name, func() (any, error) {
		return nil, c.driver.UpdateAutoScalingGroup(ctx, name, desired, minSize, maxSize)
	})

	return err
}

// SetDesiredCapacity sets the desired capacity of an auto-scaling group.
func (c *Compute) SetDesiredCapacity(ctx context.Context, name string, desired int) error {
	_, err := c.do(ctx, "SetDesiredCapacity", name, func() (any, error) {
		return nil, c.driver.SetDesiredCapacity(ctx, name, desired)
	})

	return err
}

// PutScalingPolicy attaches a scaling policy to an auto-scaling group.
//
//nolint:gocritic // hugeParam: policy passed by value to match driver.Compute interface pattern
func (c *Compute) PutScalingPolicy(ctx context.Context, policy driver.ScalingPolicy) error {
	_, err := c.do(ctx, "PutScalingPolicy", policy, func() (any, error) {
		return nil, c.driver.PutScalingPolicy(ctx, policy)
	})

	return err
}

// DeleteScalingPolicy removes a scaling policy.
func (c *Compute) DeleteScalingPolicy(ctx context.Context, asgName, policyName string) error {
	_, err := c.do(ctx, "DeleteScalingPolicy", asgName, func() (any, error) {
		return nil, c.driver.DeleteScalingPolicy(ctx, asgName, policyName)
	})

	return err
}

// ExecuteScalingPolicy executes a scaling policy.
func (c *Compute) ExecuteScalingPolicy(ctx context.Context, asgName, policyName string) error {
	_, err := c.do(ctx, "ExecuteScalingPolicy", asgName, func() (any, error) {
		return nil, c.driver.ExecuteScalingPolicy(ctx, asgName, policyName)
	})

	return err
}

// RequestSpotInstances creates spot/preemptible instance requests.
//
//nolint:gocritic // hugeParam: config passed by value to match driver.Compute interface pattern
func (c *Compute) RequestSpotInstances(
	ctx context.Context, config driver.SpotRequestConfig,
) ([]driver.SpotInstanceRequest, error) {
	out, err := c.do(ctx, "RequestSpotInstances", config, func() (any, error) {
		return c.driver.RequestSpotInstances(ctx, config)
	})

	if err != nil {
		return nil, err
	}

	return out.([]driver.SpotInstanceRequest), nil
}

// CancelSpotRequests cancels spot/preemptible instance requests.
func (c *Compute) CancelSpotRequests(ctx context.Context, requestIDs []string) error {
	_, err := c.do(ctx, "CancelSpotRequests", requestIDs, func() (any, error) {
		return nil, c.driver.CancelSpotRequests(ctx, requestIDs)
	})

	return err
}

// DescribeSpotRequests describes spot/preemptible instance requests.
func (c *Compute) DescribeSpotRequests(
	ctx context.Context, requestIDs []string,
) ([]driver.SpotInstanceRequest, error) {
	out, err := c.do(ctx, "DescribeSpotRequests", requestIDs, func() (any, error) {
		return c.driver.DescribeSpotRequests(ctx, requestIDs)
	})

	if err != nil {
		return nil, err
	}

	return out.([]driver.SpotInstanceRequest), nil
}

// CreateLaunchTemplate creates a launch template.
//
//nolint:gocritic // hugeParam: config passed by value to match driver.Compute interface pattern
func (c *Compute) CreateLaunchTemplate(
	ctx context.Context, config driver.LaunchTemplateConfig,
) (*driver.LaunchTemplate, error) {
	out, err := c.do(ctx, "CreateLaunchTemplate", config, func() (any, error) {
		return c.driver.CreateLaunchTemplate(ctx, config)
	})

	if err != nil {
		return nil, err
	}

	return out.(*driver.LaunchTemplate), nil
}

// DeleteLaunchTemplate deletes a launch template.
func (c *Compute) DeleteLaunchTemplate(ctx context.Context, name string) error {
	_, err := c.do(ctx, "DeleteLaunchTemplate", name, func() (any, error) {
		return nil, c.driver.DeleteLaunchTemplate(ctx, name)
	})

	return err
}

// GetLaunchTemplate returns a launch template.
func (c *Compute) GetLaunchTemplate(ctx context.Context, name string) (*driver.LaunchTemplate, error) {
	out, err := c.do(ctx, "GetLaunchTemplate", name, func() (any, error) {
		return c.driver.GetLaunchTemplate(ctx, name)
	})

	if err != nil {
		return nil, err
	}

	return out.(*driver.LaunchTemplate), nil
}

// ListLaunchTemplates lists all launch templates.
func (c *Compute) ListLaunchTemplates(ctx context.Context) ([]driver.LaunchTemplate, error) {
	out, err := c.do(ctx, "ListLaunchTemplates", nil, func() (any, error) {
		return c.driver.ListLaunchTemplates(ctx)
	})

	if err != nil {
		return nil, err
	}

	return out.([]driver.LaunchTemplate), nil
}

// CreateVolume creates a new block storage volume.
func (c *Compute) CreateVolume(ctx context.Context, cfg driver.VolumeConfig) (*driver.VolumeInfo, error) {
	out, err := c.do(ctx, "CreateVolume", cfg, func() (any, error) { return c.driver.CreateVolume(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.VolumeInfo), nil
}

// DeleteVolume deletes a volume.
func (c *Compute) DeleteVolume(ctx context.Context, id string) error {
	_, err := c.do(ctx, "DeleteVolume", id, func() (any, error) { return nil, c.driver.DeleteVolume(ctx, id) })
	return err
}

// DescribeVolumes returns volumes matching the given IDs.
func (c *Compute) DescribeVolumes(ctx context.Context, ids []string) ([]driver.VolumeInfo, error) {
	out, err := c.do(ctx, "DescribeVolumes", ids, func() (any, error) { return c.driver.DescribeVolumes(ctx, ids) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.VolumeInfo), nil
}

// AttachVolume attaches a volume to an instance.
func (c *Compute) AttachVolume(ctx context.Context, volumeID, instanceID, device string) error {
	_, err := c.do(ctx, "AttachVolume", volumeID, func() (any, error) {
		return nil, c.driver.AttachVolume(ctx, volumeID, instanceID, device)
	})

	return err
}

// DetachVolume detaches a volume.
func (c *Compute) DetachVolume(ctx context.Context, volumeID string) error {
	_, err := c.do(ctx, "DetachVolume", volumeID, func() (any, error) { return nil, c.driver.DetachVolume(ctx, volumeID) })
	return err
}

// CreateSnapshot creates a volume snapshot.
func (c *Compute) CreateSnapshot(ctx context.Context, cfg driver.SnapshotConfig) (*driver.SnapshotInfo, error) {
	out, err := c.do(ctx, "CreateSnapshot", cfg, func() (any, error) { return c.driver.CreateSnapshot(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.SnapshotInfo), nil
}

// DeleteSnapshot deletes a snapshot.
func (c *Compute) DeleteSnapshot(ctx context.Context, id string) error {
	_, err := c.do(ctx, "DeleteSnapshot", id, func() (any, error) { return nil, c.driver.DeleteSnapshot(ctx, id) })
	return err
}

// DescribeSnapshots returns snapshots matching the given IDs.
func (c *Compute) DescribeSnapshots(ctx context.Context, ids []string) ([]driver.SnapshotInfo, error) {
	out, err := c.do(ctx, "DescribeSnapshots", ids, func() (any, error) { return c.driver.DescribeSnapshots(ctx, ids) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.SnapshotInfo), nil
}

// CreateImage creates a machine image from an instance.
func (c *Compute) CreateImage(ctx context.Context, cfg driver.ImageConfig) (*driver.ImageInfo, error) {
	out, err := c.do(ctx, "CreateImage", cfg, func() (any, error) { return c.driver.CreateImage(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.ImageInfo), nil
}

// DeregisterImage deregisters a machine image.
func (c *Compute) DeregisterImage(ctx context.Context, id string) error {
	_, err := c.do(ctx, "DeregisterImage", id, func() (any, error) { return nil, c.driver.DeregisterImage(ctx, id) })
	return err
}

// DescribeImages returns images matching the given IDs.
func (c *Compute) DescribeImages(ctx context.Context, ids []string) ([]driver.ImageInfo, error) {
	out, err := c.do(ctx, "DescribeImages", ids, func() (any, error) { return c.driver.DescribeImages(ctx, ids) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.ImageInfo), nil
}

// CreateKeyPair creates a new key pair.
func (c *Compute) CreateKeyPair(ctx context.Context, cfg driver.KeyPairConfig) (*driver.KeyPairInfo, error) {
	out, err := c.do(ctx, "CreateKeyPair", cfg, func() (any, error) { return c.driver.CreateKeyPair(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.KeyPairInfo), nil
}

// DeleteKeyPair deletes a key pair by name.
func (c *Compute) DeleteKeyPair(ctx context.Context, name string) error {
	_, err := c.do(ctx, "DeleteKeyPair", name, func() (any, error) { return nil, c.driver.DeleteKeyPair(ctx, name) })
	return err
}

// DescribeKeyPairs returns key pairs matching the given names.
func (c *Compute) DescribeKeyPairs(ctx context.Context, names []string) ([]driver.KeyPairInfo, error) {
	out, err := c.do(ctx, "DescribeKeyPairs", names, func() (any, error) { return c.driver.DescribeKeyPairs(ctx, names) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.KeyPairInfo), nil
}
