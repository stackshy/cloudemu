package alloydb

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/relationaldb/dbengine"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// provisionInstanceEngine backs an AlloyDB instance with the real Postgres
// engine when one is configured, using the cluster's initial user/password, and
// returns the reachable host a client connects to (empty when no engine is
// wired). AlloyDB is Postgres-wire, so the engine family is taken from the
// cluster's databaseVersion (e.g. "POSTGRES_15") — the internal engine string
// "alloydb-postgresql" is not in the Postgres family — mirroring how Cloud SQL
// passes its databaseVersion. Each instance gets its own database: the engine
// dedup key is the unique "{cluster}/{instance}", while the database is named by
// the bare instance ID so a client connects with a clean dbname. The caller
// holds m.mu.
func (m *Mock) provisionInstanceEngine(ctx context.Context, cluster *rdsdriver.Cluster, instanceID string) (string, error) {
	if m.opts.DatabaseEngine == nil {
		return "", nil
	}

	cfg := rdsdriver.InstanceConfig{
		ID:                 instanceKey(cluster.ID, instanceID),
		Engine:             cluster.EngineVersion,
		DBName:             instanceID,
		MasterUsername:     cluster.MasterUsername,
		MasterUserPassword: m.initialPasswords[cluster.ID],
	}

	var inst rdsdriver.Instance
	if err := dbengine.Provision(ctx, m.opts.DatabaseEngine, &inst, &cfg); err != nil {
		return "", err
	}

	return inst.Endpoint, nil
}

// deprovisionInstanceEngine tears down the real database backing an AlloyDB
// instance, if any. It mirrors provisionInstanceEngine's Postgres-family and
// key choices: engineKey is the "{cluster}/{instance}" store key and
// engineVersion is the cluster's databaseVersion. The caller holds m.mu.
func (m *Mock) deprovisionInstanceEngine(ctx context.Context, engineKey, engineVersion string) error {
	if m.opts.DatabaseEngine == nil {
		return nil
	}

	inst := rdsdriver.Instance{ID: engineKey, Engine: engineVersion}

	return dbengine.Deprovision(ctx, m.opts.DatabaseEngine, &inst)
}
