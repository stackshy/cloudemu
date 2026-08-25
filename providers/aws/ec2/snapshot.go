package ec2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// ec2Snapshot is the full serialized state of the EC2 mock. Instances carry an
// exported form (the stored instanceData has unexported fields and a mutex that
// json.Marshal cannot see); the driver-typed stores round-trip through the
// generic memstore helper. In-flight settle overlays and the sync primitives are
// intentionally not serialized — a restored instance reports its stored state
// immediately.
type ec2Snapshot struct {
	Instances         map[string]*instanceSnapshot `json:"instances,omitempty"`
	Volumes           json.RawMessage              `json:"volumes,omitempty"`
	Snapshots         json.RawMessage              `json:"snapshots,omitempty"`
	Images            json.RawMessage              `json:"images,omitempty"`
	KeyPairs          json.RawMessage              `json:"keyPairs,omitempty"`
	SpotRequests      json.RawMessage              `json:"spotRequests,omitempty"`
	Templates         json.RawMessage              `json:"templates,omitempty"`
	TemplateVersions  json.RawMessage              `json:"templateVersions,omitempty"`
	PlacementGroups   json.RawMessage              `json:"placementGroups,omitempty"`
	ASGs              map[string]*asgSnapshot      `json:"asgs,omitempty"`
	ManagedVisibility string                       `json:"managedVisibility,omitempty"`
	ClientTokens      map[string][]string          `json:"clientTokens,omitempty"`
	SubnetIPCounters  map[string]int               `json:"subnetIpCounters,omitempty"`
	Counters          countersSnapshot             `json:"counters"`
}

type countersSnapshot struct {
	IP   int64 `json:"ip,omitempty"`
	Vol  int64 `json:"vol,omitempty"`
	Snap int64 `json:"snap,omitempty"`
	AMI  int64 `json:"ami,omitempty"`
	Key  int64 `json:"key,omitempty"`
	PG   int64 `json:"pg,omitempty"`
}

// instanceSnapshot mirrors instanceData, promoting its meaningful unexported
// fields to exported ones so they survive JSON. The mutex and the transient
// settle window are deliberately excluded.
type instanceSnapshot struct {
	ID                    string                     `json:"id"`
	ImageID               string                     `json:"imageId,omitempty"`
	InstanceType          string                     `json:"instanceType,omitempty"`
	State                 string                     `json:"state,omitempty"`
	PrivateIP             string                     `json:"privateIp,omitempty"`
	PublicIP              string                     `json:"publicIp,omitempty"`
	SubnetID              string                     `json:"subnetId,omitempty"`
	VPCID                 string                     `json:"vpcId,omitempty"`
	SecurityGroups        []string                   `json:"securityGroups,omitempty"`
	Tags                  map[string]string          `json:"tags,omitempty"`
	LaunchTime            string                     `json:"launchTime,omitempty"`
	Operator              *operatorData              `json:"operator,omitempty"`
	EngineBacked          bool                       `json:"engineBacked,omitempty"`
	DisableAPITermination bool                       `json:"disableApiTermination,omitempty"`
	SourceDestCheck       bool                       `json:"sourceDestCheck,omitempty"`
	UserData              string                     `json:"userData,omitempty"`
	EBSOptimized          bool                       `json:"ebsOptimized,omitempty"`
	ReservationID         string                     `json:"reservationId,omitempty"`
	KeyName               string                     `json:"keyName,omitempty"`
	Monitoring            string                     `json:"monitoring,omitempty"`
	MetadataOptions       driver.MetadataOptions     `json:"metadataOptions,omitempty"`
	IamInstanceProfile    *driver.IamInstanceProfile `json:"iamInstanceProfile,omitempty"`
}

