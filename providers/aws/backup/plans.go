package backup

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// newVersion builds an immutable plan version from a body: it deep-copies the
// body and mints a fresh, stable RuleId for every rule.
func (m *Mock) newVersion(body driver.PlanBody) driver.PlanVersion {
	v := driver.PlanVersion{
		VersionID:    mintVersionID(),
		CreationDate: m.now(),
		Body:         copyPlanBody(&body),
	}

	for i := range v.Body.Rules {
		v.Body.Rules[i].RuleID = idgen.UUID()
	}

	return v
}

// CreateBackupPlan provisions a plan with stable computed fields (id, arn,
// versionId, per-rule ruleId, creationDate) minted once and stored.
func (m *Mock) CreateBackupPlan(_ context.Context, in *driver.CreatePlanInput) (*driver.Plan, error) {
	if in.Body.BackupPlanName == "" {
		return nil, missingParam("BackupPlanName is required")
	}

	if len(in.Body.Rules) == 0 {
		return nil, invalidParam("a backup plan must contain at least one rule")
	}

	id := idgen.UUID()
	p := driver.Plan{
		BackupPlanID:     id,
		BackupPlanArn:    m.planARN(id),
		CreatorRequestID: in.CreatorRequestID,
		Tags:             copyTags(in.Tags),
		Versions:         []driver.PlanVersion{m.newVersion(in.Body)},
	}

	m.plans.Set(id, p)

	out := copyPlan(&p)

	return &out, nil
}

// GetBackupPlan returns the plan and one of its versions. An empty versionID
// resolves the current version.
func (m *Mock) GetBackupPlan(_ context.Context, id, versionID string) (*driver.Plan, driver.PlanVersion, error) {
	p, ok := m.plans.Get(id)
	if !ok {
		return nil, driver.PlanVersion{}, notFound("backup plan %s not found", id)
	}

	out := copyPlan(&p)

	if versionID == "" {
		return &out, out.Current(), nil
	}

	for i := range out.Versions {
		if out.Versions[i].VersionID == versionID {
			return &out, out.Versions[i], nil
		}
	}

	return nil, driver.PlanVersion{}, notFound("backup plan %s version %s not found", id, versionID)
}

// UpdateBackupPlan appends a new version to a plan, bumping its VersionId. The
// plan id, arn and creation date of the first version are unchanged.
func (m *Mock) UpdateBackupPlan(_ context.Context, in *driver.UpdatePlanInput) (*driver.Plan, error) {
	p, ok := m.plans.Get(in.BackupPlanID)
	if !ok {
		return nil, notFound("backup plan %s not found", in.BackupPlanID)
	}

	if in.Body.BackupPlanName == "" {
		return nil, missingParam("BackupPlanName is required")
	}

	if len(in.Body.Rules) == 0 {
		return nil, invalidParam("a backup plan must contain at least one rule")
	}

	p.Versions = append(copyVersions(p.Versions), m.newVersion(in.Body))
	m.plans.Set(in.BackupPlanID, p)

	out := copyPlan(&p)

	return &out, nil
}

// DeleteBackupPlan removes a plan. A plan still referenced by a backup selection
// cannot be deleted, matching the service.
func (m *Mock) DeleteBackupPlan(_ context.Context, id string) (*driver.Plan, error) {
	p, ok := m.plans.Get(id)
	if !ok {
		return nil, notFound("backup plan %s not found", id)
	}

	if n := m.selectionsForPlan(id); n > 0 {
		return nil, invalidRequest("backup plan %s cannot be deleted: %d selection(s) still reference it", id, n)
	}

	out := copyPlan(&p)

	m.plans.Delete(id)

	return &out, nil
}

// selectionsForPlan counts the selections that reference a plan.
func (m *Mock) selectionsForPlan(planID string) int {
	count := 0
	sels := m.selections.SortedValues()

	for i := range sels {
		if sels[i].BackupPlanID == planID {
			count++
		}
	}

	return count
}

// ListBackupPlans returns a deterministic page of plans ordered by id.
func (m *Mock) ListBackupPlans(_ context.Context, page driver.Page) ([]*driver.Plan, string, error) {
	stored := m.plans.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.Plan, 0, end-start)

	for i := start; i < end; i++ {
		p := copyPlan(&stored[i])
		out = append(out, &p)
	}

	return out, next, nil
}

// ListBackupPlanVersions returns the plan and a page of its version history,
// newest first.
func (m *Mock) ListBackupPlanVersions(
	_ context.Context, id string, page driver.Page,
) (*driver.Plan, []driver.PlanVersion, string, error) {
	p, ok := m.plans.Get(id)
	if !ok {
		return nil, nil, "", notFound("backup plan %s not found", id)
	}

	all := copyVersions(p.Versions)
	reversed := make([]driver.PlanVersion, len(all))

	for i := range all {
		reversed[len(all)-1-i] = all[i]
	}

	start, end, next := paginate(len(reversed), page)
	out := copyPlan(&p)

	return &out, reversed[start:end], next, nil
}
