package ssm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// ssmSnapshot is the full serialized state of the SSM mock. params holds an
// unexported paramData (with a slice of unexported *version), so it is
// promoted to an exported snapshot form keyed by parameter name. The Run
// Command history is a map of exported command records keyed by command id.
// The wired instanceResolver, outputStore and opts are not serialized.
type ssmSnapshot struct {
	Params map[string]*paramSnapshot `json:"params,omitempty"`
	// CommandHistory holds every Run Command send.
	CommandHistory json.RawMessage `json:"commandHistory,omitempty"`
	// Commands is the per-invocation form older snapshots used. It is only
	// read.
	Commands json.RawMessage            `json:"commands,omitempty"`
	Settings map[string]settingSnapshot `json:"settings,omitempty"`
	// Documents holds the customer SSM documents keyed by name.
	Documents map[string]*documentSnapshot `json:"documents,omitempty"`
}

// settingSnapshot mirrors a customized service setting.
type settingSnapshot struct {
	Value        string `json:"value"`
	LastModified string `json:"lastModified,omitempty"`
	User         string `json:"user,omitempty"`
}

// policySnapshot keeps a policy's text and its status.
type policySnapshot struct {
	Text   string `json:"text"`
	Status string `json:"status,omitempty"`
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
	Value          string           `json:"value,omitempty"`
	Typ            string           `json:"typ,omitempty"`
	DataType       string           `json:"dataType,omitempty"`
	Version        int64            `json:"version,omitempty"`
	LastModified   string           `json:"lastModified,omitempty"`
	Labels         []string         `json:"labels,omitempty"`
	KeyID          string           `json:"keyId,omitempty"`
	AllowedPattern string           `json:"allowedPattern,omitempty"`
	Policies       []policySnapshot `json:"policies,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused. SSM holds no bulk
// object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := ssmSnapshot{
		Params: m.snapshotParams(), Settings: m.snapshotSettings(), Documents: m.snapshotDocuments(),
	}

	m.cmdMu.RLock()
	cmds, err := m.commands.Snapshot()
	m.cmdMu.RUnlock()

	if err != nil {
		return nil, fmt.Errorf("ssm: snapshot commands: %w", err)
	}

	snap.CommandHistory = cmds

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
				AllowedPattern: v.allowedPattern, Policies: snapshotPolicies(v.policies),
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
			policies, err := restorePolicies(v.Policies)
			if err != nil {
				return fmt.Errorf("ssm: restore policies of %s: %w", name, err)
			}

			p.versions = append(p.versions, &version{
				value: v.Value, typ: v.Typ, dataType: v.DataType, version: v.Version,
				lastModified: v.LastModified, labels: v.Labels, keyID: v.KeyID,
				allowedPattern: v.AllowedPattern, policies: policies,
			})
		}

		m.params.Set(name, p)
	}

	m.restoreSettings(snap.Settings)

	if err := m.restoreDocuments(snap.Documents); err != nil {
		return err
	}

	return m.restoreCommands(snap.CommandHistory, snap.Commands)
}

// legacyInvocation is one entry of the per-invocation command form older
// snapshots used.
type legacyInvocation struct {
	CommandID    string
	InstanceID   string
	DocumentName string
}

// restoreCommands loads the command history, converting the older
// per-invocation form into finished commands.
func (m *Mock) restoreCommands(history, legacy json.RawMessage) error {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()

	if len(history) > 0 {
		if err := m.commands.LoadSnapshot(history); err != nil {
			return fmt.Errorf("ssm: restore commands: %w", err)
		}
	}

	if len(legacy) == 0 {
		return nil
	}

	var old map[string]legacyInvocation
	if err := json.Unmarshal(legacy, &old); err != nil {
		return fmt.Errorf("ssm: restore commands: %w", err)
	}

	keys := make([]string, 0, len(old))
	for k := range old {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		inv := old[k]

		rec, ok := m.commands.Get(inv.CommandID)
		if !ok {
			rec = &commandRecord{Command: ssmdriver.Command{
				CommandID: inv.CommandID, DocumentName: inv.DocumentName, DocumentVersion: versionDefault,
				RequestedDateTime: m.opts.Clock.Now(), MaxConcurrency: defaultMaxConcurrency,
				MaxErrors: defaultMaxErrors, TimeoutSeconds: defaultCommandTimeout,
			}}
			m.commands.Set(inv.CommandID, rec)
		}

		rec.Command.InstanceIDs = append(rec.Command.InstanceIDs, inv.InstanceID)
		rec.Invocations = append(rec.Invocations, &invocationRecord{InstanceID: inv.InstanceID, OutputWritten: true})
	}

	return nil
}

func snapshotPolicies(ps []*policy) []policySnapshot {
	out := make([]policySnapshot, 0, len(ps))
	for _, p := range ps {
		out = append(out, policySnapshot{Text: p.text, Status: p.status})
	}

	return out
}

// restorePolicies parses saved policies again. An Expiration time that has
// passed is kept, so the next evaluation deletes the parameter.
func restorePolicies(saved []policySnapshot) ([]*policy, error) {
	var out []*policy

	for _, ps := range saved {
		p, err := parsePolicy(json.RawMessage(ps.Text), time.Time{}, false)
		if err != nil {
			return nil, err
		}

		if p == nil {
			continue
		}

		if ps.Status != "" {
			p.status = ps.Status
		}

		out = append(out, p)
	}

	return out, nil
}

func (m *Mock) snapshotSettings() map[string]settingSnapshot {
	m.settings.mu.RLock()
	defer m.settings.mu.RUnlock()

	if len(m.settings.values) == 0 {
		return nil
	}

	out := make(map[string]settingSnapshot, len(m.settings.values))
	for id, v := range m.settings.values {
		out[id] = settingSnapshot{Value: v.value, LastModified: v.lastModified, User: v.user}
	}

	return out
}

func (m *Mock) restoreSettings(saved map[string]settingSnapshot) {
	m.settings.mu.Lock()
	defer m.settings.mu.Unlock()

	for id, v := range saved {
		m.settings.values[id] = settingValue{value: v.Value, lastModified: v.LastModified, user: v.User}
	}
}
