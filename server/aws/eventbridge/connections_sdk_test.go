package eventbridge_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newEBAndSecrets serves EventBridge and Secrets Manager from one cloud, so a
// connection's secret can be read back through the Secrets Manager SDK.
func newEBAndSecrets(t *testing.T) (*awseb.Client, *awssm.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		EventBridge: cloud.EventBridge, SecretsManager: cloud.SecretsManager,
		AccountID: "123456789012", Region: "us-east-1",
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	eb := awseb.NewFromConfig(cfg, func(o *awseb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	sm := awssm.NewFromConfig(cfg, func(o *awssm.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return eb, sm
}

func TestSDKConnectionAndAPIDestinationLifecycle(t *testing.T) {
	eb, sm := newEBAndSecrets(t)
	ctx := context.Background()

	created, err := eb.CreateConnection(ctx, &awseb.CreateConnectionInput{
		Name:              aws.String("c1"),
		AuthorizationType: ebtypes.ConnectionAuthorizationTypeApiKey,
		AuthParameters: &ebtypes.CreateConnectionAuthRequestParameters{
			ApiKeyAuthParameters: &ebtypes.CreateConnectionApiKeyAuthRequestParameters{
				ApiKeyName: aws.String("x-api-key"), ApiKeyValue: aws.String("s3cr3t"),
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	if created.ConnectionState != ebtypes.ConnectionStateAuthorized {
		t.Fatalf("CreateConnection state = %s, want AUTHORIZED", created.ConnectionState)
	}

	desc, err := eb.DescribeConnection(ctx, &awseb.DescribeConnectionInput{Name: aws.String("c1")})
	if err != nil {
		t.Fatalf("DescribeConnection: %v", err)
	}

	key := desc.AuthParameters.ApiKeyAuthParameters
	if aws.ToString(key.ApiKeyName) != "x-api-key" || aws.ToString(desc.SecretArn) == "" {
		t.Fatalf("DescribeConnection = %+v", desc)
	}

	secret, err := sm.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: desc.SecretArn})
	if err != nil {
		t.Fatalf("GetSecretValue(%s): %v", aws.ToString(desc.SecretArn), err)
	}

	if aws.ToString(secret.SecretString) != `{"api_key_name":"x-api-key","api_key_value":"s3cr3t"}` {
		t.Fatalf("connection secret = %s", aws.ToString(secret.SecretString))
	}

	dest, err := eb.CreateApiDestination(ctx, &awseb.CreateApiDestinationInput{
		Name: aws.String("d1"), ConnectionArn: created.ConnectionArn,
		InvocationEndpoint: aws.String("https://example.com/hook"), HttpMethod: ebtypes.ApiDestinationHttpMethodPost,
	})
	if err != nil {
		t.Fatalf("CreateApiDestination: %v", err)
	}

	if dest.ApiDestinationState != ebtypes.ApiDestinationStateActive {
		t.Fatalf("CreateApiDestination state = %s", dest.ApiDestinationState)
	}

	got, err := eb.DescribeApiDestination(ctx, &awseb.DescribeApiDestinationInput{Name: aws.String("d1")})
	if err != nil || aws.ToInt32(got.InvocationRateLimitPerSecond) != 300 {
		t.Fatalf("DescribeApiDestination = %+v, %v", got, err)
	}

	if _, err := eb.DeauthorizeConnection(ctx, &awseb.DeauthorizeConnectionInput{Name: aws.String("c1")}); err != nil {
		t.Fatalf("DeauthorizeConnection: %v", err)
	}

	got, err = eb.DescribeApiDestination(ctx, &awseb.DescribeApiDestinationInput{Name: aws.String("d1")})
	if err != nil || got.ApiDestinationState != ebtypes.ApiDestinationStateInactive {
		t.Fatalf("DescribeApiDestination after deauthorize = %+v, %v", got, err)
	}

	list, err := eb.ListConnections(ctx, &awseb.ListConnectionsInput{})
	if err != nil || len(list.Connections) != 1 {
		t.Fatalf("ListConnections = %+v, %v", list, err)
	}

	if _, err := eb.DeleteApiDestination(ctx, &awseb.DeleteApiDestinationInput{Name: aws.String("d1")}); err != nil {
		t.Fatalf("DeleteApiDestination: %v", err)
	}

	deleted, err := eb.DeleteConnection(ctx, &awseb.DeleteConnectionInput{Name: aws.String("c1")})
	if err != nil || deleted.ConnectionState != ebtypes.ConnectionStateDeleting {
		t.Fatalf("DeleteConnection = %+v, %v", deleted, err)
	}

	_, err = sm.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: desc.SecretArn})
	if err == nil {
		t.Fatal("the connection's secret still exists after DeleteConnection")
	}

	_, err = eb.DescribeConnection(ctx, &awseb.DescribeConnectionInput{Name: aws.String("c1")})

	var notFound *ebtypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("DescribeConnection after delete = %v, want ResourceNotFoundException", err)
	}
}

// TestSDKConnectionErrorDoesNotLeakSecret sends an over-long API key value and
// checks the ValidationException names the member without echoing the value.
func TestSDKConnectionErrorDoesNotLeakSecret(t *testing.T) {
	eb, _ := newEBAndSecrets(t)
	secret := strings.Repeat("TOPSECRET", 60)

	_, err := eb.CreateConnection(context.Background(), &awseb.CreateConnectionInput{
		Name:              aws.String("c1"),
		AuthorizationType: ebtypes.ConnectionAuthorizationTypeApiKey,
		AuthParameters: &ebtypes.CreateConnectionAuthRequestParameters{
			ApiKeyAuthParameters: &ebtypes.CreateConnectionApiKeyAuthRequestParameters{
				ApiKeyName: aws.String("x-api-key"), ApiKeyValue: aws.String(secret),
			},
		},
	})
	requireAPIError(t, err, "ValidationException",
		"1 validation error detected: Value at 'authParameters.apiKeyAuthParameters.apiKeyValue' failed to satisfy constraint: "+
			"Member must have length less than or equal to 512")

	if strings.Contains(err.Error(), "TOPSECRET") {
		t.Fatalf("error leaks the API key value: %v", err)
	}
}

func TestSDKCreateAPIDestinationValidation(t *testing.T) {
	eb, _ := newEBAndSecrets(t)

	_, err := eb.CreateApiDestination(context.Background(), &awseb.CreateApiDestinationInput{
		Name: aws.String("d"), ConnectionArn: aws.String("invalid-connection-arn"),
		InvocationEndpoint: aws.String("https://example.com"), HttpMethod: "INVALID_METHOD",
	})
	requireAPIError(t, err, "ValidationException", "")
}
