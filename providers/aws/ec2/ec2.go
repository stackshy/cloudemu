package ec2

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/NitinKumar004/cloudemu/compute"
	"github.com/NitinKumar004/cloudemu/compute/driver"
	"github.com/NitinKumar004/cloudemu/config"
	cerrors "github.com/NitinKumar004/cloudemu/errors"
	"github.com/NitinKumar004/cloudemu/internal/idgen"
	"github.com/NitinKumar004/cloudemu/internal/memstore"
	mondriver "github.com/NitinKumar004/cloudemu/monitoring/driver"
	"github.com/NitinKumar004/cloudemu/statemachine"
)

var _ driver.Compute = (*Mock)(nil)

type instanceData struct {
	ID             string
	ImageID        string
	InstanceType   string
	State          string
	PrivateIP      string
	PublicIP       string
	SubnetID       string
	VPCID          string
	SecurityGroups []string
	Tags           map[string]string
	LaunchTime     string
}

// Mock is an in-memory mock implementation of the AWS EC2 service.
type Mock struct {
	instances  *memstore.Store[*instanceData]
	sm         *statemachine.Machine
	opts       *config.Options
	ipCounter  atomic.Int64
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitInstanceMetrics(ctx context.Context, instanceID, launchTime string) {
	if m.monitoring == nil {
		return
	}
	lt, err := time.Parse("2006-01-02T15:04:05Z", launchTime)
	if err != nil {
		lt = m.opts.Clock.Now()
	}
	metrics := []string{"CPUUtilization", "NetworkIn", "NetworkOut", "DiskReadOps", "DiskWriteOps"}
	values := []float64{25.0, 1024.0, 512.0, 100.0, 50.0}
	var data []mondriver.MetricDatum
	for i, metricName := range metrics {
		for j := 0; j < 5; j++ {
			ts := lt.Add(time.Duration(j) * time.Minute)
			data = append(data, mondriver.MetricDatum{
				Namespace:  "AWS/EC2",
				MetricName: metricName,
				Value:      values[i],
				Unit:       "None",
				Dimensions: map[string]string{"InstanceId": instanceID},
				Timestamp:  ts,
			})
		}
	}
	_ = m.monitoring.PutMetricData(ctx, data)
}

// New creates a new EC2 mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances: memstore.New[*instanceData](),
		sm:        statemachine.New(compute.VMTransitions),
		opts:      opts,
	}
}

func (m *Mock) nextIP() string {
	n := m.ipCounter.Add(1)
	return fmt.Sprintf("10.0.%d.%d", n/256, n%256)
}

func toInstance(d *instanceData) driver.Instance {
	sg := make([]string, len(d.SecurityGroups))
	copy(sg, d.SecurityGroups)
	tags := make(map[string]string, len(d.Tags))
	for k, v := range d.Tags {
		tags[k] = v
	}
	return driver.Instance{
		ID: d.ID, ImageID: d.ImageID, InstanceType: d.InstanceType, State: d.State,
		PrivateIP: d.PrivateIP, PublicIP: d.PublicIP, SubnetID: d.SubnetID, VPCID: d.VPCID,
		SecurityGroups: sg, Tags: tags, LaunchTime: d.LaunchTime,
	}
}

func (m *Mock) RunInstances(ctx context.Context, cfg driver.InstanceConfig, count int) ([]driver.Instance, error) {
	if count <= 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "count must be greater than 0")
	}
	results := make([]driver.Instance, 0, count)
	for i := 0; i < count; i++ {
		id := idgen.GenerateID("i-")
		tags := make(map[string]string, len(cfg.Tags))
		for k, v := range cfg.Tags {
			tags[k] = v
		}
		sg := make([]string, len(cfg.SecurityGroups))
		copy(sg, cfg.SecurityGroups)
		inst := &instanceData{
			ID: id, ImageID: cfg.ImageID, InstanceType: cfg.InstanceType,
			State: compute.StatePending, PrivateIP: m.nextIP(), SubnetID: cfg.SubnetID,
			SecurityGroups: sg, Tags: tags,
			LaunchTime: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		}
		m.instances.Set(id, inst)
		m.sm.SetState(id, compute.StatePending)
		_ = m.sm.Transition(id, compute.StateRunning)
		inst.State = compute.StateRunning
		results = append(results, toInstance(inst))
		m.emitInstanceMetrics(ctx, id, inst.LaunchTime)
	}
	return results, nil
}

