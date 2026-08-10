package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// The aggregate query surface is synthesized: the emulator has no cross-account
// aggregation pipeline, so these validate the aggregator exists and return the
// local account's own recorded state (rules/resources) or plausible empty
// results. Documented in docs/services.md.
//
// Authorization: aggregated results only include a source's data when the
// aggregator selects that source AND the source has an AggregationAuthorization
// authorizing it. The emulator holds a single account/region, so the local
// account/region is the only possible source; if the aggregator does not select
// it or no matching authorization exists, the source contributes NOTHING (real
// Config returns no data from an unauthorized source).

// localSourceAuthorized reports whether the emulator's local account/region is a
// selected source of the named aggregator AND has a matching
// AggregationAuthorization. Returns noSuchAggregator if the aggregator is absent.
func (m *Mock) localSourceAuthorized(aggregatorName string) (bool, error) {
	ad, ok := m.aggregators.Get(aggregatorName)
	if !ok {
		return false, noSuchAggregator(aggregatorName)
	}

	ad.mu.RLock()
	selected := aggregatorSelectsLocal(&ad.agg, m.opts.AccountID, m.opts.Region)
	ad.mu.RUnlock()

	if !selected {
		return false, nil
	}

	m.authMu.RLock()
	defer m.authMu.RUnlock()

	for i := range m.authorizations {
		a := &m.authorizations[i]
		if a.AuthorizedAccountID == m.opts.AccountID && a.AuthorizedAwsRegion == m.opts.Region {
			return true, nil
		}
	}

	return false, nil
}

// aggregatorSelectsLocal reports whether an aggregator's sources include the
// given local account/region.
func aggregatorSelectsLocal(agg *driver.ConfigurationAggregator, accountID, region string) bool {
	// An organization-sourced aggregator selects every account in the org, which
	// includes the local one.
	if agg.OrganizationSource != nil {
		return agg.OrganizationSource.AllAwsRegions || containsString(agg.OrganizationSource.AwsRegions, region)
	}

	for i := range agg.AccountSources {
		s := &agg.AccountSources[i]
		if !containsString(s.AccountIDs, accountID) {
			continue
		}

		if s.AllAwsRegions || containsString(s.AwsRegions, region) {
			return true
		}
	}

	return false
}

func containsString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}

	return false
}

// DescribeAggregateComplianceByConfigRules returns local rules as the aggregate,
// gated on the local source being authorized.
func (m *Mock) DescribeAggregateComplianceByConfigRules(
	_ context.Context, aggregatorName string, page driver.Page,
) ([]driver.ConfigRule, string, error) {
	ok, err := m.localSourceAuthorized(aggregatorName)
	if err != nil {
		return nil, "", err
	}

	if !ok {
		return paginate([]driver.ConfigRule{}, page)
	}

	return paginate(m.allRules(), page)
}

// DescribeAggregateComplianceByConformancePacks returns local packs, gated on
// the local source being authorized.
func (m *Mock) DescribeAggregateComplianceByConformancePacks(
	_ context.Context, aggregatorName string, page driver.Page,
) ([]driver.ConformancePack, string, error) {
	ok, err := m.localSourceAuthorized(aggregatorName)
	if err != nil {
		return nil, "", err
	}

	if !ok {
		return paginate([]driver.ConformancePack{}, page)
	}

	return paginate(m.allPacks(), page)
}

// GetAggregateComplianceDetailsByConfigRule returns local rule evaluations, gated
// on the local source being authorized.
func (m *Mock) GetAggregateComplianceDetailsByConfigRule(
	ctx context.Context, aggregatorName, ruleName, _, _ string, page driver.Page,
) ([]driver.Evaluation, string, error) {
	ok, err := m.localSourceAuthorized(aggregatorName)
	if err != nil {
		return nil, "", err
	}

	if !ok {
		return paginate([]driver.Evaluation{}, page)
	}

	return m.GetComplianceDetailsByConfigRule(ctx, ruleName, page)
}

