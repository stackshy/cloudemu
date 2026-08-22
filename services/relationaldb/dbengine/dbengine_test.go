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

func TestProvisionSkipsNonPostgresAndNilEngine(t *testing.T) {
	eng := &stubEngine{}
	mysqlInst := &rdsdriver.Instance{Engine: "mysql", Endpoint: "keep"}

	_ = dbengine.Provision(context.Background(), eng, mysqlInst, &rdsdriver.InstanceConfig{ID: "m", Engine: "mysql"})
	if eng.provisioned != 0 || mysqlInst.Endpoint != "keep" {
		t.Fatal("engine should not touch a mysql instance")
	}

	pgInst := &rdsdriver.Instance{Engine: "postgres", Endpoint: "keep"}
	_ = dbengine.Provision(context.Background(), nil, pgInst, &rdsdriver.InstanceConfig{ID: "p", Engine: "postgres"})
	if pgInst.Endpoint != "keep" {
		t.Fatal("a nil engine must leave the endpoint synthetic")
	}
}
