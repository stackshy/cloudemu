package gce

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
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	return New(opts)
}

func newTestMockWithMonitoring() (*Mock, *gceMonMock) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("us-central1"), config.WithProjectID("test-project"))
	mon := &gceMonMock{data: make(map[string]int)}
	m := New(opts)
	m.SetMonitoring(mon)

	return m, mon
}

type gceMonMock struct {
	data map[string]int
}

func (mon *gceMonMock) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	for _, d := range data {
		key := d.Namespace + "/" + d.MetricName
		mon.data[key]++
	}

	return nil
}

func (mon *gceMonMock) GetMetricData(
	_ context.Context, _ mondriver.GetMetricInput,
) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (mon *gceMonMock) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (mon *gceMonMock) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (mon *gceMonMock) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (mon *gceMonMock) DescribeAlarms(_ context.Context, _ []string) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (mon *gceMonMock) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func (mon *gceMonMock) CreateNotificationChannel(_ context.Context, _ mondriver.NotificationChannelConfig) (*mondriver.NotificationChannelInfo, error) {
	return nil, nil
}

func (mon *gceMonMock) DeleteNotificationChannel(_ context.Context, _ string) error {
	return nil
}

func (mon *gceMonMock) GetNotificationChannel(_ context.Context, _ string) (*mondriver.NotificationChannelInfo, error) {
	return nil, nil
}

func (mon *gceMonMock) ListNotificationChannels(_ context.Context) ([]mondriver.NotificationChannelInfo, error) {
	return nil, nil
}

func (mon *gceMonMock) GetAlarmHistory(_ context.Context, _ string, _ int) ([]mondriver.AlarmHistoryEntry, error) {
	return nil, nil
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

func TestCreateAutoScalingGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.AutoScalingGroupConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "success",
			cfg: driver.AutoScalingGroupConfig{
				Name:            "mig-1",
				MinSize:         1,
				MaxSize:         5,
				DesiredCapacity: 2,
				InstanceConfig:  driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
				Tags:            map[string]string{"env": "test"},
			},
		},
		{
			name: "duplicate name",
			cfg: driver.AutoScalingGroupConfig{
				Name: "mig-1", MinSize: 1, MaxSize: 3, DesiredCapacity: 1,
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
			},
			wantErr:   true,
			errSubstr: "already exists",
		},
		{
			name: "empty name",
			cfg: driver.AutoScalingGroupConfig{
				MinSize: 1, MaxSize: 3, DesiredCapacity: 1,
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
			},
			wantErr:   true,
			errSubstr: "required",
		},
		{
			name: "desired outside bounds",
			cfg: driver.AutoScalingGroupConfig{
				Name: "mig-bad", MinSize: 2, MaxSize: 5, DesiredCapacity: 10,
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
			},
			wantErr:   true,
			errSubstr: "outside bounds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asg, err := m.CreateAutoScalingGroup(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.cfg.Name, asg.Name)
				assert.Equal(t, tt.cfg.DesiredCapacity, asg.DesiredCapacity)
				assert.Len(t, asg.InstanceIDs, tt.cfg.DesiredCapacity)
				assert.Equal(t, "active", asg.Status)
			}
		})
	}
}

func TestSetDesiredCapacity(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-1", MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		asgName   string
		desired   int
		wantErr   bool
		errSubstr string
	}{
		{name: "scale up", asgName: "mig-1", desired: 4},
		{name: "scale down", asgName: "mig-1", desired: 2},
		{name: "not found", asgName: "missing", desired: 1, wantErr: true, errSubstr: "not found"},
		{name: "below min", asgName: "mig-1", desired: 0, wantErr: true, errSubstr: "outside bounds"},
		{name: "above max", asgName: "mig-1", desired: 10, wantErr: true, errSubstr: "outside bounds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.SetDesiredCapacity(ctx, tt.asgName, tt.desired)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				asg, getErr := m.GetAutoScalingGroup(ctx, tt.asgName)
				require.NoError(t, getErr)
				assert.Equal(t, tt.desired, asg.DesiredCapacity)
				assert.Equal(t, tt.desired, asg.CurrentSize)
			}
		})
	}
}

func TestScalingPolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-1", MinSize: 1, MaxSize: 10, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
	})
	require.NoError(t, err)

	t.Run("put and execute ChangeInCapacity policy", func(t *testing.T) {
		policy := driver.ScalingPolicy{
			Name:              "scale-out",
			AutoScalingGroup:  "mig-1",
			PolicyType:        "SimpleScaling",
			AdjustmentType:    "ChangeInCapacity",
			ScalingAdjustment: 3,
		}
		require.NoError(t, m.PutScalingPolicy(ctx, policy))
		require.NoError(t, m.ExecuteScalingPolicy(ctx, "mig-1", "scale-out"))

		asg, getErr := m.GetAutoScalingGroup(ctx, "mig-1")
		require.NoError(t, getErr)
		assert.Equal(t, 5, asg.DesiredCapacity) // 2 + 3
	})

	t.Run("execute ExactCapacity policy", func(t *testing.T) {
		policy := driver.ScalingPolicy{
			Name:              "set-exact",
			AutoScalingGroup:  "mig-1",
			PolicyType:        "SimpleScaling",
			AdjustmentType:    "ExactCapacity",
			ScalingAdjustment: 3,
		}
		require.NoError(t, m.PutScalingPolicy(ctx, policy))
		require.NoError(t, m.ExecuteScalingPolicy(ctx, "mig-1", "set-exact"))

		asg, getErr := m.GetAutoScalingGroup(ctx, "mig-1")
		require.NoError(t, getErr)
		assert.Equal(t, 3, asg.DesiredCapacity)
	})

	t.Run("delete scaling policy", func(t *testing.T) {
		require.NoError(t, m.DeleteScalingPolicy(ctx, "mig-1", "scale-out"))

		execErr := m.ExecuteScalingPolicy(ctx, "mig-1", "scale-out")
		require.Error(t, execErr)
		assert.Contains(t, execErr.Error(), "not found")
	})

	t.Run("policy on missing ASG", func(t *testing.T) {
		putErr := m.PutScalingPolicy(ctx, driver.ScalingPolicy{
			Name: "p1", AutoScalingGroup: "missing",
		})
		require.Error(t, putErr)
		assert.Contains(t, putErr.Error(), "not found")
	})
}

func TestRequestSpotInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.SpotRequestConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "success",
			cfg: driver.SpotRequestConfig{
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
				MaxPrice:       0.05,
				Count:          2,
				Type:           "one-time",
			},
		},
		{
			name: "zero count",
			cfg: driver.SpotRequestConfig{
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
				MaxPrice:       0.05,
				Count:          0,
			},
			wantErr:   true,
			errSubstr: "greater than 0",
		},
		{
			name: "zero price",
			cfg: driver.SpotRequestConfig{
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
				MaxPrice:       0,
				Count:          1,
			},
			wantErr:   true,
			errSubstr: "greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqs, err := m.RequestSpotInstances(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				require.Len(t, reqs, tt.cfg.Count)
				assert.Equal(t, "active", reqs[0].Status)
				assert.NotEmpty(t, reqs[0].InstanceID)
				assert.NotEmpty(t, reqs[0].ID)
			}
		})
	}
}

func TestCancelSpotRequests(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
		MaxPrice:       0.05,
		Count:          1,
		Type:           "one-time",
	})
	require.NoError(t, err)
	require.Len(t, reqs, 1)

	tests := []struct {
		name      string
		ids       []string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", ids: []string{reqs[0].ID}},
		{name: "not found", ids: []string{"missing"}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cancelErr := m.CancelSpotRequests(ctx, tt.ids)
			switch {
			case tt.wantErr:
				require.Error(t, cancelErr)
				assert.Contains(t, cancelErr.Error(), tt.errSubstr)
			default:
				require.NoError(t, cancelErr)
				// Verify the request is now canceled
				described, descErr := m.DescribeSpotRequests(ctx, tt.ids)
				require.NoError(t, descErr)
				assert.Equal(t, "canceled", described[0].Status)
			}
		})
	}
}

