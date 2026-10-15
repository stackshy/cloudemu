package azure_test

import (
	"context"
	"net/http"
	"testing"
)

// TestScopeTagsClearedOnResourceDelete: the tags at a resource scope are the
// resource's own, so a deleted resource answers 404 and a resource recreated
// with the same id starts with only the tags it is created with, while a
// sibling's tags stay.
func TestScopeTagsClearedOnResourceDelete(t *testing.T) {
	ts, c := echoTestServer(t)
	base := ts.URL + "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Sql/servers/"
	tagsSuffix := "/providers/Microsoft.Resources/tags/default?api-version=2021-04-01"

	for _, name := range []string{"srv1", "srv10"} {
		putJSON(t, c, base+name+"?api-version=2021-11-01", map[string]any{"location": "eastus"})
		putJSON(t, c, base+name+tagsSuffix, map[string]any{"properties": map[string]any{"tags": map[string]any{"env": name}}})
	}

	deleteOK(t, c, base+"srv1?api-version=2021-11-01")

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"srv1"+tagsSuffix, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("tags at a deleted resource: status %d, want 404", resp.StatusCode)
	}

	putJSON(t, c, base+"srv1?api-version=2021-11-01", map[string]any{"location": "eastus"})

	if tags := tagsOf(t, getJSON(t, c, base+"srv1"+tagsSuffix)); len(tags) != 0 {
		t.Fatalf("recreated resource kept old tags: %v", tags)
	}

	if tags := tagsOf(t, getJSON(t, c, base+"srv10"+tagsSuffix)); tags["env"] != "srv10" {
		t.Fatalf("sibling srv10 lost its tags: %v", tags)
	}
}

func tagsOf(t *testing.T, resource map[string]any) map[string]any {
	t.Helper()

	tags, _ := props(t, resource)["tags"].(map[string]any)

	return tags
}
