package virtualmachines

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/compute"
	"github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/config"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub"))

	return New(opts)
}

func newTestMockWithMonitoring() (*Mock, *vmMetricsCollector) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-sub"))
	m := New(opts)
	mon := &vmMetricsCollector{}
	m.SetMonitoring(mon)

	return m, mon
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

func TestCreateAutoScalingGroup(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		cfg     driver.AutoScalingGroupConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg: driver.AutoScalingGroupConfig{
				Name: "my-vmss", MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
				Tags:           map[string]string{"env": "test"},
			},
		},
		{
			name: "empty name",
			cfg: driver.AutoScalingGroupConfig{
				MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "t2"},
			},
			wantErr: true, errMsg: "name is required",
		},
		{
			name: "desired out of bounds",
			cfg: driver.AutoScalingGroupConfig{
				Name: "bad-vmss", MinSize: 2, MaxSize: 5, DesiredCapacity: 10,
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "t2"},
			},
			wantErr: true, errMsg: "outside bounds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMock()
			asg, err := m.CreateAutoScalingGroup(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.cfg.Name, asg.Name)
				assert.Equal(t, tt.cfg.DesiredCapacity, asg.DesiredCapacity)
				assert.Len(t, asg.InstanceIDs, tt.cfg.DesiredCapacity)
				assert.Equal(t, "active", asg.Status)
			}
		})
	}

	t.Run("duplicate name", func(t *testing.T) {
		m := newTestMock()
		cfg := driver.AutoScalingGroupConfig{
			Name: "dup-vmss", MinSize: 0, MaxSize: 2, DesiredCapacity: 1,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "t2"},
		}
		_, err := m.CreateAutoScalingGroup(ctx, cfg)
		require.NoError(t, err)

		_, err = m.CreateAutoScalingGroup(ctx, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})
}

func TestSetDesiredCapacity(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "vmss1", MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
	})
	require.NoError(t, err)

	t.Run("scale up", func(t *testing.T) {
		err := m.SetDesiredCapacity(ctx, "vmss1", 4)
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss1")
		require.NoError(t, err)
		assert.Equal(t, 4, asg.CurrentSize)
		assert.Len(t, asg.InstanceIDs, 4)
	})

	t.Run("scale down", func(t *testing.T) {
		err := m.SetDesiredCapacity(ctx, "vmss1", 1)
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss1")
		require.NoError(t, err)
		assert.Equal(t, 1, asg.CurrentSize)
	})

	t.Run("outside bounds", func(t *testing.T) {
		err := m.SetDesiredCapacity(ctx, "vmss1", 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside bounds")
	})

	t.Run("not found", func(t *testing.T) {
		err := m.SetDesiredCapacity(ctx, "missing", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestScalingPolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "vmss1", MinSize: 1, MaxSize: 10, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
	})
	require.NoError(t, err)

	t.Run("put and execute ChangeInCapacity policy", func(t *testing.T) {
		policy := driver.ScalingPolicy{
			Name: "scale-out", AutoScalingGroup: "vmss1",
			PolicyType: "SimpleScaling", AdjustmentType: "ChangeInCapacity",
			ScalingAdjustment: 3,
		}
		require.NoError(t, m.PutScalingPolicy(ctx, policy))

		err := m.ExecuteScalingPolicy(ctx, "vmss1", "scale-out")
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss1")
		require.NoError(t, err)
		assert.Equal(t, 5, asg.CurrentSize)
	})

	t.Run("put and execute ExactCapacity policy", func(t *testing.T) {
		policy := driver.ScalingPolicy{
			Name: "set-exact", AutoScalingGroup: "vmss1",
			PolicyType: "SimpleScaling", AdjustmentType: "ExactCapacity",
			ScalingAdjustment: 3,
		}
		require.NoError(t, m.PutScalingPolicy(ctx, policy))

		err := m.ExecuteScalingPolicy(ctx, "vmss1", "set-exact")
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss1")
		require.NoError(t, err)
		assert.Equal(t, 3, asg.CurrentSize)
	})

	t.Run("delete policy", func(t *testing.T) {
		err := m.DeleteScalingPolicy(ctx, "vmss1", "scale-out")
		require.NoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "vmss1", "scale-out")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("policy on nonexistent ASG", func(t *testing.T) {
		err := m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name: "p", AutoScalingGroup: "missing",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestRequestSpotInstances(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		cfg     driver.SpotRequestConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg: driver.SpotRequestConfig{
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
				MaxPrice:       0.5, Count: 2, Type: "one-time",
			},
		},
		{
			name: "zero count",
			cfg: driver.SpotRequestConfig{
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "t2"},
				MaxPrice:       0.5, Count: 0,
			},
			wantErr: true, errMsg: "count must be greater than 0",
		},
		{
			name: "zero price",
			cfg: driver.SpotRequestConfig{
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "t2"},
				MaxPrice:       0, Count: 1,
			},
			wantErr: true, errMsg: "max price must be greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMock()
			reqs, err := m.RequestSpotInstances(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Len(t, reqs, tt.cfg.Count)

				for _, req := range reqs {
					assert.NotEmpty(t, req.ID)
					assert.Equal(t, "active", req.Status)
					assert.NotEmpty(t, req.InstanceID)
					assert.Equal(t, tt.cfg.MaxPrice, req.MaxPrice)
				}
			}
		})
	}
}

