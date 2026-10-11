package azure_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	armSub     = "/subscriptions/00000000-0000-0000-0000-000000000000"
	armQTRG    = armSub + "/resourceGroups/rg-qt"
	armQTAcct  = armQTRG + "/providers/Microsoft.Storage/storageAccounts/qtacct"
	armVer     = "?api-version=2023-01-01"
	qtQueueURL = armQTAcct + "/queueServices/default/queues/orders" + armVer
	qtTableURL = armQTAcct + "/tableServices/default/tables/people" + armVer
	qtAcctHost = "qtacct"
)

// newARMQTServer creates a resource group and the qtacct storage account over
// ARM.
func newARMQTServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts, _ := newFullAzureServerWithProvider(t)
	armPut(t, ts, armQTRG+"?api-version=2021-04-01", `{"location":"eastus"}`)
	armPut(t, ts, armQTAcct+armVer, `{"location":"eastus","kind":"StorageV2","sku":{"name":"Standard_LRS"}}`)

	return ts
}

// TestARMQueueSharesDataPlane is the regression for AZSTO-N13 on queues: ARM
// queueServices/default/queues answered 501. A queue created over ARM must be
// the same queue the data plane serves on the account host, with its metadata,
// and a data-plane queue must be readable and listable over ARM.
func TestARMQueueSharesDataPlane(t *testing.T) {
	ts := newARMQTServer(t)
	queueHost := qtAcctHost + ".queue.core.windows.net"

	st, body, _ := storageDo(t, ts, "", "", http.MethodPut, qtQueueURL, `{"properties":{"metadata":{"team":"ops"}}}`,
		map[string]string{"Content-Type": "application/json"})
	expectStatus(t, "ARM put queue", st, http.StatusOK, body)

	st, body, hdr := storageDo(t, ts, queueHost, queueUA, http.MethodGet, "/orders?comp=metadata", "", nil)
	if st != http.StatusOK || hdr.Get("x-ms-meta-team") != "ops" {
		t.Fatalf("data-plane metadata = %d team %q: %s", st, hdr.Get("x-ms-meta-team"), body)
	}

	st, body, _ = storageDo(t, ts, queueHost, queueUA, http.MethodPut, "/fromdata", "", nil)
	expectStatus(t, "data-plane create queue", st, http.StatusCreated, body)

	_, body, _ = storageDo(t, ts, "", "", http.MethodGet, armQTAcct+"/queueServices/default/queues"+armVer, "", nil)
	if !strings.Contains(body, `"name":"orders"`) || !strings.Contains(body, `"name":"fromdata"`) {
		t.Fatalf("ARM list queues = %s, want orders and fromdata", body)
	}

	st, body, _ = storageDo(t, ts, "", "", http.MethodGet, qtQueueURL, "", nil)
	if st != http.StatusOK || !strings.Contains(body, `"team":"ops"`) {
		t.Fatalf("ARM get queue = %d %s", st, body)
	}

	st, body, _ = storageDo(t, ts, "", "", http.MethodDelete, qtQueueURL, "", nil)
	expectStatus(t, "ARM delete queue", st, http.StatusNoContent, body)

	st, body, _ = storageDo(t, ts, "", "", http.MethodGet, qtQueueURL, "", nil)
	expectStatus(t, "ARM get deleted queue", st, http.StatusNotFound, body)

	st, body, _ = storageDo(t, ts, queueHost, queueUA, http.MethodGet, "/orders?comp=metadata", "", nil)
	expectStatus(t, "data-plane get deleted queue", st, http.StatusNotFound, body)
}

