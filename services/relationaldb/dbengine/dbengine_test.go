package dbengine_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/relationaldb/dbengine"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestIsPostgresFamily(t *testing.T) {
	cases := map[string]bool{
		"postgres": true, "POSTGRES": true, "Postgres": true,
		"aurora-postgresql": true, "Aurora-PostgreSQL": true,
		"POSTGRES_15": true, "POSTGRES_14": true, "postgres_13": true, "postgresql": true,
		"mysql": false, "aurora-mysql": false, "": false, "sqlserver": false,
		"MYSQL_8_0": false, "SQLSERVER_2019_STANDARD": false,
	}
	for engine, want := range cases {
		if got := dbengine.IsPostgresFamily(engine); got != want {
			t.Errorf("IsPostgresFamily(%q) = %v, want %v", engine, got, want)
		}
	}
}

func TestIsMySQLFamily(t *testing.T) {
	cases := map[string]bool{
		"mysql": true, "MYSQL": true, "MySQL": true,
		"MYSQL_8_0": true, "mysql_5_7": true,
		"aurora-mysql": true, "Aurora-MySQL": true,
		"mariadb": false, "MARIADB_10_6": false,
		"postgres": false, "aurora-postgresql": false, "sqlserver": false, "": false,
	}
	for engine, want := range cases {
		if got := dbengine.IsMySQLFamily(engine); got != want {
			t.Errorf("IsMySQLFamily(%q) = %v, want %v", engine, got, want)
		}
	}
}

type stubEngine struct {
	provisioned, deprovisioned int
}

func (s *stubEngine) Provision(_ context.Context, _ config.ProvisionRequest) (config.ProvisionResult, error) {
	s.provisioned++

	return config.ProvisionResult{Host: "127.0.0.1", Port: 6543}, nil
}

func (s *stubEngine) Deprovision(_ context.Context, _ string) error {
	s.deprovisioned++

	return nil
}

func TestProvisionOverridesEndpointForPostgres(t *testing.T) {
	eng := &stubEngine{}
	inst := &rdsdriver.Instance{Engine: "postgres", Endpoint: "synthetic.example.com", Port: 5432}
	cfg := &rdsdriver.InstanceConfig{ID: "db1", Engine: "postgres"}

	if err := dbengine.Provision(context.Background(), eng, inst, cfg); err != nil {
		t.Fatal(err)
	}

	if inst.Endpoint != "127.0.0.1" || inst.Port != 6543 || eng.provisioned != 1 {
		t.Fatalf("provision did not override endpoint: %+v provisioned=%d", inst, eng.provisioned)
	}
}

// namedEngine is a stub config.DatabaseEngine that tags every result with its
// own name and records the instance IDs it was asked to deprovision, so a test
// can prove which backing handled each call.
type namedEngine struct {
	name          string
	provisioned   []string
	deprovisioned []string
}

func (n *namedEngine) Provision(_ context.Context, req config.ProvisionRequest) (config.ProvisionResult, error) {
	n.provisioned = append(n.provisioned, req.InstanceID)

	return config.ProvisionResult{Host: n.name, Port: 5432}, nil
}

func (n *namedEngine) Deprovision(_ context.Context, instanceID string) error {
	n.deprovisioned = append(n.deprovisioned, instanceID)

	return nil
}

func TestNewMultiEngineRoutesByFamilyAndBack(t *testing.T) {
	pg := &namedEngine{name: "pg"}
	my := &namedEngine{name: "my"}
	eng := dbengine.NewMultiEngine(
		dbengine.FamilyEngine{Match: dbengine.IsPostgresFamily, Engine: pg},
		dbengine.FamilyEngine{Match: dbengine.IsMySQLFamily, Engine: my},
	)
	ctx := context.Background()

	pgRes, err := eng.Provision(ctx, config.ProvisionRequest{InstanceID: "p1", Engine: "postgres"})
	if err != nil || pgRes.Host != "pg" {
		t.Fatalf("postgres should route to pg engine: host=%q err=%v", pgRes.Host, err)
	}

	myRes, err := eng.Provision(ctx, config.ProvisionRequest{InstanceID: "m1", Engine: "MYSQL_8_0"})
	if err != nil || myRes.Host != "my" {
		t.Fatalf("mysql should route to my engine: host=%q err=%v", myRes.Host, err)
	}

	// Deprovision carries no engine string; it must route back to the engine
	// that provisioned each ID.
	if err := eng.Deprovision(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if err := eng.Deprovision(ctx, "m1"); err != nil {
		t.Fatal(err)
	}

	if len(pg.deprovisioned) != 1 || pg.deprovisioned[0] != "p1" {
		t.Fatalf("p1 should deprovision on pg engine, got %v", pg.deprovisioned)
	}
	if len(my.deprovisioned) != 1 || my.deprovisioned[0] != "m1" {
		t.Fatalf("m1 should deprovision on my engine, got %v", my.deprovisioned)
	}
}

func TestNewMultiEngineUnmatchedNoOp(t *testing.T) {
	pg := &namedEngine{name: "pg"}
	eng := dbengine.NewMultiEngine(dbengine.FamilyEngine{Match: dbengine.IsPostgresFamily, Engine: pg})
	ctx := context.Background()

	res, err := eng.Provision(ctx, config.ProvisionRequest{InstanceID: "s1", Engine: "sqlserver"})
	if err != nil || res != (config.ProvisionResult{}) {
		t.Fatalf("unmatched engine should no-op: res=%+v err=%v", res, err)
	}
	if len(pg.provisioned) != 0 {
		t.Fatalf("no engine should have been touched, got %v", pg.provisioned)
	}

	// Deprovisioning an unknown ID is a harmless no-op.
	if err := eng.Deprovision(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if len(pg.deprovisioned) != 0 {
		t.Fatalf("no engine should have been deprovisioned, got %v", pg.deprovisioned)
	}
}

func TestProvisionSkipsUnsupportedFamilyAndNilEngine(t *testing.T) {
	eng := &stubEngine{}
	sqlInst := &rdsdriver.Instance{Engine: "sqlserver", Endpoint: "keep"}

	_ = dbengine.Provision(context.Background(), eng, sqlInst, &rdsdriver.InstanceConfig{ID: "s", Engine: "sqlserver"})
	if eng.provisioned != 0 || sqlInst.Endpoint != "keep" {
		t.Fatal("engine should not touch an unsupported-family instance")
	}

	// A MySQL-family instance is now backed by the single wired engine.
	mysqlInst := &rdsdriver.Instance{Engine: "mysql", Endpoint: "synthetic"}
	if err := dbengine.Provision(context.Background(), eng,
		mysqlInst, &rdsdriver.InstanceConfig{ID: "m", Engine: "mysql"}); err != nil {
		t.Fatal(err)
	}
	if eng.provisioned != 1 || mysqlInst.Endpoint != "127.0.0.1" {
		t.Fatalf("mysql instance should be backed by the engine: %+v provisioned=%d", mysqlInst, eng.provisioned)
	}

	pgInst := &rdsdriver.Instance{Engine: "postgres", Endpoint: "keep"}
	_ = dbengine.Provision(context.Background(), nil, pgInst, &rdsdriver.InstanceConfig{ID: "p", Engine: "postgres"})
	if pgInst.Endpoint != "keep" {
		t.Fatal("a nil engine must leave the endpoint synthetic")
	}
}
