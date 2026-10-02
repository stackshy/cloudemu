package apigatewayv2_test

import (
	"net/http"
	"net/url"
	"testing"
)

// tagsURL builds the /v2/tags/{resource-arn} URL with the ARN path-escaped the
// way the SDKs send it.
func tagsURL(base, arn string) string {
	return base + "/v2/tags/" + url.PathEscape(arn)
}

// TestE2E_TagsOnAPIAndStage covers Create* tags plus TagResource, UntagResource
// and GetTags on api and stage ARNs.
func TestE2E_TagsOnAPIAndStage(t *testing.T) {
	ts := newE2E(t)
	apiID := newAPI(t, ts.URL, `{"name":"t","protocolType":"HTTP","tags":{"env":"dev"}}`)
	apiBase := ts.URL + "/v2/apis/" + apiID
	apiARN := "arn:aws:apigateway:us-east-1::/apis/" + apiID

	got := mustDo(t, http.MethodGet, tagsURL(ts.URL, apiARN), "", http.StatusOK)
	if tags, _ := got["tags"].(map[string]any); tags["env"] != "dev" {
		t.Fatalf("GetTags(api) = %v", got)
	}

	mustDo(t, http.MethodPost, tagsURL(ts.URL, apiARN), `{"tags":{"team":"core","env":"prod"}}`, http.StatusCreated)

	api := mustDo(t, http.MethodGet, apiBase, "", http.StatusOK)
	if tags, _ := api["tags"].(map[string]any); tags["team"] != "core" || tags["env"] != "prod" {
		t.Fatalf("GetApi tags = %v", api["tags"])
	}

	list := mustDo(t, http.MethodGet, ts.URL+"/v2/apis", "", http.StatusOK)
	first, _ := items(list)[0].(map[string]any)

	if tags, _ := first["tags"].(map[string]any); tags["team"] != "core" {
		t.Fatalf("GetApis tags = %v", first["tags"])
	}

	mustDo(t, http.MethodDelete, tagsURL(ts.URL, apiARN)+"?tagKeys=env&tagKeys=team", "", http.StatusNoContent)

	got = mustDo(t, http.MethodGet, tagsURL(ts.URL, apiARN), "", http.StatusOK)
	if tags, ok := got["tags"].(map[string]any); !ok || len(tags) != 0 {
		t.Fatalf("GetTags after untag = %v", got)
	}

	mustDo(t, http.MethodPost, apiBase+"/stages", `{"stageName":"prod","tags":{"tier":"gold"}}`, http.StatusCreated)
	stageARN := apiARN + "/stages/prod"

	mustDo(t, http.MethodPost, tagsURL(ts.URL, stageARN), `{"tags":{"owner":"me"}}`, http.StatusCreated)

	stage := mustDo(t, http.MethodGet, apiBase+"/stages/prod", "", http.StatusOK)
	if tags, _ := stage["tags"].(map[string]any); tags["tier"] != "gold" || tags["owner"] != "me" {
		t.Fatalf("GetStage tags = %v", stage["tags"])
	}
}

// TestE2E_TagErrors covers unknown resources and malformed ARNs.
func TestE2E_TagErrors(t *testing.T) {
	ts := newE2E(t)

	wantErr(t, http.MethodGet, tagsURL(ts.URL, "arn:aws:apigateway:us-east-1::/apis/missing123"), "",
		http.StatusNotFound, "NotFoundException", "Invalid API identifier specified missing123")

	wantErr(t, http.MethodGet, tagsURL(ts.URL, "arn:aws:apigateway:us-east-1::/domainnames/api.example.com"), "",
		http.StatusNotFound, "NotFoundException", "Invalid domain name identifier specified api.example.com")

	wantErr(t, http.MethodGet, tagsURL(ts.URL, "arn:aws:apigateway:us-east-1::/vpclinks/abc123"), "",
		http.StatusNotFound, "NotFoundException", "Invalid VpcLink identifier specified abc123")

	wantErr(t, http.MethodGet, tagsURL(ts.URL, "not-an-arn"), "",
		http.StatusBadRequest, "BadRequestException", "Invalid resource ARN specified not-an-arn")

	apiID := newHTTPAPI(t, ts.URL)
	wantErr(t, http.MethodPost, tagsURL(ts.URL, "arn:aws:apigateway:us-east-1::/apis/"+apiID), `{"tags":{"aws:x":"y"}}`,
		http.StatusBadRequest, "BadRequestException", "Tag keys cannot start with the reserved prefix aws:")
}