type asgSnapshot struct {
	Config   driver.AutoScalingGroup `json:"config"`
	Policies json.RawMessage         `json:"policies,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// EC2 holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := ec2Snapshot{Instances: m.snapshotInstances()}

	if err := m.snapshotStores(&snap); err != nil {
		return nil, err
	}

	asgs, err := m.snapshotASGs()
	if err != nil {
		return nil, err
	}

	snap.ASGs = asgs

	m.mu.RLock()
	snap.ManagedVisibility = m.managedResourceVisibility
	snap.ClientTokens = m.clientTokens
	snap.SubnetIPCounters = m.subnetIPCounters
	m.mu.RUnlock()

	snap.Counters = countersSnapshot{
		IP: m.ipCounter.Load(), Vol: m.volCounter.Load(), Snap: m.snapCounter.Load(),
		AMI: m.amiCounter.Load(), Key: m.keyCounter.Load(), PG: m.pgCounter.Load(),
	}

	return json.Marshal(snap)
}

// snapshotInstances promotes each stored instanceData to its exported snapshot
// form.
//
//nolint:dupl // inverse field map of restoreInstances; mirrored lists are inherent.
func (m *Mock) snapshotInstances() map[string]*instanceSnapshot {
	out := make(map[string]*instanceSnapshot, m.instances.Len())

	for id, d := range m.instances.All() {
		d.mu.Lock()
		out[id] = &instanceSnapshot{
			ID: d.ID, ImageID: d.ImageID, InstanceType: d.InstanceType, State: d.State,
			PrivateIP: d.PrivateIP, PublicIP: d.PublicIP, SubnetID: d.SubnetID, VPCID: d.VPCID,
			SecurityGroups: d.SecurityGroups, Tags: d.Tags, LaunchTime: d.LaunchTime, Operator: d.Operator,
			EngineBacked: d.engineBacked, DisableAPITermination: d.disableAPITermination,
			SourceDestCheck: d.sourceDestCheck, UserData: d.userData, EBSOptimized: d.ebsOptimized,
			ReservationID: d.reservationID, KeyName: d.keyName, Monitoring: d.monitoring,
			MetadataOptions: d.metadataOptions, IamInstanceProfile: d.iamInstanceProfile,
		}
		d.mu.Unlock()
	}

	return out
}

func (m *Mock) snapshotStores(snap *ec2Snapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.Volumes, m.volumes.Snapshot},
		{&snap.Snapshots, m.snapshots.Snapshot},
		{&snap.Images, m.images.Snapshot},
		{&snap.KeyPairs, m.keyPairs.Snapshot},
		{&snap.SpotRequests, m.spotRequests.Snapshot},
		{&snap.Templates, m.templates.Snapshot},
		{&snap.TemplateVersions, m.templateVersions.Snapshot},
		{&snap.PlacementGroups, m.placementGroups.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("ec2: snapshot store: %w", err)
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
			return nil, fmt.Errorf("ec2: snapshot asg policies: %w", err)
		}

		out[name] = &asgSnapshot{Config: a.config, Policies: pol}
	}

	return out, nil
}

// Restore rebuilds the mock's state under the original identities: instance ids
// (and their id-string cross-references, e.g. security groups) are preserved,
// and each restored instance is re-registered with the state machine.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap ec2Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("ec2: parse snapshot: %w", err)
	}

	m.restoreInstances(snap.Instances)

	if err := m.restoreStores(&snap); err != nil {
		return err
	}

	if err := m.restoreASGs(snap.ASGs); err != nil {
		return err
	}

	m.restoreScalarState(&snap)

	c := snap.Counters
	m.ipCounter.Store(c.IP)
	m.volCounter.Store(c.Vol)
	m.snapCounter.Store(c.Snap)
	m.amiCounter.Store(c.AMI)
	m.keyCounter.Store(c.Key)
	m.pgCounter.Store(c.PG)

	return nil
}

// restoreScalarState reinstates the mu-guarded scalar/map fields from the
// snapshot, leaving unset ones at their New() defaults.
func (m *Mock) restoreScalarState(snap *ec2Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if snap.ManagedVisibility != "" {
		m.managedResourceVisibility = snap.ManagedVisibility
	}

	if snap.ClientTokens != nil {
		m.clientTokens = snap.ClientTokens
	}

	if snap.SubnetIPCounters != nil {
		m.subnetIPCounters = snap.SubnetIPCounters
	}
}

// restoreInstances reinstates each instance under its original id and
// re-registers it with the state machine.
//
//nolint:dupl // inverse field map of snapshotInstances; mirrored lists are inherent.
func (m *Mock) restoreInstances(instances map[string]*instanceSnapshot) {
	for id, s := range instances {
		m.instances.Set(id, &instanceData{
			ID: s.ID, ImageID: s.ImageID, InstanceType: s.InstanceType, State: s.State,
			PrivateIP: s.PrivateIP, PublicIP: s.PublicIP, SubnetID: s.SubnetID, VPCID: s.VPCID,
			SecurityGroups: s.SecurityGroups, Tags: s.Tags, LaunchTime: s.LaunchTime, Operator: s.Operator,
			engineBacked: s.EngineBacked, disableAPITermination: s.DisableAPITermination,
			sourceDestCheck: s.SourceDestCheck, userData: s.UserData, ebsOptimized: s.EBSOptimized,
			reservationID: s.ReservationID, keyName: s.KeyName, monitoring: s.Monitoring,
			metadataOptions: s.MetadataOptions, iamInstanceProfile: s.IamInstanceProfile,
		})
		// Re-register with the state machine so lifecycle transitions
		// (Start/Stop/Terminate) validate against the restored state.
		m.sm.SetState(id, s.State)
	}
}

func (m *Mock) restoreStores(snap *ec2Snapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.Volumes, m.volumes.LoadSnapshot},
		{snap.Snapshots, m.snapshots.LoadSnapshot},
		{snap.Images, m.images.LoadSnapshot},
		{snap.KeyPairs, m.keyPairs.LoadSnapshot},
		{snap.SpotRequests, m.spotRequests.LoadSnapshot},
		{snap.Templates, m.templates.LoadSnapshot},
		{snap.TemplateVersions, m.templateVersions.LoadSnapshot},
		{snap.PlacementGroups, m.placementGroups.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("ec2: restore store: %w", err)
		}
	}

	return nil
}

func (m *Mock) restoreASGs(asgs map[string]*asgSnapshot) error {
	for name, a := range asgs {
		ad := &asgData{config: a.Config, policies: memstore.New[driver.ScalingPolicy]()}
		if len(a.Policies) > 0 {
			if err := ad.policies.LoadSnapshot(a.Policies); err != nil {
				return fmt.Errorf("ec2: restore asg policies: %w", err)
			}
		}

		m.asgs.Set(name, ad)
	}

	return nil
}
