package monitor_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// respStatus unwraps err as an *azcore.ResponseError and returns its HTTP
// status code, failing the test if err isn't one (mirrors the azurelb
// sdk_roundtrip/lb_subresource convention for asserting SDK error responses).
func respStatus(t *testing.T, err error) int {
	t.Helper()

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("error %v is not an *azcore.ResponseError", err)
	}

	return respErr.StatusCode
}

// TestMetricAlertRejectsUnknownActionGroup covers the referential-integrity
// gap: a metric alert whose actions[].actionGroupId does not resolve to a
// stored action group must be rejected (400), not silently stored to fail
// resolving only later, at breach time.
func TestMetricAlertRejectsUnknownActionGroup(t *testing.T) {
	client := newInsightsServer(t).metricAlerts(t)
	ctx := context.Background()

	const missingAgID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/microsoft.insights/actionGroups/does-not-exist"

	rule := metricAlertResource(80)
	rule.Properties.Actions = []*armmonitor.MetricAlertAction{{ActionGroupID: to.Ptr(missingAgID)}}

	_, err := client.CreateOrUpdate(ctx, "rg-1", "cpu-alert", rule, nil)
	if err == nil {
		t.Fatal("CreateOrUpdate with unknown actionGroupId succeeded, want an error")
	}

	if status := respStatus(t, err); status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}

	// The rejected alert must not be persisted.
	if _, getErr := client.Get(ctx, "rg-1", "cpu-alert", nil); getErr == nil {
		t.Fatal("Get succeeded for a metric alert that failed validation, want NotFound")
	}
}

