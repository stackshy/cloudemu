package glue

import "context"

// This file implements the synthesizedAPI: operations for surfaces with no real
// compute or data plane behind the emulator (read-only analytics, ML
// transforms, data quality, integrations, glossary/assets/forms, column
// statistics, sessions/statements, usage profiles, materialized views, identity
// center, dashboards, unfiltered metadata, catalog import, job bookmarks,
// partition indexes, table optimizers, and schema-version metadata). Each
// accepts the decoded request and returns an empty, well-formed response body
// so the SDK wire contract holds; the emulator never fabricates fake job
// results, ML models, or data-quality scores. See docs/services.md.
//
// synthEmpty is the shared body: it returns an empty object, which the SDK
// decodes as all-absent optional fields — a valid, honest "no data" response.
func synthEmpty(_ context.Context, _ map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

// AssociateGlossaryTerms returns a synthesized empty result. See the file header.
func (*Mock) AssociateGlossaryTerms(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// BatchGetCustomEntityTypes returns a synthesized empty result. See the file header.
func (*Mock) BatchGetCustomEntityTypes(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// BatchGetDataQualityResult returns a synthesized empty result. See the file header.
func (*Mock) BatchGetDataQualityResult(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// BatchGetDataQualityRulesetEvaluationRun returns a synthesized empty result. See the file header.
func (*Mock) BatchGetDataQualityRulesetEvaluationRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// BatchGetIterableForms returns a synthesized empty result. See the file header.
func (*Mock) BatchGetIterableForms(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// BatchGetTableOptimizer returns a synthesized empty result. See the file header.
func (*Mock) BatchGetTableOptimizer(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// BatchPutDataQualityStatisticAnnotation returns a synthesized empty result. See the file header.
func (*Mock) BatchPutDataQualityStatisticAnnotation(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CancelDataQualityRuleRecommendationRun returns a synthesized empty result. See the file header.
func (*Mock) CancelDataQualityRuleRecommendationRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CancelDataQualityRulesetEvaluationRun returns a synthesized empty result. See the file header.
func (*Mock) CancelDataQualityRulesetEvaluationRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CancelMLTaskRun returns a synthesized empty result. See the file header.
func (*Mock) CancelMLTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CancelStatement returns a synthesized empty result. See the file header.
func (*Mock) CancelStatement(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateColumnStatisticsTaskSettings returns a synthesized empty result. See the file header.
func (*Mock) CreateColumnStatisticsTaskSettings(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateCustomEntityType returns a synthesized empty result. See the file header.
func (*Mock) CreateCustomEntityType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateDataQualityRuleset returns a synthesized empty result. See the file header.
func (*Mock) CreateDataQualityRuleset(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateGlossary returns a synthesized empty result. See the file header.
func (*Mock) CreateGlossary(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateGlossaryTerm returns a synthesized empty result. See the file header.
func (*Mock) CreateGlossaryTerm(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateGlueIdentityCenterConfiguration returns a synthesized empty result. See the file header.
func (*Mock) CreateGlueIdentityCenterConfiguration(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateIntegration returns a synthesized empty result. See the file header.
func (*Mock) CreateIntegration(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateIntegrationResourceProperty returns a synthesized empty result. See the file header.
func (*Mock) CreateIntegrationResourceProperty(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateIntegrationTableProperties returns a synthesized empty result. See the file header.
func (*Mock) CreateIntegrationTableProperties(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateMLTransform returns a synthesized empty result. See the file header.
func (*Mock) CreateMLTransform(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// synthRequireTable validates that the parent table named in a synth request
// exists, so a partition-index mutation on a nonexistent table errors with
// EntityNotFoundException rather than returning an empty 200.
func (m *Mock) synthRequireTable(req map[string]any) error {
	cat, _ := req["CatalogId"].(string)
	db, _ := req["DatabaseName"].(string)
	tbl, _ := req["TableName"].(string)

	if db == "" || tbl == "" {
		return invalidInput("DatabaseName and TableName are required")
	}

	return m.requireTable(m.catalogOrDefault(cat), db, tbl)
}

// CreatePartitionIndex validates the parent table then returns a synthesized
// empty result. See the file header.
func (m *Mock) CreatePartitionIndex(ctx context.Context, req map[string]any) (map[string]any, error) {
	if err := m.synthRequireTable(req); err != nil {
		return nil, err
	}

	return synthEmpty(ctx, req)
}

// CreateScript returns a synthesized empty result. See the file header.
func (*Mock) CreateScript(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateSession returns a synthesized empty result. See the file header.
func (*Mock) CreateSession(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateTableOptimizer returns a synthesized empty result. See the file header.
func (*Mock) CreateTableOptimizer(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// CreateUsageProfile returns a synthesized empty result. See the file header.
func (*Mock) CreateUsageProfile(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteAsset returns a synthesized empty result. See the file header.
func (*Mock) DeleteAsset(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteAssetType returns a synthesized empty result. See the file header.
func (*Mock) DeleteAssetType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteAttachment returns a synthesized empty result. See the file header.
func (*Mock) DeleteAttachment(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteColumnStatisticsForPartition returns a synthesized empty result. See the file header.
func (*Mock) DeleteColumnStatisticsForPartition(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteColumnStatisticsForTable returns a synthesized empty result. See the file header.
func (*Mock) DeleteColumnStatisticsForTable(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteColumnStatisticsTaskSettings returns a synthesized empty result. See the file header.
func (*Mock) DeleteColumnStatisticsTaskSettings(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteConnectionType returns a synthesized empty result. See the file header.
func (*Mock) DeleteConnectionType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteCustomEntityType returns a synthesized empty result. See the file header.
func (*Mock) DeleteCustomEntityType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteDataQualityRuleset returns a synthesized empty result. See the file header.
func (*Mock) DeleteDataQualityRuleset(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteFormType returns a synthesized empty result. See the file header.
func (*Mock) DeleteFormType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteGlossary returns a synthesized empty result. See the file header.
func (*Mock) DeleteGlossary(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteGlossaryTerm returns a synthesized empty result. See the file header.
func (*Mock) DeleteGlossaryTerm(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteGlueIdentityCenterConfiguration returns a synthesized empty result. See the file header.
func (*Mock) DeleteGlueIdentityCenterConfiguration(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteIntegration returns a synthesized empty result. See the file header.
func (*Mock) DeleteIntegration(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteIntegrationResourceProperty returns a synthesized empty result. See the file header.
func (*Mock) DeleteIntegrationResourceProperty(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteIntegrationTableProperties returns a synthesized empty result. See the file header.
func (*Mock) DeleteIntegrationTableProperties(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteMLTransform returns a synthesized empty result. See the file header.
func (*Mock) DeleteMLTransform(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeletePartitionIndex validates the parent table then returns a synthesized
// empty result. See the file header.
func (m *Mock) DeletePartitionIndex(ctx context.Context, req map[string]any) (map[string]any, error) {
	if err := m.synthRequireTable(req); err != nil {
		return nil, err
	}

	return synthEmpty(ctx, req)
}

// DeleteSession returns a synthesized empty result. See the file header.
func (*Mock) DeleteSession(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteTableOptimizer returns a synthesized empty result. See the file header.
func (*Mock) DeleteTableOptimizer(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DeleteUsageProfile returns a synthesized empty result. See the file header.
func (*Mock) DeleteUsageProfile(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DescribeConnectionType returns a synthesized empty result. See the file header.
func (*Mock) DescribeConnectionType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DescribeEntity returns a synthesized empty result. See the file header.
func (*Mock) DescribeEntity(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DescribeInboundIntegrations returns a synthesized empty result. See the file header.
func (*Mock) DescribeInboundIntegrations(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DescribeIntegrations returns a synthesized empty result. See the file header.
func (*Mock) DescribeIntegrations(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// DisassociateGlossaryTerms returns a synthesized empty result. See the file header.
func (*Mock) DisassociateGlossaryTerms(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetAsset returns a synthesized empty result. See the file header.
func (*Mock) GetAsset(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetAssetType returns a synthesized empty result. See the file header.
func (*Mock) GetAssetType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetCatalogImportStatus returns a synthesized empty result. See the file header.
func (*Mock) GetCatalogImportStatus(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetColumnStatisticsForPartition returns a synthesized empty result. See the file header.
func (*Mock) GetColumnStatisticsForPartition(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetColumnStatisticsForTable returns a synthesized empty result. See the file header.
func (*Mock) GetColumnStatisticsForTable(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetColumnStatisticsTaskRun returns a synthesized empty result. See the file header.
func (*Mock) GetColumnStatisticsTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetColumnStatisticsTaskRuns returns a synthesized empty result. See the file header.
func (*Mock) GetColumnStatisticsTaskRuns(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetColumnStatisticsTaskSettings returns a synthesized empty result. See the file header.
func (*Mock) GetColumnStatisticsTaskSettings(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetCrawlerMetrics returns a synthesized empty result. See the file header.
func (*Mock) GetCrawlerMetrics(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetCustomEntityType returns a synthesized empty result. See the file header.
func (*Mock) GetCustomEntityType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetDashboardUrl returns a synthesized empty result. See the file header.
//
//nolint:staticcheck,revive,stylecheck // method name mirrors the SDK operation GetDashboardUrl verbatim
func (*Mock) GetDashboardUrl(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetDataCatalogExportConfiguration returns a synthesized empty result. See the file header.
func (*Mock) GetDataCatalogExportConfiguration(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetDataflowGraph returns a synthesized empty result. See the file header.
func (*Mock) GetDataflowGraph(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetDataQualityModel returns a synthesized empty result. See the file header.
func (*Mock) GetDataQualityModel(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetDataQualityModelResult returns a synthesized empty result. See the file header.
func (*Mock) GetDataQualityModelResult(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetDataQualityResult returns a synthesized empty result. See the file header.
func (*Mock) GetDataQualityResult(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetDataQualityRuleRecommendationRun returns a synthesized empty result. See the file header.
func (*Mock) GetDataQualityRuleRecommendationRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetDataQualityRuleset returns a synthesized empty result. See the file header.
func (*Mock) GetDataQualityRuleset(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetDataQualityRulesetEvaluationRun returns a synthesized empty result. See the file header.
func (*Mock) GetDataQualityRulesetEvaluationRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetEntityRecords returns a synthesized empty result. See the file header.
func (*Mock) GetEntityRecords(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetFormType returns a synthesized empty result. See the file header.
func (*Mock) GetFormType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetGlossary returns a synthesized empty result. See the file header.
func (*Mock) GetGlossary(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetGlossaryTerm returns a synthesized empty result. See the file header.
func (*Mock) GetGlossaryTerm(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetGlueIdentityCenterConfiguration returns a synthesized empty result. See the file header.
func (*Mock) GetGlueIdentityCenterConfiguration(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetIntegrationResourceProperty returns a synthesized empty result. See the file header.
func (*Mock) GetIntegrationResourceProperty(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetIntegrationTableProperties returns a synthesized empty result. See the file header.
func (*Mock) GetIntegrationTableProperties(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetJobBookmark returns a synthesized empty result. See the file header.
func (*Mock) GetJobBookmark(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetMapping returns a synthesized empty result. See the file header.
func (*Mock) GetMapping(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetMaterializedViewRefreshTaskRun returns a synthesized empty result. See the file header.
func (*Mock) GetMaterializedViewRefreshTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetMLTaskRun returns a synthesized empty result. See the file header.
func (*Mock) GetMLTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetMLTaskRuns returns a synthesized empty result. See the file header.
func (*Mock) GetMLTaskRuns(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetMLTransform returns a synthesized empty result. See the file header.
func (*Mock) GetMLTransform(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetMLTransforms returns a synthesized empty result. See the file header.
func (*Mock) GetMLTransforms(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetPartitionIndexes validates the parent table then returns a synthesized
// empty result. See the file header.
func (m *Mock) GetPartitionIndexes(ctx context.Context, req map[string]any) (map[string]any, error) {
	if err := m.synthRequireTable(req); err != nil {
		return nil, err
	}

	return synthEmpty(ctx, req)
}

// GetPlan returns a synthesized empty result. See the file header.
func (*Mock) GetPlan(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetResourcePolicies returns a synthesized empty result. See the file header.
func (*Mock) GetResourcePolicies(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetSession returns a synthesized empty result. See the file header.
func (*Mock) GetSession(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetSessionEndpoint returns a synthesized empty result. See the file header.
func (*Mock) GetSessionEndpoint(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetStatement returns a synthesized empty result. See the file header.
func (*Mock) GetStatement(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetTableOptimizer returns a synthesized empty result. See the file header.
func (*Mock) GetTableOptimizer(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetUnfilteredPartitionMetadata returns a synthesized empty result. See the file header.
func (*Mock) GetUnfilteredPartitionMetadata(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetUnfilteredPartitionsMetadata returns a synthesized empty result. See the file header.
func (*Mock) GetUnfilteredPartitionsMetadata(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetUnfilteredTableMetadata returns a synthesized empty result. See the file header.
func (*Mock) GetUnfilteredTableMetadata(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// GetUsageProfile returns a synthesized empty result. See the file header.
func (*Mock) GetUsageProfile(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ImportCatalogToGlue returns a synthesized empty result. See the file header.
func (*Mock) ImportCatalogToGlue(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListAssetTypes returns a synthesized empty result. See the file header.
func (*Mock) ListAssetTypes(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListColumnStatisticsTaskRuns returns a synthesized empty result. See the file header.
func (*Mock) ListColumnStatisticsTaskRuns(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListConnectionTypes returns a synthesized empty result. See the file header.
func (*Mock) ListConnectionTypes(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListCrawls returns a synthesized empty result. See the file header.
func (*Mock) ListCrawls(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListCustomEntityTypes returns a synthesized empty result. See the file header.
func (*Mock) ListCustomEntityTypes(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListDataQualityResults returns a synthesized empty result. See the file header.
func (*Mock) ListDataQualityResults(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListDataQualityRuleRecommendationRuns returns a synthesized empty result. See the file header.
func (*Mock) ListDataQualityRuleRecommendationRuns(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListDataQualityRulesetEvaluationRuns returns a synthesized empty result. See the file header.
func (*Mock) ListDataQualityRulesetEvaluationRuns(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListDataQualityRulesets returns a synthesized empty result. See the file header.
func (*Mock) ListDataQualityRulesets(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListDataQualityStatisticAnnotations returns a synthesized empty result. See the file header.
func (*Mock) ListDataQualityStatisticAnnotations(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListDataQualityStatistics returns a synthesized empty result. See the file header.
func (*Mock) ListDataQualityStatistics(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListEntities returns a synthesized empty result. See the file header.
func (*Mock) ListEntities(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListFormTypes returns a synthesized empty result. See the file header.
func (*Mock) ListFormTypes(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListGlossaries returns a synthesized empty result. See the file header.
func (*Mock) ListGlossaries(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListGlossaryTerms returns a synthesized empty result. See the file header.
func (*Mock) ListGlossaryTerms(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListIntegrationResourceProperties returns a synthesized empty result. See the file header.
func (*Mock) ListIntegrationResourceProperties(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListIterableForms returns a synthesized empty result. See the file header.
func (*Mock) ListIterableForms(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListMaterializedViewRefreshTaskRuns returns a synthesized empty result. See the file header.
func (*Mock) ListMaterializedViewRefreshTaskRuns(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListMLTransforms returns a synthesized empty result. See the file header.
func (*Mock) ListMLTransforms(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListSessions returns a synthesized empty result. See the file header.
func (*Mock) ListSessions(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListStatements returns a synthesized empty result. See the file header.
func (*Mock) ListStatements(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListTableOptimizerRuns returns a synthesized empty result. See the file header.
func (*Mock) ListTableOptimizerRuns(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ListUsageProfiles returns a synthesized empty result. See the file header.
func (*Mock) ListUsageProfiles(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ModifyIntegration returns a synthesized empty result. See the file header.
func (*Mock) ModifyIntegration(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// PutAsset returns a synthesized empty result. See the file header.
func (*Mock) PutAsset(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// PutAssetType returns a synthesized empty result. See the file header.
func (*Mock) PutAssetType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// PutAttachment returns a synthesized empty result. See the file header.
func (*Mock) PutAttachment(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// PutDataCatalogExportConfiguration returns a synthesized empty result. See the file header.
func (*Mock) PutDataCatalogExportConfiguration(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// PutDataQualityProfileAnnotation returns a synthesized empty result. See the file header.
func (*Mock) PutDataQualityProfileAnnotation(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// PutFormType returns a synthesized empty result. See the file header.
func (*Mock) PutFormType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// PutSchemaVersionMetadata returns a synthesized empty result. See the file header.
func (*Mock) PutSchemaVersionMetadata(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// QuerySchemaVersionMetadata returns a synthesized empty result. See the file header.
func (*Mock) QuerySchemaVersionMetadata(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// RegisterConnectionType returns a synthesized empty result. See the file header.
func (*Mock) RegisterConnectionType(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// RemoveSchemaVersionMetadata returns a synthesized empty result. See the file header.
func (*Mock) RemoveSchemaVersionMetadata(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// ResetJobBookmark returns a synthesized empty result. See the file header.
func (*Mock) ResetJobBookmark(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// RunStatement returns a synthesized empty result. See the file header.
func (*Mock) RunStatement(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// SearchAssets returns a synthesized empty result. See the file header.
func (*Mock) SearchAssets(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StartColumnStatisticsTaskRun returns a synthesized empty result. See the file header.
func (*Mock) StartColumnStatisticsTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StartColumnStatisticsTaskRunSchedule returns a synthesized empty result. See the file header.
func (*Mock) StartColumnStatisticsTaskRunSchedule(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StartCrawlerSchedule returns a synthesized empty result. See the file header.
func (*Mock) StartCrawlerSchedule(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StartDataQualityRuleRecommendationRun returns a synthesized empty result. See the file header.
func (*Mock) StartDataQualityRuleRecommendationRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StartDataQualityRulesetEvaluationRun returns a synthesized empty result. See the file header.
func (*Mock) StartDataQualityRulesetEvaluationRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StartExportLabelsTaskRun returns a synthesized empty result. See the file header.
func (*Mock) StartExportLabelsTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StartImportLabelsTaskRun returns a synthesized empty result. See the file header.
func (*Mock) StartImportLabelsTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StartMaterializedViewRefreshTaskRun returns a synthesized empty result. See the file header.
func (*Mock) StartMaterializedViewRefreshTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StartMLEvaluationTaskRun returns a synthesized empty result. See the file header.
func (*Mock) StartMLEvaluationTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StartMLLabelingSetGenerationTaskRun returns a synthesized empty result. See the file header.
func (*Mock) StartMLLabelingSetGenerationTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StopColumnStatisticsTaskRun returns a synthesized empty result. See the file header.
func (*Mock) StopColumnStatisticsTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StopColumnStatisticsTaskRunSchedule returns a synthesized empty result. See the file header.
func (*Mock) StopColumnStatisticsTaskRunSchedule(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StopCrawlerSchedule returns a synthesized empty result. See the file header.
func (*Mock) StopCrawlerSchedule(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StopMaterializedViewRefreshTaskRun returns a synthesized empty result. See the file header.
func (*Mock) StopMaterializedViewRefreshTaskRun(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// StopSession returns a synthesized empty result. See the file header.
func (*Mock) StopSession(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateAsset returns a synthesized empty result. See the file header.
func (*Mock) UpdateAsset(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateColumnStatisticsForPartition returns a synthesized empty result. See the file header.
func (*Mock) UpdateColumnStatisticsForPartition(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateColumnStatisticsForTable returns a synthesized empty result. See the file header.
func (*Mock) UpdateColumnStatisticsForTable(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateColumnStatisticsTaskSettings returns a synthesized empty result. See the file header.
func (*Mock) UpdateColumnStatisticsTaskSettings(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateCrawlerSchedule returns a synthesized empty result. See the file header.
func (*Mock) UpdateCrawlerSchedule(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateDataQualityRuleset returns a synthesized empty result. See the file header.
func (*Mock) UpdateDataQualityRuleset(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateGlossary returns a synthesized empty result. See the file header.
func (*Mock) UpdateGlossary(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateGlossaryTerm returns a synthesized empty result. See the file header.
func (*Mock) UpdateGlossaryTerm(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateGlueIdentityCenterConfiguration returns a synthesized empty result. See the file header.
func (*Mock) UpdateGlueIdentityCenterConfiguration(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateIntegrationResourceProperty returns a synthesized empty result. See the file header.
func (*Mock) UpdateIntegrationResourceProperty(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateIntegrationTableProperties returns a synthesized empty result. See the file header.
func (*Mock) UpdateIntegrationTableProperties(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateJobFromSourceControl returns a synthesized empty result. See the file header.
func (*Mock) UpdateJobFromSourceControl(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateMLTransform returns a synthesized empty result. See the file header.
func (*Mock) UpdateMLTransform(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateSourceControlFromJob returns a synthesized empty result. See the file header.
func (*Mock) UpdateSourceControlFromJob(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateTableOptimizer returns a synthesized empty result. See the file header.
func (*Mock) UpdateTableOptimizer(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}

// UpdateUsageProfile returns a synthesized empty result. See the file header.
func (*Mock) UpdateUsageProfile(ctx context.Context, req map[string]any) (map[string]any, error) {
	return synthEmpty(ctx, req)
}