func TestCancelSpotRequests(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		MaxPrice:       0.5, Count: 1, Type: "one-time",
	})
	require.NoError(t, err)
	require.Len(t, reqs, 1)

	t.Run("cancel one-time request terminates instance", func(t *testing.T) {
		err := m.CancelSpotRequests(ctx, []string{reqs[0].ID})
		require.NoError(t, err)

		described, err := m.DescribeSpotRequests(ctx, []string{reqs[0].ID})
		require.NoError(t, err)
		assert.Equal(t, "canceled", described[0].Status)

		// Instance should be terminated
		instances, _ := m.DescribeInstances(ctx, []string{reqs[0].InstanceID}, nil)
		require.Len(t, instances, 1)
		assert.Equal(t, compute.StateTerminated, instances[0].State)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.CancelSpotRequests(ctx, []string{"missing-id"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestCreateLaunchTemplate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		cfg     driver.LaunchTemplateConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg: driver.LaunchTemplateConfig{
				Name:           "web-template",
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B2s"},
			},
		},
		{
			name:    "empty name",
			cfg:     driver.LaunchTemplateConfig{InstanceConfig: driver.InstanceConfig{ImageID: "img-1"}},
			wantErr: true, errMsg: "template name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMock()
			tmpl, err := m.CreateLaunchTemplate(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, tmpl.ID)
				assert.Equal(t, tt.cfg.Name, tmpl.Name)
				assert.Equal(t, tt.cfg.InstanceConfig.ImageID, tmpl.InstanceConfig.ImageID)
				assert.Greater(t, tmpl.Version, 0)
				assert.NotEmpty(t, tmpl.CreatedAt)
			}
		})
	}

	t.Run("duplicate name", func(t *testing.T) {
		m := newTestMock()
		cfg := driver.LaunchTemplateConfig{
			Name:           "dup-tmpl",
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
		}
		_, err := m.CreateLaunchTemplate(ctx, cfg)
		require.NoError(t, err)

		_, err = m.CreateLaunchTemplate(ctx, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("get and list templates", func(t *testing.T) {
		m := newTestMock()
		cfg := driver.LaunchTemplateConfig{
			Name:           "my-tmpl",
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		}
		_, err := m.CreateLaunchTemplate(ctx, cfg)
		require.NoError(t, err)

		tmpl, err := m.GetLaunchTemplate(ctx, "my-tmpl")
		require.NoError(t, err)
		assert.Equal(t, "my-tmpl", tmpl.Name)

		list, err := m.ListLaunchTemplates(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 1)
	})

	t.Run("delete template", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "del-tmpl",
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
		})
		require.NoError(t, err)

		require.NoError(t, m.DeleteLaunchTemplate(ctx, "del-tmpl"))

		_, err = m.GetLaunchTemplate(ctx, "del-tmpl")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeleteAutoScalingGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("force delete terminates instances", func(t *testing.T) {
		m := newTestMock()
		asg, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss1", MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)
		require.Len(t, asg.InstanceIDs, 2)

		err = m.DeleteAutoScalingGroup(ctx, "vmss1", true)
		require.NoError(t, err)

		// ASG should be gone
		_, err = m.GetAutoScalingGroup(ctx, "vmss1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		// Instances should be terminated
		for _, id := range asg.InstanceIDs {
			instances, _ := m.DescribeInstances(ctx, []string{id}, nil)
			require.Len(t, instances, 1)
			assert.Equal(t, compute.StateTerminated, instances[0].State)
		}
	})

	t.Run("non-force delete with instances fails", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss2", MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		err = m.DeleteAutoScalingGroup(ctx, "vmss2", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has instances")
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		err := m.DeleteAutoScalingGroup(ctx, "missing", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestGetAutoScalingGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "vmss1", MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		asg, err := m.GetAutoScalingGroup(ctx, "vmss1")
		require.NoError(t, err)
		assert.Equal(t, "vmss1", asg.Name)
		assert.Equal(t, 2, asg.DesiredCapacity)
		assert.Equal(t, 2, asg.CurrentSize)
		assert.Equal(t, "active", asg.Status)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetAutoScalingGroup(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListAutoScalingGroups(t *testing.T) {
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		m := newTestMock()
		asgs, err := m.ListAutoScalingGroups(ctx)
		require.NoError(t, err)
		assert.Empty(t, asgs)
	})

	t.Run("multiple", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss1", MinSize: 0, MaxSize: 3, DesiredCapacity: 1,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		_, err = m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss2", MinSize: 0, MaxSize: 5, DesiredCapacity: 2,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		asgs, err := m.ListAutoScalingGroups(ctx)
		require.NoError(t, err)
		assert.Len(t, asgs, 2)
	})
}

func TestUpdateAutoScalingGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("scale up", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss1", MinSize: 1, MaxSize: 10, DesiredCapacity: 2,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		err = m.UpdateAutoScalingGroup(ctx, "vmss1", 5, 1, 10)
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss1")
		require.NoError(t, err)
		assert.Equal(t, 5, asg.CurrentSize)
		assert.Len(t, asg.InstanceIDs, 5)
	})

	t.Run("scale down", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss2", MinSize: 1, MaxSize: 10, DesiredCapacity: 5,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		err = m.UpdateAutoScalingGroup(ctx, "vmss2", 2, 1, 10)
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss2")
		require.NoError(t, err)
		assert.Equal(t, 2, asg.CurrentSize)
		assert.Len(t, asg.InstanceIDs, 2)
	})

	t.Run("invalid bounds", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss3", MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		err = m.UpdateAutoScalingGroup(ctx, "vmss3", 10, 1, 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside bounds")
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		err := m.UpdateAutoScalingGroup(ctx, "missing", 2, 1, 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestPutScalingPolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "vmss1", MinSize: 1, MaxSize: 10, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
	})
	require.NoError(t, err)

	t.Run("create policy", func(t *testing.T) {
		policy := driver.ScalingPolicy{
			Name: "scale-out", AutoScalingGroup: "vmss1",
			PolicyType: "SimpleScaling", AdjustmentType: "ChangeInCapacity",
			ScalingAdjustment: 2,
		}
		err := m.PutScalingPolicy(ctx, policy)
		require.NoError(t, err)
	})

	t.Run("update existing policy", func(t *testing.T) {
		policy := driver.ScalingPolicy{
			Name: "scale-out", AutoScalingGroup: "vmss1",
			PolicyType: "SimpleScaling", AdjustmentType: "ExactCapacity",
			ScalingAdjustment: 5,
		}
		err := m.PutScalingPolicy(ctx, policy)
		require.NoError(t, err)

		// Execute to verify it was updated
		err = m.ExecuteScalingPolicy(ctx, "vmss1", "scale-out")
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss1")
		require.NoError(t, err)
		assert.Equal(t, 5, asg.CurrentSize)
	})

	t.Run("ASG not found", func(t *testing.T) {
		err := m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name: "p", AutoScalingGroup: "missing",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeleteScalingPolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "vmss1", MinSize: 1, MaxSize: 10, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
	})
	require.NoError(t, err)

	err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
		Name: "scale-out", AutoScalingGroup: "vmss1",
		PolicyType: "SimpleScaling", AdjustmentType: "ChangeInCapacity",
		ScalingAdjustment: 2,
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := m.DeleteScalingPolicy(ctx, "vmss1", "scale-out")
		require.NoError(t, err)
	})

	t.Run("policy not found", func(t *testing.T) {
		err := m.DeleteScalingPolicy(ctx, "vmss1", "missing-policy")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("ASG not found", func(t *testing.T) {
		err := m.DeleteScalingPolicy(ctx, "missing-asg", "scale-out")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestExecuteScalingPolicy(t *testing.T) {
	ctx := context.Background()

	t.Run("ChangeInCapacity", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss1", MinSize: 1, MaxSize: 10, DesiredCapacity: 3,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name: "add2", AutoScalingGroup: "vmss1",
			AdjustmentType: "ChangeInCapacity", ScalingAdjustment: 2,
		})
		require.NoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "vmss1", "add2")
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss1")
		require.NoError(t, err)
		assert.Equal(t, 5, asg.CurrentSize)
	})

	t.Run("ExactCapacity", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss2", MinSize: 1, MaxSize: 10, DesiredCapacity: 3,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name: "set7", AutoScalingGroup: "vmss2",
			AdjustmentType: "ExactCapacity", ScalingAdjustment: 7,
		})
		require.NoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "vmss2", "set7")
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss2")
		require.NoError(t, err)
		assert.Equal(t, 7, asg.CurrentSize)
	})

	t.Run("PercentChangeInCapacity", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss3", MinSize: 1, MaxSize: 20, DesiredCapacity: 10,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name: "grow50pct", AutoScalingGroup: "vmss3",
			AdjustmentType: "PercentChangeInCapacity", ScalingAdjustment: 50,
		})
		require.NoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "vmss3", "grow50pct")
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss3")
		require.NoError(t, err)
		assert.Equal(t, 15, asg.CurrentSize)
	})

	t.Run("clamped to max", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss4", MinSize: 1, MaxSize: 5, DesiredCapacity: 3,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name: "big-jump", AutoScalingGroup: "vmss4",
			AdjustmentType: "ChangeInCapacity", ScalingAdjustment: 100,
		})
		require.NoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "vmss4", "big-jump")
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss4")
		require.NoError(t, err)
		assert.Equal(t, 5, asg.CurrentSize) // clamped to max
	})

	t.Run("clamped to min", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss5", MinSize: 2, MaxSize: 10, DesiredCapacity: 5,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		err = m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name: "big-shrink", AutoScalingGroup: "vmss5",
			AdjustmentType: "ChangeInCapacity", ScalingAdjustment: -100,
		})
		require.NoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "vmss5", "big-shrink")
		require.NoError(t, err)

		asg, err := m.GetAutoScalingGroup(ctx, "vmss5")
		require.NoError(t, err)
		assert.Equal(t, 2, asg.CurrentSize) // clamped to min
	})

	t.Run("ASG not found", func(t *testing.T) {
		m := newTestMock()
		err := m.ExecuteScalingPolicy(ctx, "missing", "policy")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("policy not found", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
			Name: "vmss6", MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		})
		require.NoError(t, err)

		err = m.ExecuteScalingPolicy(ctx, "vmss6", "missing-policy")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDescribeSpotRequests(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		MaxPrice:       0.5, Count: 3, Type: "one-time",
	})
	require.NoError(t, err)
	require.Len(t, reqs, 3)

	t.Run("all spot requests without filter", func(t *testing.T) {
		results, err := m.DescribeSpotRequests(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, results, 3)
	})

	t.Run("filter by specific IDs", func(t *testing.T) {
		results, err := m.DescribeSpotRequests(ctx, []string{reqs[0].ID, reqs[1].ID})
		require.NoError(t, err)
		assert.Len(t, results, 2)
	})

	t.Run("not found ID", func(t *testing.T) {
		_, err := m.DescribeSpotRequests(ctx, []string{"spot-missing"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestGetLaunchTemplate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name:           "web-tmpl",
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B2s"},
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		tmpl, err := m.GetLaunchTemplate(ctx, "web-tmpl")
		require.NoError(t, err)
		assert.Equal(t, "web-tmpl", tmpl.Name)
		assert.Equal(t, "img-1", tmpl.InstanceConfig.ImageID)
		assert.NotEmpty(t, tmpl.ID)
		assert.NotEmpty(t, tmpl.CreatedAt)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetLaunchTemplate(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListLaunchTemplates(t *testing.T) {
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		m := newTestMock()
		list, err := m.ListLaunchTemplates(ctx)
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("multiple", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "tmpl-a",
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
		})
		require.NoError(t, err)

		_, err = m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "tmpl-b",
			InstanceConfig: driver.InstanceConfig{ImageID: "img-2"},
		})
		require.NoError(t, err)

		list, err := m.ListLaunchTemplates(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})
}

func TestDeleteLaunchTemplate(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
			Name:           "del-tmpl",
			InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
		})
		require.NoError(t, err)

		err = m.DeleteLaunchTemplate(ctx, "del-tmpl")
		require.NoError(t, err)

		_, err = m.GetLaunchTemplate(ctx, "del-tmpl")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		err := m.DeleteLaunchTemplate(ctx, "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestRunInstancesWithMonitoring(t *testing.T) {
	ctx := context.Background()
	m, mon := newTestMockWithMonitoring()

	cfg := driver.InstanceConfig{
		ImageID:      "img-1",
		InstanceType: "Standard_B1s",
	}

	instances, err := m.RunInstances(ctx, cfg, 1)
	require.NoError(t, err)
	require.Len(t, instances, 1)

	// Should have emitted 5 metrics x 5 backfill datapoints = 25 data points
	assert.Len(t, mon.data, 25)
	assert.True(t, mon.hasMetric("Microsoft.Compute/virtualMachines", "Percentage CPU"))
	assert.True(t, mon.hasMetric("Microsoft.Compute/virtualMachines", "Network In Total"))
	assert.True(t, mon.hasMetric("Microsoft.Compute/virtualMachines", "Network Out Total"))
	assert.True(t, mon.hasMetric("Microsoft.Compute/virtualMachines", "Disk Read Operations/Sec"))
	assert.True(t, mon.hasMetric("Microsoft.Compute/virtualMachines", "Disk Write Operations/Sec"))
}

