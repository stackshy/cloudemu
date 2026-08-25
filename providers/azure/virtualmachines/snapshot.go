package virtualmachines

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// vmSnapshot is the full serialized state of the Azure VM mock. Instances carry
// an exported form (the stored instanceData has an unexported engineBacked
// flag); the driver-typed stores round-trip through the generic memstore
// helper. The state machine is rebuilt from each restored instance's stored
// state. The nicAttacher hook is a live cross-service wiring, not state, and is
// re-attached by the provider factory rather than serialized.
type vmSnapshot struct {
	Instances    map[string]*instanceSnapshot `json:"instances,omitempty"`
	SpotRequests json.RawMessage              `json:"spotRequests,omitempty"`
	Templates    json.RawMessage              `json:"templates,omitempty"`
	Volumes      json.RawMessage              `json:"volumes,omitempty"`
	Snapshots    json.RawMessage              `json:"snapshots,omitempty"`
	Images       json.RawMessage              `json:"images,omitempty"`
	KeyPairs     json.RawMessage              `json:"keyPairs,omitempty"`
	ScaleSets    json.RawMessage              `json:"scaleSets,omitempty"`
	DiskAccess   json.RawMessage              `json:"diskAccess,omitempty"`
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
	ID             string                  `json:"id"`
	ImageID        string                  `json:"imageId,omitempty"`
	InstanceType   string                  `json:"instanceType,omitempty"`
	State          string                  `json:"state,omitempty"`
	PrivateIP      string                  `json:"privateIp,omitempty"`
	PublicIP       string                  `json:"publicIp,omitempty"`
	SubnetID       string                  `json:"subnetId,omitempty"`
	VPCID          string                  `json:"vpcId,omitempty"`
	SecurityGroups []string                `json:"securityGroups,omitempty"`
	Tags           map[string]string       `json:"tags,omitempty"`
	LaunchTime     string                  `json:"launchTime,omitempty"`
	OSType         string                  `json:"osType,omitempty"`
	Priority       string                  `json:"priority,omitempty"`
	LicenseType    string                  `json:"licenseType,omitempty"`
	Zones          []string                `json:"zones,omitempty"`
	Region         string                  `json:"region,omitempty"`
	ResourceGroup  string                  `json:"resourceGroup,omitempty"`
	PowerState     string                  `json:"powerState,omitempty"`
	Generalized    bool                    `json:"generalized,omitempty"`
	Identity       *driver.ManagedIdentity `json:"identity,omitempty"`
	EngineBacked   bool                    `json:"engineBacked,omitempty"`
	NICRefs        []driver.AzureNICRef    `json:"nicRefs,omitempty"`
}

type asgSnapshot struct {
	Config   driver.AutoScalingGroup `json:"config"`
	Policies json.RawMessage         `json:"policies,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// the VM service holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := vmSnapshot{Instances: m.snapshotInstances()}

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

//nolint:dupl // inverse field map of restoreInstances; mirrored lists are inherent.
func (m *Mock) snapshotInstances() map[string]*instanceSnapshot {
	out := make(map[string]*instanceSnapshot, m.instances.Len())

	for id, d := range m.instances.All() {
		out[id] = &instanceSnapshot{
			ID: d.ID, ImageID: d.ImageID, InstanceType: d.InstanceType, State: d.State,
			PrivateIP: d.PrivateIP, PublicIP: d.PublicIP, SubnetID: d.SubnetID, VPCID: d.VPCID,
			SecurityGroups: d.SecurityGroups, Tags: d.Tags, LaunchTime: d.LaunchTime,
			OSType: d.OSType, Priority: d.Priority, LicenseType: d.LicenseType, Zones: d.Zones,
			Region: d.Region, ResourceGroup: d.ResourceGroup, PowerState: d.PowerState,
			Generalized: d.Generalized, Identity: d.Identity, EngineBacked: d.engineBacked,
			NICRefs: d.NICRefs,
		}
	}

	return out
}

func (m *Mock) snapshotStores(snap *vmSnapshot) error {
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
		{&snap.ScaleSets, m.scaleSets.Snapshot},
		{&snap.DiskAccess, m.diskAccess.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("virtualmachines: snapshot store: %w", err)
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
			return nil, fmt.Errorf("virtualmachines: snapshot asg policies: %w", err)
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
	var snap vmSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("virtualmachines: parse snapshot: %w", err)
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

//nolint:dupl // inverse field map of snapshotInstances; mirrored lists are inherent.
func (m *Mock) restoreInstances(instances map[string]*instanceSnapshot) {
	for id, s := range instances {
		m.instances.Set(id, &instanceData{
			ID: s.ID, ImageID: s.ImageID, InstanceType: s.InstanceType, State: s.State,
			PrivateIP: s.PrivateIP, PublicIP: s.PublicIP, SubnetID: s.SubnetID, VPCID: s.VPCID,
			SecurityGroups: s.SecurityGroups, Tags: s.Tags, LaunchTime: s.LaunchTime,
			OSType: s.OSType, Priority: s.Priority, LicenseType: s.LicenseType, Zones: s.Zones,
			Region: s.Region, ResourceGroup: s.ResourceGroup, PowerState: s.PowerState,
			Generalized: s.Generalized, Identity: s.Identity, engineBacked: s.EngineBacked,
			NICRefs: s.NICRefs,
		})
		// Re-register with the state machine so lifecycle transitions
		// (Start/Stop/Terminate) validate against the restored state.
		m.sm.SetState(id, s.State)
	}
}

func (m *Mock) restoreStores(snap *vmSnapshot) error {
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
		{snap.ScaleSets, m.scaleSets.LoadSnapshot},
		{snap.DiskAccess, m.diskAccess.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("virtualmachines: restore store: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreASGs(asgs map[string]*asgSnapshot) error {
	for name, a := range asgs {
		ad := &asgData{config: a.Config, policies: memstore.New[driver.ScalingPolicy]()}
		if len(a.Policies) > 0 {
			if err := ad.policies.LoadSnapshot(a.Policies); err != nil {
				return fmt.Errorf("virtualmachines: restore asg policies: %w", err)
			}
		}

		m.asgs.Set(name, ad)
	}

	return nil
}