func TestCreateLaunchTemplate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.LaunchTemplateConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "success",
			cfg: driver.LaunchTemplateConfig{
				Name: "tmpl-1",
				InstanceConfig: driver.InstanceConfig{
					ImageID: "img-1", InstanceType: "n1-standard-1",
				},
			},
		},
		{
			name: "duplicate",
			cfg: driver.LaunchTemplateConfig{
				Name:           "tmpl-1",
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
			},
			wantErr:   true,
			errSubstr: "already exists",
		},
		{
			name:      "empty name",
			cfg:       driver.LaunchTemplateConfig{},
			wantErr:   true,
			errSubstr: "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := m.CreateLaunchTemplate(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.cfg.Name, tmpl.Name)
				assert.NotEmpty(t, tmpl.ID)
				assert.Greater(t, tmpl.Version, 0)
				assert.NotEmpty(t, tmpl.CreatedAt)

				// Verify get works
				got, getErr := m.GetLaunchTemplate(ctx, tt.cfg.Name)
				require.NoError(t, getErr)
				assert.Equal(t, tmpl.Name, got.Name)
			}
		})
	}

	t.Run("delete template", func(t *testing.T) {
		require.NoError(t, m.DeleteLaunchTemplate(ctx, "tmpl-1"))
		_, getErr := m.GetLaunchTemplate(ctx, "tmpl-1")
		require.Error(t, getErr)
		assert.Contains(t, getErr.Error(), "not found")
	})

	t.Run("delete not found", func(t *testing.T) {
		delErr := m.DeleteLaunchTemplate(ctx, "missing")
		require.Error(t, delErr)
		assert.Contains(t, delErr.Error(), "not found")
	})
}

func TestDeleteAutoScalingGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-del", MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
	})
	require.NoError(t, err)

	tests := []struct {
		name        string
		asgName     string
		forceDelete bool
		wantErr     bool
		errSubstr   string
	}{
		{
			name:        "non-force with instances fails",
			asgName:     "mig-del",
			forceDelete: false,
			wantErr:     true,
			errSubstr:   "has instances",
		},
		{
			name:        "force delete with instances succeeds",
			asgName:     "mig-del",
			forceDelete: true,
		},
		{
			name:        "not found",
			asgName:     "mig-missing",
			forceDelete: false,
			wantErr:     true,
			errSubstr:   "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteAutoScalingGroup(ctx, tt.asgName, tt.forceDelete)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				_, getErr := m.GetAutoScalingGroup(ctx, tt.asgName)
				require.Error(t, getErr)
				assert.Contains(t, getErr.Error(), "not found")
			}
		})
	}
}

func TestGetAutoScalingGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-get", MinSize: 1, MaxSize: 5, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		asgName   string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", asgName: "mig-get"},
		{name: "not found", asgName: "mig-missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asg, err := m.GetAutoScalingGroup(ctx, tt.asgName)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "mig-get", asg.Name)
				assert.Equal(t, 2, asg.DesiredCapacity)
				assert.Equal(t, "active", asg.Status)
				assert.Len(t, asg.InstanceIDs, 2)
			}
		})
	}
}

func TestListAutoScalingGroups(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("empty list", func(t *testing.T) {
		asgs, err := m.ListAutoScalingGroups(ctx)
		require.NoError(t, err)
		assert.Empty(t, asgs)
	})

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-a", MinSize: 1, MaxSize: 3, DesiredCapacity: 1,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
	})
	require.NoError(t, err)

	_, err = m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-b", MinSize: 1, MaxSize: 3, DesiredCapacity: 1,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
	})
	require.NoError(t, err)

	t.Run("list two groups", func(t *testing.T) {
		asgs, listErr := m.ListAutoScalingGroups(ctx)
		require.NoError(t, listErr)
		assert.Len(t, asgs, 2)
	})
}

func TestUpdateAutoScalingGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-upd", MinSize: 1, MaxSize: 10, DesiredCapacity: 3,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		asgName   string
		desired   int
		minSize   int
		maxSize   int
		wantErr   bool
		errSubstr string
	}{
		{name: "scale up", asgName: "mig-upd", desired: 5, minSize: 1, maxSize: 10},
		{name: "scale down", asgName: "mig-upd", desired: 2, minSize: 1, maxSize: 10},
		{name: "update bounds", asgName: "mig-upd", desired: 2, minSize: 2, maxSize: 8},
		{name: "not found", asgName: "mig-missing", desired: 1, minSize: 1, maxSize: 5, wantErr: true, errSubstr: "not found"},
		{name: "invalid bounds", asgName: "mig-upd", desired: 15, minSize: 1, maxSize: 10, wantErr: true, errSubstr: "outside bounds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.UpdateAutoScalingGroup(ctx, tt.asgName, tt.desired, tt.minSize, tt.maxSize)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				asg, getErr := m.GetAutoScalingGroup(ctx, tt.asgName)
				require.NoError(t, getErr)
				assert.Equal(t, tt.desired, asg.DesiredCapacity)
				assert.Equal(t, tt.minSize, asg.MinSize)
				assert.Equal(t, tt.maxSize, asg.MaxSize)
			}
		})
	}
}