func TestLifecycleMetricsEmission(t *testing.T) {
	ctx := context.Background()
	m, mon := newTestMockWithMonitoring()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "img-1", InstanceType: "t2"}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	t.Run("stop emits zero metrics", func(t *testing.T) {
		mon.reset()
		err := m.StopInstances(ctx, []string{id})
		require.NoError(t, err)
		// 5 metrics, 1 datapoint each
		assert.Len(t, mon.data, 5)

		for _, d := range mon.data {
			assert.Equal(t, 0.0, d.Value)
		}
	})

	t.Run("start emits running metrics", func(t *testing.T) {
		mon.reset()
		err := m.StartInstances(ctx, []string{id})
		require.NoError(t, err)
		assert.Len(t, mon.data, 5)

		assert.True(t, mon.hasMetric("Microsoft.Compute/virtualMachines", "Percentage CPU"))
	})

	t.Run("reboot emits running metrics", func(t *testing.T) {
		mon.reset()
		err := m.RebootInstances(ctx, []string{id})
		require.NoError(t, err)
		assert.Len(t, mon.data, 5)
	})

	t.Run("terminate emits zero metrics", func(t *testing.T) {
		mon.reset()
		err := m.TerminateInstances(ctx, []string{id})
		require.NoError(t, err)
		assert.Len(t, mon.data, 5)

		for _, d := range mon.data {
			assert.Equal(t, 0.0, d.Value)
		}
	})
}

