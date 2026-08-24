package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// TestSDKAzureIAMDenyAssignmentsListEmpty confirms that a subscription with no
// deny assignments returns a correctly enveloped, empty collection (not an
// error) — matching a real subscription that has never had Blueprints or
// Managed Applications create one.
func TestSDKAzureIAMDenyAssignmentsListEmpty(t *testing.T) {
	cf := newClientFactory(t)
	ctx := context.Background()

	pager := cf.NewDenyAssignmentsClient().NewListForScopePager(testScope, nil)

	var count int
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("DenyAssignments ListForScope: %v", err)
		}
		count += len(page.Value)
	}

	if count != 0 {
		t.Fatalf("expected 0 deny assignments, got %d", count)
	}
}

// TestSDKAzureIAMDenyAssignmentGetNotFound confirms Get on an unknown deny
// assignment id returns a typed 404 rather than a malformed body.
func TestSDKAzureIAMDenyAssignmentGetNotFound(t *testing.T) {
	cf := newClientFactory(t)
	ctx := context.Background()

	_, err := cf.NewDenyAssignmentsClient().Get(ctx, testScope, "missing-deny-id", nil)
	if err == nil {
		t.Fatalf("Get on missing deny assignment: expected error, got nil")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected *azcore.ResponseError, got %T: %v", err, err)
	}

	if respErr.StatusCode != 404 {
		t.Fatalf("got status %d, want 404", respErr.StatusCode)
	}
}
