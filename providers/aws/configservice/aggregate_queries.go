package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// The aggregate query surface is synthesized: the emulator has no cross-account
// aggregation pipeline, so these validate the aggregator exists and return the
// local account's own recorded state (rules/resources) or plausible empty
// results. Documented in docs/services.md.

// DescribeAggregateComplianceByConfigRules returns local rules as the aggregate.
func (m *Mock) DescribeAggregateComplianceByConfigRules(
	_ context.Context, aggregatorName string, page driver.Page,
) ([]driver.ConfigRule, string, error) {
	if !m.aggregators.Has(aggregatorName) {
		return nil, "", noSuchAggregator(aggregatorName)
	}

	return paginate(m.allRules(), page)
}

// DescribeAggregateComplianceByConformancePacks returns local packs.
func (m *Mock) DescribeAggregateComplianceByConformancePacks(
	_ context.Context, aggregatorName string, page driver.Page,
) ([]driver.ConformancePack, string, error) {
	if !m.aggregators.Has(aggregatorName) {
		return nil, "", noSuchAggregator(aggregatorName)
	}

	return paginate(m.allPacks(), page)
}

// GetAggregateComplianceDetailsByConfigRule returns local rule evaluations.
func (m *Mock) GetAggregateComplianceDetailsByConfigRule(
	ctx context.Context, aggregatorName, ruleName, _, _ string, page driver.Page,
) ([]driver.Evaluation, string, error) {
	if !m.aggregators.Has(aggregatorName) {
		return nil, "", noSuchAggregator(aggregatorName)
	}

	return m.GetComplianceDetailsByConfigRule(ctx, ruleName, page)
}

// GetAggregateConfigRuleComplianceSummary summarizes local rule compliance.
func (m *Mock) GetAggregateConfigRuleComplianceSummary(
	ctx context.Context, aggregatorName string, _ driver.Page,
) (compliant, nonCompliant int32, err error) {
	if !m.aggregators.Has(aggregatorName) {
		return 0, 0, noSuchAggregator(aggregatorName)
	}

	return m.GetComplianceSummaryByConfigRule(ctx)
}

// GetAggregateConformancePackComplianceSummary summarizes local pack compliance.
func (m *Mock) GetAggregateConformancePackComplianceSummary(
	_ context.Context, aggregatorName string, _ driver.Page,
) (compliant, nonCompliant int32, err error) {
	if !m.aggregators.Has(aggregatorName) {
		return 0, 0, noSuchAggregator(aggregatorName)
	}

	//nolint:gosec // pack count is small and non-negative; conversion cannot overflow int32.
	return int32(m.packs.Len()), 0, nil
}

// GetAggregateDiscoveredResourceCounts returns local discovered-resource counts.
func (m *Mock) GetAggregateDiscoveredResourceCounts(
	_ context.Context, aggregatorName string, page driver.Page,
) (total int64, counts []driver.ResourceCount, next string, err error) {
	if !m.aggregators.Has(aggregatorName) {
		return 0, nil, "", noSuchAggregator(aggregatorName)
	}

	return m.discoveredCounts(nil, page)
}

// GetAggregateResourceConfig returns a locally-recorded resource item.
func (m *Mock) GetAggregateResourceConfig(
	_ context.Context, aggregatorName, resourceType, resourceID string,
) (*driver.ConfigurationItem, error) {
	if !m.aggregators.Has(aggregatorName) {
		return nil, noSuchAggregator(aggregatorName)
	}

	item, ok := m.resources.Get(resourceKey(resourceType, resourceID))
	if !ok {
		return nil, tagged(driver.ExResourceNotDiscovered, notFoundCode,
			"resource %s/%s has not been discovered", resourceType, resourceID)
	}

	out := copyItem(item)

	return &out, nil
}

// BatchGetAggregateResourceConfig returns local items for the requested keys.
func (m *Mock) BatchGetAggregateResourceConfig(
	_ context.Context, aggregatorName string, keys []driver.ResourceKey,
) (found []driver.ConfigurationItem, unprocessed []driver.ResourceKey, err error) {
	if !m.aggregators.Has(aggregatorName) {
		return nil, nil, noSuchAggregator(aggregatorName)
	}

	return m.batchGet(keys)
}

// ListAggregateDiscoveredResources lists local resource keys.
func (m *Mock) ListAggregateDiscoveredResources(
	_ context.Context, aggregatorName, resourceType string, page driver.Page,
) ([]driver.ResourceKey, string, error) {
	if !m.aggregators.Has(aggregatorName) {
		return nil, "", noSuchAggregator(aggregatorName)
	}

	return m.listResourceKeys(resourceType, nil, page)
}

// SelectAggregateResourceConfig runs a SQL-like query against local resources.
// Synthesized: returns each recorded item's Configuration JSON.
func (m *Mock) SelectAggregateResourceConfig(
	_ context.Context, aggregatorName, expression string, page driver.Page,
) (rows []string, nextToken string, err error) {
	if !m.aggregators.Has(aggregatorName) {
		return nil, "", noSuchAggregator(aggregatorName)
	}

	if expression == "" {
		return nil, "", invalidParameter("Expression is required")
	}

	return m.selectResults(page)
}