// TestARMTableSharesDataPlane is the regression for AZSTO-N13 on tables: an
// ARM-created table is served by the data plane on the account host, its
// signedIdentifiers echo, and a data-plane table lists over ARM.
func TestARMTableSharesDataPlane(t *testing.T) {
	ts := newARMQTServer(t)
	tableHost := qtAcctHost + ".table.core.windows.net"
	jsonHdr := map[string]string{"Content-Type": "application/json"}

	acl := `{"properties":{"signedIdentifiers":[{"id":"read1","accessPolicy":{"permission":"r"}}]}}`
	st, body, _ := storageDo(t, ts, "", "", http.MethodPut, qtTableURL, acl, jsonHdr)
	if st != http.StatusOK || !strings.Contains(body, `"read1"`) || !strings.Contains(body, `"tableName":"people"`) {
		t.Fatalf("ARM put table = %d %s", st, body)
	}

	st, body, _ = storageDo(t, ts, tableHost, tableUA, http.MethodPost, "/people", `{"PartitionKey":"p","RowKey":"r"}`, jsonHdr)
	expectStatus(t, "data-plane insert into ARM table", st, http.StatusCreated, body)

	st, body, _ = storageDo(t, ts, tableHost, tableUA, http.MethodPost, "/Tables", `{"TableName":"fromdata"}`, jsonHdr)
	expectStatus(t, "data-plane create table", st, http.StatusCreated, body)

	_, body, _ = storageDo(t, ts, "", "", http.MethodGet, armQTAcct+"/tableServices/default/tables"+armVer, "", nil)
	if !strings.Contains(body, `"name":"people"`) || !strings.Contains(body, `"name":"fromdata"`) {
		t.Fatalf("ARM list tables = %s, want people and fromdata", body)
	}

	st, body, _ = storageDo(t, ts, "", "", http.MethodGet, qtTableURL, "", nil)
	if st != http.StatusOK || !strings.Contains(body, `"read1"`) {
		t.Fatalf("ARM get table = %d %s", st, body)
	}

	// azurerm's data-plane calls: Query Table, Create Table with
	// return-no-content and Get/Set Table ACL, sharing the ARM access policies.
	st, body, _ = storageDo(t, ts, tableHost, tableUA, http.MethodGet, "/Tables('people')", "", jsonHdr)
	expectStatus(t, "data-plane get table", st, http.StatusOK, body)

	st, body, _ = storageDo(t, ts, tableHost, tableUA, http.MethodGet, "/Tables('nosuch')", "", jsonHdr)
	expectStatus(t, "data-plane get missing table", st, http.StatusNotFound, body)

	st, body, _ = storageDo(t, ts, tableHost, tableUA, http.MethodPost, "/Tables", `{"TableName":"quiet"}`,
		map[string]string{"Content-Type": "application/json", "Prefer": "return-no-content"})
	expectStatus(t, "data-plane create table, return-no-content", st, http.StatusNoContent, body)

	_, body, _ = storageDo(t, ts, tableHost, tableUA, http.MethodGet, "/people?comp=acl", "", nil)
	if !strings.Contains(body, "<Id>read1</Id>") {
		t.Fatalf("data-plane get ACL = %s, want read1", body)
	}

	setACL := `<?xml version="1.0" encoding="utf-8"?><SignedIdentifiers><SignedIdentifier><Id>write2</Id>` +
		`<AccessPolicy><Permission>raud</Permission></AccessPolicy></SignedIdentifier></SignedIdentifiers>`
	st, body, _ = storageDo(t, ts, tableHost, tableUA, http.MethodPut, "/people?comp=acl", setACL, nil)
	expectStatus(t, "data-plane set ACL", st, http.StatusNoContent, body)

	_, body, _ = storageDo(t, ts, "", "", http.MethodGet, qtTableURL, "", nil)
	if !strings.Contains(body, `"write2"`) || strings.Contains(body, `"read1"`) {
		t.Fatalf("ARM get table after data-plane ACL = %s, want write2 only", body)
	}

	st, body, _ = storageDo(t, ts, "", "", http.MethodDelete, qtTableURL, "", nil)
	expectStatus(t, "ARM delete table", st, http.StatusNoContent, body)

	st, body, _ = storageDo(t, ts, tableHost, tableUA, http.MethodGet, "/people(PartitionKey='p',RowKey='r')", "", jsonHdr)
	expectStatus(t, "data-plane get from deleted table", st, http.StatusNotFound, body)
}