func TestPutScalingPolicyCreateAndUpdate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-pol", MinSize: 1, MaxSize: 10, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
	})
	require.NoError(t, err)

	t.Run("create policy", func(t *testing.T) {
		policy := driver.ScalingPolicy{
			Name:              "p1",
			AutoScalingGroup:  "mig-pol",
			PolicyType:        "SimpleScaling",
			AdjustmentType:    "ChangeInCapacity",
			ScalingAdjustment: 2,
		}
		require.NoError(t, m.PutScalingPolicy(ctx, policy))

		// Execute to verify it works
		require.NoError(t, m.ExecuteScalingPolicy(ctx, "mig-pol", "p1"))
		asg, getErr := m.GetAutoScalingGroup(ctx, "mig-pol")
		require.NoError(t, getErr)
		assert.Equal(t, 4, asg.DesiredCapacity)
	})

	t.Run("update policy by re-putting", func(t *testing.T) {
		policy := driver.ScalingPolicy{
			Name:              "p1",
			AutoScalingGroup:  "mig-pol",
			PolicyType:        "SimpleScaling",
			AdjustmentType:    "ExactCapacity",
			ScalingAdjustment: 2,
		}
		require.NoError(t, m.PutScalingPolicy(ctx, policy))

		require.NoError(t, m.ExecuteScalingPolicy(ctx, "mig-pol", "p1"))
		asg, getErr := m.GetAutoScalingGroup(ctx, "mig-pol")
		require.NoError(t, getErr)
		assert.Equal(t, 2, asg.DesiredCapacity)
	})
}

func TestDeleteScalingPolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-delpol", MinSize: 1, MaxSize: 10, DesiredCapacity: 2,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
	})
	require.NoError(t, err)

	require.NoError(t, m.PutScalingPolicy(ctx, driver.ScalingPolicy{
		Name: "p1", AutoScalingGroup: "mig-delpol",
		AdjustmentType: "ChangeInCapacity", ScalingAdjustment: 1,
	}))

	tests := []struct {
		name      string
		asgName   string
		polName   string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", asgName: "mig-delpol", polName: "p1"},
		{name: "policy not found", asgName: "mig-delpol", polName: "p1", wantErr: true, errSubstr: "not found"},
		{name: "ASG not found", asgName: "missing", polName: "p1", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteScalingPolicy(ctx, tt.asgName, tt.polName)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestExecuteScalingPolicyAllTypes(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-exec", MinSize: 1, MaxSize: 20, DesiredCapacity: 10,
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
	})
	require.NoError(t, err)

	tests := []struct {
		name        string
		policy      driver.ScalingPolicy
		wantDesired int
	}{
		{
			name: "ChangeInCapacity positive",
			policy: driver.ScalingPolicy{
				Name: "change-up", AutoScalingGroup: "mig-exec",
				AdjustmentType: "ChangeInCapacity", ScalingAdjustment: 3,
			},
			wantDesired: 13,
		},
		{
			name: "ExactCapacity",
			policy: driver.ScalingPolicy{
				Name: "exact", AutoScalingGroup: "mig-exec",
				AdjustmentType: "ExactCapacity", ScalingAdjustment: 5,
			},
			wantDesired: 5,
		},
		{
			name: "PercentChangeInCapacity",
			policy: driver.ScalingPolicy{
				Name: "percent", AutoScalingGroup: "mig-exec",
				AdjustmentType: "PercentChangeInCapacity", ScalingAdjustment: 100,
			},
			wantDesired: 10, // 5 + 100% of 5 = 10
		},
		{
			name: "ChangeInCapacity negative",
			policy: driver.ScalingPolicy{
				Name: "change-down", AutoScalingGroup: "mig-exec",
				AdjustmentType: "ChangeInCapacity", ScalingAdjustment: -5,
			},
			wantDesired: 5,
		},
		{
			name: "clamp to min",
			policy: driver.ScalingPolicy{
				Name: "clamp-min", AutoScalingGroup: "mig-exec",
				AdjustmentType: "ChangeInCapacity", ScalingAdjustment: -20,
			},
			wantDesired: 1,
		},
		{
			name: "clamp to max",
			policy: driver.ScalingPolicy{
				Name: "clamp-max", AutoScalingGroup: "mig-exec",
				AdjustmentType: "ExactCapacity", ScalingAdjustment: 100,
			},
			wantDesired: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, m.PutScalingPolicy(ctx, tt.policy))
			require.NoError(t, m.ExecuteScalingPolicy(ctx, "mig-exec", tt.policy.Name))

			asg, getErr := m.GetAutoScalingGroup(ctx, "mig-exec")
			require.NoError(t, getErr)
			assert.Equal(t, tt.wantDesired, asg.DesiredCapacity)
		})
	}

	t.Run("execute on missing ASG", func(t *testing.T) {
		execErr := m.ExecuteScalingPolicy(ctx, "missing-asg", "any-policy")
		require.Error(t, execErr)
		assert.Contains(t, execErr.Error(), "not found")
	})

	t.Run("execute missing policy", func(t *testing.T) {
		execErr := m.ExecuteScalingPolicy(ctx, "mig-exec", "nonexistent-policy")
		require.Error(t, execErr)
		assert.Contains(t, execErr.Error(), "not found")
	})
}