func TestValidateASGBoundsNegativeMin(t *testing.T) {
	err := validateASGBounds(1, -1, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "min size must be >= 0")
}

func TestValidateASGBoundsMaxLessThanMin(t *testing.T) {
	err := validateASGBounds(3, 5, 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max size must be >= min size")
}

func TestCancelSpotRequestPersistentType(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		MaxPrice:       0.5, Count: 1, Type: "persistent",
	})
	require.NoError(t, err)
	require.Len(t, reqs, 1)

	// Cancel persistent request should NOT terminate instances
	err = m.CancelSpotRequests(ctx, []string{reqs[0].ID})
	require.NoError(t, err)

	described, err := m.DescribeSpotRequests(ctx, []string{reqs[0].ID})
	require.NoError(t, err)
	assert.Equal(t, "canceled", described[0].Status)

	// Instance should still be running (not terminated)
	instances, _ := m.DescribeInstances(ctx, []string{reqs[0].InstanceID}, nil)
	require.Len(t, instances, 1)
	assert.Equal(t, compute.StateRunning, instances[0].State)
}

func TestCreateAutoScalingGroupWithAvailabilityZones(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	asg, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "vmss-az", MinSize: 0, MaxSize: 3, DesiredCapacity: 1,
		InstanceConfig:    driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
		AvailabilityZones: []string{"us-east-1a", "us-east-1b"},
		HealthCheckType:   "ELB",
		Tags:              map[string]string{"env": "test"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"us-east-1a", "us-east-1b"}, asg.AvailabilityZones)
	assert.Equal(t, "ELB", asg.HealthCheckType)
	assert.Equal(t, "test", asg.Tags["env"])
	assert.NotEmpty(t, asg.CreatedAt)
}

