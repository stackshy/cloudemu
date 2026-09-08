package healthcareapis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// snapshotState is the on-disk shape: the workspace, FHIR and DICOM stores keyed
// by their (lowercased) resource ids. The estate tenant id is deterministic
// (minted from a fixed seed in New), so it needs no persistence.
type snapshotState struct {
	Workspaces json.RawMessage `json:"workspaces,omitempty"`
	Fhir       json.RawMessage `json:"fhir,omitempty"`
	Dicom      json.RawMessage `json:"dicom,omitempty"`
}

// Snapshot captures every healthcareapis workspace, FHIR service and DICOM
// service. includeAssets is unused — these resources hold no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workspaces, err := m.workspaces.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("healthcareapis: snapshot workspaces: %w", err)
	}

	fhir, err := m.fhir.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("healthcareapis: snapshot fhir: %w", err)
	}

	dicom, err := m.dicom.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("healthcareapis: snapshot dicom: %w", err)
	}

	data, err := json.Marshal(snapshotState{Workspaces: workspaces, Fhir: fhir, Dicom: dicom})
	if err != nil {
		return nil, fmt.Errorf("healthcareapis: marshal snapshot: %w", err)
	}

	return data, nil
}

// Restore rebuilds every healthcareapis workspace, FHIR service and DICOM service
// under its original id.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	var state snapshotState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("healthcareapis: unmarshal snapshot: %w", err)
	}

	if len(state.Workspaces) > 0 {
		if err := m.workspaces.LoadSnapshot(state.Workspaces); err != nil {
			return fmt.Errorf("healthcareapis: restore workspaces: %w", err)
		}
	}

	if len(state.Fhir) > 0 {
		if err := m.fhir.LoadSnapshot(state.Fhir); err != nil {
			return fmt.Errorf("healthcareapis: restore fhir: %w", err)
		}
	}

	if len(state.Dicom) > 0 {
		if err := m.dicom.LoadSnapshot(state.Dicom); err != nil {
			return fmt.Errorf("healthcareapis: restore dicom: %w", err)
		}
	}

	return nil
}