// TestMetricAlertPatchRejectsUnknownActionGroup covers the same referential
// check on PATCH (Update): re-pointing an already-valid alert at a
// nonexistent action group must be rejected too, not only on first create.
func TestMetricAlertPatchRejectsUnknownActionGroup(t *testing.T) {
	client := newInsightsServer(t).metricAlerts(t)
	ctx := context.Background()

	if _, err := client.CreateOrUpdate(ctx, "rg-1", "cpu-alert", metricAlertResource(80), nil); err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	const missingAgID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/microsoft.insights/actionGroups/does-not-exist"

	_, err := client.Update(ctx, "rg-1", "cpu-alert", armmonitor.MetricAlertResourcePatch{
		Properties: &armmonitor.MetricAlertPropertiesPatch{
			Actions: []*armmonitor.MetricAlertAction{{ActionGroupID: to.Ptr(missingAgID)}},
		},
	}, nil)
	if err == nil {
		t.Fatal("Update with unknown actionGroupId succeeded, want an error")
	}

	if status := respStatus(t, err); status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

// TestMetricAlertAcceptsCaseInsensitiveActionGroupRef is the regression for the
// case-sensitivity false-rejection: real ARM resource ids are case-insensitive,
// and providers/azure/monitor's RegisterActionGroup/fireActionGroups already
// resolve an actionGroupId case-insensitively at breach time (actiongroups.go).
// A metricAlert PUT referencing an existing action group with different casing
// in the resource-group and action-group-name segments must succeed, not be
// rejected as "not found".
func TestMetricAlertAcceptsCaseInsensitiveActionGroupRef(t *testing.T) {
	srv := newInsightsServer(t)
	ctx := context.Background()

	agClient := srv.actionGroups(t)

	if _, err := agClient.CreateOrUpdate(ctx, "rg-1", "ag1", armmonitor.ActionGroupResource{
		Location:   to.Ptr("global"),
		Properties: &armmonitor.ActionGroup{GroupShortName: to.Ptr("ag1"), Enabled: to.Ptr(true)},
	}, nil); err != nil {
		t.Fatalf("action group CreateOrUpdate: %v", err)
	}

	// Same action group, different casing in the resourceGroups and
	// actionGroups name segments of the ARM id.
	const differentCaseAgID = "/subscriptions/sub-1/resourceGroups/RG-1/providers/microsoft.insights/actionGroups/AG1"

	rule := metricAlertResource(80)
	rule.Properties.Actions = []*armmonitor.MetricAlertAction{{ActionGroupID: to.Ptr(differentCaseAgID)}}

	alertClient := srv.metricAlerts(t)

	if _, err := alertClient.CreateOrUpdate(ctx, "rg-1", "cpu-alert", rule, nil); err != nil {
		t.Fatalf("CreateOrUpdate with differently-cased actionGroupId = %v, want success", err)
	}
}

// TestMetricAlertPatchAcceptsCaseInsensitiveActionGroupRef is the PATCH-path
// counterpart of TestMetricAlertAcceptsCaseInsensitiveActionGroupRef: adding a
// differently-cased actionGroupId reference via Update (PATCH) must also
// succeed.
func TestMetricAlertPatchAcceptsCaseInsensitiveActionGroupRef(t *testing.T) {
	srv := newInsightsServer(t)
	ctx := context.Background()

	agClient := srv.actionGroups(t)

	if _, err := agClient.CreateOrUpdate(ctx, "rg-1", "ag1", armmonitor.ActionGroupResource{
		Location:   to.Ptr("global"),
		Properties: &armmonitor.ActionGroup{GroupShortName: to.Ptr("ag1"), Enabled: to.Ptr(true)},
	}, nil); err != nil {
		t.Fatalf("action group CreateOrUpdate: %v", err)
	}

	alertClient := srv.metricAlerts(t)

	if _, err := alertClient.CreateOrUpdate(ctx, "rg-1", "cpu-alert", metricAlertResource(80), nil); err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	const differentCaseAgID = "/subscriptions/sub-1/resourceGroups/RG-1/providers/microsoft.insights/actionGroups/AG1"

	_, err := alertClient.Update(ctx, "rg-1", "cpu-alert", armmonitor.MetricAlertResourcePatch{
		Properties: &armmonitor.MetricAlertPropertiesPatch{
			Actions: []*armmonitor.MetricAlertAction{{ActionGroupID: to.Ptr(differentCaseAgID)}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Update with differently-cased actionGroupId = %v, want success", err)
	}
}

// TestActionGroupDeleteGuardedByMetricAlert covers the in-use delete guard: an
// action group referenced by a metric alert's actions[] cannot be deleted
// until the alert is removed or repointed, matching the azurelb backend-pool
// in-use guard convention (409, not a dangling reference).
func TestActionGroupDeleteGuardedByMetricAlert(t *testing.T) {
	srv := newInsightsServer(t)
	ctx := context.Background()

	agClient := srv.actionGroups(t)

	if _, err := agClient.CreateOrUpdate(ctx, "rg-1", "ag1", armmonitor.ActionGroupResource{
		Location:   to.Ptr("global"),
		Properties: &armmonitor.ActionGroup{GroupShortName: to.Ptr("ag1"), Enabled: to.Ptr(true)},
	}, nil); err != nil {
		t.Fatalf("action group CreateOrUpdate: %v", err)
	}

	const agID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/microsoft.insights/actionGroups/ag1"

	alertClient := srv.metricAlerts(t)

	rule := metricAlertResource(80)
	rule.Properties.Actions = []*armmonitor.MetricAlertAction{{ActionGroupID: to.Ptr(agID)}}

	if _, err := alertClient.CreateOrUpdate(ctx, "rg-1", "cpu-alert", rule, nil); err != nil {
		t.Fatalf("metric alert CreateOrUpdate: %v", err)
	}

	if _, err := agClient.Delete(ctx, "rg-1", "ag1", nil); err == nil {
		t.Fatal("Delete of an in-use action group succeeded, want an error")
	} else if status := respStatus(t, err); status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}

	if _, err := alertClient.Delete(ctx, "rg-1", "cpu-alert", nil); err != nil {
		t.Fatalf("metric alert Delete: %v", err)
	}

	if _, err := agClient.Delete(ctx, "rg-1", "ag1", nil); err != nil {
		t.Fatalf("action group Delete after releasing reference: %v", err)
	}
}

// TestActionGroupDeleteGuardedByActivityLogAlert covers the same in-use guard
// for the other reference shape: an activityLogAlert's nested
// properties.actions.actionGroups[].actionGroupId.
func TestActionGroupDeleteGuardedByActivityLogAlert(t *testing.T) {
	srv := newInsightsServer(t)
	ctx := context.Background()

	agClient := srv.actionGroups(t)

	if _, err := agClient.CreateOrUpdate(ctx, "rg-1", "ag1", armmonitor.ActionGroupResource{
		Location:   to.Ptr("global"),
		Properties: &armmonitor.ActionGroup{GroupShortName: to.Ptr("ag1"), Enabled: to.Ptr(true)},
	}, nil); err != nil {
		t.Fatalf("action group CreateOrUpdate: %v", err)
	}

	const agID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/microsoft.insights/actionGroups/ag1"

	alaClient := srv.activityLogAlerts(t)

	if _, err := alaClient.CreateOrUpdate(ctx, "rg-1", "ala1", armmonitor.ActivityLogAlertResource{
		Location: to.Ptr("global"),
		Properties: &armmonitor.AlertRuleProperties{
			Enabled: to.Ptr(true),
			Scopes:  []*string{to.Ptr("/subscriptions/sub-1")},
			Condition: &armmonitor.AlertRuleAllOfCondition{
				AllOf: []*armmonitor.AlertRuleAnyOfOrLeafCondition{{
					Field: to.Ptr("category"), Equals: to.Ptr("Administrative"),
				}},
			},
			Actions: &armmonitor.ActionList{
				ActionGroups: []*armmonitor.ActivityLogAlertActionGroup{{ActionGroupID: to.Ptr(agID)}},
			},
		},
	}, nil); err != nil {
		t.Fatalf("activity log alert CreateOrUpdate: %v", err)
	}

	if _, err := agClient.Delete(ctx, "rg-1", "ag1", nil); err == nil {
		t.Fatal("Delete of an in-use action group succeeded, want an error")
	} else if status := respStatus(t, err); status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}

	if _, err := alaClient.Delete(ctx, "rg-1", "ala1", nil); err != nil {
		t.Fatalf("activity log alert Delete: %v", err)
	}

	if _, err := agClient.Delete(ctx, "rg-1", "ag1", nil); err != nil {
		t.Fatalf("action group Delete after releasing reference: %v", err)
	}
}