func (m *Mock) StartInstances(_ context.Context, instanceIDs []string) error {
	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}
		if err := m.sm.Transition(id, compute.StatePending); err != nil {
			return cerrors.Newf(cerrors.FailedPrecondition, "cannot start instance %q: %v", id, err)
		}
		inst.State = compute.StatePending
		_ = m.sm.Transition(id, compute.StateRunning)
		inst.State = compute.StateRunning
	}
	return nil
}

func (m *Mock) StopInstances(_ context.Context, instanceIDs []string) error {
	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}
		if err := m.sm.Transition(id, compute.StateStopping); err != nil {
			return cerrors.Newf(cerrors.FailedPrecondition, "cannot stop instance %q: %v", id, err)
		}
		inst.State = compute.StateStopping
		_ = m.sm.Transition(id, compute.StateStopped)
		inst.State = compute.StateStopped
	}
	return nil
}

func (m *Mock) RebootInstances(_ context.Context, instanceIDs []string) error {
	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}
		if err := m.sm.Transition(id, compute.StateRestarting); err != nil {
			return cerrors.Newf(cerrors.FailedPrecondition, "cannot reboot instance %q: %v", id, err)
		}
		inst.State = compute.StateRestarting
		_ = m.sm.Transition(id, compute.StateRunning)
		inst.State = compute.StateRunning
	}
	return nil
}

func (m *Mock) TerminateInstances(_ context.Context, instanceIDs []string) error {
	for _, id := range instanceIDs {
		inst, ok := m.instances.Get(id)
		if !ok {
			return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
		}
		if err := m.sm.Transition(id, compute.StateShuttingDown); err != nil {
			return cerrors.Newf(cerrors.FailedPrecondition, "cannot terminate instance %q: %v", id, err)
		}
		inst.State = compute.StateShuttingDown
		_ = m.sm.Transition(id, compute.StateTerminated)
		inst.State = compute.StateTerminated
	}
	return nil
}

func (m *Mock) DescribeInstances(_ context.Context, instanceIDs []string, filters []driver.DescribeFilter) ([]driver.Instance, error) {
	var candidates []*instanceData
	if len(instanceIDs) > 0 {
		for _, id := range instanceIDs {
			if inst, ok := m.instances.Get(id); ok {
				candidates = append(candidates, inst)
			}
		}
	} else {
		for _, inst := range m.instances.All() {
			candidates = append(candidates, inst)
		}
	}
	results := make([]driver.Instance, 0)
	for _, inst := range candidates {
		if matchesFilters(inst, filters) {
			results = append(results, toInstance(inst))
		}
	}
	return results, nil
}

func matchesFilters(inst *instanceData, filters []driver.DescribeFilter) bool {
	for _, f := range filters {
		switch f.Name {
		case "instance-id":
			if !containsValue(f.Values, inst.ID) {
				return false
			}
		case "instance-type":
			if !containsValue(f.Values, inst.InstanceType) {
				return false
			}
		case "instance-state-name":
			if !containsValue(f.Values, inst.State) {
				return false
			}
		default:
			if len(f.Name) > 4 && f.Name[:4] == "tag:" {
				tagKey := f.Name[4:]
				tagVal, ok := inst.Tags[tagKey]
				if !ok || !containsValue(f.Values, tagVal) {
					return false
				}
			}
		}
	}
	return true
}

func containsValue(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func (m *Mock) ModifyInstance(_ context.Context, instanceID string, input driver.ModifyInstanceInput) error {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}
	if inst.State != compute.StateStopped {
		return cerrors.Newf(cerrors.FailedPrecondition, "instance %q must be stopped to modify", instanceID)
	}
	if input.InstanceType != "" {
		inst.InstanceType = input.InstanceType
	}
	if input.Tags != nil {
		for k, v := range input.Tags {
			inst.Tags[k] = v
		}
	}
	return nil
}
