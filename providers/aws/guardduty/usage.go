package guardduty

import (
	"context"
	"encoding/json"
)

// usageCurrencyUnit is the only currency real GuardDuty reports usage in.
const usageCurrencyUnit = "USD"

// freeTrialDaysRemaining is the fixed number of free-trial days the emulator
// reports for every feature, so GetRemainingFreeTrialDays is deterministic.
const freeTrialDaysRemaining int32 = 30

// syntheticFeatures is the ordered set of feature names the emulator models for
// usage-by-feature and free-trial reporting. Order is fixed for determinism.
func syntheticFeatures() []string {
	return []string{
		"FLOW_LOGS",
		"CLOUD_TRAIL",
		"DNS_LOGS",
		"S3_DATA_EVENTS",
		"EKS_AUDIT_LOGS",
		"EBS_MALWARE_PROTECTION",
	}
}

// syntheticDataSources is the ordered set of (deprecated) data-source names used
// for usage-by-data-source reporting.
func syntheticDataSources() []string {
	return []string{
		"FLOW_LOGS",
		"CLOUD_TRAIL",
		"DNS_LOGS",
		"S3_LOGS",
	}
}

// usageCriteriaRequest is the UsageCriteria request block.
type usageCriteriaRequest struct {
	AccountIDs  []string `json:"accountIds"`
	DataSources []string `json:"dataSources"`
	Features    []string `json:"features"`
	Resources   []string `json:"resources"`
}

// getUsageStatisticsRequest is the GetUsageStatistics request body.
type getUsageStatisticsRequest struct {
	UsageStatisticType string                `json:"usageStatisticsType"`
	UsageCriteria      *usageCriteriaRequest `json:"usageCriteria"`
	Unit               string                `json:"unit"`
}

// total builds a Total wire block for a fixed USD amount.
func total(amount string) map[string]any {
	return map[string]any{"amount": amount, "unit": usageCurrencyUnit}
}

// GetUsageStatistics returns synthetic, well-formed usage totals in USD for the
// requested UsageStatisticType. Only the requested type is populated; the others
// are omitted, matching real GuardDuty (which nulls the non-requested objects).
func (m *Mock) GetUsageStatistics(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	if _, err := m.getDetector(detectorID); err != nil {
		return nil, err
	}

	var req getUsageStatisticsRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	stats := m.usageStatistics(req)

	return json.Marshal(map[string]any{"usageStatistics": stats})
}

// usageStatistics builds the UsageStatistics block for the requested statistic
// type, honoring the criteria's account/feature/resource selection.
func (m *Mock) usageStatistics(req getUsageStatisticsRequest) map[string]any {
	switch req.UsageStatisticType {
	case "SUM_BY_ACCOUNT":
		return map[string]any{"sumByAccount": m.usageByAccount(req.UsageCriteria)}
	case "SUM_BY_DATA_SOURCE":
		return map[string]any{"sumByDataSource": usageByDataSource(req.UsageCriteria)}
	case "SUM_BY_RESOURCE":
		return map[string]any{"sumByResource": usageByResource(req.UsageCriteria)}
	case "SUM_BY_FEATURES":
		return map[string]any{"sumByFeature": usageByFeature(req.UsageCriteria)}
	default:
		return map[string]any{}
	}
}

// usageByAccount returns per-account usage totals for the requested accounts,
// defaulting to this emulator's account when none are requested.
func (m *Mock) usageByAccount(crit *usageCriteriaRequest) []map[string]any {
	accounts := []string{m.opts.AccountID}
	if crit != nil && len(crit.AccountIDs) > 0 {
		accounts = crit.AccountIDs
	}

	out := make([]map[string]any, 0, len(accounts))
	for _, acct := range accounts {
		out = append(out, map[string]any{"accountId": acct, "total": total("10.00")})
	}

	return out
}

// usageByDataSource returns per-data-source usage totals for the requested data
// sources, defaulting to the synthetic set when none are requested.
func usageByDataSource(crit *usageCriteriaRequest) []map[string]any {
	sources := syntheticDataSources()
	if crit != nil && len(crit.DataSources) > 0 {
		sources = crit.DataSources
	}

	out := make([]map[string]any, 0, len(sources))
	for _, ds := range sources {
		out = append(out, map[string]any{"dataSource": ds, "total": total("5.00")})
	}

	return out
}

// usageByFeature returns per-feature usage totals for the requested features,
// defaulting to the synthetic set when none are requested.
func usageByFeature(crit *usageCriteriaRequest) []map[string]any {
	features := syntheticFeatures()
	if crit != nil && len(crit.Features) > 0 {
		features = crit.Features
	}

	out := make([]map[string]any, 0, len(features))
	for _, f := range features {
		out = append(out, map[string]any{"feature": f, "total": total("3.00")})
	}

	return out
}

// usageByResource returns per-resource usage totals for the requested resources.
// GuardDuty accepts exact resource names, so with none requested there is nothing
// to sum and the list is empty.
func usageByResource(crit *usageCriteriaRequest) []map[string]any {
	if crit == nil || len(crit.Resources) == 0 {
		return []map[string]any{}
	}

	out := make([]map[string]any, 0, len(crit.Resources))
	for _, r := range crit.Resources {
		out = append(out, map[string]any{"resource": r, "total": total("2.00")})
	}

	return out
}

// getRemainingFreeTrialDaysRequest is the GetRemainingFreeTrialDays request body.
type getRemainingFreeTrialDaysRequest struct {
	AccountIDs []string `json:"accountIds"`
}

// GetRemainingFreeTrialDays returns, per requested account, the free-trial days
// remaining for each modeled feature. With no account requested it reports this
// emulator's own account. The remaining-days value is fixed for determinism.
func (m *Mock) GetRemainingFreeTrialDays(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	if _, err := m.getDetector(detectorID); err != nil {
		return nil, err
	}

	var req getRemainingFreeTrialDaysRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	accounts := req.AccountIDs
	if len(accounts) == 0 {
		accounts = []string{m.opts.AccountID}
	}

	out := make([]map[string]any, 0, len(accounts))
	for _, acct := range accounts {
		out = append(out, map[string]any{
			"accountId": acct,
			"features":  freeTrialFeatures(),
		})
	}

	return json.Marshal(map[string]any{"accounts": out})
}

// freeTrialFeatures returns the FreeTrialFeatureConfigurationResult list, one
// entry per modeled feature with the fixed remaining-days value.
func freeTrialFeatures() []map[string]any {
	features := syntheticFeatures()
	out := make([]map[string]any, 0, len(features))

	for _, f := range features {
		out = append(out, map[string]any{
			"name":                   f,
			"freeTrialDaysRemaining": freeTrialDaysRemaining,
		})
	}

	return out
}
