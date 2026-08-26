package lambda

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// SetFunctionAWSConfig stores the AWS Lambda-only settings (VpcConfig,
// DeadLetterConfig, TracingConfig) for a function. When create is true, AWS
// create-time defaults apply (TracingConfig defaults to {Mode: "PassThrough"});
// when false, only the non-nil fields are overlaid, leaving the rest unchanged.
// It implements the AWS-only optional interface the Lambda server type-asserts
// for, keeping these AWS-specific settings off the provider-agnostic Serverless
// surface.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) SetFunctionAWSConfig(_ context.Context, name string, cfg driver.AWSFunctionConfig, create bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	if create {
		fd.awsConfig = driver.AWSFunctionConfig{
			VPCConfig:        cloneVPCConfig(cfg.VPCConfig),
			DeadLetterConfig: cloneDeadLetterConfig(cfg.DeadLetterConfig),
			TracingConfig:    tracingConfigOrDefault(cfg.TracingConfig),
			Architectures:    cloneArchitectures(cfg.Architectures),
			EphemeralStorage: cloneEphemeralStorage(cfg.EphemeralStorage),
			Layers:           cloneLayers(cfg.Layers),
		}
		m.funcs.Set(name, fd)

		return nil
	}

	overlayAWSConfig(&fd.awsConfig, cfg)
	m.funcs.Set(name, fd)

	return nil
}

// overlayAWSConfig applies only the non-nil fields of cfg onto dst, leaving the
// rest of the stored AWS-only settings unchanged (the UpdateFunctionConfiguration
// merge semantics).
//
//nolint:gocritic // hugeParam: cfg mirrors the SetFunctionAWSConfig value receiver.
func overlayAWSConfig(dst *driver.AWSFunctionConfig, cfg driver.AWSFunctionConfig) {
	if cfg.VPCConfig != nil {
		dst.VPCConfig = cloneVPCConfig(cfg.VPCConfig)
	}

	if cfg.DeadLetterConfig != nil {
		dst.DeadLetterConfig = cloneDeadLetterConfig(cfg.DeadLetterConfig)
	}

	if cfg.TracingConfig != nil {
		dst.TracingConfig = cloneTracingConfig(cfg.TracingConfig)
	}

	if len(cfg.Architectures) > 0 {
		dst.Architectures = cloneArchitectures(cfg.Architectures)
	}

	if cfg.EphemeralStorage != nil {
		dst.EphemeralStorage = cloneEphemeralStorage(cfg.EphemeralStorage)
	}

	if cfg.Layers != nil {
		dst.Layers = cloneLayers(cfg.Layers)
	}
}

// GetFunctionAWSConfig returns a copy of the stored AWS-only settings for a
// function.
func (m *Mock) GetFunctionAWSConfig(_ context.Context, name string) (driver.AWSFunctionConfig, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return driver.AWSFunctionConfig{}, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	return driver.AWSFunctionConfig{
		VPCConfig:        cloneVPCConfig(fd.awsConfig.VPCConfig),
		DeadLetterConfig: cloneDeadLetterConfig(fd.awsConfig.DeadLetterConfig),
		TracingConfig:    cloneTracingConfig(fd.awsConfig.TracingConfig),
		Architectures:    cloneArchitectures(fd.awsConfig.Architectures),
		EphemeralStorage: cloneEphemeralStorage(fd.awsConfig.EphemeralStorage),
		Layers:           cloneLayers(fd.awsConfig.Layers),
	}, nil
}
