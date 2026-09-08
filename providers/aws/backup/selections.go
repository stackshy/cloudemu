package backup

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// CreateBackupSelection assigns a set of resources to a plan. The selection id
// and creation date are minted once and stored.
func (m *Mock) CreateBackupSelection(_ context.Context, in *driver.CreateSelectionInput) (*driver.Selection, error) {
	if !m.plans.Has(in.BackupPlanID) {
		return nil, notFound("backup plan %s not found", in.BackupPlanID)
	}

	if in.Body.SelectionName == "" {
		return nil, missingParam("SelectionName is required")
	}

	if in.Body.IamRoleArn == "" {
		return nil, missingParam("IamRoleArn is required")
	}

	s := driver.Selection{
		SelectionID:      idgen.UUID(),
		BackupPlanID:     in.BackupPlanID,
		CreatorRequestID: in.CreatorRequestID,
		CreationDate:     m.now(),
		Body:             copySelectionBody(&in.Body),
	}

	m.selections.Set(s.SelectionID, s)

	out := copySelection(&s)

	return &out, nil
}

// GetBackupSelection returns a selection that belongs to the given plan.
func (m *Mock) GetBackupSelection(_ context.Context, planID, selectionID string) (*driver.Selection, error) {
	s, ok := m.selections.Get(selectionID)
	if !ok || s.BackupPlanID != planID {
		return nil, notFound("backup selection %s not found for plan %s", selectionID, planID)
	}

	out := copySelection(&s)

	return &out, nil
}

// DeleteBackupSelection removes a selection that belongs to the given plan.
func (m *Mock) DeleteBackupSelection(_ context.Context, planID, selectionID string) error {
	s, ok := m.selections.Get(selectionID)
	if !ok || s.BackupPlanID != planID {
		return notFound("backup selection %s not found for plan %s", selectionID, planID)
	}

	m.selections.Delete(selectionID)

	return nil
}

// ListBackupSelections returns a deterministic page of a plan's selections,
// ordered by selection id.
func (m *Mock) ListBackupSelections(
	_ context.Context, planID string, page driver.Page,
) ([]*driver.Selection, string, error) {
	if !m.plans.Has(planID) {
		return nil, "", notFound("backup plan %s not found", planID)
	}

	var filtered []driver.Selection

	sorted := m.selections.SortedValues()

	for i := range sorted {
		if sorted[i].BackupPlanID == planID {
			filtered = append(filtered, sorted[i])
		}
	}

	start, end, next := paginate(len(filtered), page)
	out := make([]*driver.Selection, 0, end-start)

	for i := start; i < end; i++ {
		s := copySelection(&filtered[i])
		out = append(out, &s)
	}

	return out, next, nil
}
