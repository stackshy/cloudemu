package compute

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// gceSnapshot is the full serialized state of the GCE mock. Instances carry an
// exported form (the stored instanceData has an unexported engineBacked flag);
// the driver-typed stores round-trip through the generic memstore helper. The
// state machine is rebuilt from each restored instance's stored state.
type gceSnapshot struct {
	Instances    map[string]*instanceSnapshot `json:"instances,omitempty"`
	SpotRequests json.RawMessage              `json:"spotRequests,omitempty"`
	Templates    json.RawMessage              `json:"templates,omitempty"`
	Volumes      json.RawMessage              `json:"volumes,omitempty"`
	Snapshots    json.RawMessage              `json:"snapshots,omitempty"`
	Images       json.RawMessage              `json:"images,omitempty"`
	KeyPairs     json.RawMessage              `json:"keyPairs,omitempty"`
	ASGs         map[string]*asgSnapshot      `json:"asgs,omitempty"`
	Counters     countersSnapshot             `json:"counters"`
}

type countersSnapshot struct {
	IP   int64 `json:"ip,omitempty"`
	Vol  int64 `json:"vol,omitempty"`
	Snap int64 `json:"snap,omitempty"`
	Img  int64 `json:"img,omitempty"`
}

// instanceSnapshot mirrors instanceData, promoting its one unexported field
// (engineBacked) to an exported one so it survives JSON.
type instanceSnapshot struct {
	ID             string            `json:"id"`
	ImageID        string            `json:"imageId,omitempty"`
	InstanceType   string            `json:"instanceType,omitempty"`
	State          string            `json:"state,omitempty"`
	PrivateIP      string            `json:"privateIp,omitempty"`
	PublicIP       string            `json:"publicIp,omitempty"`
	SubnetID       string            `json:"subnetId,omitempty"`
	VPCID          string            `json:"vpcId,omitempty"`
	SecurityGroups []string          `json:"securityGroups,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	LaunchTime     string            `json:"launchTime,omitempty"`
	EngineBacked   bool              `json:"engineBacked,omitempty"`
}

type asgSnapshot struct {
	Config   driver.AutoScalingGroup `json:"config"`
	Policies json.RawMessage         `json:"policies,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// GCE holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := gceSnapshot{Instances: m.snapshotInstances()}

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	asgs, err := m.snapshotASGs()
	if err != nil {
		return nil, err
	}

	snap.ASGs = asgs
	snap.Counters = countersSnapshot{
		IP: m.ipCounter.Load(), Vol: m.volCounter.Load(),
		Snap: m.snapCounter.Load(), Img: m.imgCounter.Load(),
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotInstances() map[string]*instanceSnapshot {
	out := make(map[string]*instanceSnapshot, m.instances.Len())

	for id, d := range m.instances.All() {
		out[id] = &instanceSnapshot{
			ID: d.ID, ImageID: d.ImageID, InstanceType: d.InstanceType, State: d.State,
			PrivateIP: d.PrivateIP, PublicIP: d.PublicIP, SubnetID: d.SubnetID, VPCID: d.VPCID,
			SecurityGroups: d.SecurityGroups, Tags: d.Tags, LaunchTime: d.LaunchTime,
			EngineBacked: d.engineBacked,
		}
	}

	return out
}

func (m *Mock) snapshotStores(snap *gceSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.SpotRequests, m.spotRequests.Snapshot},
		{&snap.Templates, m.templates.Snapshot},
		{&snap.Volumes, m.volumes.Snapshot},
		{&snap.Snapshots, m.snapshots.Snapshot},
		{&snap.Images, m.images.Snapshot},
		{&snap.KeyPairs, m.keyPairs.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("gce: snapshot store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

func (m *Mock) snapshotASGs() (map[string]*asgSnapshot, error) {
	if m.asgs.Len() == 0 {
		return nil, nil
	}

	out := make(map[string]*asgSnapshot, m.asgs.Len())

	for name, a := range m.asgs.All() {
		pol, err := a.policies.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("gce: snapshot asg policies: %w", err)
		}

		out[name] = &asgSnapshot{Config: a.config, Policies: pol}
	}

	return out, nil
}

// Restore rebuilds the mock's state under the original identities: instance ids
// (and their id-string cross-references, e.g. security groups) are preserved,
// and each restored instance is re-registered with the state machine at its
// stored state.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap gceSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("gce: parse snapshot: %w", err)
	}

	m.restoreInstances(snap.Instances)

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	if err := m.restoreASGs(snap.ASGs); err != nil {
		return err
	}

	c := snap.Counters
	m.ipCounter.Store(c.IP)
	m.volCounter.Store(c.Vol)
	m.snapCounter.Store(c.Snap)
	m.imgCounter.Store(c.Img)

	return nil
}

func (m *Mock) restoreInstances(instances map[string]*instanceSnapshot) {
	for id, s := range instances {
		m.instances.Set(id, &instanceData{
			ID: s.ID, ImageID: s.ImageID, InstanceType: s.InstanceType, State: s.State,
			PrivateIP: s.PrivateIP, PublicIP: s.PublicIP, SubnetID: s.SubnetID, VPCID: s.VPCID,
			SecurityGroups: s.SecurityGroups, Tags: s.Tags, LaunchTime: s.LaunchTime,
			engineBacked: s.EngineBacked,
		})
		// Re-register with the state machine so lifecycle transitions
		// (Start/Stop/Terminate) validate against the restored state.
		m.sm.SetState(id, s.State)
	}
}

func (m *Mock) restoreStores(snap *gceSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.SpotRequests, m.spotRequests.LoadSnapshot},
		{snap.Templates, m.templates.LoadSnapshot},
		{snap.Volumes, m.volumes.LoadSnapshot},
		{snap.Snapshots, m.snapshots.LoadSnapshot},
		{snap.Images, m.images.LoadSnapshot},
		{snap.KeyPairs, m.keyPairs.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("gce: restore store: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreASGs(asgs map[string]*asgSnapshot) error {
	for name, a := range asgs {
		ad := &asgData{config: a.Config, policies: memstore.New[driver.ScalingPolicy]()}
		if len(a.Policies) > 0 {
			if err := ad.policies.LoadSnapshot(a.Policies); err != nil {
				return fmt.Errorf("gce: restore asg policies: %w", err)
			}
		}

		m.asgs.Set(name, ad)
	}

	return nil
}
