package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// PutConformancePack creates or updates a conformance pack (upsert). Exactly one
// of TemplateBody / TemplateS3URI must be supplied.
//
//nolint:gocritic // pack is the driver ConformancePack input, taken by value to match the driver API.
func (m *Mock) PutConformancePack(_ context.Context, pack driver.ConformancePack) (string, error) {
	if pack.ConformancePackName == "" {
		return "", invalidParameter("ConformancePackName is required")
	}

	if (pack.TemplateBody == "") == (pack.TemplateS3URI == "") {
		return "", invalidParameter("exactly one of TemplateBody or TemplateS3Uri must be specified")
	}

	now := m.now()

	if existing, ok := m.packs.Get(pack.ConformancePackName); ok {
		existing.mu.Lock()
		pack.ConformancePackArn = existing.pack.ConformancePackArn
		pack.ConformancePackID = existing.pack.ConformancePackID
		pack.State = driver.PackStateCreateComplete
		pack.LastUpdateRequestedTime = now
		pack.InputParameters = copyTags(pack.InputParameters)
		existing.pack = pack
		existing.mu.Unlock()

		return pack.ConformancePackArn, nil
	}

	pack.ConformancePackID = idgen.GenerateID("conformance-pack-")
	pack.ConformancePackArn = m.arn("conformance-pack/" + pack.ConformancePackName + "-" + pack.ConformancePackID)
	pack.State = driver.PackStateCreateComplete
	pack.LastUpdateRequestedTime = now
	pack.InputParameters = copyTags(pack.InputParameters)

	if !m.packs.SetIfAbsent(pack.ConformancePackName, &packData{pack: pack}) {
		return "", resourceInUse("conformance pack %q already exists", pack.ConformancePackName)
	}

	return pack.ConformancePackArn, nil
}

func copyPack(p *driver.ConformancePack) driver.ConformancePack {
	out := *p
	out.InputParameters = copyTags(p.InputParameters)

	return out
}

func (m *Mock) allPacks() []driver.ConformancePack {
	keys := sortedKeys(m.packs.Keys())
	out := make([]driver.ConformancePack, 0, len(keys))

	for _, k := range keys {
		pd, ok := m.packs.Get(k)
		if !ok {
			continue
		}

		pd.mu.RLock()
		out = append(out, copyPack(&pd.pack))
		pd.mu.RUnlock()
	}

	return out
}

// DescribeConformancePacks returns the named packs (all if empty), paginated.
func (m *Mock) DescribeConformancePacks(
	_ context.Context, names []string, page driver.Page,
) ([]driver.ConformancePack, string, error) {
	for _, n := range names {
		if !m.packs.Has(n) {
			return nil, "", noSuchConformancePack(n)
		}
	}

	filtered := filterByNames(m.allPacks(), func(p driver.ConformancePack) string { return p.ConformancePackName }, names)

	return paginate(filtered, page)
}

// DescribeConformancePackStatus returns pack deployment status.
func (m *Mock) DescribeConformancePackStatus(
	_ context.Context, names []string, page driver.Page,
) ([]driver.ConformancePack, string, error) {
	for _, n := range names {
		if !m.packs.Has(n) {
			return nil, "", noSuchConformancePack(n)
		}
	}

	filtered := filterByNames(m.allPacks(), func(p driver.ConformancePack) string { return p.ConformancePackName }, names)

	return paginate(filtered, page)
}

// DeleteConformancePack removes a pack.
func (m *Mock) DeleteConformancePack(_ context.Context, name string) error {
	if !m.packs.Delete(name) {
		return noSuchConformancePack(name)
	}

	return nil
}

// GetConformancePackComplianceDetails returns per-rule evaluations for a pack.
// Synthesized: empty until evaluations are reported.
func (m *Mock) GetConformancePackComplianceDetails(
	_ context.Context, name string, page driver.Page,
) ([]driver.Evaluation, string, error) {
	if !m.packs.Has(name) {
		return nil, "", noSuchConformancePack(name)
	}

	return paginate([]driver.Evaluation{}, page)
}

// GetConformancePackComplianceSummary returns compliance summaries for packs.
func (m *Mock) GetConformancePackComplianceSummary(
	_ context.Context, names []string, page driver.Page,
) ([]driver.ConformancePack, string, error) {
	filtered := filterByNames(m.allPacks(), func(p driver.ConformancePack) string { return p.ConformancePackName }, names)

	return paginate(filtered, page)
}

// DescribeConformancePackCompliance returns per-rule compliance within a pack.
// Synthesized: empty until rules within packs are evaluated.
func (m *Mock) DescribeConformancePackCompliance(
	_ context.Context, name string, page driver.Page,
) ([]driver.ConfigRule, string, error) {
	if !m.packs.Has(name) {
		return nil, "", noSuchConformancePack(name)
	}

	return paginate([]driver.ConfigRule{}, page)
}

// ListConformancePackComplianceScores returns compliance scores for all packs.
func (m *Mock) ListConformancePackComplianceScores(
	_ context.Context, page driver.Page,
) ([]driver.ConformancePack, string, error) {
	return paginate(m.allPacks(), page)
}
