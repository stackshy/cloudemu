package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// mockSnapshot is the full serialized state of the CloudFormation mock: every
// stack keyed by name, with the provisioning bookkeeping needed to tear it down
// or update it after a restore. The registry of provisioners is wiring, not
// state, so it is not captured. The restored mock reuses the live one.
type mockSnapshot struct {
	Stacks map[string]*stackSnapshot `json:"stacks"`
}

type stackSnapshot struct {
	Stack          cfn.Stack                       `json:"stack"`
	ProvisionOrder []string                        `json:"provisionOrder,omitempty"`
	Resolved       map[string]cfn.ResolvedResource `json:"resolved,omitempty"`
	DeleteIDs      map[string]string               `json:"deleteIds,omitempty"`
	Props          map[string]map[string]any       `json:"props,omitempty"`
	RollbackFailed []string                        `json:"rollbackFailed,omitempty"`
	ChangeSets     []changeSetRecord               `json:"changeSets,omitempty"`
	Retained       []retainedResource              `json:"retained,omitempty"`
	Imports        []string                        `json:"imports,omitempty"`
	Policies       map[string]resourcePolicy       `json:"policies,omitempty"`
	StackPolicy    string                          `json:"stackPolicy,omitempty"`
	Pending        *pendingOp                      `json:"pending,omitempty"`
	Tokens         map[string]string               `json:"tokens,omitempty"`
	OpToken        string                          `json:"opToken,omitempty"`
	Stable         *storedPrior                    `json:"stable,omitempty"`
}

// Snapshot captures every stack's state under its own name so a restore
// preserves stack ids, resource mappings, outputs, and events. Each stack
// is read under its opMu, so a phase still running is captured before or
// after it, never halfway.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := mockSnapshot{Stacks: map[string]*stackSnapshot{}}

	for name, sd := range m.stacks.All() {
		sd.opMu.Lock()
		sd.mu.RLock()
		snap.Stacks[name] = &stackSnapshot{
			Stack:          sd.stack,
			ProvisionOrder: append([]string(nil), sd.provisionOrder...),
			Resolved:       cloneResolved(sd.resolved),
			DeleteIDs:      cloneStringMap(sd.deleteIDs),
			Props:          cloneProps(sd.props),
			RollbackFailed: append([]string(nil), sd.rollbackFailed...),
			ChangeSets:     cloneChangeSets(sd.changeSets),
			Retained:       append([]retainedResource(nil), sd.retained...),
			Imports:        append([]string(nil), sd.imports...),
			Policies:       maps.Clone(sd.policies),
			StackPolicy:    sd.stackPolicy,
			Pending:        clonePending(sd.pending),
			Tokens:         maps.Clone(sd.tokens),
			OpToken:        sd.opToken,
			Stable:         sd.stable,
		}
		sd.mu.RUnlock()
		sd.opMu.Unlock()
	}

	return json.Marshal(snap)
}

// Restore rebuilds the mock's stacks under their original names and identities.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap mockSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("cloudformation: parse snapshot: %w", err)
	}

	for name, ss := range snap.Stacks {
		sd := &stackData{
			stack:          ss.Stack,
			provisionOrder: ss.ProvisionOrder,
			resolved:       ss.Resolved,
			deleteIDs:      ss.DeleteIDs,
			props:          ss.Props,
			rollbackFailed: ss.RollbackFailed,
			retained:       ss.Retained,
			imports:        ss.Imports,
			policies:       ss.Policies,
			stackPolicy:    ss.StackPolicy,
			pending:        ss.Pending,
			tokens:         ss.Tokens,
			opToken:        ss.OpToken,
			stable:         ss.Stable,
		}

		for i := range ss.ChangeSets {
			sd.changeSets = append(sd.changeSets, &ss.ChangeSets[i])
		}

		if sd.resolved == nil {
			sd.resolved = map[string]cfn.ResolvedResource{}
		}

		if sd.deleteIDs == nil {
			sd.deleteIDs = map[string]string{}
		}

		if sd.props == nil {
			sd.props = map[string]map[string]any{}
		}

		normalizeStranded(sd)

		m.stacks.Set(name, sd)
	}

	return nil
}

// clonePending copies the pending phase for a snapshot. Its slices are
// never mutated in place, so sharing them is safe.
func clonePending(op *pendingOp) *pendingOp {
	if op == nil {
		return nil
	}

	out := *op

	return &out
}

// cloneChangeSets copies the stored change sets. Their slices and maps are
// never mutated in place, so sharing them is safe.
func cloneChangeSets(in []*changeSetRecord) []changeSetRecord {
	out := make([]changeSetRecord, len(in))
	for i, rec := range in {
		out[i] = *rec
	}

	return out
}

func cloneResolved(in map[string]cfn.ResolvedResource) map[string]cfn.ResolvedResource {
	out := make(map[string]cfn.ResolvedResource, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// cloneProps copies the outer map. The property trees are never mutated in
// place, so sharing them is safe.
func cloneProps(in map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
