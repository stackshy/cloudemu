// Server-side Table projection for core/v1 Events. `kubectl get events` renders
// LAST SEEN / TYPE / REASON / OBJECT / MESSAGE (plus -o wide extras) rather than
// the generic NAME / AGE fallback.

package kubernetes_test

import (
	"net/http"
	"testing"
	"time"
)

func TestEvents_TableColumns(t *testing.T) {
	base, closeFn := newFixture(t)
	defer closeFn()

	now := time.Now().UTC().Format(time.RFC3339)
	eventBody := []byte(`{
		"apiVersion":"v1","kind":"Event",
		"metadata":{"name":"web.evt1","namespace":"default"},
		"involvedObject":{"apiVersion":"v1","kind":"Pod","name":"web-xyz","namespace":"default"},
		"reason":"Scheduled","message":"Successfully assigned default/web-xyz",
		"type":"Normal","source":{"component":"default-scheduler"},
		"count":1,"firstTimestamp":"` + now + `","lastTimestamp":"` + now + `"}`)

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/events", eventBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create event: status %d", resp.StatusCode)
	}

	resp.Body.Close()

	table := getTable(t, base+"/api/v1/namespaces/default/events")

	// Default (priority-0) columns kubectl prints, in order.
	wantCols := []string{"Last Seen", "Type", "Reason", "Object", "Message"}
	for _, name := range wantCols {
		_ = columnIndex(t, table, name) // fatals if missing
	}

	// -o wide extras must also be declared.
	for _, name := range []string{"Subobject", "Source", "First Seen", "Count", "Name"} {
		_ = columnIndex(t, table, name)
	}

	if len(table.Rows) != 1 {
		t.Fatalf("event table rows: got %d, want 1", len(table.Rows))
	}

	cells := table.Rows[0].Cells

	cell := func(col string) any { return cells[columnIndex(t, table, col)] }

	if got := cell("Type"); got != "Normal" {
		t.Fatalf("Type cell: got %v, want Normal", got)
	}

	if got := cell("Reason"); got != "Scheduled" {
		t.Fatalf("Reason cell: got %v, want Scheduled", got)
	}

	if got := cell("Object"); got != "pod/web-xyz" {
		t.Fatalf("Object cell: got %v, want pod/web-xyz", got)
	}

	if got := cell("Message"); got != "Successfully assigned default/web-xyz" {
		t.Fatalf("Message cell: got %v", got)
	}

	if got := cell("Source"); got != "default-scheduler" {
		t.Fatalf("Source cell: got %v, want default-scheduler", got)
	}

	if got, ok := cell("Last Seen").(string); !ok || got == "" || got == "<unknown>" {
		t.Fatalf("Last Seen cell: got %v, want a non-empty age", cells[columnIndex(t, table, "Last Seen")])
	}
}
