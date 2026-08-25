package tablestorage_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKTableSelectProjection drives Query Entities' $select through the
// real aztables client, confirming only the requested properties (plus the
// always-present system properties) come back.
func TestSDKTableSelectProjection(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{TableStorage: cloudP.TableStorage})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	svc, err := aztables.NewServiceClientWithNoCredential(ts.URL+"/", &aztables.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatalf("NewServiceClientWithNoCredential: %v", err)
	}

	const tableName = "selectpeople"

	if _, err := svc.CreateTable(ctx, tableName, nil); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	client := svc.NewClient(tableName)

	entity := map[string]any{
		"PartitionKey": "org",
		"RowKey":       "bob",
		"Email":        "bob@example.com",
		"Age":          int64(41),
	}

	marshalled, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("marshal entity: %v", err)
	}

	if _, err := client.AddEntity(ctx, marshalled, nil); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	filter := "PartitionKey eq 'org'"
	sel := "Email"

	pager := client.NewListEntitiesPager(&aztables.ListEntitiesOptions{Filter: &filter, Select: &sel})

	var props map[string]any

	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListEntities: %v", perr)
		}

		for _, raw := range page.Entities {
			if uerr := json.Unmarshal(raw, &props); uerr != nil {
				t.Fatalf("unmarshal query entity: %v", uerr)
			}
		}
	}

	if props == nil {
		t.Fatal("no entity returned")
	}

	if props["Email"] != "bob@example.com" {
		t.Errorf("Email = %v, want bob@example.com (selected property must survive projection)", props["Email"])
	}

	if _, ok := props["Age"]; ok {
		t.Errorf("Age present in projected entity %v, want it dropped ($select=Email only)", props)
	}

	if props["PartitionKey"] != "org" || props["RowKey"] != "bob" {
		t.Errorf("system keys dropped by projection: pk=%v rk=%v", props["PartitionKey"], props["RowKey"])
	}

	if _, ok := props["Timestamp"]; !ok {
		t.Errorf("Timestamp dropped by projection, want it always present: %v", props)
	}
}

// TestSDKTableNotFoundVsEntityNotFound confirms a missing table reports the
// real Table Storage "TableNotFound" code while a missing entity in an
// existing table reports "EntityNotFound", rather than both collapsing to
// the non-standard "ResourceNotFound".
func TestSDKTableNotFoundVsEntityNotFound(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{TableStorage: cloudP.TableStorage})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	svc, err := aztables.NewServiceClientWithNoCredential(ts.URL+"/", &aztables.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatalf("NewServiceClientWithNoCredential: %v", err)
	}

	// A GetEntity against a table that was never created: TableNotFound.
	missingTableClient := svc.NewClient("nosuchtable")

	_, err = missingTableClient.GetEntity(ctx, "p", "r", nil)
	if err == nil {
		t.Fatal("GetEntity on a missing table succeeded, want an error")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("expected an azcore ResponseError, got %T: %v", err, err)
	}

	if respErr.ErrorCode != "TableNotFound" {
		t.Errorf("missing-table error code = %q, want TableNotFound", respErr.ErrorCode)
	}

	// A GetEntity against a real table but a row that was never inserted:
	// EntityNotFound.
	const tableName = "existingtable"

	if _, err := svc.CreateTable(ctx, tableName, nil); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	existingTableClient := svc.NewClient(tableName)

	_, err = existingTableClient.GetEntity(ctx, "p", "missing-row", nil)
	if err == nil {
		t.Fatal("GetEntity on a missing entity succeeded, want an error")
	}

	if !errors.As(err, &respErr) {
		t.Fatalf("expected an azcore ResponseError, got %T: %v", err, err)
	}

	if respErr.ErrorCode != "EntityNotFound" {
		t.Errorf("missing-entity error code = %q, want EntityNotFound", respErr.ErrorCode)
	}
}
