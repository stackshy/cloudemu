package azure_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestUnknownARMRouteIsJSON checks that a route no handler serves answers 501
// with an ARM error envelope, not plain text the Azure SDKs cannot parse.
func TestUnknownARMRouteIsJSON(t *testing.T) {
	ts, _ := newFullAzureServerWithProvider(t)

	resp, err := http.Get(ts.URL + "/subscriptions/s1/providers/Microsoft.Nope/widgets?api-version=2024-01-01")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out struct {
		Error struct{ Code, Message string } `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusNotImplemented ||
		out.Error.Code != "NotImplemented" {
		t.Fatalf("got %d %+v (%v), want 501 NotImplemented JSON", resp.StatusCode, out, err)
	}
}