func TestDeleteAutoScalingGroupForceDeleteNoInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	// Create ASG with 1 instance, then scale down to 0, then non-force delete
	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "vmss-empty", MinSize: 0, MaxSize: 5, DesiredCapacity: 1,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "Standard_B1s"},
	})
	require.NoError(t, err)

	// Scale down to 0 instances
	err = m.SetDesiredCapacity(ctx, "vmss-empty", 0)
	require.NoError(t, err)

	// Non-force delete should succeed when there are no instances
	err = m.DeleteAutoScalingGroup(ctx, "vmss-empty", false)
	require.NoError(t, err)
}

type vmMetricsCollector struct {
	data []mondriver.MetricDatum
}

func (c *vmMetricsCollector) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	c.data = append(c.data, data...)
	return nil
}

func (c *vmMetricsCollector) GetMetricData(_ context.Context, _ mondriver.GetMetricInput) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (c *vmMetricsCollector) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (c *vmMetricsCollector) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (c *vmMetricsCollector) DescribeAlarms(_ context.Context, _ []string) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (c *vmMetricsCollector) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func (c *vmMetricsCollector) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (c *vmMetricsCollector) reset() {
	c.data = nil
}

func (c *vmMetricsCollector) hasMetric(namespace, metricName string) bool {
	for _, d := range c.data {
		if d.Namespace == namespace && d.MetricName == metricName {
			return true
		}
	}

	return false
}
