package ecr

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// ecrSnapshot is the full serialized state of the ECR mock. The repos store
// holds an unexported *repoData whose nested images store (unexported *imageData)
// and unexported scalar settings must be captured, so it is promoted to an
// exported snapshot form keyed by repository name. The scans store holds a
// fully-exported *driver.ScanResult and round-trips through the generic memstore
// helper. The mutex and the wired deps (opts, monitoring) are not serialized.
type ecrSnapshot struct {
	Repos map[string]*repoSnapshot `json:"repos,omitempty"`
}

// repoSnapshot mirrors repoData, promoting its unexported settings and nested
// stores. Images carries an exported form because imageData is unexported;
// scans round-trips through the generic helper.
type repoSnapshot struct {
	Info          driver.Repository         `json:"info"`
	Images        map[string]*imageSnapshot `json:"images,omitempty"`
	Scans         json.RawMessage           `json:"scans,omitempty"`
	Policy        *driver.LifecyclePolicy   `json:"policy,omitempty"`
	RepoPolicy    string                    `json:"repoPolicy,omitempty"`
	ScanOnPush    bool                      `json:"scanOnPush,omitempty"`
	TagMutability string                    `json:"tagMutability,omitempty"`
}

// imageSnapshot mirrors imageData (whose fields are exported but whose type is
// unexported, so json.Marshal cannot reach it through the store).
type imageSnapshot struct {
	Detail driver.ImageDetail `json:"detail"`
	Layers []driver.LayerInfo `json:"layers,omitempty"`
}

// Snapshot captures every repository's state as JSON. includeAssets is unused —
// ECR stores image manifests/metadata, not object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := ecrSnapshot{}
	if m.repos.Len() == 0 {
		return json.Marshal(snap)
	}

	snap.Repos = make(map[string]*repoSnapshot, m.repos.Len())

	for name, rd := range m.repos.All() {
		rs, err := snapshotRepo(rd)
		if err != nil {
			return nil, err
		}

		snap.Repos[name] = rs
	}

	return json.Marshal(snap)
}

func snapshotRepo(rd *repoData) (*repoSnapshot, error) {
	rs := &repoSnapshot{
		Info: rd.info, Policy: rd.policy, RepoPolicy: rd.repoPolicy,
		ScanOnPush: rd.scanOnPush, TagMutability: rd.tagMutability,
	}

	scans, err := rd.scans.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("ecr: snapshot scans: %w", err)
	}

	rs.Scans = scans

	if rd.images.Len() > 0 {
		rs.Images = make(map[string]*imageSnapshot, rd.images.Len())

		for key, img := range rd.images.All() {
			rs.Images[key] = &imageSnapshot{Detail: img.detail, Layers: img.layers}
		}
	}

	return rs, nil
}

// Restore rebuilds every repository under its original name with its images,
// scans, lifecycle/repository policies, and settings intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap ecrSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("ecr: parse snapshot: %w", err)
	}

	for name, rs := range snap.Repos {
		rd, err := restoreRepo(rs)
		if err != nil {
			return err
		}

		m.repos.Set(name, rd)
	}

	return nil
}

func restoreRepo(rs *repoSnapshot) (*repoData, error) {
	rd := &repoData{
		info:          rs.Info,
		images:        memstore.New[*imageData](),
		scans:         memstore.New[*driver.ScanResult](),
		policy:        rs.Policy,
		repoPolicy:    rs.RepoPolicy,
		scanOnPush:    rs.ScanOnPush,
		tagMutability: rs.TagMutability,
	}

	if len(rs.Scans) > 0 {
		if err := rd.scans.LoadSnapshot(rs.Scans); err != nil {
			return nil, fmt.Errorf("ecr: restore scans: %w", err)
		}
	}

	for key, is := range rs.Images {
		rd.images.Set(key, &imageData{detail: is.Detail, layers: is.Layers})
	}

	return rd, nil
}
