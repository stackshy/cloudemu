package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// mockSnapshot is the full serialized state of the CloudFormation mock: every
// stack keyed by name, with the provisioning bookkeeping needed to tear it down
// or update it after a restore. The registry of provisioners is wiring, not
// state, so it is not captured — the restored mock reuses the live one.
type mockSnapshot struct {
	Stacks map[string]*stackSnapshot `json:"stacks"`
}

type stackSnapshot struct {
	Stack          cfn.Stack                       `json:"stack"`
	ProvisionOrder []string                        `json:"provisionOrder,omitempty"`
	Resolved       map[string]cfn.ResolvedResource `json:"resolved,omitempty"`
	DeleteIDs      map[string]string               `json:"deleteIds,omitempty"`
}

// Snapshot captures every stack's state under its own name so a restore
// preserves stack ids, resource mappings, outputs, and events.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := mockSnapshot{Stacks: map[string]*stackSnapshot{}}

	for name, sd := range m.stacks.All() {
		sd.mu.RLock()
		snap.Stacks[name] = &stackSnapshot{
			Stack:          sd.stack,
			ProvisionOrder: append([]string(nil), sd.provisionOrder...),
			Resolved:       cloneResolved(sd.resolved),
			DeleteIDs:      cloneStringMap(sd.deleteIDs),
		}
		sd.mu.RUnlock()
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
		}

		if sd.resolved == nil {
			sd.resolved = map[string]cfn.ResolvedResource{}
		}

		if sd.deleteIDs == nil {
			sd.deleteIDs = map[string]string{}
		}

		m.stacks.Set(name, sd)
	}

	return nil
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
