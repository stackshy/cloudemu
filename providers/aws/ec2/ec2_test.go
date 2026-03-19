package ec2

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/compute"
	"github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/config"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	return New(opts)
}

func defaultConfig() driver.InstanceConfig {
	return driver.InstanceConfig{
		ImageID:      "ami-12345",
		InstanceType: "t2.micro",
		Tags:         map[string]string{"env": "test"},
	}
}

func TestRunInstances(t *testing.T) {
	tests := []struct {
		name      string
		count     int
		expectErr bool
		expectLen int
	}{
		{name: "single instance", count: 1, expectLen: 1},
		{name: "multiple instances", count: 3, expectLen: 3},
		{name: "zero count", count: 0, expectErr: true},
		{name: "negative count", count: -1, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			instances, err := m.RunInstances(context.Background(), defaultConfig(), tc.count)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.expectLen, len(instances))

			for _, inst := range instances {
				assertEqual(t, compute.StateRunning, inst.State)
				assertEqual(t, "ami-12345", inst.ImageID)
				assertEqual(t, "t2.micro", inst.InstanceType)
				assertNotEmpty(t, inst.ID)
				assertNotEmpty(t, inst.PrivateIP)
			}
		})
	}
}

func TestDescribeInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 2)

	t.Run("all instances", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, nil, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(result))
	})

	t.Run("by ID", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, []string{instances[0].ID}, nil)
		requireNoError(t, err)
		assertEqual(t, 1, len(result))
		assertEqual(t, instances[0].ID, result[0].ID)
	})

	t.Run("filter by state", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, nil, []driver.DescribeFilter{
			{Name: "instance-state-name", Values: []string{"running"}},
		})
		requireNoError(t, err)
		assertEqual(t, 2, len(result))
	})

	t.Run("filter by instance-type", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, nil, []driver.DescribeFilter{
			{Name: "instance-type", Values: []string{"t2.micro"}},
		})
		requireNoError(t, err)
		assertEqual(t, 2, len(result))
	})

	t.Run("filter by tag", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, nil, []driver.DescribeFilter{
			{Name: "tag:env", Values: []string{"test"}},
		})
		requireNoError(t, err)
		assertEqual(t, 2, len(result))
	})

	t.Run("filter no match", func(t *testing.T) {
		result, err := m.DescribeInstances(ctx, nil, []driver.DescribeFilter{
			{Name: "instance-state-name", Values: []string{"stopped"}},
		})
		requireNoError(t, err)
		assertEqual(t, 0, len(result))
	})
}

func TestStartInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	_ = m.StopInstances(ctx, []string{id})

	t.Run("success", func(t *testing.T) {
		err := m.StartInstances(ctx, []string{id})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, compute.StateRunning, result[0].State)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.StartInstances(ctx, []string{"i-nonexistent"})
		assertError(t, err, true)
	})
}

func TestStopInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	t.Run("success", func(t *testing.T) {
		err := m.StopInstances(ctx, []string{id})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, compute.StateStopped, result[0].State)
	})

	t.Run("cannot stop already stopped", func(t *testing.T) {
		err := m.StopInstances(ctx, []string{id})
		assertError(t, err, true)
	})
}

func TestRebootInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	t.Run("success", func(t *testing.T) {
		err := m.RebootInstances(ctx, []string{id})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, compute.StateRunning, result[0].State)
	})
}

func TestTerminateInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	t.Run("success", func(t *testing.T) {
		err := m.TerminateInstances(ctx, []string{id})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, compute.StateTerminated, result[0].State)
	})

	t.Run("cannot terminate terminated", func(t *testing.T) {
		err := m.TerminateInstances(ctx, []string{id})
		assertError(t, err, true)
	})
}

func TestModifyInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	t.Run("must be stopped", func(t *testing.T) {
		err := m.ModifyInstance(ctx, id, driver.ModifyInstanceInput{InstanceType: "t2.large"})
		assertError(t, err, true)
	})

	_ = m.StopInstances(ctx, []string{id})

	t.Run("success - change type", func(t *testing.T) {
		err := m.ModifyInstance(ctx, id, driver.ModifyInstanceInput{InstanceType: "t2.large"})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, "t2.large", result[0].InstanceType)
	})

	t.Run("success - change tags", func(t *testing.T) {
		err := m.ModifyInstance(ctx, id, driver.ModifyInstanceInput{Tags: map[string]string{"new": "tag"}})
		requireNoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		assertEqual(t, "tag", result[0].Tags["new"])
	})

	t.Run("not found", func(t *testing.T) {
		err := m.ModifyInstance(ctx, "i-nonexistent", driver.ModifyInstanceInput{})
		assertError(t, err, true)
	})
}

func TestMatchesFilters(t *testing.T) {
	inst := &instanceData{
		ID:           "i-123",
		InstanceType: "t2.micro",
		State:        "running",
		Tags:         map[string]string{"env": "prod"},
	}

	tests := []struct {
		name    string
		filters []driver.DescribeFilter
		expect  bool
	}{
		{name: "no filters", filters: nil, expect: true},
		{name: "match instance-id", filters: []driver.DescribeFilter{{Name: "instance-id", Values: []string{"i-123"}}}, expect: true},
		{name: "no match instance-id", filters: []driver.DescribeFilter{{Name: "instance-id", Values: []string{"i-999"}}}, expect: false},
		{name: "match tag", filters: []driver.DescribeFilter{{Name: "tag:env", Values: []string{"prod"}}}, expect: true},
		{name: "no match tag", filters: []driver.DescribeFilter{{Name: "tag:env", Values: []string{"dev"}}}, expect: false},
		{name: "unknown filter passes", filters: []driver.DescribeFilter{{Name: "unknown", Values: []string{"x"}}}, expect: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := matchesFilters(inst, tc.filters)
			assertEqual(t, tc.expect, result)
		})
	}
}

func TestLifecycleStateMachine(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, _ := m.RunInstances(ctx, defaultConfig(), 1)
	id := instances[0].ID

	// running -> stop -> stopped
	requireNoError(t, m.StopInstances(ctx, []string{id}))
	desc, _ := m.DescribeInstances(ctx, []string{id}, nil)
	assertEqual(t, compute.StateStopped, desc[0].State)

	// stopped -> start -> running
	requireNoError(t, m.StartInstances(ctx, []string{id}))
	desc, _ = m.DescribeInstances(ctx, []string{id}, nil)
	assertEqual(t, compute.StateRunning, desc[0].State)

	// running -> reboot -> running
	requireNoError(t, m.RebootInstances(ctx, []string{id}))
	desc, _ = m.DescribeInstances(ctx, []string{id}, nil)
	assertEqual(t, compute.StateRunning, desc[0].State)

	// running -> terminate -> terminated
	requireNoError(t, m.TerminateInstances(ctx, []string{id}))
	desc, _ = m.DescribeInstances(ctx, []string{id}, nil)
	assertEqual(t, compute.StateTerminated, desc[0].State)
}

// --- test helpers ---

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()
	if s == "" {
		t.Error("expected non-empty string")
	}
}
