// Package containerregistry provides a portable container registry API with cross-cutting concerns.
package containerregistry

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/containerregistry/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

const serviceName = "containerregistry"

// ContainerRegistry is the portable container registry type wrapping a driver with cross-cutting concerns.
type ContainerRegistry struct {
	driver   driver.ContainerRegistry
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewContainerRegistry creates a new portable ContainerRegistry wrapping the given driver.
func NewContainerRegistry(d driver.ContainerRegistry, opts ...Option) *ContainerRegistry {
	c := &ContainerRegistry{driver: d}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Option configures a portable ContainerRegistry.
type Option func(*ContainerRegistry)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option {
	return func(c *ContainerRegistry) { c.recorder = r }
}

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option {
	return func(c *ContainerRegistry) { c.metrics = m }
}

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option {
	return func(c *ContainerRegistry) { c.limiter = l }
}

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option {
	return func(c *ContainerRegistry) { c.injector = i }
}

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(c *ContainerRegistry) { c.latency = d } }

func (c *ContainerRegistry) do(
	_ context.Context, op string, input any, fn func() (any, error),
) (any, error) {
	start := time.Now()

	if c.injector != nil {
		if err := c.injector.Check(serviceName, op); err != nil {
			c.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if c.limiter != nil {
		if err := c.limiter.Allow(); err != nil {
			c.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if c.latency > 0 {
		time.Sleep(c.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if c.metrics != nil {
		labels := map[string]string{"service": serviceName, "operation": op}
		c.metrics.Counter("calls_total", 1, labels)
		c.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			c.metrics.Counter("errors_total", 1, labels)
		}
	}

	c.rec(op, input, out, err, dur)

	return out, err
}

func (c *ContainerRegistry) rec(op string, input, output any, err error, dur time.Duration) {
	if c.recorder != nil {
		c.recorder.Record(serviceName, op, input, output, err, dur)
	}
}

// CreateRepository creates a new container repository.
func (c *ContainerRegistry) CreateRepository(
	ctx context.Context, config driver.RepositoryConfig,
) (*driver.Repository, error) {
	out, err := c.do(ctx, "CreateRepository", config, func() (any, error) {
		return c.driver.CreateRepository(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Repository), nil
}

// DeleteRepository deletes a container repository.
func (c *ContainerRegistry) DeleteRepository(ctx context.Context, name string, force bool) error {
	_, err := c.do(ctx, "DeleteRepository", map[string]any{"name": name, "force": force}, func() (any, error) {
		return nil, c.driver.DeleteRepository(ctx, name, force)
	})

	return err
}

// GetRepository retrieves repository info.
func (c *ContainerRegistry) GetRepository(ctx context.Context, name string) (*driver.Repository, error) {
	out, err := c.do(ctx, "GetRepository", name, func() (any, error) {
		return c.driver.GetRepository(ctx, name)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Repository), nil
}

// ListRepositories lists all repositories.
func (c *ContainerRegistry) ListRepositories(ctx context.Context) ([]driver.Repository, error) {
	out, err := c.do(ctx, "ListRepositories", nil, func() (any, error) {
		return c.driver.ListRepositories(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Repository), nil
}

// PutImage pushes an image manifest to a repository.
func (c *ContainerRegistry) PutImage(
	ctx context.Context, manifest *driver.ImageManifest,
) (*driver.ImageDetail, error) {
	out, err := c.do(ctx, "PutImage", manifest, func() (any, error) {
		return c.driver.PutImage(ctx, manifest)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ImageDetail), nil
}

// GetImage retrieves image details by repository and reference (tag or digest).
func (c *ContainerRegistry) GetImage(
	ctx context.Context, repository, reference string,
) (*driver.ImageDetail, error) {
	out, err := c.do(ctx, "GetImage", map[string]string{"repository": repository, "reference": reference},
		func() (any, error) {
			return c.driver.GetImage(ctx, repository, reference)
		})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ImageDetail), nil
}

// ListImages lists all images in a repository.
func (c *ContainerRegistry) ListImages(ctx context.Context, repository string) ([]driver.ImageDetail, error) {
	out, err := c.do(ctx, "ListImages", repository, func() (any, error) {
		return c.driver.ListImages(ctx, repository)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.ImageDetail), nil
}

// DeleteImage deletes an image from a repository by reference (tag or digest).
func (c *ContainerRegistry) DeleteImage(ctx context.Context, repository, reference string) error {
	_, err := c.do(ctx, "DeleteImage", map[string]string{"repository": repository, "reference": reference},
		func() (any, error) {
			return nil, c.driver.DeleteImage(ctx, repository, reference)
		})

	return err
}

// TagImage adds a new tag to an existing image.
func (c *ContainerRegistry) TagImage(ctx context.Context, repository, sourceRef, targetTag string) error {
	_, err := c.do(ctx, "TagImage", map[string]string{
		"repository": repository, "sourceRef": sourceRef, "targetTag": targetTag,
	}, func() (any, error) {
		return nil, c.driver.TagImage(ctx, repository, sourceRef, targetTag)
	})

	return err
}

// PutLifecyclePolicy sets a lifecycle policy on a repository.
func (c *ContainerRegistry) PutLifecyclePolicy(
	ctx context.Context, repository string, policy driver.LifecyclePolicy,
) error {
	_, err := c.do(ctx, "PutLifecyclePolicy", map[string]any{
		"repository": repository, "policy": policy,
	}, func() (any, error) {
		return nil, c.driver.PutLifecyclePolicy(ctx, repository, policy)
	})

	return err
}

// GetLifecyclePolicy retrieves the lifecycle policy for a repository.
func (c *ContainerRegistry) GetLifecyclePolicy(
	ctx context.Context, repository string,
) (*driver.LifecyclePolicy, error) {
	out, err := c.do(ctx, "GetLifecyclePolicy", repository, func() (any, error) {
		return c.driver.GetLifecyclePolicy(ctx, repository)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.LifecyclePolicy), nil
}

// EvaluateLifecyclePolicy evaluates the lifecycle policy and returns digests to expire.
func (c *ContainerRegistry) EvaluateLifecyclePolicy(
	ctx context.Context, repository string,
) ([]string, error) {
	out, err := c.do(ctx, "EvaluateLifecyclePolicy", repository, func() (any, error) {
		return c.driver.EvaluateLifecyclePolicy(ctx, repository)
	})
	if err != nil {
		return nil, err
	}

	return out.([]string), nil
}

// StartImageScan starts a vulnerability scan on an image.
func (c *ContainerRegistry) StartImageScan(
	ctx context.Context, repository, reference string,
) (*driver.ScanResult, error) {
	out, err := c.do(ctx, "StartImageScan", map[string]string{
		"repository": repository, "reference": reference,
	}, func() (any, error) {
		return c.driver.StartImageScan(ctx, repository, reference)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ScanResult), nil
}

// GetImageScanResults retrieves scan results for an image.
func (c *ContainerRegistry) GetImageScanResults(
	ctx context.Context, repository, reference string,
) (*driver.ScanResult, error) {
	out, err := c.do(ctx, "GetImageScanResults", map[string]string{
		"repository": repository, "reference": reference,
	}, func() (any, error) {
		return c.driver.GetImageScanResults(ctx, repository, reference)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.ScanResult), nil
}
