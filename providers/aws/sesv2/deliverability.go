package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// Deliverability data (inbox placement, ISP campaigns, blacklist status) cannot
// be observed by an emulator with no real mail flow. These operations manage
// dashboard opt-in state and synthesize plausible, self-consistent reports so a
// caller's request/response wiring works end-to-end; the figures are not real.

// PutDeliverabilityDashboardOption toggles the deliverability dashboard.
func (m *Mock) PutDeliverabilityDashboardOption(_ context.Context, enabled bool) error {
	m.dashMu.Lock()
	defer m.dashMu.Unlock()

	m.dashboardEnabled = enabled

	return nil
}

// GetDeliverabilityDashboardOptions reports the dashboard opt-in state.
func (m *Mock) GetDeliverabilityDashboardOptions(_ context.Context) (bool, error) {
	m.dashMu.RLock()
	defer m.dashMu.RUnlock()

	return m.dashboardEnabled, nil
}

// CreateDeliverabilityTestReport creates a synthesized inbox-placement report.
func (m *Mock) CreateDeliverabilityTestReport(
	_ context.Context, in driver.DeliverabilityTestReportInput,
) (*driver.DeliverabilityTestReport, error) {
	if in.FromEmailAddress == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "FromEmailAddress is required")
	}

	report := &driver.DeliverabilityTestReport{
		ReportID:             idgen.GenerateID(""),
		ReportName:           in.ReportName,
		Subject:              in.Subject,
		FromEmailAddress:     in.FromEmailAddress,
		CreatedAt:            m.now(),
		DeliverabilityStatus: driver.DeliverabilityStatusCompleted,
	}

	m.testReports.Set(report.ReportID, report)

	out := *report

	return &out, nil
}

// GetDeliverabilityTestReport returns a test report by ID.
func (m *Mock) GetDeliverabilityTestReport(_ context.Context, reportID string) (*driver.DeliverabilityTestReport, error) {
	r, ok := m.testReports.Get(reportID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "deliverability test report %q does not exist", reportID)
	}

	out := *r

	return &out, nil
}

// ListDeliverabilityTestReports returns all test reports.
func (m *Mock) ListDeliverabilityTestReports(_ context.Context) ([]driver.DeliverabilityTestReport, error) {
	all := m.testReports.SortedValues()
	out := make([]driver.DeliverabilityTestReport, 0, len(all))

	for _, r := range all {
		out = append(out, *r)
	}

	return out, nil
}

// GetDomainDeliverabilityCampaign returns a synthesized campaign JSON blob.
func (*Mock) GetDomainDeliverabilityCampaign(_ context.Context, campaignID string) (string, error) {
	return `{"CampaignId":"` + campaignID + `","Subject":"(synthesized)","SendingIps":[]}`, nil
}

// ListDomainDeliverabilityCampaigns returns synthesized campaign IDs (none).
func (*Mock) ListDomainDeliverabilityCampaigns(_ context.Context, _ string) ([]string, error) {
	return []string{}, nil
}

// GetDomainStatisticsReport returns a synthesized statistics JSON blob.
func (*Mock) GetDomainStatisticsReport(_ context.Context, domain string) (string, error) {
	return `{"Domain":"` + domain + `","OverallVolume":{},"DailyVolumes":[]}`, nil
}

// GetBlacklistReports returns an empty blacklist entry per requested IP.
func (*Mock) GetBlacklistReports(_ context.Context, ips []string) (map[string][]string, error) {
	out := make(map[string][]string, len(ips))
	for _, ip := range ips {
		out[ip] = []string{}
	}

	return out, nil
}
