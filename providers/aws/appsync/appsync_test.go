package appsync_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/appsync"
	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

func newMock(t *testing.T) *appsync.Mock {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC))

	return appsync.New(config.NewOptions(config.WithClock(clock)))
}

func createAPI(t *testing.T, m *appsync.Mock, name string) *driver.GraphqlAPI {
	t.Helper()

	out, err := m.CreateGraphqlAPI(context.Background(), &driver.CreateGraphqlAPIInput{
		Name:               name,
		AuthenticationType: driver.AuthAPIKey,
	})
	if err != nil {
		t.Fatalf("CreateGraphqlAPI: %v", err)
	}

	return out
}

func TestGraphqlAPIComputedFieldsStable(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	api := createAPI(t, m, "my-api")

	if api.APIID == "" {
		t.Fatal("apiId empty")
	}

	wantARN := "arn:aws:appsync:us-east-1:123456789012:apis/" + api.APIID
	if api.ARN != wantARN {
		t.Fatalf("arn = %q, want %q", api.ARN, wantARN)
	}

	if len(api.URIs) != 2 || api.URIs[driver.URIKeyGraphQL] == "" || api.URIs[driver.URIKeyRealtime] == "" {
		t.Fatalf("uris not two-key populated: %#v", api.URIs)
	}

	if api.Visibility != driver.VisibilityGlobal || api.APIType != driver.APITypeGraphQL {
		t.Fatalf("defaults wrong: visibility=%q apiType=%q", api.Visibility, api.APIType)
	}

	// apiId, arn, uris must never drift across reads.
	for i := 0; i < 3; i++ {
		got, err := m.GetGraphqlAPI(ctx, api.APIID)
		if err != nil {
			t.Fatalf("GetGraphqlAPI: %v", err)
		}

		if got.APIID != api.APIID || got.ARN != api.ARN {
			t.Fatalf("identity drifted on read %d", i)
		}

		if got.URIs[driver.URIKeyGraphQL] != api.URIs[driver.URIKeyGraphQL] {
			t.Fatalf("uris drifted on read %d", i)
		}
	}
}

func TestUpdateGraphqlAPIKeepsIdentity(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "my-api")

	xray := true

	upd, err := m.UpdateGraphqlAPI(ctx, &driver.UpdateGraphqlAPIInput{
		APIID:              api.APIID,
		Name:               "renamed",
		AuthenticationType: driver.AuthAWSIAM,
		XrayEnabled:        &xray,
	})
	if err != nil {
		t.Fatalf("UpdateGraphqlAPI: %v", err)
	}

	if upd.Name != "renamed" || upd.AuthenticationType != driver.AuthAWSIAM || !upd.XrayEnabled {
		t.Fatalf("update not applied: %#v", upd)
	}

	if upd.APIID != api.APIID || upd.ARN != api.ARN || upd.Visibility != driver.VisibilityGlobal {
		t.Fatal("identity/immutable field changed on update")
	}
}

func TestGraphqlAPIValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.CreateGraphqlAPI(ctx, &driver.CreateGraphqlAPIInput{AuthenticationType: driver.AuthAPIKey}); err == nil {
		t.Fatal("want error for empty name")
	}

	_, err := m.CreateGraphqlAPI(ctx, &driver.CreateGraphqlAPIInput{Name: "x", AuthenticationType: "BOGUS"})
	assertException(t, err, driver.ExBadRequest)

	if _, err := m.GetGraphqlAPI(ctx, "missing"); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestDataSourceLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "my-api")

	ds, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
		APIID: api.APIID, Name: "src", Type: driver.DataSourceNone,
	})
	if err != nil {
		t.Fatalf("CreateDataSource: %v", err)
	}

	wantARN := "arn:aws:appsync:us-east-1:123456789012:apis/" + api.APIID + "/datasources/src"
	if ds.DataSourceArn != wantARN {
		t.Fatalf("dataSourceArn = %q, want %q", ds.DataSourceArn, wantARN)
	}

	// Duplicate create is a BadRequest.
	_, err = m.CreateDataSource(ctx, &driver.CreateDataSourceInput{APIID: api.APIID, Name: "src", Type: driver.DataSourceNone})
	assertException(t, err, driver.ExBadRequest)

	list, _, err := m.ListDataSources(ctx, api.APIID, driver.Page{})
	if err != nil || len(list) != 1 {
		t.Fatalf("ListDataSources: %v len=%d", err, len(list))
	}

	if err = m.DeleteDataSource(ctx, api.APIID, "src"); err != nil {
		t.Fatalf("DeleteDataSource: %v", err)
	}

	if _, err = m.GetDataSource(ctx, api.APIID, "src"); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound after delete, got %v", err)
	}
}

