package mysqlflex

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// recordingEngine is a stub config.DatabaseEngine that records calls and returns
// a fixed address, so the MySQL Flexible Server engine wiring can be tested
// without a real database (no Docker in core CI).
type recordingEngine struct {
	provisioned   []config.ProvisionRequest
	deprovisioned []string
	host          string
	port          int
}

func (e *recordingEngine) Provision(_ context.Context, req config.ProvisionRequest) (config.ProvisionResult, error) {
	e.provisioned = append(e.provisioned, req)

	return config.ProvisionResult{Host: e.host, Port: e.port}, nil
}

func (e *recordingEngine) Deprovision(_ context.Context, id string) error {
	e.deprovisioned = append(e.deprovisioned, id)

	return nil
}

func TestCreateInstanceUsesEngineForMySQL(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 33060}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	inst, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "app-my", Engine: "MySQL", MasterUsername: "myadmin", MasterUserPassword: "secret", DBName: "app",
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// The engine overrides the reachable host/port carried in Endpoint (which the
	// wire handler surfaces as fullyQualifiedDomainName), replacing the synthetic
	// <name>.mysql.database.azure.com FQDN.
	if inst.Endpoint != "127.0.0.1" || inst.Port != 33060 {
		t.Fatalf("endpoint not overridden by engine: got %s:%d", inst.Endpoint, inst.Port)
	}

	if len(eng.provisioned) != 1 ||
		eng.provisioned[0].Username != "myadmin" ||
		eng.provisioned[0].DBName != "app" ||
		eng.provisioned[0].Engine != "MySQL" {
		t.Fatalf("unexpected provision calls: %+v", eng.provisioned)
	}

	if err := m.DeleteInstance(ctx, "app-my"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "app-my" {
		t.Fatalf("expected one deprovision for app-my, got %v", eng.deprovisioned)
	}
}

func TestCreateInstanceDefaultsEngineToMySQL(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 33060}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))

	// An empty Engine defaults to "MySQL", so the engine is still invoked.
	if _, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "my1"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0].Engine != defaultEngine {
		t.Fatalf("engine should back the defaulted MySQL family, got %+v", eng.provisioned)
	}
}

func TestCreateInstanceNoEngineIsSynthetic(t *testing.T) {
	m := New(config.NewOptions())

	inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "my2", Engine: "MySQL"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Without an engine the reported FQDN stays the synthetic Azure endpoint.
	if inst.Endpoint != "my2"+endpointSuffix {
		t.Fatalf("without an engine the endpoint should be the synthetic FQDN, got %q", inst.Endpoint)
	}
}

type blockingEngine struct {
	enterOnce sync.Once
	entered   chan struct{}
	release   chan struct{}
}

func newBlockingEngine() *blockingEngine {
	return &blockingEngine{entered: make(chan struct{}), release: make(chan struct{})}
}

func (e *blockingEngine) Provision(_ context.Context, _ config.ProvisionRequest) (config.ProvisionResult, error) {
	e.enterOnce.Do(func() { close(e.entered) })
	<-e.release

	return config.ProvisionResult{Host: "127.0.0.1", Port: 33060}, nil
}

func (e *blockingEngine) Deprovision(_ context.Context, _ string) error { return nil }

// TestCreateInstanceReleasesLockDuringProvision proves the provider lock is
// released around the engine provision so concurrent reads never block.
func TestCreateInstanceReleasesLockDuringProvision(t *testing.T) {
	eng := newBlockingEngine()
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	created := make(chan error, 1)

	go func() {
		_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "my1", Engine: "MySQL"})
		created <- err
	}()

	select {
	case <-eng.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provision never started")
	}

	describeReturned := make(chan struct{})

	go func() {
		_, _ = m.DescribeInstances(ctx, nil)
		close(describeReturned)
	}()

	select {
	case <-describeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("DescribeInstances blocked while a provision held the provider lock")
	}

	close(eng.release)

	if err := <-created; err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	got, err := m.DescribeInstances(ctx, []string{"my1"})
	if err != nil || len(got) != 1 {
		t.Fatalf("DescribeInstances: %v (%d)", err, len(got))
	}

	if got[0].Endpoint != "127.0.0.1" || got[0].Port != 33060 {
		t.Fatalf("finalized endpoint not written back: %s:%d", got[0].Endpoint, got[0].Port)
	}
}

// TestModifyServerRotatesPassword proves ModifyInstance re-runs the engine
// upsert with the new administrator password (and skips it otherwise).
func TestModifyServerRotatesPassword(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 33060}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "my1", Engine: "MySQL", MasterUsername: "myadmin", MasterUserPassword: "old",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.ModifyInstance(ctx, "my1", rdsdriver.ModifyInstanceInput{MasterUserPassword: "new"}); err != nil {
		t.Fatalf("ModifyInstance: %v", err)
	}

	last := eng.provisioned[len(eng.provisioned)-1]
	if last.InstanceID != "my1" || last.Username != "myadmin" || last.Password != "new" {
		t.Fatalf("password rotation not sent to the engine: %+v", last)
	}

	before := len(eng.provisioned)

	if _, err := m.ModifyInstance(ctx, "my1", rdsdriver.ModifyInstanceInput{InstanceClass: "Standard_B2ms"}); err != nil {
		t.Fatalf("ModifyInstance (no password): %v", err)
	}

	if len(eng.provisioned) != before {
		t.Fatalf("a modify without a password must not touch the engine, got %d extra", len(eng.provisioned)-before)
	}
}
