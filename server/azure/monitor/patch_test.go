package monitor_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKMetricAlertPatchMergesAndPreserves is the load-bearing regression for
// the PATCH BLOCKER: the armmonitor MetricAlertsClient.Update issues an HTTP
// PATCH, which the handler used to reject with 405. It must return 200, apply
// the supplied fields, and preserve fields the patch omits.
func TestSDKMetricAlertPatchMergesAndPreserves(t *testing.T) {
	client := newInsightsServer(t).metricAlerts(t)
	ctx := context.Background()

	create := metricAlertResource(80)
	create.Tags = map[string]*string{"team": to.Ptr("payments"), "env": to.Ptr("prod")}
	create.Properties.Description = to.Ptr("cpu alert")

	if _, err := client.CreateOrUpdate(ctx, "rg-1", "cpu", create, nil); err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	// PATCH only severity and one tag; description, scopes, criteria and the
	// other tag must survive.
	patched, err := client.Update(ctx, "rg-1", "cpu", armmonitor.MetricAlertResourcePatch{
		Tags:       map[string]*string{"env": to.Ptr("staging")},
		Properties: &armmonitor.MetricAlertPropertiesPatch{Severity: to.Ptr[int32](1)},
	}, nil)
	if err != nil {
		t.Fatalf("Update (PATCH): %v", err)
	}

	if patched.Properties == nil || patched.Properties.Severity == nil || *patched.Properties.Severity != 1 {
		t.Fatalf("severity after patch = %v, want 1", patched.Properties.Severity)
	}

	if patched.Properties.Description == nil || *patched.Properties.Description != "cpu alert" {
		t.Fatalf("description = %v, want preserved 'cpu alert'", patched.Properties.Description)
	}

	if patched.Properties.Criteria == nil {
		t.Fatal("criteria dropped by patch (omitted field not preserved)")
	}

	if got := deref(patched.Tags["team"]); got != "payments" {
		t.Fatalf("tag team = %q, want preserved 'payments'", got)
	}

	if got := deref(patched.Tags["env"]); got != "staging" {
		t.Fatalf("tag env = %q, want updated 'staging'", got)
	}
}

// TestSDKActionGroupPatchReturns200 covers the PATCH BLOCKER for actionGroups:
// ActionGroupsClient.Update (PATCH) toggling enabled must return 200 and
// preserve the receivers the patch omits.
func TestSDKActionGroupPatchReturns200(t *testing.T) {
	client := newInsightsServer(t).actionGroups(t)
	ctx := context.Background()

	if _, err := client.CreateOrUpdate(ctx, "rg-1", "ag", armmonitor.ActionGroupResource{
		Location: to.Ptr("global"),
		Properties: &armmonitor.ActionGroup{
			GroupShortName: to.Ptr("ops"),
			Enabled:        to.Ptr(true),
			EmailReceivers: []*armmonitor.EmailReceiver{{Name: to.Ptr("oncall"), EmailAddress: to.Ptr("a@b.com")}},
		},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	patched, err := client.Update(ctx, "rg-1", "ag", armmonitor.ActionGroupPatchBody{
		Tags:       map[string]*string{"env": to.Ptr("prod")},
		Properties: &armmonitor.ActionGroupPatch{Enabled: to.Ptr(false)},
	}, nil)
	if err != nil {
		t.Fatalf("Update (PATCH): %v", err)
	}

	if patched.Properties == nil || patched.Properties.GroupShortName == nil || *patched.Properties.GroupShortName != "ops" {
		t.Fatalf("groupShortName = %v, want preserved 'ops'", patched.Properties)
	}

	if len(patched.Properties.EmailReceivers) != 1 {
		t.Fatalf("emailReceivers = %d, want 1 (omitted receivers dropped)", len(patched.Properties.EmailReceivers))
	}
}

// TestSDKActivityLogAlertPatchReturns200 covers the PATCH BLOCKER for
// activityLogAlerts: ActivityLogAlertsClient.Update (PATCH) must return 200 and
// preserve the condition/scopes the patch omits.
func TestSDKActivityLogAlertPatchReturns200(t *testing.T) {
	client := newInsightsServer(t).activityLogAlerts(t)
	ctx := context.Background()

	if _, err := client.CreateOrUpdate(ctx, "rg-1", "ala", armmonitor.ActivityLogAlertResource{
		Location: to.Ptr("global"),
		Properties: &armmonitor.AlertRuleProperties{
			Enabled: to.Ptr(true),
			Scopes:  []*string{to.Ptr("/subscriptions/sub-1")},
			Condition: &armmonitor.AlertRuleAllOfCondition{
				AllOf: []*armmonitor.AlertRuleAnyOfOrLeafCondition{{
					Field: to.Ptr("category"), Equals: to.Ptr("Administrative"),
				}},
			},
		},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	patched, err := client.Update(ctx, "rg-1", "ala", armmonitor.AlertRulePatchObject{
		Tags:       map[string]*string{"env": to.Ptr("prod")},
		Properties: &armmonitor.AlertRulePatchProperties{Enabled: to.Ptr(false)},
	}, nil)
	if err != nil {
		t.Fatalf("Update (PATCH): %v", err)
	}

	if patched.Properties == nil || patched.Properties.Condition == nil {
		t.Fatal("condition dropped by patch (omitted field not preserved)")
	}

	if len(patched.Properties.Scopes) != 1 {
		t.Fatalf("scopes = %d, want 1 (omitted field not preserved)", len(patched.Properties.Scopes))
	}
}

// insightsServer bundles a running Azure server for the insights ARM clients.
type insightsServer struct{ ts *httptest.Server }

func newInsightsServer(t *testing.T) *insightsServer {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Monitor: cloudP.Monitor}))
	t.Cleanup(ts.Close)

	return &insightsServer{ts: ts}
}

func (s *insightsServer) metricAlerts(t *testing.T) *armmonitor.MetricAlertsClient {
	t.Helper()

	c, err := armmonitor.NewMetricAlertsClient("sub-1", fakeCred{}, armClientOptions(s.ts))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func (s *insightsServer) actionGroups(t *testing.T) *armmonitor.ActionGroupsClient {
	t.Helper()

	c, err := armmonitor.NewActionGroupsClient("sub-1", fakeCred{}, armClientOptions(s.ts))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func (s *insightsServer) activityLogAlerts(t *testing.T) *armmonitor.ActivityLogAlertsClient {
	t.Helper()

	c, err := armmonitor.NewActivityLogAlertsClient("sub-1", fakeCred{}, armClientOptions(s.ts))
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func deref(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}