func TestDescribeSpotRequests(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
		MaxPrice:       0.05,
		Count:          2,
		Type:           "one-time",
	})
	require.NoError(t, err)
	require.Len(t, reqs, 2)

	tests := []struct {
		name      string
		ids       []string
		wantCount int
		wantErr   bool
		errSubstr string
	}{
		{name: "all requests (empty IDs)", ids: nil, wantCount: 2},
		{name: "by specific ID", ids: []string{reqs[0].ID}, wantCount: 1},
		{name: "multiple IDs", ids: []string{reqs[0].ID, reqs[1].ID}, wantCount: 2},
		{name: "not found", ids: []string{"preempt-missing"}, wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.DescribeSpotRequests(ctx, tt.ids)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Len(t, result, tt.wantCount)
			}
		})
	}
}

func TestGetLaunchTemplate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name:           "tmpl-get",
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		tmplName  string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", tmplName: "tmpl-get"},
		{name: "not found", tmplName: "tmpl-missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := m.GetLaunchTemplate(ctx, tt.tmplName)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "tmpl-get", tmpl.Name)
				assert.Equal(t, "img-1", tmpl.InstanceConfig.ImageID)
				assert.NotEmpty(t, tmpl.ID)
			}
		})
	}
}

func TestListLaunchTemplates(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	t.Run("empty list", func(t *testing.T) {
		templates, err := m.ListLaunchTemplates(ctx)
		require.NoError(t, err)
		assert.Empty(t, templates)
	})

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

	t.Run("list two templates", func(t *testing.T) {
		templates, listErr := m.ListLaunchTemplates(ctx)
		require.NoError(t, listErr)
		assert.Len(t, templates, 2)
	})
}

func TestDeleteLaunchTemplate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name:           "tmpl-del",
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		tmplName  string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", tmplName: "tmpl-del"},
		{name: "already deleted", tmplName: "tmpl-del", wantErr: true, errSubstr: "not found"},
		{name: "never existed", tmplName: "tmpl-missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteLaunchTemplate(ctx, tt.tmplName)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				_, getErr := m.GetLaunchTemplate(ctx, tt.tmplName)
				require.Error(t, getErr)
			}
		})
	}
}

func TestRunInstancesWithMonitoring(t *testing.T) {
	ctx := context.Background()
	m, mon := newTestMockWithMonitoring()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "img-1", InstanceType: "n1-standard-1",
	}, 1)
	require.NoError(t, err)
	require.Len(t, instances, 1)

	// Each metric should have 5 backfill datapoints
	assert.Equal(t, 5, mon.data["compute.googleapis.com/instance/cpu/utilization"])
	assert.Equal(t, 5, mon.data["compute.googleapis.com/instance/network/received_bytes_count"])
	assert.Equal(t, 5, mon.data["compute.googleapis.com/instance/network/sent_bytes_count"])
	assert.Equal(t, 5, mon.data["compute.googleapis.com/instance/disk/read_ops_count"])
	assert.Equal(t, 5, mon.data["compute.googleapis.com/instance/disk/write_ops_count"])
}