func TestAPIKeyExpiresComputedOnceAndFloored(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "my-api")

	key, err := m.CreateAPIKey(ctx, &driver.CreateAPIKeyInput{APIID: api.APIID})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	// Clock is 2026-01-01 12:30:00; default +7d floored to the hour => 2026-01-08 12:00:00.
	want := time.Date(2026, 1, 8, 12, 0, 0, 0, time.UTC).Unix()
	if key.Expires != want {
		t.Fatalf("expires = %d, want %d", key.Expires, want)
	}

	if key.Expires%3600 != 0 {
		t.Fatalf("expires not floored to hour: %d", key.Expires)
	}

	// List must NOT recompute expiry.
	for i := 0; i < 3; i++ {
		list, _, lerr := m.ListAPIKeys(ctx, api.APIID, driver.Page{})
		if lerr != nil || len(list) != 1 {
			t.Fatalf("ListAPIKeys: %v len=%d", lerr, len(list))
		}

		if list[0].Expires != want {
			t.Fatalf("expires recomputed on list %d: %d", i, list[0].Expires)
		}
	}
}

func TestAPIKeyValidityBounds(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "my-api")

	now := time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC)

	// Below the 1-day minimum.
	_, err := m.CreateAPIKey(ctx, &driver.CreateAPIKeyInput{APIID: api.APIID, Expires: now.Add(12 * time.Hour).Unix()})
	assertException(t, err, driver.ExAPIKeyValidity)

	// Above the 365-day maximum.
	_, err = m.CreateAPIKey(ctx, &driver.CreateAPIKeyInput{APIID: api.APIID, Expires: now.Add(400 * 24 * time.Hour).Unix()})
	assertException(t, err, driver.ExAPIKeyValidity)

	// Within bounds succeeds.
	if _, err = m.CreateAPIKey(ctx, &driver.CreateAPIKeyInput{APIID: api.APIID, Expires: now.Add(30 * 24 * time.Hour).Unix()}); err != nil {
		t.Fatalf("in-bounds create failed: %v", err)
	}
}

func TestTagsRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "my-api")

	if err := m.TagResource(ctx, api.ARN, map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := m.ListTagsForResource(ctx, api.ARN)
	if err != nil || tags["a"] != "1" || tags["b"] != "2" {
		t.Fatalf("ListTags: %v %#v", err, tags)
	}

	if err = m.UntagResource(ctx, api.ARN, []string{"a"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, _ = m.ListTagsForResource(ctx, api.ARN)
	if _, ok := tags["a"]; ok || tags["b"] != "2" {
		t.Fatalf("untag wrong: %#v", tags)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "my-api")

	if _, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{APIID: api.APIID, Name: "src", Type: driver.DataSourceNone}); err != nil {
		t.Fatalf("CreateDataSource: %v", err)
	}

	key, err := m.CreateAPIKey(ctx, &driver.CreateAPIKeyInput{APIID: api.APIID, Description: "k"})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	blob, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock(t)
	if err = restored.Restore(ctx, blob); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := restored.GetGraphqlAPI(ctx, api.APIID)
	if err != nil {
		t.Fatalf("GetGraphqlAPI after restore: %v", err)
	}

	if got.ARN != api.ARN || got.URIs[driver.URIKeyGraphQL] != api.URIs[driver.URIKeyGraphQL] {
		t.Fatal("api identity not preserved across restore")
	}

	keys, _, err := restored.ListAPIKeys(ctx, api.APIID, driver.Page{})
	if err != nil || len(keys) != 1 || keys[0].Expires != key.Expires {
		t.Fatalf("api key not preserved: %v %#v", err, keys)
	}
}

func TestUpdateAndDeleteAPIKey(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "my-api")

	now := time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC)

	key, err := m.CreateAPIKey(ctx, &driver.CreateAPIKeyInput{APIID: api.APIID, Description: "old"})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	desc := "new"
	newExp := now.Add(90 * 24 * time.Hour).Unix()

	upd, err := m.UpdateAPIKey(ctx, &driver.UpdateAPIKeyInput{
		APIID: api.APIID, ID: key.ID, Description: &desc, Expires: newExp,
	})
	if err != nil {
		t.Fatalf("UpdateAPIKey: %v", err)
	}

	if upd.Description != "new" || upd.Expires == key.Expires {
		t.Fatalf("update not applied: %#v", upd)
	}

	if upd.Expires%3600 != 0 {
		t.Fatalf("updated expiry not floored: %d", upd.Expires)
	}

	// Out-of-bounds expiry on update is rejected.
	_, err = m.UpdateAPIKey(ctx, &driver.UpdateAPIKeyInput{APIID: api.APIID, ID: key.ID, Expires: now.Add(time.Hour).Unix()})
	assertException(t, err, driver.ExAPIKeyValidity)

	// Missing key.
	_, err = m.UpdateAPIKey(ctx, &driver.UpdateAPIKeyInput{APIID: api.APIID, ID: "missing"})
	assertException(t, err, driver.ExNotFound)

	if err = m.DeleteAPIKey(ctx, api.APIID, key.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}

	if err = m.DeleteAPIKey(ctx, api.APIID, key.ID); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound on second delete, got %v", err)
	}
}

