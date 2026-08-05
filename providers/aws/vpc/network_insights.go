package vpc

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// ---- Reachability Analyzer: paths ----

// CreateNetworkInsightsPath creates a reachability path definition.
//
//nolint:gocritic // cfg is passed by value to satisfy the driver interface.
func (m *Mock) CreateNetworkInsightsPath(
	_ context.Context, cfg driver.NetworkInsightsPathConfig,
) (*driver.NetworkInsightsPath, error) {
	if cfg.Source == "" {
		return nil, errors.Newf(errors.InvalidArgument, "source is required")
	}

	if cfg.Destination == "" {
		return nil, errors.Newf(errors.InvalidArgument, "destination is required")
	}

	id := idgen.GenerateID("nip-")
	p := &driver.NetworkInsightsPath{
		ID:              id,
		ARN:             m.insightsARN("network-insights-path", id),
		Protocol:        orDefaultStr(cfg.Protocol, "tcp"),
		Source:          cfg.Source,
		SourceIP:        cfg.SourceIP,
		Destination:     cfg.Destination,
		DestinationIP:   cfg.DestinationIP,
		DestinationPort: cfg.DestinationPort,
		CreatedDate:     m.opts.Clock.Now().UTC(),
		Tags:            copyTags(cfg.Tags),
	}
	m.networkInsightsPaths.Set(id, p)

	out := cloneInsightsPath(p)

	return &out, nil
}

// DeleteNetworkInsightsPath deletes a path and its analyses.
func (m *Mock) DeleteNetworkInsightsPath(_ context.Context, id string) error {
	if !m.networkInsightsPaths.Delete(id) {
		return errors.Newf(errors.NotFound, "network insights path %q not found", id)
	}

	for aID, a := range m.networkInsightsAnalyses.All() {
		if a.PathID == id {
			m.networkInsightsAnalyses.Delete(aID)
		}
	}

	return nil
}

// DescribeNetworkInsightsPaths returns paths matching ids.
func (m *Mock) DescribeNetworkInsightsPaths(_ context.Context, ids []string) ([]driver.NetworkInsightsPath, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.networkInsightsPaths, ids, cloneInsightsPath), nil
}

// ---- Reachability Analyzer: analyses ----

// StartNetworkInsightsAnalysis runs reachability analysis on a path. The mock
// completes synchronously with a reachable result.
//
//nolint:gocritic // cfg is passed by value to satisfy the driver interface.
func (m *Mock) StartNetworkInsightsAnalysis(
	_ context.Context, cfg driver.NetworkInsightsAnalysisConfig,
) (*driver.NetworkInsightsAnalysis, error) {
	if !m.networkInsightsPaths.Has(cfg.PathID) {
		return nil, errors.Newf(errors.NotFound, "network insights path %q not found", cfg.PathID)
	}

	id := idgen.GenerateID("nia-")
	a := &driver.NetworkInsightsAnalysis{
		ID:                 id,
		ARN:                m.insightsARN("network-insights-analysis", id),
		PathID:             cfg.PathID,
		StartDate:          m.opts.Clock.Now().UTC(),
		Status:             "succeeded",
		NetworkPathFound:   true,
		FilterInARNs:       append([]string(nil), cfg.FilterInARNs...),
		FilterOutARNs:      append([]string(nil), cfg.FilterOutARNs...),
		AdditionalAccounts: append([]string(nil), cfg.AdditionalAccounts...),
		Tags:               copyTags(cfg.Tags),
	}
	m.networkInsightsAnalyses.Set(id, a)

	out := cloneInsightsAnalysis(a)

	return &out, nil
}

// DeleteNetworkInsightsAnalysis deletes an analysis.
func (m *Mock) DeleteNetworkInsightsAnalysis(_ context.Context, id string) error {
	if !m.networkInsightsAnalyses.Delete(id) {
		return errors.Newf(errors.NotFound, "network insights analysis %q not found", id)
	}

	return nil
}

