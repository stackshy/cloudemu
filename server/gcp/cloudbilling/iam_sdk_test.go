package cloudbilling_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	cloudbilling "google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/googleapi"
)

// TestSDKBillingAccountIamPolicy drives the billingAccounts IAM verbs the way
// google_billing_account_iam_member does: get (a GET in the real API), set with
// the read etag, then a stale etag is 409 ABORTED and an unknown account 404s.
func TestSDKBillingAccountIamPolicy(t *testing.T) {
	svc := newBillingService(t, newServer(t))
	ctx := context.Background()

	pol, err := svc.BillingAccounts.GetIamPolicy(seedAccount).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if pol.Etag == "" || len(pol.Bindings) != 0 {
		t.Fatalf("unset policy = %+v, want an etag and no bindings", pol)
	}

	set, err := svc.BillingAccounts.SetIamPolicy(seedAccount, &cloudbilling.SetIamPolicyRequest{
		Policy: &cloudbilling.Policy{
			Bindings: []*cloudbilling.Binding{{Role: "roles/billing.viewer", Members: []string{"user:a@example.com"}}},
			Etag:     pol.Etag,
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if set.Etag == pol.Etag {
		t.Fatal("etag did not change on set")
	}

	got, err := svc.BillingAccounts.GetIamPolicy(seedAccount).Context(ctx).Do()
	if err != nil || got.Etag != set.Etag || len(got.Bindings) != 1 {
		t.Fatalf("GetIamPolicy after set = %+v, %v", got, err)
	}

	_, err = svc.BillingAccounts.SetIamPolicy(seedAccount, &cloudbilling.SetIamPolicyRequest{
		Policy: &cloudbilling.Policy{Etag: pol.Etag},
	}).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusConflict {
		t.Fatalf("stale etag: want 409, got %v", err)
	}

	perms, err := svc.BillingAccounts.TestIamPermissions(seedAccount, &cloudbilling.TestIamPermissionsRequest{
		Permissions: []string{"billing.accounts.get"},
	}).Context(ctx).Do()
	if err != nil || len(perms.Permissions) != 1 {
		t.Fatalf("TestIamPermissions = %+v, %v", perms, err)
	}

	_, err = svc.BillingAccounts.GetIamPolicy("billingAccounts/000000-000000-000000").Context(ctx).Do()
	if !errors.As(err, &gerr) || gerr.Code != http.StatusNotFound {
		t.Fatalf("unknown account: want 404, got %v", err)
	}
}
