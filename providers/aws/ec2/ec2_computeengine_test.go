package ec2

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

// fakeComputeEngine is a recording config.ComputeEngine used to prove the EC2
// provider wires provision/deprovision/console-output correctly without Docker.
type fakeComputeEngine struct {
	mu            sync.Mutex
	provisioned   []config.ComputeProvisionRequest
	deprovisioned []string
	ip            string
	console       []byte
	failOn        string // instanceID whose Deprovision returns an error
}

func (f *fakeComputeEngine) Provision(
	_ context.Context, req config.ComputeProvisionRequest,
) (config.ComputeProvisionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.provisioned = append(f.provisioned, req)

	return config.ComputeProvisionResult{IP: f.ip}, nil
}

func (f *fakeComputeEngine) ConsoleOutput(_ context.Context, _ string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.console, nil
}

func (f *fakeComputeEngine) Deprovision(_ context.Context, instanceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deprovisioned = append(f.deprovisioned, instanceID)

	if instanceID == f.failOn {
		return errors.New("deprovision failed")
	}

	return nil
}

func newEngineMock(engine config.ComputeEngine) *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithComputeEngine(engine),
	)

	return New(opts)
}

func TestRunInstancesProvisionsEngineBacked(t *testing.T) {
	eng := &fakeComputeEngine{ip: "10.9.8.7", console: []byte("boot log")}
	m := newEngineMock(eng)

	cfg := defaultConfig()
	cfg.UserData = "#!/bin/sh\necho hi"

	instances, err := m.RunInstances(context.Background(), cfg, 2)
	requireNoError(t, err)
	assertEqual(t, 2, len(instances))

	// The engine provisioned each instance with the decoded boot script.
	assertEqual(t, 2, len(eng.provisioned))
	assertEqual(t, cfg.UserData, string(eng.provisioned[0].BootScript))
	assertEqual(t, cfg.ImageID, eng.provisioned[0].ImageID)
	assertEqual(t, instances[0].ID, eng.provisioned[0].InstanceID)

	// The engine-surfaced IP overrides the synthetic private IP.
	for _, inst := range instances {
		assertEqual(t, "10.9.8.7", inst.PrivateIP)
	}

	// GetConsoleOutput returns the engine's captured output.
	out, err := m.GetConsoleOutput(context.Background(), instances[0].ID)
	requireNoError(t, err)
	assertEqual(t, "boot log", string(out))
}

func TestTerminateInstancesDeprovisionsEngineBacked(t *testing.T) {
	eng := &fakeComputeEngine{ip: "10.1.1.1"}
	m := newEngineMock(eng)

	instances, err := m.RunInstances(context.Background(), defaultConfig(), 1)
	requireNoError(t, err)
	id := instances[0].ID

	requireNoError(t, m.TerminateInstances(context.Background(), []string{id}))

	assertEqual(t, 1, len(eng.deprovisioned))
	assertEqual(t, id, eng.deprovisioned[0])
}

func TestTerminateBestEffortOnDeprovisionFailure(t *testing.T) {
	eng := &fakeComputeEngine{ip: "10.1.1.1", console: []byte("log")}
	m := newEngineMock(eng)

	instances, err := m.RunInstances(context.Background(), defaultConfig(), 3)
	requireNoError(t, err)

	ids := []string{instances[0].ID, instances[1].ID, instances[2].ID}
	eng.failOn = ids[1] // the middle instance's Deprovision fails

	err = m.TerminateInstances(context.Background(), ids)
	assertTrue(t, err != nil, "an aggregated deprovision error is expected")

	// Best-effort: every instance was attempted, not stopped at the failure.
	assertEqual(t, 3, len(eng.deprovisioned))

	// The successful ones cleared engineBacked (persisted) → no console output.
	out0, _ := m.GetConsoleOutput(context.Background(), ids[0])
	assertEqual(t, 0, len(out0))
	out2, _ := m.GetConsoleOutput(context.Background(), ids[2])
	assertEqual(t, 0, len(out2))

	// The failed one keeps its backing so it can still be cleaned up later.
	out1, _ := m.GetConsoleOutput(context.Background(), ids[1])
	assertEqual(t, "log", string(out1))
}

func TestRunInstancesNilEngineUnchanged(t *testing.T) {
	m := newEngineMock(nil)

	cfg := defaultConfig()
	cfg.UserData = "raw-user-data"

	instances, err := m.RunInstances(context.Background(), cfg, 1)
	requireNoError(t, err)

	// A synthetic private IP is assigned and left untouched (10.0.x.x range).
	assertNotEmpty(t, instances[0].PrivateIP)
	assertTrue(t, instances[0].PrivateIP != "10.9.8.7", "synthetic IP should not be an engine IP")

	// A non-engine-backed instance produces no console output.
	out, err := m.GetConsoleOutput(context.Background(), instances[0].ID)
	requireNoError(t, err)
	assertTrue(t, out == nil, "nil-engine console output should be empty")

	// Terminate is a plain lifecycle transition with no engine to deprovision.
	requireNoError(t, m.TerminateInstances(context.Background(), []string{instances[0].ID}))
}

func TestGetConsoleOutputUnknownInstance(t *testing.T) {
	m := newEngineMock(&fakeComputeEngine{})

	_, err := m.GetConsoleOutput(context.Background(), "i-doesnotexist")
	assertError(t, err, true)
}
