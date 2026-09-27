package appsync_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsappsync "github.com/aws/aws-sdk-go-v2/service/appsync"
	astypes "github.com/aws/aws-sdk-go-v2/service/appsync/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func assertSDKBadRequest(t *testing.T, err error, wantMsg string) {
	t.Helper()

	var bre *astypes.BadRequestException
	if !errors.As(err, &bre) {
		t.Fatalf("want BadRequestException, got %v", err)
	}

	if got := aws.ToString(bre.Message); got != wantMsg {
		t.Fatalf("message = %q, want %q", got, wantMsg)
	}
}

func TestSDKListMaxResultsCap(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	apiID := mustCreateAPI(t, c)

	const want = "1 validation error detected: Value '26' at 'maxResults' failed to satisfy constraint: " +
		"Member must have value less than or equal to 25"

	_, err := c.ListGraphqlApis(ctx, &awsappsync.ListGraphqlApisInput{MaxResults: 26})
	assertSDKBadRequest(t, err, want)

	_, err = c.ListDataSources(ctx, &awsappsync.ListDataSourcesInput{ApiId: aws.String(apiID), MaxResults: 26})
	assertSDKBadRequest(t, err, want)

	_, err = c.ListApiKeys(ctx, &awsappsync.ListApiKeysInput{ApiId: aws.String(apiID), MaxResults: 26})
	assertSDKBadRequest(t, err, want)

	if _, err = c.ListGraphqlApis(ctx, &awsappsync.ListGraphqlApisInput{MaxResults: 25}); err != nil {
		t.Fatalf("maxResults=25 rejected: %v", err)
	}
}

// TestWireOversizedMaxResultsRejected checks a maxResults too big for int32
// is still rejected rather than wrapping into range.
func TestWireOversizedMaxResultsRejected(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{AppSync: cloud.AppSync}))
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/v1/apis?maxResults=4294967297", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("X-Amzn-Errortype") != "BadRequestException" {
		t.Fatalf("status=%d errortype=%q, want 400 BadRequestException", resp.StatusCode, resp.Header.Get("X-Amzn-Errortype"))
	}
}

func TestSDKConfigCrossValidation(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	_, err := c.CreateGraphqlApi(ctx, &awsappsync.CreateGraphqlApiInput{
		Name: aws.String("cognito"), AuthenticationType: astypes.AuthenticationTypeAmazonCognitoUserPools,
	})
	assertSDKBadRequest(t, err, "UserPoolConfig can't be null.")

	_, err = c.CreateGraphqlApi(ctx, &awsappsync.CreateGraphqlApiInput{
		Name: aws.String("extra"), AuthenticationType: astypes.AuthenticationTypeApiKey,
		AdditionalAuthenticationProviders: []astypes.AdditionalAuthenticationProvider{
			{AuthenticationType: astypes.AuthenticationTypeAwsLambda},
		},
	})
	assertSDKBadRequest(t, err, "LambdaAuthorizerConfig can't be null.")

	apiID := mustCreateAPI(t, c)

	_, err = c.CreateDataSource(ctx, &awsappsync.CreateDataSourceInput{
		ApiId: aws.String(apiID), Name: aws.String("ddb"), Type: astypes.DataSourceTypeAmazonDynamodb,
		ServiceRoleArn: aws.String("arn:aws:iam::123456789012:role/r"),
	})
	assertSDKBadRequest(t, err, "DynamodbConfig can't be null.")

	out, err := c.CreateDataSource(ctx, &awsappsync.CreateDataSourceInput{
		ApiId: aws.String(apiID), Name: aws.String("ddb"), Type: astypes.DataSourceTypeAmazonDynamodb,
		ServiceRoleArn: aws.String("arn:aws:iam::123456789012:role/r"),
		DynamodbConfig: &astypes.DynamodbDataSourceConfig{TableName: aws.String("t"), AwsRegion: aws.String("us-east-1")},
	})
	if err != nil {
		t.Fatalf("CreateDataSource with config: %v", err)
	}

	if out.DataSource.DynamodbConfig == nil || aws.ToString(out.DataSource.DynamodbConfig.TableName) != "t" {
		t.Fatalf("dynamodbConfig not round-tripped: %#v", out.DataSource.DynamodbConfig)
	}
}

// TestSDKUpdateEventBridgeDataSourceWithoutConfig sends the UpdateDataSource
// shape terraform-provider-aws uses, which never includes eventBridgeConfig.
func TestSDKUpdateEventBridgeDataSourceWithoutConfig(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	apiID := mustCreateAPI(t, c)

	const bus = "arn:aws:events:us-east-1:123456789012:event-bus/default"

	role := aws.String("arn:aws:iam::123456789012:role/r")

	if _, err := c.CreateDataSource(ctx, &awsappsync.CreateDataSourceInput{
		ApiId: aws.String(apiID), Name: aws.String("bus"), Type: astypes.DataSourceTypeAmazonEventbridge,
		ServiceRoleArn: role, EventBridgeConfig: &astypes.EventBridgeDataSourceConfig{EventBusArn: aws.String(bus)},
	}); err != nil {
		t.Fatalf("CreateDataSource: %v", err)
	}

	upd, err := c.UpdateDataSource(ctx, &awsappsync.UpdateDataSourceInput{
		ApiId: aws.String(apiID), Name: aws.String("bus"), Type: astypes.DataSourceTypeAmazonEventbridge,
		ServiceRoleArn: role, Description: aws.String("changed"),
	})
	if err != nil {
		t.Fatalf("UpdateDataSource: %v", err)
	}

	ds := upd.DataSource
	if aws.ToString(ds.Description) != "changed" || ds.EventBridgeConfig == nil || aws.ToString(ds.EventBridgeConfig.EventBusArn) != bus {
		t.Fatalf("update lost eventBridgeConfig or description: %#v", ds)
	}

	_, err = c.UpdateDataSource(ctx, &awsappsync.UpdateDataSourceInput{
		ApiId: aws.String(apiID), Name: aws.String("bus"), Type: astypes.DataSourceTypeAmazonEventbridge,
		ServiceRoleArn: role, HttpConfig: &astypes.HttpDataSourceConfig{Endpoint: aws.String("https://example.com")},
	})
	assertSDKBadRequest(t, err, "HttpConfig is not supported for data source type AMAZON_EVENTBRIDGE.")
}
