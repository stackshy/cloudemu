package appsync_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsappsync "github.com/aws/aws-sdk-go-v2/service/appsync"
	astypes "github.com/aws/aws-sdk-go-v2/service/appsync/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awsappsync.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{AppSync: cloud.AppSync})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsappsync.NewFromConfig(cfg, func(o *awsappsync.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKGraphqlApiLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateGraphqlApi(ctx, &awsappsync.CreateGraphqlApiInput{
		Name:               aws.String("sdk-api"),
		AuthenticationType: astypes.AuthenticationTypeApiKey,
		Tags:               map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateGraphqlApi: %v", err)
	}

	api := create.GraphqlApi
	apiID := aws.ToString(api.ApiId)

	if apiID == "" {
		t.Fatal("apiId empty")
	}

	if got := aws.ToString(api.Arn); got != "arn:aws:appsync:us-east-1:123456789012:apis/"+apiID {
		t.Fatalf("arn = %q", got)
	}

	if api.Uris["GRAPHQL"] == "" || api.Uris["REALTIME"] == "" {
		t.Fatalf("uris missing a key: %#v", api.Uris)
	}

	if api.Visibility != astypes.GraphQLApiVisibilityGlobal {
		t.Fatalf("visibility = %q, want GLOBAL", api.Visibility)
	}

	// apiId, arn, uris must be stable across repeated GETs.
	for i := 0; i < 3; i++ {
		got, gerr := c.GetGraphqlApi(ctx, &awsappsync.GetGraphqlApiInput{ApiId: aws.String(apiID)})
		if gerr != nil {
			t.Fatalf("GetGraphqlApi: %v", gerr)
		}

		if aws.ToString(got.GraphqlApi.ApiId) != apiID {
			t.Fatalf("apiId drifted on get %d: %q", i, aws.ToString(got.GraphqlApi.ApiId))
		}

		if aws.ToString(got.GraphqlApi.Arn) != aws.ToString(api.Arn) {
			t.Fatalf("arn drifted on get %d", i)
		}

		if got.GraphqlApi.Uris["GRAPHQL"] != api.Uris["GRAPHQL"] || got.GraphqlApi.Uris["REALTIME"] != api.Uris["REALTIME"] {
			t.Fatalf("uris drifted on get %d", i)
		}
	}

	// Update: change name and enable X-Ray.
	upd, err := c.UpdateGraphqlApi(ctx, &awsappsync.UpdateGraphqlApiInput{
		ApiId:              aws.String(apiID),
		Name:               aws.String("sdk-api-renamed"),
		AuthenticationType: astypes.AuthenticationTypeApiKey,
		XrayEnabled:        true,
	})
	if err != nil {
		t.Fatalf("UpdateGraphqlApi: %v", err)
	}

	if aws.ToString(upd.GraphqlApi.Name) != "sdk-api-renamed" || !upd.GraphqlApi.XrayEnabled {
		t.Fatalf("update not applied: %#v", upd.GraphqlApi)
	}

	if aws.ToString(upd.GraphqlApi.ApiId) != apiID {
		t.Fatal("apiId changed on update")
	}

	list, err := c.ListGraphqlApis(ctx, &awsappsync.ListGraphqlApisInput{})
	if err != nil {
		t.Fatalf("ListGraphqlApis: %v", err)
	}

	if len(list.GraphqlApis) != 1 {
		t.Fatalf("list len = %d", len(list.GraphqlApis))
	}

	if _, err = c.DeleteGraphqlApi(ctx, &awsappsync.DeleteGraphqlApiInput{ApiId: aws.String(apiID)}); err != nil {
		t.Fatalf("DeleteGraphqlApi: %v", err)
	}

	_, err = c.GetGraphqlApi(ctx, &awsappsync.GetGraphqlApiInput{ApiId: aws.String(apiID)})
	assertNotFound(t, err)
}

func TestSDKDataSourceLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	apiID := mustCreateAPI(t, c)

	create, err := c.CreateDataSource(ctx, &awsappsync.CreateDataSourceInput{
		ApiId:       aws.String(apiID),
		Name:        aws.String("none_ds"),
		Type:        astypes.DataSourceTypeNone,
		Description: aws.String("a source"),
	})
	if err != nil {
		t.Fatalf("CreateDataSource: %v", err)
	}

	wantArn := "arn:aws:appsync:us-east-1:123456789012:apis/" + apiID + "/datasources/none_ds"
	if got := aws.ToString(create.DataSource.DataSourceArn); got != wantArn {
		t.Fatalf("dataSourceArn = %q, want %q", got, wantArn)
	}

	if create.DataSource.Type != astypes.DataSourceTypeNone {
		t.Fatalf("type = %q", create.DataSource.Type)
	}

	got, err := c.GetDataSource(ctx, &awsappsync.GetDataSourceInput{ApiId: aws.String(apiID), Name: aws.String("none_ds")})
	if err != nil {
		t.Fatalf("GetDataSource: %v", err)
	}

	if aws.ToString(got.DataSource.DataSourceArn) != wantArn {
		t.Fatal("dataSourceArn drifted on get")
	}

	if _, err = c.UpdateDataSource(ctx, &awsappsync.UpdateDataSourceInput{
		ApiId: aws.String(apiID), Name: aws.String("none_ds"), Type: astypes.DataSourceTypeNone,
		Description: aws.String("updated"),
	}); err != nil {
		t.Fatalf("UpdateDataSource: %v", err)
	}

	if _, err = c.DeleteDataSource(ctx, &awsappsync.DeleteDataSourceInput{ApiId: aws.String(apiID), Name: aws.String("none_ds")}); err != nil {
		t.Fatalf("DeleteDataSource: %v", err)
	}

	_, err = c.GetDataSource(ctx, &awsappsync.GetDataSourceInput{ApiId: aws.String(apiID), Name: aws.String("none_ds")})
	assertNotFound(t, err)
}

func TestSDKApiKeyExpiresComputedOnce(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	apiID := mustCreateAPI(t, c)

	create, err := c.CreateApiKey(ctx, &awsappsync.CreateApiKeyInput{
		ApiId:       aws.String(apiID),
		Description: aws.String("primary"),
	})
	if err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	keyID := aws.ToString(create.ApiKey.Id)
	expires := create.ApiKey.Expires

	if keyID == "" {
		t.Fatal("api key id empty")
	}

	if expires == 0 || expires%3600 != 0 {
		t.Fatalf("expires not floored to the hour: %d", expires)
	}

	// Expires must NOT be recomputed on list.
	for i := 0; i < 3; i++ {
		list, lerr := c.ListApiKeys(ctx, &awsappsync.ListApiKeysInput{ApiId: aws.String(apiID)})
		if lerr != nil {
			t.Fatalf("ListApiKeys: %v", lerr)
		}

		if len(list.ApiKeys) != 1 {
			t.Fatalf("list len = %d", len(list.ApiKeys))
		}

		if list.ApiKeys[0].Expires != expires {
			t.Fatalf("expires drifted on list %d: %d != %d", i, list.ApiKeys[0].Expires, expires)
		}
	}

	if _, err = c.DeleteApiKey(ctx, &awsappsync.DeleteApiKeyInput{ApiId: aws.String(apiID), Id: aws.String(keyID)}); err != nil {
		t.Fatalf("DeleteApiKey: %v", err)
	}
}

func TestSDKApiKeyValidityOutOfBounds(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	apiID := mustCreateAPI(t, c)

	// 12 hours from now is below the 1-day minimum.
	_, err := c.CreateApiKey(ctx, &awsappsync.CreateApiKeyInput{
		ApiId:   aws.String(apiID),
		Expires: 12 * 3600,
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want APIError, got %v", err)
	}

	if apiErr.ErrorCode() != "ApiKeyValidityOutOfBoundsException" {
		t.Fatalf("error code = %q", apiErr.ErrorCode())
	}
}

func TestSDKTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	apiID := mustCreateAPI(t, c)

	arn := "arn:aws:appsync:us-east-1:123456789012:apis/" + apiID

	if _, err := c.TagResource(ctx, &awsappsync.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        map[string]string{"team": "search"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	got, err := c.ListTagsForResource(ctx, &awsappsync.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if got.Tags["team"] != "search" {
		t.Fatalf("tags = %#v", got.Tags)
	}

	if _, err = c.UntagResource(ctx, &awsappsync.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	got, err = c.ListTagsForResource(ctx, &awsappsync.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource after untag: %v", err)
	}

	if len(got.Tags) != 0 {
		t.Fatalf("tags not removed: %#v", got.Tags)
	}
}

func mustCreateAPI(t *testing.T, c *awsappsync.Client) string {
	t.Helper()

	out, err := c.CreateGraphqlApi(context.Background(), &awsappsync.CreateGraphqlApiInput{
		Name:               aws.String("fixture-api"),
		AuthenticationType: astypes.AuthenticationTypeApiKey,
	})
	if err != nil {
		t.Fatalf("CreateGraphqlApi fixture: %v", err)
	}

	return aws.ToString(out.GraphqlApi.ApiId)
}

func assertNotFound(t *testing.T, err error) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want APIError, got %v", err)
	}

	if apiErr.ErrorCode() != "NotFoundException" {
		t.Fatalf("error code = %q, want NotFoundException", apiErr.ErrorCode())
	}
}
