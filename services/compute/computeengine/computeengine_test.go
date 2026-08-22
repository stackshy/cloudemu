package computeengine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/compute/computeengine"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

type stubEngine struct {
	ip            string
	console       []byte
	failProvision bool
	failDeprov    bool
	failConsole   bool
	lastBoot      []byte
	deprovisioned []string
}

func (s *stubEngine) Provision(_ context.Context, req config.ComputeProvisionRequest) (config.ComputeProvisionResult, error) {
	if s.failProvision {
		return config.ComputeProvisionResult{}, errors.New("boom")
	}
	s.lastBoot = req.BootScript

	return config.ComputeProvisionResult{IP: s.ip}, nil
}

func (s *stubEngine) ConsoleOutput(_ context.Context, _ string) ([]byte, error) {
	if s.failConsole {
		return nil, errors.New("boom")
	}

	return s.console, nil
}

func (s *stubEngine) Deprovision(_ context.Context, id string) error {
	if s.failDeprov {
		return errors.New("boom")
	}
	s.deprovisioned = append(s.deprovisioned, id)

	return nil
}

func TestProvisionOverridesIPAndPassesBootScript(t *testing.T) {
	eng := &stubEngine{ip: "10.0.0.5"}
	inst := &driver.Instance{ID: "i-1", PrivateIP: "172.16.0.1"}
	cfg := &driver.InstanceConfig{ImageID: "ami-1", UserData: "#!/bin/sh"}

	if err := computeengine.Provision(context.Background(), eng, inst, cfg); err != nil {
		t.Fatal(err)
	}
	if inst.PrivateIP != "10.0.0.5" {
		t.Fatalf("private IP not overridden: %q", inst.PrivateIP)
	}
	if string(eng.lastBoot) != "#!/bin/sh" {
		t.Fatalf("boot script not passed: %q", eng.lastBoot)
	}
}

func TestProvisionEmptyIPKeepsSynthetic(t *testing.T) {
	eng := &stubEngine{ip: ""}
	inst := &driver.Instance{ID: "i-1", PrivateIP: "172.16.0.1"}

	if err := computeengine.Provision(context.Background(), eng, inst, &driver.InstanceConfig{}); err != nil {
		t.Fatal(err)
	}
	if inst.PrivateIP != "172.16.0.1" {
		t.Fatalf("empty engine IP must keep synthetic IP, got %q", inst.PrivateIP)
	}
}

func TestProvisionNilEngineNoOp(t *testing.T) {
	inst := &driver.Instance{ID: "i-1", PrivateIP: "172.16.0.1"}
	if err := computeengine.Provision(context.Background(), nil, inst, &driver.InstanceConfig{}); err != nil {
		t.Fatal(err)
	}
	if inst.PrivateIP != "172.16.0.1" {
		t.Fatal("nil engine must leave the IP synthetic")
	}
}

func TestProvisionEngineErrorWrapped(t *testing.T) {
	eng := &stubEngine{failProvision: true}
	err := computeengine.Provision(context.Background(), eng, &driver.Instance{ID: "i-1"}, &driver.InstanceConfig{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDeprovision(t *testing.T) {
	eng := &stubEngine{}
	if err := computeengine.Deprovision(context.Background(), eng, &driver.Instance{ID: "i-9"}); err != nil {
		t.Fatal(err)
	}
	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "i-9" {
		t.Fatalf("deprovision not routed: %v", eng.deprovisioned)
	}

	if err := computeengine.Deprovision(context.Background(), nil, &driver.Instance{ID: "i-9"}); err != nil {
		t.Fatal("nil engine deprovision must be a no-op")
	}

	if err := computeengine.Deprovision(context.Background(), &stubEngine{failDeprov: true}, &driver.Instance{ID: "x"}); err == nil {
		t.Fatal("expected deprovision error")
	}
}

func TestConsoleOutput(t *testing.T) {
	eng := &stubEngine{console: []byte("log")}
	out, err := computeengine.ConsoleOutput(context.Background(), eng, "i-1")
	if err != nil || string(out) != "log" {
		t.Fatalf("console output: %q err=%v", out, err)
	}

	nilOut, err := computeengine.ConsoleOutput(context.Background(), nil, "i-1")
	if err != nil || nilOut != nil {
		t.Fatalf("nil engine must return nil output: %q err=%v", nilOut, err)
	}

	if _, err := computeengine.ConsoleOutput(context.Background(), &stubEngine{failConsole: true}, "i-1"); err == nil {
		t.Fatal("expected console error")
	}
}
