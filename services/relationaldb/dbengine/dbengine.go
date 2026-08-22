// Package dbengine wires an optional real database engine into a relational
// provider's instance lifecycle. It is shared by every RDS-style provider
// (AWS RDS, Azure Flexible Server, GCP Cloud SQL) so the provision/deprovision
// hook stays identical across clouds and cannot drift.
package dbengine

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

const (
	enginePostgres       = "postgres"
	engineAuroraPostgres = "aurora-postgresql"
)

// IsPostgresFamily reports whether a real Postgres engine can back this engine
// family. The no-Docker embedded-postgres backing serves all Postgres-wire
// services (RDS/Aurora Postgres, Azure Flexible Server, Cloud SQL Postgres).
// Matching is case-insensitive — providers spell the engine differently
// ("postgres", "POSTGRES", "Postgres").
func IsPostgresFamily(engine string) bool {
	return strings.EqualFold(engine, enginePostgres) || strings.EqualFold(engine, engineAuroraPostgres)
}

// Provision backs the instance with the engine when one is configured and the
// engine family is supported, overriding the synthetic endpoint with the real
// host:port a client connects to. No-op otherwise.
func Provision(ctx context.Context, engine config.DatabaseEngine, inst *rdsdriver.Instance, cfg *rdsdriver.InstanceConfig) error {
	if engine == nil || !IsPostgresFamily(cfg.Engine) {
		return nil
	}

	res, err := engine.Provision(ctx, config.ProvisionRequest{
		InstanceID: cfg.ID,
		Engine:     cfg.Engine,
		DBName:     cfg.DBName,
		Username:   cfg.MasterUsername,
		Password:   cfg.MasterUserPassword,
	})
	if err != nil {
		return cerrors.Newf(cerrors.Internal, "provision database engine: %v", err)
	}

	inst.Endpoint = res.Host
	inst.Port = res.Port

	return nil
}

// Deprovision tears down the real database backing the instance, if any.
func Deprovision(ctx context.Context, engine config.DatabaseEngine, inst *rdsdriver.Instance) error {
	if engine == nil || !IsPostgresFamily(inst.Engine) {
		return nil
	}

	if err := engine.Deprovision(ctx, inst.ID); err != nil {
		return cerrors.Newf(cerrors.Internal, "deprovision database engine: %v", err)
	}

	return nil
}
