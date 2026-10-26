package networkfirewall

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const reportCompleted = "COMPLETED"

// StartAnalysisReport requests a traffic analysis report. The analysis type has
// to be enabled on the firewall; with no data plane the report has no results.
func (m *Mock) StartAnalysisReport(_ context.Context, firewallName, firewallARN, analysisType string) (*nfdriver.AnalysisReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fw, err := m.reportFirewall(firewallName, firewallARN)
	if err != nil {
		return nil, err
	}

	if !oneOf(analysisType, analysisTypes...) {
		return nil, invalid("AnalysisType %q is not valid", analysisType)
	}

	if !oneOf(analysisType, fw.EnabledAnalysisTypes...) {
		return nil, invalid("analysis type %s is not enabled on firewall %q", analysisType, fw.Name)
	}

	rep := &nfdriver.AnalysisReport{
		ID: idgen.UUID(), FirewallARN: fw.ARN, AnalysisType: analysisType,
		Status: reportCompleted, ReportTime: m.now(),
	}
	m.analysisReports.Set(rep.ID, rep)

	out := *rep

	return &out, nil
}

// ListAnalysisReports lists the analysis reports of a firewall, oldest first.
func (m *Mock) ListAnalysisReports(
	_ context.Context, firewallName, firewallARN string, opts nfdriver.ListOptions,
) ([]nfdriver.AnalysisReport, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	fw, err := m.reportFirewall(firewallName, firewallARN)
	if err != nil {
		return nil, "", err
	}

	var matched []*nfdriver.AnalysisReport

	for _, r := range m.analysisReports.SortedValues() {
		if r.FirewallARN == fw.ARN {
			matched = append(matched, r)
		}
	}

	sort.SliceStable(matched, func(i, j int) bool { return matched[i].ReportTime.Before(matched[j].ReportTime) })

	items, next, err := paginate(matched, opts)
	if err != nil {
		return nil, "", err
	}

	out := make([]nfdriver.AnalysisReport, 0, len(items))
	for _, r := range items {
		out = append(out, *r)
	}

	return out, next, nil
}

// GetAnalysisReport returns one analysis report (it never has results).
func (m *Mock) GetAnalysisReport(_ context.Context, firewallName, firewallARN, reportID string) (*nfdriver.AnalysisReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	fw, err := m.reportFirewall(firewallName, firewallARN)
	if err != nil {
		return nil, err
	}

	if reportID == "" {
		return nil, invalid("AnalysisReportId is required")
	}

	rep, ok := m.analysisReports.Get(reportID)
	if !ok || rep.FirewallARN != fw.ARN {
		return nil, cerrors.Newf(cerrors.NotFound, "analysis report %q not found", reportID)
	}

	out := *rep

	return &out, nil
}

func (m *Mock) reportFirewall(name, arn string) (*nfdriver.Firewall, error) {
	if err := requireIdentifier("Firewall", name, arn); err != nil {
		return nil, err
	}

	fw, ok := m.lookupFirewall(name, arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall %q not found", nonEmpty(name, arn))
	}

	return fw, nil
}
