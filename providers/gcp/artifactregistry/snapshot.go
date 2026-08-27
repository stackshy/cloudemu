package artifactregistry

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// artifactregistrySnapshot is the full serialized state of the GCP Artifact
// Registry mock: every repository keyed by name, each carrying its metadata,
// lifecycle policy, config flags, images (with layers), and scan results. The
// stored *repoData/*imageData have unexported layouts, so they are promoted to
// exported snapshot forms. The wired *config.Options and monitoring backend are
// intentionally not serialized.
type artifactregistrySnapshot struct {
	Repos map[string]*repoSnapshot `json:"repos,omitempty"`
}

// repoSnapshot mirrors repoData, promoting its unexported image store and config
// fields to exported ones. The scan store holds fully-exported driver values, so
// it round-trips through the generic memstore helper.
type repoSnapshot struct {
	Info          driver.Repository         `json:"info"`
	Images        map[string]*imageSnapshot `json:"images,omitempty"`
	Scans         json.RawMessage           `json:"scans,omitempty"`
	Policy        *driver.LifecyclePolicy   `json:"policy,omitempty"`
	ScanOnPush    bool                      `json:"scanOnPush,omitempty"`
	TagMutability string                    `json:"tagMutability,omitempty"`
}

// imageSnapshot mirrors imageData, promoting its unexported detail/layers to
// exported fields.
type imageSnapshot struct {
	Detail driver.ImageDetail `json:"detail"`
	Layers []driver.LayerInfo `json:"layers,omitempty"`
}

// Snapshot captures every repository and its images as JSON. includeAssets is
// unused — image records carry no bulk blob bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := artifactregistrySnapshot{Repos: make(map[string]*repoSnapshot, m.repos.Len())}

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
		Info:          rd.info,
		Images:        make(map[string]*imageSnapshot, rd.images.Len()),
		Policy:        rd.policy,
		ScanOnPush:    rd.scanOnPush,
		TagMutability: rd.tagMutability,
	}

	for digest, img := range rd.images.All() {
		rs.Images[digest] = &imageSnapshot{Detail: img.detail, Layers: img.layers}
	}

	scans, err := rd.scans.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("artifactregistry: snapshot scans: %w", err)
	}

	rs.Scans = scans

	return rs, nil
}

// Restore rebuilds every repository under its original name with its images and
// scan results intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap artifactregistrySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("artifactregistry: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

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
		scanOnPush:    rs.ScanOnPush,
		tagMutability: rs.TagMutability,
	}

	for digest, is := range rs.Images {
		rd.images.Set(digest, &imageData{detail: is.Detail, layers: is.Layers})
	}

	if len(rs.Scans) > 0 {
		if err := rd.scans.LoadSnapshot(rs.Scans); err != nil {
			return nil, fmt.Errorf("artifactregistry: restore scans: %w", err)
		}
	}

	return rd, nil
}
