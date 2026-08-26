package tablestorage_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"
)

// addEDMEntity inserts an EDMEntity, marshalling it the way the aztables client
// does so Edm.Int64 travels as a string with its @odata.type companion.
func addEDMEntity(t *testing.T, client *aztables.Client, e aztables.EDMEntity) {
	t.Helper()

	marshalled, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal EDMEntity: %v", err)
	}

	if _, err := client.AddEntity(context.Background(), marshalled, nil); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}
}

// listEDM runs a query and decodes every returned row into an EDMEntity.
func listEDM(t *testing.T, client *aztables.Client, opts *aztables.ListEntitiesOptions) []aztables.EDMEntity {
	t.Helper()

	pager := client.NewListEntitiesPager(opts)

	var out []aztables.EDMEntity

	for pager.More() {
		page, err := pager.NextPage(context.Background())
		if err != nil {
			t.Fatalf("ListEntities: %v", err)
		}

		for _, raw := range page.Entities {
			var e aztables.EDMEntity
			if uerr := json.Unmarshal(raw, &e); uerr != nil {
				t.Fatalf("unmarshal EDMEntity: %v", uerr)
			}

			out = append(out, e)
		}
	}

	return out
}

// TestSDKTableInt64FilterMatches confirms a $filter on an Edm.Int64 column
// compares numerically: "Amount gt 100" must return only the larger value.
func TestSDKTableInt64FilterMatches(t *testing.T) {
	client, _ := newTableClient(t, "int64filter")

	addEDMEntity(t, client, aztables.EDMEntity{
		Entity:     aztables.Entity{PartitionKey: "org", RowKey: "big"},
		Properties: map[string]any{"Amount": aztables.EDMInt64(500)},
	})
	addEDMEntity(t, client, aztables.EDMEntity{
		Entity:     aztables.Entity{PartitionKey: "org", RowKey: "small"},
		Properties: map[string]any{"Amount": aztables.EDMInt64(50)},
	})

	filter := "Amount gt 100"
	rows := listEDM(t, client, &aztables.ListEntitiesOptions{Filter: &filter})

	if len(rows) != 1 {
		t.Fatalf("Amount gt 100 returned %d rows, want exactly 1: %+v", len(rows), rows)
	}

	if rows[0].RowKey != "big" {
		t.Errorf("Amount gt 100 matched RowKey=%q, want big", rows[0].RowKey)
	}

	if v, ok := rows[0].Properties["Amount"].(aztables.EDMInt64); !ok || v != aztables.EDMInt64(500) {
		t.Errorf("Amount = %v (%T), want EDMInt64(500)", rows[0].Properties["Amount"], rows[0].Properties["Amount"])
	}
}

// TestSDKTableInt64SelectKeepsType confirms $select on an Edm.Int64 property
// preserves the @odata.type companion so it decodes back as int64, not string.
func TestSDKTableInt64SelectKeepsType(t *testing.T) {
	client, _ := newTableClient(t, "int64select")

	addEDMEntity(t, client, aztables.EDMEntity{
		Entity:     aztables.Entity{PartitionKey: "org", RowKey: "row"},
		Properties: map[string]any{"Amount": aztables.EDMInt64(999), "Note": "ignored"},
	})

	filter := "PartitionKey eq 'org'"
	sel := "PartitionKey,RowKey,Amount"

	rows := listEDM(t, client, &aztables.ListEntitiesOptions{Filter: &filter, Select: &sel})
	if len(rows) != 1 {
		t.Fatalf("query returned %d rows, want 1", len(rows))
	}

	amount := rows[0].Properties["Amount"]

	v, ok := amount.(aztables.EDMInt64)
	if !ok {
		t.Fatalf("selected Amount decoded as %T (%v), want aztables.EDMInt64", amount, amount)
	}

	if v != aztables.EDMInt64(999) {
		t.Errorf("Amount = %d, want 999", v)
	}

	if _, present := rows[0].Properties["Note"]; present {
		t.Errorf("Note present in projection %v, want it dropped", rows[0].Properties)
	}
}

// TestSDKTableNumericFilterInt32Double confirms plain numeric columns (int32
// and float64) still filter numerically alongside the Int64 fix.
func TestSDKTableNumericFilterInt32Double(t *testing.T) {
	client, _ := newTableClient(t, "numfilter")

	addEDMEntity(t, client, aztables.EDMEntity{
		Entity:     aztables.Entity{PartitionKey: "org", RowKey: "a"},
		Properties: map[string]any{"Count": int32(10), "Ratio": 1.5},
	})
	addEDMEntity(t, client, aztables.EDMEntity{
		Entity:     aztables.Entity{PartitionKey: "org", RowKey: "b"},
		Properties: map[string]any{"Count": int32(200), "Ratio": 9.5},
	})

	int32Filter := "Count gt 100"
	if rows := listEDM(t, client, &aztables.ListEntitiesOptions{Filter: &int32Filter}); len(rows) != 1 || rows[0].RowKey != "b" {
		t.Errorf("Count gt 100 = %+v, want exactly RowKey=b", rows)
	}

	doubleFilter := "Ratio lt 5.0"
	if rows := listEDM(t, client, &aztables.ListEntitiesOptions{Filter: &doubleFilter}); len(rows) != 1 || rows[0].RowKey != "a" {
		t.Errorf("Ratio lt 5.0 = %+v, want exactly RowKey=a", rows)
	}
}

// TestSDKTableDuplicateCreateAndInsertCodes confirms a duplicate CreateTable
// returns TableAlreadyExists while a duplicate entity insert returns the
// distinct EntityAlreadyExists.
func TestSDKTableDuplicateCreateAndInsertCodes(t *testing.T) {
	client, ts := newTableClient(t, "dupcodes")

	svc, err := aztables.NewServiceClientWithNoCredential(ts.URL+"/", &aztables.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatalf("NewServiceClientWithNoCredential: %v", err)
	}

	_, err = svc.CreateTable(context.Background(), "dupcodes", nil)
	if err == nil {
		t.Fatal("duplicate CreateTable succeeded, want an error")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("CreateTable error is %T, want *azcore.ResponseError: %v", err, err)
	}

	if respErr.ErrorCode != "TableAlreadyExists" {
		t.Errorf("duplicate table code = %q, want TableAlreadyExists", respErr.ErrorCode)
	}

	entity := map[string]any{"PartitionKey": "p", "RowKey": "r", "V": "1"}

	marshalled, merr := json.Marshal(entity)
	if merr != nil {
		t.Fatalf("marshal entity: %v", merr)
	}

	if _, err := client.AddEntity(context.Background(), marshalled, nil); err != nil {
		t.Fatalf("first AddEntity: %v", err)
	}

	_, err = client.AddEntity(context.Background(), marshalled, nil)
	if err == nil {
		t.Fatal("duplicate AddEntity succeeded, want an error")
	}

	if !errors.As(err, &respErr) {
		t.Fatalf("AddEntity error is %T, want *azcore.ResponseError: %v", err, err)
	}

	if respErr.ErrorCode != "EntityAlreadyExists" {
		t.Errorf("duplicate entity code = %q, want EntityAlreadyExists", respErr.ErrorCode)
	}
}
