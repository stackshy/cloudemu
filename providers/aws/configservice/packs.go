package configservice

import (
	"context"
	"strings"

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

	if err := validateTemplate(pack.TemplateBody, pack.TemplateS3URI); err != nil {
		return "", err
	}

	now := m.now()

	if existing, ok := m.packs.Get(pack.ConformancePackName); ok {
		existing.mu.Lock()
		pack.ConformancePackArn = existing.pack.ConformancePackArn
		pack.ConformancePackID = existing.pack.ConformancePackID
		pack.State = driver.PackStateCreateComplete
		pack.LastUpdateRequestedTime = now
		pack.InputParameters = copyTags(pack.InputParameters)
		pack.Tags = copyTags(existing.pack.Tags)
		existing.pack = pack
		existing.mu.Unlock()

		return pack.ConformancePackArn, nil
	}

	// Cap creates atomically under createMu (upsert of the same name above is
	// handled before the lock; a same-name race re-checks below).
	m.createMu.Lock()
	defer m.createMu.Unlock()

	if existing, ok := m.packs.Get(pack.ConformancePackName); ok {
		existing.mu.Lock()
		pack.ConformancePackArn = existing.pack.ConformancePackArn
		pack.ConformancePackID = existing.pack.ConformancePackID
		pack.State = driver.PackStateCreateComplete
		pack.LastUpdateRequestedTime = now
		pack.InputParameters = copyTags(pack.InputParameters)
		pack.Tags = copyTags(existing.pack.Tags)
		existing.pack = pack
		existing.mu.Unlock()

		return pack.ConformancePackArn, nil
	}

	if m.packs.Len() >= maxConformancePacks {
		return "", tagged(driver.ExMaxNumberOfConformancePacksExceeded, failedPreconditionCode,
			"an account supports at most %d conformance packs", maxConformancePacks)
	}

	pack.ConformancePackID = idgen.GenerateID("conformance-pack-")
	pack.ConformancePackArn = m.arn("conformance-pack/" + pack.ConformancePackName + "-" + pack.ConformancePackID)
	pack.State = driver.PackStateCreateComplete
	pack.LastUpdateRequestedTime = now
	pack.InputParameters = copyTags(pack.InputParameters)
	m.packs.Set(pack.ConformancePackName, &packData{pack: pack})

	return pack.ConformancePackArn, nil
}

// validateTemplate performs a minimal validity check on a conformance-pack /
// org-pack template: an inline TemplateBody must be non-blank and look like JSON
// or YAML; an S3 URI must use the s3:// scheme.
func validateTemplate(templateBody, templateS3URI string) error {
	if templateS3URI != "" {
		if !strings.HasPrefix(templateS3URI, "s3://") {
			return invalidParameter("TemplateS3Uri must be an s3:// URI")
		}

		return nil
	}

	body := strings.TrimSpace(templateBody)
	if body == "" {
		return invalidParameter("TemplateBody must not be blank")
	}

	// A CloudFormation/Guard template is JSON- or YAML-shaped. Reject values that
	// are clearly neither (no structural markers at all).
	if !strings.ContainsAny(body, "{:") {
		return invalidParameter("TemplateBody is not a valid JSON or YAML template")
	}

	return nil
}

func copyPack(p *driver.ConformancePack) driver.ConformancePack {
	out := *p
	out.InputParameters = copyTags(p.InputParameters)
	out.Tags = copyTags(p.Tags)

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
