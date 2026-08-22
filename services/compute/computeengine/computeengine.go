// Package computeengine wires an optional real compute engine into a compute
// provider's instance lifecycle. It is shared by every VM-style provider (AWS
// EC2, Azure Virtual Machines, GCP Compute Engine) so the provision/deprovision/
// console-output hook stays identical across clouds and cannot drift.
package computeengine

import (
	"context"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Provision backs the instance with the engine when one is configured, running
// the instance's decoded UserData as the boot script and overriding the
// instance's private IP with the reachable address the engine surfaces (when
// non-empty). No-op when engine is nil, leaving the synthetic private IP.
func Provision(ctx context.Context, engine config.ComputeEngine, inst *driver.Instance, cfg *driver.InstanceConfig) error {
	if engine == nil {
		return nil
	}

	res, err := engine.Provision(ctx, config.ComputeProvisionRequest{
		InstanceID: inst.ID,
		ImageID:    cfg.ImageID,
		BootScript: []byte(cfg.UserData),
	})
	if err != nil {
		return cerrors.Newf(cerrors.Internal, "provision compute engine: %v", err)
	}

	if res.IP != "" {
		inst.PrivateIP = res.IP
	}

	return nil
}

// Deprovision tears down the real backing for the instance, if any. No-op when
// engine is nil.
func Deprovision(ctx context.Context, engine config.ComputeEngine, inst *driver.Instance) error {
	if engine == nil {
		return nil
	}

	if err := engine.Deprovision(ctx, inst.ID); err != nil {
		return cerrors.Newf(cerrors.Internal, "deprovision compute engine: %v", err)
	}

	return nil
}

// ConsoleOutput returns the console output the engine captured for the instance.
// It returns a nil slice when engine is nil.
func ConsoleOutput(ctx context.Context, engine config.ComputeEngine, id string) ([]byte, error) {
	if engine == nil {
		return nil, nil
	}

	out, err := engine.ConsoleOutput(ctx, id)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "compute engine console output: %v", err)
	}

	return out, nil
}
