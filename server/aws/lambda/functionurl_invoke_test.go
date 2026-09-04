package lambda_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/smithy-go"
	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newFunctionURLTestServer sets up an aws-sdk-go-v2 Lambda client plus the
// underlying httptest.Server, so invoke-via-URL tests can dial the same
// listener a real Function URL's generated host is DNS-resolved to and set
// the Host header to that generated host by hand.
func newFunctionURLTestServer(t *testing.T) (*awslambda.Client, *awsprovider.Provider, *httptest.Server) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Lambda: cloud.Lambda})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return client, cloud, ts
}

// invokeEvent is the subset of the payload-format-2.0 Function URL request
// event this test cares about.
type invokeEvent struct {
	Version        string            `json:"version"`
	RouteKey       string            `json:"routeKey"`
	RawPath        string            `json:"rawPath"`
	RawQueryString string            `json:"rawQueryString"`
	Headers        map[string]string `json:"headers"`
	RequestContext struct {
		HTTP struct {
			Method   string `json:"method"`
			Path     string `json:"path"`
			SourceIP string `json:"sourceIp"`
		} `json:"http"`
		Stage    string `json:"stage"`
		RouteKey string `json:"routeKey"`
	} `json:"requestContext"`
	Body            string `json:"body"`
	IsBase64Encoded bool   `json:"isBase64Encoded"`
}

// dialFunctionURL issues an HTTP request against ts using rawURL's path/query
// but the httptest server's actual host:port, while sending rawURL's own host
// as the Host header — exactly how a real client resolves
// <url-id>.lambda-url.<region>.on.aws to an IP and presents that hostname over
// the wire.
func dialFunctionURL(t *testing.T, ts *httptest.Server, method, rawURL string, body []byte) *http.Response {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse function url: %v", err)
	}

	target, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}

	parsed.Scheme = target.Scheme
	parsed.Host = target.Host

	req, err := http.NewRequestWithContext(context.Background(), method, parsed.String(), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Host = mustParseHost(t, rawURL)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}

	return resp
}

func mustParseHost(t *testing.T, rawURL string) string {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	return parsed.Host
}

// TestFunctionURLInvokeStructuredResponse covers a handler that returns the
// structured {statusCode,headers,body} shape: the response must carry the
// given status/headers/body, and the request the handler received must be a
// well-formed payload-2.0 event.
func TestFunctionURLInvokeStructuredResponse(t *testing.T) {
	client, cloud, ts := newFunctionURLTestServer(t)
	ctx := context.Background()
	createBasicFunction(t, client, "urlinvoke-structured")

	var received invokeEvent

	cloud.Lambda.RegisterHandler("urlinvoke-structured", func(_ context.Context, payload []byte) ([]byte, error) {
		if err := json.Unmarshal(payload, &received); err != nil {
			t.Errorf("unmarshal invoke event: %v", err)
		}

		return json.Marshal(map[string]any{
			"statusCode": 201,
			"headers":    map[string]string{"X-Handler": "yes"},
			"body":       "hello from handler",
		})
	})

	created, err := client.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("urlinvoke-structured"),
		AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
	})
	if err != nil {
		t.Fatalf("CreateFunctionUrlConfig: %v", err)
	}

	functionURL := aws.ToString(created.FunctionUrl) + "foo/bar?a=1&b=2"

	resp := dialFunctionURL(t, ts, http.MethodPost, functionURL, []byte(`{"ping":"pong"}`))
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", resp.StatusCode, body)
	}

	if string(body) != "hello from handler" {
		t.Fatalf("body = %q, want %q", body, "hello from handler")
	}

	if got := resp.Header.Get("X-Handler"); got != "yes" {
		t.Fatalf("X-Handler header = %q, want yes", got)
	}

	if received.Version != "2.0" {
		t.Fatalf("event version = %q, want 2.0", received.Version)
	}

	if received.RouteKey != "$default" || received.RequestContext.RouteKey != "$default" || received.RequestContext.Stage != "$default" {
		t.Fatalf("event routeKey/stage = %+v, want $default everywhere", received)
	}

	if received.RequestContext.HTTP.Method != http.MethodPost {
		t.Fatalf("event http.method = %q, want POST", received.RequestContext.HTTP.Method)
	}

	if received.RawPath != "/foo/bar" || received.RequestContext.HTTP.Path != "/foo/bar" {
		t.Fatalf("event rawPath/http.path = %q/%q, want /foo/bar", received.RawPath, received.RequestContext.HTTP.Path)
	}

	if received.RawQueryString != "a=1&b=2" {
		t.Fatalf("event rawQueryString = %q, want a=1&b=2", received.RawQueryString)
	}

	if received.Body != `{"ping":"pong"}` {
		t.Fatalf("event body = %q, want the request body verbatim", received.Body)
	}

	if received.IsBase64Encoded {
		t.Fatal("event isBase64Encoded = true for a UTF-8 body, want false")
	}
}

