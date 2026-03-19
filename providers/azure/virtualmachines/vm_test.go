package virtualmachines

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/compute"
	"github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub"))

	return New(opts)
}

func TestRunInstances(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		count   int
		wantErr bool
		errMsg  string
	}{
		{name: "single instance", count: 1},
		{name: "multiple instances", count: 3},
		{name: "zero count", count: 0, wantErr: true, errMsg: "count must be greater than 0"},
		{name: "negative count", count: -1, wantErr: true, errMsg: "count must be greater than 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMock()

			cfg := driver.InstanceConfig{
				ImageID:      "img-123",
				InstanceType: "Standard_B1s",
				Tags:         map[string]string{"env": "test"},
				SubnetID:     "subnet-1",
			}

			instances, err := m.RunInstances(ctx, cfg, tt.count)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				require.Len(t, instances, tt.count)

				for _, inst := range instances {
					assert.Equal(t, compute.StateRunning, inst.State)
					assert.Equal(t, "img-123", inst.ImageID)
					assert.Equal(t, "Standard_B1s", inst.InstanceType)
					assert.NotEmpty(t, inst.ID)
					assert.NotEmpty(t, inst.PrivateIP)
					assert.NotEmpty(t, inst.LaunchTime)
					assert.Equal(t, "test", inst.Tags["env"])
				}
			}
		})
	}
}

func TestDescribeInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	cfg := driver.InstanceConfig{
		ImageID:      "img-1",
		InstanceType: "Standard_B1s",
		Tags:         map[string]string{"env": "prod"},
	}

	instances, err := m.RunInstances(ctx, cfg, 2)
	require.NoError(t, err)
	require.Len(t, instances, 2)

	tests := []struct {
		name      string
		ids       []string
		filters   []driver.DescribeFilter
		wantCount int
	}{
		{name: "all instances", wantCount: 2},
		{name: "by ID", ids: []string{instances[0].ID}, wantCount: 1},
		{name: "by state filter", filters: []driver.DescribeFilter{{Name: "instance-state-name", Values: []string{"running"}}}, wantCount: 2},
		{name: "by type filter", filters: []driver.DescribeFilter{{Name: "instance-type", Values: []string{"Standard_B1s"}}}, wantCount: 2},
		{name: "by tag filter", filters: []driver.DescribeFilter{{Name: "tag:env", Values: []string{"prod"}}}, wantCount: 2},
		{name: "no match filter", filters: []driver.DescribeFilter{{Name: "instance-state-name", Values: []string{"stopped"}}}, wantCount: 0},
		{name: "nonexistent ID", ids: []string{"vm-nonexistent"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.DescribeInstances(ctx, tt.ids, tt.filters)
			require.NoError(t, err)
			assert.Len(t, result, tt.wantCount)
		})
	}
}

func TestStartInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "t2"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	// Stop first
	require.NoError(t, m.StopInstances(ctx, []string{id}))

	tests := []struct {
		name    string
		ids     []string
		wantErr bool
		errMsg  string
	}{
		{name: "success", ids: []string{id}},
		{name: "not found", ids: []string{"vm-missing"}, wantErr: true, errMsg: "not found"},
		{name: "already running", ids: []string{id}, wantErr: true, errMsg: "cannot start"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.StartInstances(ctx, tt.ids)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)

				result, _ := m.DescribeInstances(ctx, tt.ids, nil)
				require.Len(t, result, 1)
				assert.Equal(t, compute.StateRunning, result[0].State)
			}
		})
	}
}

func TestStopInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "t2"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	tests := []struct {
		name    string
		ids     []string
		wantErr bool
		errMsg  string
	}{
		{name: "success", ids: []string{id}},
		{name: "not found", ids: []string{"vm-missing"}, wantErr: true, errMsg: "not found"},
		{name: "already stopped", ids: []string{id}, wantErr: true, errMsg: "cannot stop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.StopInstances(ctx, tt.ids)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)

				result, _ := m.DescribeInstances(ctx, tt.ids, nil)
				require.Len(t, result, 1)
				assert.Equal(t, compute.StateStopped, result[0].State)
			}
		})
	}
}

func TestRebootInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "t2"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	t.Run("success", func(t *testing.T) {
		err := m.RebootInstances(ctx, []string{id})
		require.NoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		require.Len(t, result, 1)
		assert.Equal(t, compute.StateRunning, result[0].State)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.RebootInstances(ctx, []string{"vm-missing"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestTerminateInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "t2"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	t.Run("success", func(t *testing.T) {
		err := m.TerminateInstances(ctx, []string{id})
		require.NoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		require.Len(t, result, 1)
		assert.Equal(t, compute.StateTerminated, result[0].State)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.TerminateInstances(ctx, []string{"vm-missing"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestModifyInstance(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	t.Run("must be stopped", func(t *testing.T) {
		err := m.ModifyInstance(ctx, id, driver.ModifyInstanceInput{InstanceType: "Standard_B2s"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be stopped")
	})

	require.NoError(t, m.StopInstances(ctx, []string{id}))

	t.Run("change instance type", func(t *testing.T) {
		err := m.ModifyInstance(ctx, id, driver.ModifyInstanceInput{InstanceType: "Standard_B2s"})
		require.NoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		require.Len(t, result, 1)
		assert.Equal(t, "Standard_B2s", result[0].InstanceType)
	})

	t.Run("add tags", func(t *testing.T) {
		err := m.ModifyInstance(ctx, id, driver.ModifyInstanceInput{Tags: map[string]string{"new": "tag"}})
		require.NoError(t, err)

		result, _ := m.DescribeInstances(ctx, []string{id}, nil)
		require.Len(t, result, 1)
		assert.Equal(t, "tag", result[0].Tags["new"])
	})

	t.Run("not found", func(t *testing.T) {
		err := m.ModifyInstance(ctx, "vm-missing", driver.ModifyInstanceInput{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestMatchesFilters(t *testing.T) {
	inst := &instanceData{
		ID:           "vm-123",
		InstanceType: "Standard_B1s",
		State:        compute.StateRunning,
		Tags:         map[string]string{"env": "prod", "team": "backend"},
	}

	tests := []struct {
		name    string
		filters []driver.DescribeFilter
		want    bool
	}{
		{name: "no filters", filters: nil, want: true},
		{name: "match instance-id", filters: []driver.DescribeFilter{{Name: "instance-id", Values: []string{"vm-123"}}}, want: true},
		{name: "no match instance-id", filters: []driver.DescribeFilter{{Name: "instance-id", Values: []string{"vm-999"}}}, want: false},
		{name: "match tag", filters: []driver.DescribeFilter{{Name: "tag:env", Values: []string{"prod"}}}, want: true},
		{name: "no match tag", filters: []driver.DescribeFilter{{Name: "tag:env", Values: []string{"dev"}}}, want: false},
		{name: "missing tag", filters: []driver.DescribeFilter{{Name: "tag:missing", Values: []string{"val"}}}, want: false},
		{name: "unknown filter passthrough", filters: []driver.DescribeFilter{{Name: "unknown", Values: []string{"x"}}}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchesFilters(inst, tt.filters))
		})
	}
}
