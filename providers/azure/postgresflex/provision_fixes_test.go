package postgresflex

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

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

	return config.ProvisionResult{Host: "127.0.0.1", Port: 55432}, nil
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
		_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "pgflex1"})
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

	got, err := m.DescribeInstances(ctx, []string{"pgflex1"})
	if err != nil || len(got) != 1 {
		t.Fatalf("DescribeInstances: %v (%d)", err, len(got))
	}

	if got[0].Endpoint != "127.0.0.1" || got[0].Port != 55432 {
		t.Fatalf("finalized endpoint not written back: %s:%d", got[0].Endpoint, got[0].Port)
	}
}

// TestModifyServerRotatesPassword proves ModifyInstance re-runs the engine
// upsert with the new administrator password (and skips it otherwise).
func TestModifyServerRotatesPassword(t *testing.T) {
	eng := &recordingEngine{host: "127.0.0.1", port: 55432}
	m := New(config.NewOptions(config.WithDatabaseEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "pgflex1", MasterUsername: "pgadmin", MasterUserPassword: "old",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.ModifyInstance(ctx, "pgflex1", rdsdriver.ModifyInstanceInput{MasterUserPassword: "new"}); err != nil {
		t.Fatalf("ModifyInstance: %v", err)
	}

	last := eng.provisioned[len(eng.provisioned)-1]
	if last.InstanceID != "pgflex1" || last.Username != "pgadmin" || last.Password != "new" {
		t.Fatalf("password rotation not sent to the engine: %+v", last)
	}

	before := len(eng.provisioned)

	if _, err := m.ModifyInstance(ctx, "pgflex1", rdsdriver.ModifyInstanceInput{InstanceClass: "Standard_B2ms"}); err != nil {
		t.Fatalf("ModifyInstance (no password): %v", err)
	}

	if len(eng.provisioned) != before {
		t.Fatalf("a modify without a password must not touch the engine, got %d extra", len(eng.provisioned)-before)
	}
}
