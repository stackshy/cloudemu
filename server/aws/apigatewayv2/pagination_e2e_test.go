package apigatewayv2_test

import (
	"net/http"
	"net/url"
	"testing"
)

// TestE2E_PaginationAcrossCollections walks GetApis and every sub-collection
// with maxResults=2, the way the CLI paginator does.
func TestE2E_PaginationAcrossCollections(t *testing.T) {
	ts := newE2E(t)

	for range 4 {
		newHTTPAPI(t, ts.URL)
	}

	apiID := newHTTPAPI(t, ts.URL)
	apiBase := ts.URL + "/v2/apis/" + apiID
	igID := newLambdaIntegration(t, apiBase)

	for _, k := range []string{"GET /a", "GET /b", "GET /c"} {
		mustDo(t, http.MethodPost, apiBase+"/routes", `{"routeKey":"`+k+`","target":"integrations/`+igID+`"}`, http.StatusCreated)
	}

	newLambdaIntegration(t, apiBase)
	newLambdaIntegration(t, apiBase)

	for _, s := range []string{"a", "b", "c"} {
		mustDo(t, http.MethodPost, apiBase+"/stages", `{"stageName":"`+s+`"}`, http.StatusCreated)
		mustDo(t, http.MethodPost, apiBase+"/deployments", `{"stageName":"`+s+`"}`, http.StatusCreated)
	}

	cases := []struct {
		coll, key string
		want      int
	}{
		{ts.URL + "/v2/apis", "apiId", 5},
		{apiBase + "/routes", "routeId", 3},
		{apiBase + "/integrations", "integrationId", 3},
		{apiBase + "/stages", "stageName", 3},
		{apiBase + "/deployments", "deploymentId", 3},
	}

	for _, tc := range cases {
		coll, want := tc.coll, tc.want
		seen := map[string]bool{}
		token := ""

		for page := 0; ; page++ {
			u := coll + "?maxResults=2"
			if token != "" {
				u += "&nextToken=" + url.QueryEscape(token)
			}

			out := mustDo(t, http.MethodGet, u, "", http.StatusOK)
			if n := len(items(out)); n > 2 {
				t.Fatalf("%s page %d has %d items, want <= 2", coll, page, n)
			}

			for _, it := range items(out) {
				m, _ := it.(map[string]any)
				if v, _ := m[tc.key].(string); v != "" {
					seen[v] = true
				}
			}

			token, _ = out["nextToken"].(string)
			if token == "" {
				break
			}
		}

		if len(seen) != want {
			t.Fatalf("%s paged %d distinct items, want %d", coll, len(seen), want)
		}
	}
}

// TestE2E_PaginationBounds covers malformed maxResults and nextToken values.
func TestE2E_PaginationBounds(t *testing.T) {
	ts := newE2E(t)

	for _, q := range []string{"maxResults=abc", "maxResults=0", "maxResults=-1"} {
		wantErr(t, http.MethodGet, ts.URL+"/v2/apis?"+q, "",
			http.StatusBadRequest, "BadRequestException", "MaxResults must be a positive integer")
	}

	wantErr(t, http.MethodGet, ts.URL+"/v2/apis?nextToken=%21%21bad", "",
		http.StatusBadRequest, "BadRequestException", "Invalid NextToken specified")

	mustDo(t, http.MethodGet, ts.URL+"/v2/apis?maxResults=500", "", http.StatusOK)
	mustDo(t, http.MethodGet, ts.URL+"/v2/apis?maxResults=5000", "", http.StatusOK)
}