func TestUpdateDataSource(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "my-api")

	if _, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{APIID: api.APIID, Name: "src", Type: driver.DataSourceNone}); err != nil {
		t.Fatalf("CreateDataSource: %v", err)
	}

	upd, err := m.UpdateDataSource(ctx, &driver.UpdateDataSourceInput{
		APIID: api.APIID, Name: "src", Type: driver.DataSourceHTTP, Description: "changed",
	})
	if err != nil {
		t.Fatalf("UpdateDataSource: %v", err)
	}

	if upd.Type != driver.DataSourceHTTP || upd.Description != "changed" {
		t.Fatalf("update not applied: %#v", upd)
	}

	// dataSourceArn is immutable.
	wantARN := "arn:aws:appsync:us-east-1:123456789012:apis/" + api.APIID + "/datasources/src"
	if upd.DataSourceArn != wantARN {
		t.Fatalf("dataSourceArn drifted: %q", upd.DataSourceArn)
	}

	// Invalid type and missing source.
	_, err = m.UpdateDataSource(ctx, &driver.UpdateDataSourceInput{APIID: api.APIID, Name: "src", Type: "BOGUS"})
	assertException(t, err, driver.ExBadRequest)

	_, err = m.UpdateDataSource(ctx, &driver.UpdateDataSourceInput{APIID: api.APIID, Name: "missing", Type: driver.DataSourceNone})
	assertException(t, err, driver.ExNotFound)
}

func TestListAndDeleteGraphqlAPIs(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	first := createAPI(t, m, "api-1")
	createAPI(t, m, "api-2")
	createAPI(t, m, "api-3")

	// First page of size 2 yields a token; the token yields the rest.
	page1, next, err := m.ListGraphqlAPIs(ctx, driver.Page{MaxResults: 2})
	if err != nil || len(page1) != 2 || next == "" {
		t.Fatalf("page1: len=%d next=%q err=%v", len(page1), next, err)
	}

	page2, next2, err := m.ListGraphqlAPIs(ctx, driver.Page{MaxResults: 2, NextToken: next})
	if err != nil || len(page2) != 1 || next2 != "" {
		t.Fatalf("page2: len=%d next=%q err=%v", len(page2), next2, err)
	}

	if err = m.DeleteGraphqlAPI(ctx, first.APIID); err != nil {
		t.Fatalf("DeleteGraphqlAPI: %v", err)
	}

	if err = m.DeleteGraphqlAPI(ctx, first.APIID); !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound on second delete, got %v", err)
	}
}

func TestTagInvalidARN(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	err := m.TagResource(ctx, "not-an-arn", map[string]string{"a": "1"})
	assertException(t, err, driver.ExBadRequest)

	// Well-formed ARN for an API that does not exist -> NotFound.
	_, err = m.ListTagsForResource(ctx, "arn:aws:appsync:us-east-1:123456789012:apis/ghost")
	assertException(t, err, driver.ExNotFound)
}

func assertException(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want APIError %s, got %v", want, err)
	}

	if apiErr.Exception != want {
		t.Fatalf("exception = %q, want %q", apiErr.Exception, want)
	}
}