// DescribeNetworkInsightsAnalyses returns analyses matching ids, optionally
// scoped to a path.
func (m *Mock) DescribeNetworkInsightsAnalyses(
	_ context.Context, ids []string, pathID string,
) ([]driver.NetworkInsightsAnalysis, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := describeResources(m.networkInsightsAnalyses, ids, cloneInsightsAnalysis)
	if pathID == "" {
		return all, nil
	}

	out := make([]driver.NetworkInsightsAnalysis, 0, len(all))

	for i := range all {
		if all[i].PathID == pathID {
			out = append(out, all[i])
		}
	}

	return out, nil
}

// ---- Network Access Analyzer: access scopes ----

// CreateNetworkInsightsAccessScope creates an access-scope definition.
func (m *Mock) CreateNetworkInsightsAccessScope(
	_ context.Context, cfg driver.NetworkInsightsAccessScopeConfig,
) (*driver.NetworkInsightsAccessScope, error) {
	now := m.opts.Clock.Now().UTC()
	id := idgen.GenerateID("nis-")
	s := &driver.NetworkInsightsAccessScope{
		ID:           id,
		ARN:          m.insightsARN("network-insights-access-scope", id),
		MatchPaths:   cloneAccessScopePaths(cfg.MatchPaths),
		ExcludePaths: cloneAccessScopePaths(cfg.ExcludePaths),
		CreatedDate:  now,
		UpdatedDate:  now,
		Tags:         copyTags(cfg.Tags),
	}
	m.networkInsightsAccessScopes.Set(id, s)

	out := cloneAccessScope(s)

	return &out, nil
}

// DeleteNetworkInsightsAccessScope deletes a scope and its analyses.
func (m *Mock) DeleteNetworkInsightsAccessScope(_ context.Context, id string) error {
	if !m.networkInsightsAccessScopes.Delete(id) {
		return errors.Newf(errors.NotFound, "network insights access scope %q not found", id)
	}

	for aID, a := range m.networkInsightsAccessScopeAnalyses.All() {
		if a.AccessScopeID == id {
			m.networkInsightsAccessScopeAnalyses.Delete(aID)
		}
	}

	return nil
}

// DescribeNetworkInsightsAccessScopes returns scopes matching ids.
func (m *Mock) DescribeNetworkInsightsAccessScopes(
	_ context.Context, ids []string,
) ([]driver.NetworkInsightsAccessScope, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.networkInsightsAccessScopes, ids, cloneAccessScope), nil
}

// GetNetworkInsightsAccessScopeContent returns the scope with its match/exclude
// paths.
func (m *Mock) GetNetworkInsightsAccessScopeContent(
	_ context.Context, id string,
) (*driver.NetworkInsightsAccessScope, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.networkInsightsAccessScopes.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "network insights access scope %q not found", id)
	}

	out := cloneAccessScope(s)

	return &out, nil
}

// ---- Network Access Analyzer: scope analyses ----

// StartNetworkInsightsAccessScopeAnalysis runs analysis on an access scope. The
// mock completes synchronously with no findings.
func (m *Mock) StartNetworkInsightsAccessScopeAnalysis(
	_ context.Context, accessScopeID string, tags map[string]string,
) (*driver.NetworkInsightsAccessScopeAnalysis, error) {
	if !m.networkInsightsAccessScopes.Has(accessScopeID) {
		return nil, errors.Newf(errors.NotFound,
			"network insights access scope %q not found", accessScopeID)
	}

	now := m.opts.Clock.Now().UTC()
	id := idgen.GenerateID("nisa-")
	a := &driver.NetworkInsightsAccessScopeAnalysis{
		ID:               id,
		ARN:              m.insightsARN("network-insights-access-scope-analysis", id),
		AccessScopeID:    accessScopeID,
		Status:           "succeeded",
		StartDate:        now,
		EndDate:          now,
		FindingsFound:    "false",
		AnalyzedEniCount: 0,
		Tags:             copyTags(tags),
	}
	m.networkInsightsAccessScopeAnalyses.Set(id, a)

	out := cloneAccessScopeAnalysis(a)

	return &out, nil
}

