package ssm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// ssmSnapshot is the full serialized state of the SSM Parameter Store mock.
// params holds an unexported paramData (with a slice of unexported *version), so
// it is promoted to an exported snapshot form keyed by parameter name; commands
// holds a fully-exported driver.CommandInvocation and round-trips through the
// generic memstore helper. The wired instanceResolver and opts are not
// serialized.
type ssmSnapshot struct {
	Params   map[string]*paramSnapshot `json:"params,omitempty"`
	Commands json.RawMessage           `json:"commands,omitempty"`
}

// paramSnapshot mirrors paramData, promoting its unexported fields (and its
// slice of unexported *version) to exported ones so they survive JSON.
type paramSnapshot struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Tier        string             `json:"tier,omitempty"`
	Versions    []*versionSnapshot `json:"versions,omitempty"`
	Latest      int64              `json:"latest,omitempty"`
	Tags        map[string]string  `json:"tags,omitempty"`
}

// versionSnapshot mirrors the unexported version struct.
type versionSnapshot struct {
	Value          string   `json:"value,omitempty"`
	Typ            string   `json:"typ,omitempty"`
	DataType       string   `json:"dataType,omitempty"`
	Version        int64    `json:"version,omitempty"`
	LastModified   string   `json:"lastModified,omitempty"`
	Labels         []string `json:"labels,omitempty"`
	KeyID          string   `json:"keyId,omitempty"`
	AllowedPattern string   `json:"allowedPattern,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// SSM holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := ssmSnapshot{Params: m.snapshotParams()}

	cmds, err := m.commands.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("ssm: snapshot commands: %w", err)
	}

	snap.Commands = cmds

	return json.Marshal(snap)
}

func (m *Mock) snapshotParams() map[string]*paramSnapshot {
	if m.params.Len() == 0 {
		return nil
	}

	out := make(map[string]*paramSnapshot, m.params.Len())

	for name, p := range m.params.All() {
		p.mu.RLock()
		ps := &paramSnapshot{
			Name: p.name, Description: p.description, Tier: p.tier,
			Latest: p.latest, Tags: p.tags,
		}

		for _, v := range p.versions {
			ps.Versions = append(ps.Versions, &versionSnapshot{
				Value: v.value, Typ: v.typ, DataType: v.dataType, Version: v.version,
				LastModified: v.lastModified, Labels: v.labels, KeyID: v.keyID,
				AllowedPattern: v.allowedPattern,
			})
		}
		p.mu.RUnlock()

		out[name] = ps
	}

	return out
}

// Restore rebuilds the mock's state under the original identities: every
// parameter name and version number is preserved.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap ssmSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("ssm: parse snapshot: %w", err)
	}

	for name, ps := range snap.Params {
		p := &paramData{
			name: ps.Name, description: ps.Description, tier: ps.Tier,
			latest: ps.Latest, tags: ps.Tags,
		}

		for _, v := range ps.Versions {
			p.versions = append(p.versions, &version{
				value: v.Value, typ: v.Typ, dataType: v.DataType, version: v.Version,
				lastModified: v.LastModified, labels: v.Labels, keyID: v.KeyID,
				allowedPattern: v.AllowedPattern,
			})
		}

		m.params.Set(name, p)
	}

	if len(snap.Commands) > 0 {
		if err := m.commands.LoadSnapshot(snap.Commands); err != nil {
			return fmt.Errorf("ssm: restore commands: %w", err)
		}
	}

	return nil
}
