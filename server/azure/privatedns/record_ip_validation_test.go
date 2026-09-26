package privatedns_test

import (
	"net/http"
	"testing"
)

// TestRecordRejectsBadAddress checks that a PUT of an A or AAAA record set with
// a value that is not an address of the right family gets 400 BadRequest and
// stores nothing.
func TestRecordRejectsBadAddress(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		props   map[string]any
		wantMsg string
	}{
		{
			name: "A not an IP", path: "/A/bad",
			props: map[string]any{"ttl": 300, "aRecords": []any{
				map[string]any{"ipv4Address": "10.0.0.1"}, map[string]any{"ipv4Address": "999.1.1.1"},
			}},
			wantMsg: "The value '999.1.1.1' of field 'ipv4Address' is not a valid IPv4 address.",
		},
		{
			name: "A missing address", path: "/A/bad",
			props:   map[string]any{"ttl": 300, "aRecords": []any{map[string]any{}}},
			wantMsg: "The resource record is missing field 'ipv4Address'.",
		},
		{
			name: "AAAA holding IPv4", path: "/AAAA/bad",
			props:   map[string]any{"ttl": 300, "aaaaRecords": []any{map[string]any{"ipv6Address": "10.0.0.1"}}},
			wantMsg: "The value '10.0.0.1' of field 'ipv6Address' is not a valid IPv6 address.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newServer(t)
			mustCreateZone(t, ts)

			recPath := zonePath(zoneName) + tt.path

			status, body := doJSON(t, ts, http.MethodPut, recPath, map[string]any{"properties": tt.props})
			if status != http.StatusBadRequest {
				t.Fatalf("PUT status = %d, want 400", status)
			}

			e, _ := body["error"].(map[string]any)
			if e["code"] != "BadRequest" || e["message"] != tt.wantMsg {
				t.Fatalf("error = %v, want BadRequest %q", e, tt.wantMsg)
			}

			if status, _ = doJSON(t, ts, http.MethodGet, recPath, nil); status != http.StatusNotFound {
				t.Fatalf("GET after rejected PUT = %d, want 404", status)
			}
		})
	}
}
