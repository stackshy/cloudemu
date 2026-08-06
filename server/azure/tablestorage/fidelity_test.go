package tablestorage_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newTableClient(t *testing.T, table string) (*aztables.Client, *httptest.Server) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{TableStorage: cloudP.TableStorage})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := aztables.NewServiceClientWithNoCredential(ts.URL+"/", &aztables.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatalf("NewServiceClientWithNoCredential: %v", err)
	}
	if _, err := svc.CreateTable(context.Background(), table, nil); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	return svc.NewClient(table), ts
}

func addEntity(t *testing.T, client *aztables.Client, pk, rk string, extra map[string]any) {
	t.Helper()

	e := map[string]any{"PartitionKey": pk, "RowKey": rk}
	for k, v := range extra {
		e[k] = v
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal entity: %v", err)
	}
	if _, err := client.AddEntity(context.Background(), b, nil); err != nil {
		t.Fatalf("AddEntity %s/%s: %v", pk, rk, err)
	}
}

func listRowKeys(t *testing.T, client *aztables.Client, filter string) ([]string, error) {
	t.Helper()

	pager := client.NewListEntitiesPager(&aztables.ListEntitiesOptions{Filter: &filter})
	var rks []string
	for pager.More() {
		page, err := pager.NextPage(context.Background())
		if err != nil {
			return nil, err
		}
		for _, raw := range page.Entities {
			var e map[string]any
			if err := json.Unmarshal(raw, &e); err != nil {
				t.Fatalf("unmarshal entity: %v", err)
			}
			if rk, ok := e["RowKey"].(string); ok {
				rks = append(rks, rk)
			}
		}
	}
	return rks, nil
}

// TestQueryFilterActuallyFilters covers #266: a supported eq/and $filter must
// narrow the result set rather than degrade to match-all.
func TestQueryFilterActuallyFilters(t *testing.T) {
	client, _ := newTableClient(t, "flt")
	addEntity(t, client, "org", "alice", nil)
	addEntity(t, client, "org", "bob", nil)
	addEntity(t, client, "other", "carol", nil)

	rks, err := listRowKeys(t, client, "PartitionKey eq 'org' and RowKey eq 'alice'")
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(rks) != 1 || rks[0] != "alice" {
		t.Fatalf("eq/and filter = %v, want [alice]", rks)
	}

	rks, err = listRowKeys(t, client, "PartitionKey eq 'nope'")
	if err != nil {
		t.Fatalf("no-match list: %v", err)
	}
	if len(rks) != 0 {
		t.Fatalf("no-match filter = %v, want []", rks)
	}

	// Extra whitespace between tokens is tolerated (not rejected as unsupported).
	rks, err = listRowKeys(t, client, "PartitionKey  eq  'org'")
	if err != nil {
		t.Fatalf("whitespace filter: %v", err)
	}
	if len(rks) != 2 {
		t.Fatalf("whitespace filter = %v, want 2 rows", rks)
	}
}

// TestQueryUnsupportedFilterRejected covers #266: an unsupported operator must
// return 400 InvalidInput, not silently match everything.
func TestQueryUnsupportedFilterRejected(t *testing.T) {
	client, _ := newTableClient(t, "flt2")
	addEntity(t, client, "org", "alice", map[string]any{"Age": int64(30)})
	addEntity(t, client, "org", "bob", map[string]any{"Age": int64(20)})

	if _, err := listRowKeys(t, client, "Age gt 25"); err == nil {
		t.Fatal("unsupported $filter 'Age gt 25' returned no error (degraded to match-all)")
	} else if !strings.Contains(err.Error(), "InvalidInput") {
		t.Fatalf("error = %v, want InvalidInput (400)", err)
	}
}

// TestGetEntitySingleKeyPredicateRejected covers #266: a key predicate that
// names only one of the two keys is malformed → 400, not a 404 not-found.
func TestGetEntitySingleKeyPredicateRejected(t *testing.T) {
	client, ts := newTableClient(t, "kp")
	addEntity(t, client, "org", "alice", nil)

	status := func(path string) int {
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := status("/kp(PartitionKey='org')"); code != http.StatusBadRequest {
		t.Fatalf("single-key predicate status = %d, want 400", code)
	}
	if code := status("/kp(PartitionKey='org',RowKey='alice')"); code != http.StatusOK {
		t.Fatalf("both-keys predicate status = %d, want 200", code)
	}
}