// TestStorageAccountDeleteCascadesQueuesAndTables: deleting the account, or
// its resource group, removes its queues and tables, so a new account of the
// same name starts empty.
func TestStorageAccountDeleteCascadesQueuesAndTables(t *testing.T) {
	for _, tc := range []struct{ name, deleteURL string }{
		{"account delete", armQTAcct + armVer},
		{"resource group delete", armQTRG + "?api-version=2021-04-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newARMQTServer(t)
			jsonHdr := map[string]string{"Content-Type": "application/json"}

			queueHost := qtAcctHost + ".queue.core.windows.net"
			tableHost := qtAcctHost + ".table.core.windows.net"

			st, body, _ := storageDo(t, ts, queueHost, queueUA, http.MethodPut, "/orders", "", nil)
			expectStatus(t, "data-plane create queue", st, http.StatusCreated, body)

			st, body, _ = storageDo(t, ts, tableHost, tableUA, http.MethodPost, "/Tables", `{"TableName":"people"}`, jsonHdr)
			expectStatus(t, "data-plane create table", st, http.StatusCreated, body)

			st, body, _ = storageDo(t, ts, "", "", http.MethodDelete, tc.deleteURL, "", nil)
			if st != http.StatusOK && st != http.StatusAccepted {
				t.Fatalf("delete = %d %s", st, body)
			}

			armPut(t, ts, armQTRG+"?api-version=2021-04-01", `{"location":"eastus"}`)
			armPut(t, ts, armQTAcct+armVer, `{"location":"eastus","kind":"StorageV2","sku":{"name":"Standard_LRS"}}`)

			st, body, _ = storageDo(t, ts, queueHost, queueUA, http.MethodGet, "/orders?comp=metadata", "", nil)
			expectStatus(t, "queue after delete", st, http.StatusNotFound, body)

			st, body, _ = storageDo(t, ts, tableHost, tableUA, http.MethodPost, "/Tables", `{"TableName":"people"}`, jsonHdr)
			expectStatus(t, "re-create table after delete", st, http.StatusCreated, body)
		})
	}
}

// TestARMQueuePatchKeepsMetadata checks that a PATCH without metadata keeps the
// queue's metadata and that the ARM list carries each queue's metadata.
func TestARMQueuePatchKeepsMetadata(t *testing.T) {
	ts := newARMQTServer(t)
	jsonHdr := map[string]string{"Content-Type": "application/json"}

	st, body, _ := storageDo(t, ts, "", "", http.MethodPut, qtQueueURL, `{"properties":{"metadata":{"team":"ops"}}}`, jsonHdr)
	expectStatus(t, "ARM put queue", st, http.StatusOK, body)

	st, body, _ = storageDo(t, ts, "", "", http.MethodPatch, qtQueueURL, `{"properties":{}}`, jsonHdr)
	if st != http.StatusOK || !strings.Contains(body, `"team":"ops"`) {
		t.Fatalf("ARM patch queue = %d %s, want metadata kept", st, body)
	}

	_, body, _ = storageDo(t, ts, "", "", http.MethodGet, armQTAcct+"/queueServices/default/queues"+armVer, "", nil)
	if !strings.Contains(body, `"team":"ops"`) {
		t.Fatalf("ARM list queues = %s, want metadata", body)
	}

	st, body, _ = storageDo(t, ts, "", "", http.MethodPut, qtQueueURL, `{"properties":{}}`, jsonHdr)
	if st != http.StatusOK || strings.Contains(body, `"team"`) {
		t.Fatalf("ARM put queue without metadata = %d %s, want metadata cleared", st, body)
	}
}