func TestLifecycleMetricsEmission(t *testing.T) {
	ctx := context.Background()
	m, mon := newTestMockWithMonitoring()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "img-1", InstanceType: "n1-standard-1",
	}, 1)
	require.NoError(t, err)
	id := instances[0].ID

	// Clear the counters from RunInstances
	for k := range mon.data {
		mon.data[k] = 0
	}

	t.Run("stop emits lifecycle metrics", func(t *testing.T) {
		require.NoError(t, m.StopInstances(ctx, []string{id}))
		assert.Equal(t, 1, mon.data["compute.googleapis.com/instance/cpu/utilization"])
	})

	t.Run("start emits lifecycle metrics", func(t *testing.T) {
		// Clear again
		for k := range mon.data {
			mon.data[k] = 0
		}

		require.NoError(t, m.StartInstances(ctx, []string{id}))
		assert.Equal(t, 1, mon.data["compute.googleapis.com/instance/cpu/utilization"])
	})

	t.Run("reboot emits lifecycle metrics", func(t *testing.T) {
		for k := range mon.data {
			mon.data[k] = 0
		}

		require.NoError(t, m.RebootInstances(ctx, []string{id}))
		assert.Equal(t, 1, mon.data["compute.googleapis.com/instance/cpu/utilization"])
	})

	t.Run("terminate emits lifecycle metrics", func(t *testing.T) {
		for k := range mon.data {
			mon.data[k] = 0
		}

		require.NoError(t, m.TerminateInstances(ctx, []string{id}))
		assert.Equal(t, 1, mon.data["compute.googleapis.com/instance/cpu/utilization"])
	})
}

func TestValidateASGBoundsEdgeCases(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.AutoScalingGroupConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "negative min size",
			cfg: driver.AutoScalingGroupConfig{
				Name: "mig-neg", MinSize: -1, MaxSize: 5, DesiredCapacity: 1,
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
			},
			wantErr:   true,
			errSubstr: "min size must be >= 0",
		},
		{
			name: "max less than min",
			cfg: driver.AutoScalingGroupConfig{
				Name: "mig-bad", MinSize: 5, MaxSize: 2, DesiredCapacity: 3,
				InstanceConfig: driver.InstanceConfig{ImageID: "img-1"},
			},
			wantErr:   true,
			errSubstr: "max size must be >= min size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.CreateAutoScalingGroup(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestCreateASGWithAvailabilityZones(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	asg, err := m.CreateAutoScalingGroup(ctx, driver.AutoScalingGroupConfig{
		Name: "mig-zones", MinSize: 0, MaxSize: 3, DesiredCapacity: 1,
		InstanceConfig:    driver.InstanceConfig{ImageID: "img-1"},
		AvailabilityZones: []string{"us-central1-a", "us-central1-b"},
		Tags:              map[string]string{"env": "test"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"us-central1-a", "us-central1-b"}, asg.AvailabilityZones)
	assert.Equal(t, "test", asg.Tags["env"])
}

func TestCancelSpotRequestsPersistentType(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	// Create a persistent-type spot request
	reqs, err := m.RequestSpotInstances(ctx, driver.SpotRequestConfig{
		InstanceConfig: driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"},
		MaxPrice:       0.05,
		Count:          1,
		Type:           "persistent",
	})
	require.NoError(t, err)
	require.Len(t, reqs, 1)

	instID := reqs[0].InstanceID

	// Cancel should not terminate instance for persistent type
	require.NoError(t, m.CancelSpotRequests(ctx, []string{reqs[0].ID}))

	// The instance should still exist and be running (persistent type does not auto-terminate)
	desc, err := m.DescribeInstances(ctx, []string{instID}, nil)
	require.NoError(t, err)
	require.Len(t, desc, 1)
	assert.Equal(t, compute.StateRunning, desc[0].State)
}

// =====================================================================
// Volume Tests
// =====================================================================

func TestCreateVolume(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		cfg       driver.VolumeConfig
		wantErr   bool
		errSubstr string
	}{
		{
			name: "success",
			cfg:  driver.VolumeConfig{Size: 100, VolumeType: "pd-ssd", Tags: map[string]string{"env": "test"}},
		},
		{
			name: "default volume type",
			cfg:  driver.VolumeConfig{Size: 50},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMock()
			vol, err := m.CreateVolume(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.NotEmpty(t, vol.ID)
				assert.Equal(t, tt.cfg.Size, vol.Size)
				assert.Equal(t, "available", vol.State)
				assert.NotEmpty(t, vol.CreatedAt)
			}
		})
	}
}

func TestDeleteVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		require.NoError(t, err)

		err = m.DeleteVolume(ctx, vol.ID)
		require.NoError(t, err)

		// Should be gone
		vols, err := m.DescribeVolumes(ctx, []string{vol.ID})
		require.NoError(t, err)
		assert.Empty(t, vols)
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		err := m.DeleteVolume(ctx, "disk-nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDescribeVolumes(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vol1, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	require.NoError(t, err)

	vol2, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 20})
	require.NoError(t, err)

	t.Run("describe all", func(t *testing.T) {
		vols, err := m.DescribeVolumes(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, vols, 2)
	})

	t.Run("describe by ID", func(t *testing.T) {
		vols, err := m.DescribeVolumes(ctx, []string{vol1.ID})
		require.NoError(t, err)
		assert.Len(t, vols, 1)
		assert.Equal(t, vol1.ID, vols[0].ID)
	})

	t.Run("empty list", func(t *testing.T) {
		fresh := newTestMock()
		vols, err := fresh.DescribeVolumes(ctx, nil)
		require.NoError(t, err)
		assert.Empty(t, vols)
	})

	// Keep vol2 referenced
	assert.NotEmpty(t, vol2.ID)
}

func TestAttachVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		instances, err := m.RunInstances(ctx, driver.InstanceConfig{
			ImageID: "img-1", InstanceType: "n1-standard-1",
		}, 1)
		require.NoError(t, err)

		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		require.NoError(t, err)

		err = m.AttachVolume(ctx, vol.ID, instances[0].ID, "/dev/sdf")
		require.NoError(t, err)

		// Verify state changed to in-use
		vols, err := m.DescribeVolumes(ctx, []string{vol.ID})
		require.NoError(t, err)
		require.Len(t, vols, 1)
		assert.Equal(t, "in-use", vols[0].State)
		assert.Equal(t, instances[0].ID, vols[0].AttachedTo)
	})

	t.Run("nonexistent instance", func(t *testing.T) {
		m := newTestMock()
		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		require.NoError(t, err)

		err = m.AttachVolume(ctx, vol.ID, "inst-nonexistent", "/dev/sdf")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("nonexistent volume", func(t *testing.T) {
		m := newTestMock()
		instances, err := m.RunInstances(ctx, driver.InstanceConfig{
			ImageID: "img-1", InstanceType: "n1-standard-1",
		}, 1)
		require.NoError(t, err)

		err = m.AttachVolume(ctx, "disk-nonexistent", instances[0].ID, "/dev/sdf")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDetachVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		instances, err := m.RunInstances(ctx, driver.InstanceConfig{
			ImageID: "img-1", InstanceType: "n1-standard-1",
		}, 1)
		require.NoError(t, err)

		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		require.NoError(t, err)

		err = m.AttachVolume(ctx, vol.ID, instances[0].ID, "/dev/sdf")
		require.NoError(t, err)

		err = m.DetachVolume(ctx, vol.ID)
		require.NoError(t, err)

		// Verify state changed back to available
		vols, err := m.DescribeVolumes(ctx, []string{vol.ID})
		require.NoError(t, err)
		require.Len(t, vols, 1)
		assert.Equal(t, "available", vols[0].State)
	})

	t.Run("detach unattached volume", func(t *testing.T) {
		m := newTestMock()
		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		require.NoError(t, err)

		err = m.DetachVolume(ctx, vol.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not attached")
	})
}

// =====================================================================
// Snapshot Tests
// =====================================================================

func TestCreateSnapshot(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 50})
		require.NoError(t, err)

		snap, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{
			VolumeID:    vol.ID,
			Description: "test snapshot",
			Tags:        map[string]string{"env": "test"},
		})
		require.NoError(t, err)

		assert.NotEmpty(t, snap.ID)
		assert.Equal(t, vol.ID, snap.VolumeID)
		assert.Equal(t, "completed", snap.State)
		assert.Equal(t, "test snapshot", snap.Description)
		assert.Equal(t, 50, snap.Size)
		assert.NotEmpty(t, snap.CreatedAt)
	})

	t.Run("nonexistent volume", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{
			VolumeID: "disk-nonexistent",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeleteSnapshot(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
		require.NoError(t, err)

		snap, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID})
		require.NoError(t, err)

		err = m.DeleteSnapshot(ctx, snap.ID)
		require.NoError(t, err)

		// Should be gone
		snaps, err := m.DescribeSnapshots(ctx, []string{snap.ID})
		require.NoError(t, err)
		assert.Empty(t, snaps)
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		err := m.DeleteSnapshot(ctx, "snap-nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDescribeSnapshots(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 10})
	require.NoError(t, err)

	snap1, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID})
	require.NoError(t, err)

	snap2, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{VolumeID: vol.ID})
	require.NoError(t, err)

	t.Run("describe all", func(t *testing.T) {
		snaps, err := m.DescribeSnapshots(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, snaps, 2)
	})

	t.Run("describe by ID", func(t *testing.T) {
		snaps, err := m.DescribeSnapshots(ctx, []string{snap1.ID})
		require.NoError(t, err)
		assert.Len(t, snaps, 1)
		assert.Equal(t, snap1.ID, snaps[0].ID)
	})

	// Keep snap2 referenced
	assert.NotEmpty(t, snap2.ID)
}