// TestFunctionURLInvokeBareResponse covers a handler that returns a plain
// object with no "statusCode" key: real Function URLs treat the whole return
// value as the response body with a 200, rather than trying to interpret it
// as the structured shape.
func TestFunctionURLInvokeBareResponse(t *testing.T) {
	client, cloud, ts := newFunctionURLTestServer(t)
	ctx := context.Background()
	createBasicFunction(t, client, "urlinvoke-bare")

	cloud.Lambda.RegisterHandler("urlinvoke-bare", func(context.Context, []byte) ([]byte, error) {
		return json.Marshal(map[string]any{"message": "ok"})
	})

	created, err := client.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("urlinvoke-bare"),
		AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
	})
	if err != nil {
		t.Fatalf("CreateFunctionUrlConfig: %v", err)
	}

	resp := dialFunctionURL(t, ts, http.MethodGet, aws.ToString(created.FunctionUrl), nil)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	var decoded map[string]string
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("response body isn't JSON: %v (%s)", err, body)
	}

	if decoded["message"] != "ok" {
		t.Fatalf("body = %q, want the handler's bare object verbatim", body)
	}
}

// TestFunctionURLInvokeHandlerError covers a handler that raises: real
// Function URLs return 502 with a fixed body, unlike the raw Invoke API's 200
// + X-Amz-Function-Error.
func TestFunctionURLInvokeHandlerError(t *testing.T) {
	client, cloud, ts := newFunctionURLTestServer(t)
	ctx := context.Background()
	createBasicFunction(t, client, "urlinvoke-error")

	cloud.Lambda.RegisterHandler("urlinvoke-error", func(context.Context, []byte) ([]byte, error) {
		return nil, errors.New("boom")
	})

	created, err := client.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("urlinvoke-error"),
		AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
	})
	if err != nil {
		t.Fatalf("CreateFunctionUrlConfig: %v", err)
	}

	resp := dialFunctionURL(t, ts, http.MethodGet, aws.ToString(created.FunctionUrl), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

// TestFunctionURLQualifierScoping covers Function URL configs on an alias
// qualifier, rejecting a numbered-version qualifier, and the duplicate-create
// conflict for the same (function, qualifier).
func TestFunctionURLQualifierScoping(t *testing.T) {
	client, _, _ := newFunctionURLTestServer(t)
	ctx := context.Background()
	createBasicFunction(t, client, "urlquals")

	if _, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{
		FunctionName: aws.String("urlquals"),
	}); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}

	if _, err := client.CreateAlias(ctx, &awslambda.CreateAliasInput{
		FunctionName:    aws.String("urlquals"),
		Name:            aws.String("live"),
		FunctionVersion: aws.String("1"),
	}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	aliasURL, err := client.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("urlquals"),
		Qualifier:    aws.String("live"),
		AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
	})
	if err != nil {
		t.Fatalf("CreateFunctionUrlConfig(live): %v", err)
	}

	latestURL, err := client.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("urlquals"),
		AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
	})
	if err != nil {
		t.Fatalf("CreateFunctionUrlConfig($LATEST): %v", err)
	}

	if aws.ToString(aliasURL.FunctionUrl) == aws.ToString(latestURL.FunctionUrl) {
		t.Fatal("the alias and $LATEST Function URLs must differ")
	}

	// A numbered-version qualifier is rejected.
	_, err = client.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("urlquals"),
		Qualifier:    aws.String("1"),
		AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("CreateFunctionUrlConfig(qualifier=1) err = %v, want InvalidParameterValueException", err)
	}

	// Creating the same (function, qualifier) twice conflicts.
	_, err = client.CreateFunctionUrlConfig(ctx, &awslambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String("urlquals"),
		Qualifier:    aws.String("live"),
		AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
	})
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ResourceConflictException" {
		t.Fatalf("duplicate CreateFunctionUrlConfig(live) err = %v, want ResourceConflictException", err)
	}

	list, err := client.ListFunctionUrlConfigs(ctx, &awslambda.ListFunctionUrlConfigsInput{
		FunctionName: aws.String("urlquals"),
	})
	if err != nil {
		t.Fatalf("ListFunctionUrlConfigs: %v", err)
	}

	if len(list.FunctionUrlConfigs) != 2 {
		t.Fatalf("FunctionUrlConfigs = %d, want 2 (one per qualifier)", len(list.FunctionUrlConfigs))
	}
}
