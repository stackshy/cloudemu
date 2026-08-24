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
		}
		m.funcs.Set(name, fd)

		return nil
	}

	if cfg.VPCConfig != nil {
		fd.awsConfig.VPCConfig = cloneVPCConfig(cfg.VPCConfig)
	}

	if cfg.DeadLetterConfig != nil {
		fd.awsConfig.DeadLetterConfig = cloneDeadLetterConfig(cfg.DeadLetterConfig)
	}

	if cfg.TracingConfig != nil {
		fd.awsConfig.TracingConfig = cloneTracingConfig(cfg.TracingConfig)
	}

	m.funcs.Set(name, fd)

	return nil
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
	}, nil
}