// GetAggregateConfigRuleComplianceSummary summarizes local rule compliance, gated
// on the local source being authorized.
func (m *Mock) GetAggregateConfigRuleComplianceSummary(
	ctx context.Context, aggregatorName string, _ driver.Page,
) (compliant, nonCompliant int32, err error) {
	ok, err := m.localSourceAuthorized(aggregatorName)
	if err != nil {
		return 0, 0, err
	}

	if !ok {
		return 0, 0, nil
	}

	return m.GetComplianceSummaryByConfigRule(ctx)
}

// GetAggregateConformancePackComplianceSummary summarizes local pack compliance,
// gated on the local source being authorized.
func (m *Mock) GetAggregateConformancePackComplianceSummary(
	_ context.Context, aggregatorName string, _ driver.Page,
) (compliant, nonCompliant int32, err error) {
	ok, err := m.localSourceAuthorized(aggregatorName)
	if err != nil {
		return 0, 0, err
	}

	if !ok {
		return 0, 0, nil
	}

	//nolint:gosec // pack count is small and non-negative; conversion cannot overflow int32.
	return int32(m.packs.Len()), 0, nil
}

// GetAggregateDiscoveredResourceCounts returns local discovered-resource counts,
// gated on the local source being authorized.
func (m *Mock) GetAggregateDiscoveredResourceCounts(
	_ context.Context, aggregatorName string, page driver.Page,
) (total int64, counts []driver.ResourceCount, next string, err error) {
	ok, err := m.localSourceAuthorized(aggregatorName)
	if err != nil {
		return 0, nil, "", err
	}

	if !ok {
		return 0, nil, "", nil
	}

	return m.discoveredCounts(nil, page)
}

// GetAggregateResourceConfig returns a locally-recorded resource item, gated on
// the local source being authorized.
func (m *Mock) GetAggregateResourceConfig(
	_ context.Context, aggregatorName, resourceType, resourceID string,
) (*driver.ConfigurationItem, error) {
	authorized, err := m.localSourceAuthorized(aggregatorName)
	if err != nil {
		return nil, err
	}

	if !authorized {
		return nil, tagged(driver.ExResourceNotDiscovered, notFoundCode,
			"resource %s/%s has not been discovered", resourceType, resourceID)
	}

	item, ok := m.resources.Get(resourceKey(resourceType, resourceID))
	if !ok {
		return nil, tagged(driver.ExResourceNotDiscovered, notFoundCode,
			"resource %s/%s has not been discovered", resourceType, resourceID)
	}

	out := copyItem(item)

	return &out, nil
}

// BatchGetAggregateResourceConfig returns local items for the requested keys,
// gated on the local source being authorized.
func (m *Mock) BatchGetAggregateResourceConfig(
	_ context.Context, aggregatorName string, keys []driver.ResourceKey,
) (found []driver.ConfigurationItem, unprocessed []driver.ResourceKey, err error) {
	ok, err := m.localSourceAuthorized(aggregatorName)
	if err != nil {
		return nil, nil, err
	}

	if !ok {
		// An unauthorized source discovers nothing; every requested key is
		// unprocessed.
		return nil, append([]driver.ResourceKey(nil), keys...), nil
	}

	return m.batchGet(keys)
}

// ListAggregateDiscoveredResources lists local resource keys, gated on the local
// source being authorized.
func (m *Mock) ListAggregateDiscoveredResources(
	_ context.Context, aggregatorName, resourceType string, page driver.Page,
) ([]driver.ResourceKey, string, error) {
	ok, err := m.localSourceAuthorized(aggregatorName)
	if err != nil {
		return nil, "", err
	}

	if !ok {
		return paginate([]driver.ResourceKey{}, page)
	}

	return m.listResourceKeys(resourceType, nil, page)
}

// SelectAggregateResourceConfig runs a SQL-like query against local resources,
// gated on the local source being authorized.
func (m *Mock) SelectAggregateResourceConfig(
	_ context.Context, aggregatorName, expression string, page driver.Page,
) (rows []string, nextToken string, err error) {
	ok, err := m.localSourceAuthorized(aggregatorName)
	if err != nil {
		return nil, "", err
	}

	if expression == "" {
		return nil, "", invalidParameter("Expression is required")
	}

	sel, perr := parseSelect(expression)
	if perr != nil {
		return nil, "", perr
	}

	if !ok {
		return paginate([]string{}, page)
	}

	return m.selectResultsFiltered(sel, page)
}
