package gce

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
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	return New(opts)
}

func TestRunInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.InstanceConfig
		count     int
		wantErr   bool
		errSubstr string
	}{
		{
			name:  "single instance",
			cfg:   driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1", Tags: map[string]string{"env": "test"}},
			count: 1,
		},
		{
			name:  "multiple instances",
			cfg:   driver.InstanceConfig{ImageID: "img-2", InstanceType: "n1-standard-2"},
			count: 3,
		},
		{
			name:      "zero count",
			cfg:       driver.InstanceConfig{ImageID: "img-1"},
			count:     0,
			wantErr:   true,
			errSubstr: "greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instances, err := m.RunInstances(ctx, tt.cfg, tt.count)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				require.Len(t, instances, tt.count)
				assert.Equal(t, compute.StateRunning, instances[0].State)
				assert.Equal(t, tt.cfg.ImageID, instances[0].ImageID)
				assert.Equal(t, tt.cfg.InstanceType, instances[0].InstanceType)
				assert.NotEmpty(t, instances[0].ID)
				assert.NotEmpty(t, instances[0].PrivateIP)
			}
		})
	}
}

func TestDescribeInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "img-1", InstanceType: "n1-standard-1", Tags: map[string]string{"env": "prod"},
	}, 2)
	require.NoError(t, err)
	require.Len(t, instances, 2)

	tests := []struct {
		name      string
		ids       []string
		filters   []driver.DescribeFilter
		wantCount int
	}{
		{name: "all instances", wantCount: 2},
		{name: "by id", ids: []string{instances[0].ID}, wantCount: 1},
		{name: "by state filter", filters: []driver.DescribeFilter{{Name: "instance-state-name", Values: []string{"running"}}}, wantCount: 2},
		{name: "by type filter", filters: []driver.DescribeFilter{{Name: "instance-type", Values: []string{"n1-standard-1"}}}, wantCount: 2},
		{name: "by tag filter", filters: []driver.DescribeFilter{{Name: "tag:env", Values: []string{"prod"}}}, wantCount: 2},
		{name: "no match", filters: []driver.DescribeFilter{{Name: "instance-state-name", Values: []string{"stopped"}}}, wantCount: 0},
		{name: "unknown id", ids: []string{"unknown"}, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, descErr := m.DescribeInstances(ctx, tt.ids, tt.filters)
			require.NoError(t, descErr)
			assert.Len(t, result, tt.wantCount)
		})
	}
}

func TestStartInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	require.NoError(t, m.StopInstances(ctx, []string{id}))

	tests := []struct {
		name      string
		ids       []string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", ids: []string{id}},
		{name: "not found", ids: []string{"nonexistent"}, wantErr: true, errSubstr: "not found"},
		{name: "already running", ids: []string{id}, wantErr: true, errSubstr: "cannot start"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.StartInstances(ctx, tt.ids)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				desc, descErr := m.DescribeInstances(ctx, tt.ids, nil)
				require.NoError(t, descErr)
				assert.Equal(t, compute.StateRunning, desc[0].State)
			}
		})
	}
}

func TestStopInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	tests := []struct {
		name      string
		ids       []string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", ids: []string{id}},
		{name: "not found", ids: []string{"nonexistent"}, wantErr: true, errSubstr: "not found"},
		{name: "already stopped", ids: []string{id}, wantErr: true, errSubstr: "cannot stop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.StopInstances(ctx, tt.ids)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				desc, descErr := m.DescribeInstances(ctx, tt.ids, nil)
				require.NoError(t, descErr)
				assert.Equal(t, compute.StateStopped, desc[0].State)
			}
		})
	}
}

func TestRebootInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	tests := []struct {
		name      string
		ids       []string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", ids: []string{id}},
		{name: "not found", ids: []string{"nonexistent"}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.RebootInstances(ctx, tt.ids)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				desc, descErr := m.DescribeInstances(ctx, tt.ids, nil)
				require.NoError(t, descErr)
				assert.Equal(t, compute.StateRunning, desc[0].State)
			}
		})
	}
}

func TestTerminateInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	tests := []struct {
		name      string
		ids       []string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", ids: []string{id}},
		{name: "not found", ids: []string{"nonexistent"}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.TerminateInstances(ctx, tt.ids)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				desc, descErr := m.DescribeInstances(ctx, []string{id}, nil)
				require.NoError(t, descErr)
				require.Len(t, desc, 1)
				assert.Equal(t, compute.StateTerminated, desc[0].State)
			}
		})
	}
}

func TestModifyInstance(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	tests := []struct {
		name      string
		setup     func()
		instID    string
		input     driver.ModifyInstanceInput
		wantErr   bool
		errSubstr string
	}{
		{name: "must be stopped", instID: id, input: driver.ModifyInstanceInput{InstanceType: "n1-standard-2"}, wantErr: true, errSubstr: "must be stopped"},
		{name: "not found", instID: "nonexistent", input: driver.ModifyInstanceInput{}, wantErr: true, errSubstr: "not found"},
		{name: "success after stop", instID: id, setup: func() {
			require.NoError(t, m.StopInstances(ctx, []string{id}))
		}, input: driver.ModifyInstanceInput{InstanceType: "n1-standard-4", Tags: map[string]string{"new": "tag"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			err := m.ModifyInstance(ctx, tt.instID, tt.input)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				desc, descErr := m.DescribeInstances(ctx, []string{tt.instID}, nil)
				require.NoError(t, descErr)
				require.Len(t, desc, 1)
				assert.Equal(t, "n1-standard-4", desc[0].InstanceType)
				assert.Equal(t, "tag", desc[0].Tags["new"])
			}
		})
	}
}

func TestMatchesFilters(t *testing.T) {
	inst := &instanceData{
		ID: "i-123", InstanceType: "n1-standard-1", State: "running",
		Tags: map[string]string{"Name": "web"},
	}

	tests := []struct {
		name    string
		filters []driver.DescribeFilter
		want    bool
	}{
		{name: "no filters", filters: nil, want: true},
		{name: "match id", filters: []driver.DescribeFilter{{Name: "instance-id", Values: []string{"i-123"}}}, want: true},
		{name: "no match id", filters: []driver.DescribeFilter{{Name: "instance-id", Values: []string{"i-999"}}}, want: false},
		{name: "match type", filters: []driver.DescribeFilter{{Name: "instance-type", Values: []string{"n1-standard-1"}}}, want: true},
		{name: "match state", filters: []driver.DescribeFilter{{Name: "instance-state-name", Values: []string{"running"}}}, want: true},
		{name: "match tag", filters: []driver.DescribeFilter{{Name: "tag:Name", Values: []string{"web"}}}, want: true},
		{name: "no match tag", filters: []driver.DescribeFilter{{Name: "tag:Name", Values: []string{"api"}}}, want: false},
		{name: "unknown filter passes", filters: []driver.DescribeFilter{{Name: "other", Values: []string{"x"}}}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchesFilters(inst, tt.filters))
		})
	}
}