// =====================================================================
// Image Tests
// =====================================================================

func TestCreateImage(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		instances, err := m.RunInstances(ctx, driver.InstanceConfig{
			ImageID: "img-1", InstanceType: "n1-standard-1",
		}, 1)
		require.NoError(t, err)

		img, err := m.CreateImage(ctx, driver.ImageConfig{
			InstanceID:  instances[0].ID,
			Name:        "my-image",
			Description: "test image",
			Tags:        map[string]string{"env": "test"},
		})
		require.NoError(t, err)

		assert.NotEmpty(t, img.ID)
		assert.Equal(t, "my-image", img.Name)
		assert.Equal(t, "available", img.State)
		assert.Equal(t, "test image", img.Description)
		assert.NotEmpty(t, img.CreatedAt)
	})

	t.Run("nonexistent instance", func(t *testing.T) {
		m := newTestMock()
		_, err := m.CreateImage(ctx, driver.ImageConfig{
			InstanceID: "inst-nonexistent",
			Name:       "bad-image",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeregisterImage(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		instances, err := m.RunInstances(ctx, driver.InstanceConfig{
			ImageID: "img-1", InstanceType: "n1-standard-1",
		}, 1)
		require.NoError(t, err)

		img, err := m.CreateImage(ctx, driver.ImageConfig{
			InstanceID: instances[0].ID,
			Name:       "del-image",
		})
		require.NoError(t, err)

		err = m.DeregisterImage(ctx, img.ID)
		require.NoError(t, err)

		// Should be gone
		imgs, err := m.DescribeImages(ctx, []string{img.ID})
		require.NoError(t, err)
		assert.Empty(t, imgs)
	})

	t.Run("not found", func(t *testing.T) {
		m := newTestMock()
		err := m.DeregisterImage(ctx, "img-nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDescribeImages(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{
		ImageID: "img-1", InstanceType: "n1-standard-1",
	}, 1)
	require.NoError(t, err)

	img1, err := m.CreateImage(ctx, driver.ImageConfig{
		InstanceID: instances[0].ID,
		Name:       "image-1",
	})
	require.NoError(t, err)

	img2, err := m.CreateImage(ctx, driver.ImageConfig{
		InstanceID: instances[0].ID,
		Name:       "image-2",
	})
	require.NoError(t, err)

	t.Run("describe all", func(t *testing.T) {
		imgs, err := m.DescribeImages(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, imgs, 2)
	})

	t.Run("describe by ID", func(t *testing.T) {
		imgs, err := m.DescribeImages(ctx, []string{img1.ID})
		require.NoError(t, err)
		assert.Len(t, imgs, 1)
		assert.Equal(t, img1.ID, imgs[0].ID)
	})

	// Keep img2 referenced
	assert.NotEmpty(t, img2.ID)
}

func TestSetInstanceVPC(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cfg := driver.InstanceConfig{
		ImageID:      "img-123",
		InstanceType: "n1-standard-1",
		Tags:         map[string]string{"env": "test"},
	}

	t.Run("success", func(t *testing.T) {
		instances, err := m.RunInstances(ctx, cfg, 1)
		require.NoError(t, err)

		err = m.SetInstanceVPC(instances[0].ID, "vpc-123")
		require.NoError(t, err)

		desc, err := m.DescribeInstances(ctx, []string{instances[0].ID}, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, len(desc))
		assert.Equal(t, "vpc-123", desc[0].VPCID)
	})

	t.Run("instance not found", func(t *testing.T) {
		err := m.SetInstanceVPC("i-nonexistent", "vpc-123")
		require.Error(t, err)
	})
}