// DeleteNetworkInsightsAccessScopeAnalysis deletes a scope analysis.
func (m *Mock) DeleteNetworkInsightsAccessScopeAnalysis(_ context.Context, id string) error {
	if !m.networkInsightsAccessScopeAnalyses.Delete(id) {
		return errors.Newf(errors.NotFound, "network insights access scope analysis %q not found", id)
	}

	return nil
}

// DescribeNetworkInsightsAccessScopeAnalyses returns scope analyses matching
// ids, optionally scoped to an access scope.
func (m *Mock) DescribeNetworkInsightsAccessScopeAnalyses(
	_ context.Context, ids []string, accessScopeID string,
) ([]driver.NetworkInsightsAccessScopeAnalysis, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := describeResources(m.networkInsightsAccessScopeAnalyses, ids, cloneAccessScopeAnalysis)
	if accessScopeID == "" {
		return all, nil
	}

	out := make([]driver.NetworkInsightsAccessScopeAnalysis, 0, len(all))

	for i := range all {
		if all[i].AccessScopeID == accessScopeID {
			out = append(out, all[i])
		}
	}

	return out, nil
}

// GetNetworkInsightsAccessScopeAnalysisFindings returns findings for an analysis
// and its status. The mock produces no findings.
func (m *Mock) GetNetworkInsightsAccessScopeAnalysisFindings(
	_ context.Context, analysisID string,
) ([]driver.AccessScopeAnalysisFinding, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.networkInsightsAccessScopeAnalyses.Get(analysisID)
	if !ok {
		return nil, "", errors.Newf(errors.NotFound,
			"network insights access scope analysis %q not found", analysisID)
	}

	return []driver.AccessScopeAnalysisFinding{}, a.Status, nil
}

// ---- helpers ----

func (m *Mock) insightsARN(resource, id string) string {
	return "arn:aws:ec2:" + m.opts.Region + ":" + m.opts.AccountID + ":" + resource + "/" + id
}

func cloneInsightsPath(p *driver.NetworkInsightsPath) driver.NetworkInsightsPath {
	out := *p
	out.Tags = copyTags(p.Tags)

	return out
}

func cloneInsightsAnalysis(a *driver.NetworkInsightsAnalysis) driver.NetworkInsightsAnalysis {
	out := *a
	out.Tags = copyTags(a.Tags)
	out.FilterInARNs = append([]string(nil), a.FilterInARNs...)
	out.FilterOutARNs = append([]string(nil), a.FilterOutARNs...)
	out.AdditionalAccounts = append([]string(nil), a.AdditionalAccounts...)

	return out
}

func cloneAccessScope(s *driver.NetworkInsightsAccessScope) driver.NetworkInsightsAccessScope {
	out := *s
	out.Tags = copyTags(s.Tags)
	out.MatchPaths = cloneAccessScopePaths(s.MatchPaths)
	out.ExcludePaths = cloneAccessScopePaths(s.ExcludePaths)

	return out
}

func cloneAccessScopeAnalysis(a *driver.NetworkInsightsAccessScopeAnalysis) driver.NetworkInsightsAccessScopeAnalysis {
	out := *a
	out.Tags = copyTags(a.Tags)

	return out
}

func cloneAccessScopePaths(paths []driver.AccessScopePath) []driver.AccessScopePath {
	if len(paths) == 0 {
		return nil
	}

	out := make([]driver.AccessScopePath, 0, len(paths))
	for i := range paths {
		out = append(out, driver.AccessScopePath{
			Source:      cloneAccessScopeStatement(paths[i].Source),
			Destination: cloneAccessScopeStatement(paths[i].Destination),
		})
	}

	return out
}

func cloneAccessScopeStatement(s *driver.AccessScopeStatement) *driver.AccessScopeStatement {
	if s == nil || s.ResourceStatement == nil {
		return nil
	}

	return &driver.AccessScopeStatement{
		ResourceStatement: &driver.AccessScopeResourceStatement{
			ResourceTypes: append([]string(nil), s.ResourceStatement.ResourceTypes...),
			Resources:     append([]string(nil), s.ResourceStatement.Resources...),
		},
	}
}
