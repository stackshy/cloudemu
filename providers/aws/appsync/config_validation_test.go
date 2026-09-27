package appsync_test

import (
	"context"
	"encoding/json"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

const (
	userPoolJSON = `{"userPoolId":"us-east-1_abc","awsRegion":"us-east-1","defaultAction":"ALLOW"}`
	oidcJSON     = `{"issuer":"https://issuer.example.com"}`
	lambdaAuth   = `{"authorizerUri":"arn:aws:lambda:us-east-1:123456789012:function:auth"}`
	roleARN      = "arn:aws:iam::123456789012:role/appsync"
	emptyString  = "The validated string is empty"
)

func raw(kv ...string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = json.RawMessage(kv[i+1])
	}

	return out
}

func assertBadRequest(t *testing.T, err error, wantMsg string) {
	t.Helper()

	assertException(t, err, driver.ExBadRequest)

	if got := cerrors.Message(err); got != wantMsg {
		t.Fatalf("message = %q, want %q", got, wantMsg)
	}
}

func TestGraphqlAPIAuthConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		auth    string
		extra   map[string]json.RawMessage
		wantErr string
	}{
		{"api key needs nothing", driver.AuthAPIKey, nil, ""},
		{"iam needs nothing", driver.AuthAWSIAM, nil, ""},
		{"cognito with config", driver.AuthCognito, raw("userPoolConfig", userPoolJSON), ""},
		{"cognito without config", driver.AuthCognito, nil, "UserPoolConfig can't be null."},
		{"cognito null config", driver.AuthCognito, raw("userPoolConfig", "null"), "UserPoolConfig can't be null."},
		{
			"cognito bad default action", driver.AuthCognito,
			raw("userPoolConfig", `{"userPoolId":"p","awsRegion":"us-east-1"}`), "Invalid default effect type",
		},
		{"oidc with config", driver.AuthOpenIDConnect, raw("openIDConnectConfig", oidcJSON), ""},
		{"oidc without config", driver.AuthOpenIDConnect, nil, "OpenIDConnectConfig can't be null."},
		{"lambda with config", driver.AuthLambda, raw("lambdaAuthorizerConfig", lambdaAuth), ""},
		{"lambda without config", driver.AuthLambda, nil, "LambdaAuthorizerConfig can't be null."},
		{
			"additional providers with config", driver.AuthAPIKey,
			raw("additionalAuthenticationProviders", `[{"authenticationType":"AWS_IAM"},`+
				`{"authenticationType":"AMAZON_COGNITO_USER_POOLS","userPoolConfig":{"userPoolId":"p","awsRegion":"us-east-1"}},`+
				`{"authenticationType":"OPENID_CONNECT","openIDConnectConfig":`+oidcJSON+`},`+
				`{"authenticationType":"AWS_LAMBDA","lambdaAuthorizerConfig":`+lambdaAuth+`}]`), "",
		},
		{
			"additional cognito without config", driver.AuthAPIKey,
			raw("additionalAuthenticationProviders", `[{"authenticationType":"AMAZON_COGNITO_USER_POOLS"}]`),
			"UserPoolConfig can't be null.",
		},
		{
			"additional oidc without config", driver.AuthAPIKey,
			raw("additionalAuthenticationProviders", `[{"authenticationType":"OPENID_CONNECT"}]`),
			"OpenIDConnectConfig can't be null.",
		},
		{
			"additional lambda without config", driver.AuthAPIKey,
			raw("additionalAuthenticationProviders", `[{"authenticationType":"AWS_LAMBDA"}]`),
			"LambdaAuthorizerConfig can't be null.",
		},
		{
			"additional provider without type", driver.AuthAPIKey,
			raw("additionalAuthenticationProviders", `[{}]`), "AuthenticationType can't be null.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)
			ctx := context.Background()

			created, err := m.CreateGraphqlAPI(ctx, &driver.CreateGraphqlAPIInput{
				Name: "api", AuthenticationType: tc.auth, Extra: tc.extra,
			})
			if tc.wantErr != "" {
				assertBadRequest(t, err, tc.wantErr)
			} else if err != nil {
				t.Fatalf("CreateGraphqlAPI: %v", err)
			}

			// UpdateGraphqlAPI applies the same rules to an existing API.
			base := createAPI(t, m, "base")

			_, err = m.UpdateGraphqlAPI(ctx, &driver.UpdateGraphqlAPIInput{
				APIID: base.APIID, Name: "base", AuthenticationType: tc.auth, Extra: tc.extra,
			})
			if tc.wantErr != "" {
				assertBadRequest(t, err, tc.wantErr)

				return
			}

			if err != nil {
				t.Fatalf("UpdateGraphqlAPI: %v", err)
			}

			if created == nil {
				t.Fatal("create returned nil")
			}
		})
	}
}

func TestDataSourceConfigValidation(t *testing.T) {
	const (
		ddb    = `{"tableName":"t","awsRegion":"us-east-1"}`
		lambda = `{"lambdaFunctionArn":"arn:aws:lambda:us-east-1:123456789012:function:f"}`
		search = `{"endpoint":"https://search.example.com","awsRegion":"us-east-1"}`
		http   = `{"endpoint":"https://example.com"}`
		rds    = `{"relationalDatabaseSourceType":"RDS_HTTP_ENDPOINT","rdsHttpEndpointConfig":{"awsRegion":"us-east-1",` +
			`"dbClusterIdentifier":"arn:aws:rds:us-east-1:123456789012:cluster:c",` +
			`"awsSecretStoreArn":"arn:aws:secretsmanager:us-east-1:123456789012:secret:s"}}`
		bus = `{"eventBusArn":"arn:aws:events:us-east-1:123456789012:event-bus/default"}`
	)

	cases := []struct {
		name    string
		dsType  string
		role    string
		extra   map[string]json.RawMessage
		wantErr string
	}{
		{"none takes nothing", driver.DataSourceNone, "", nil, ""},
		{"none rejects a config", driver.DataSourceNone, "", raw("httpConfig", http),
			"HttpConfig is not supported for data source type NONE."},
		{"bedrock runtime takes nothing", "AMAZON_BEDROCK_RUNTIME", roleARN, nil, ""},
		{"dynamodb ok", driver.DataSourceDynamoDB, roleARN, raw("dynamodbConfig", ddb), ""},
		{"dynamodb missing config", driver.DataSourceDynamoDB, roleARN, nil, "DynamodbConfig can't be null."},
		{"dynamodb missing table", driver.DataSourceDynamoDB, roleARN, raw("dynamodbConfig", `{"awsRegion":"us-east-1"}`), emptyString},
		{"dynamodb missing role", driver.DataSourceDynamoDB, "", raw("dynamodbConfig", ddb), emptyString},
		{"dynamodb with lambda config", driver.DataSourceDynamoDB, roleARN, raw("dynamodbConfig", ddb, "lambdaConfig", lambda),
			"LambdaConfig is not supported for data source type AMAZON_DYNAMODB."},
		{"lambda ok", driver.DataSourceLambda, roleARN, raw("lambdaConfig", lambda), ""},
		{"lambda missing config", driver.DataSourceLambda, roleARN, nil, "LambdaConfig can't be null."},
		{"lambda missing role", driver.DataSourceLambda, "", raw("lambdaConfig", lambda), emptyString},
		{"opensearch ok", driver.DataSourceOpenSearch, roleARN, raw("openSearchServiceConfig", search), ""},
		{"opensearch missing config", driver.DataSourceOpenSearch, roleARN, nil, "OpenSearchServiceConfig can't be null."},
		{"elasticsearch ok", driver.DataSourceElasticsearch, roleARN, raw("elasticsearchConfig", search), ""},
		{"elasticsearch missing config", driver.DataSourceElasticsearch, roleARN, nil, "ElasticsearchConfig can't be null."},
		{"http ok without role", driver.DataSourceHTTP, "", raw("httpConfig", http), ""},
		{"http missing config", driver.DataSourceHTTP, "", nil, "HttpConfig can't be null."},
		{"http missing endpoint", driver.DataSourceHTTP, "", raw("httpConfig", `{}`), emptyString},
		{"http iam auth needs role", driver.DataSourceHTTP, "",
			raw("httpConfig", `{"endpoint":"https://example.com","authorizationConfig":{"authorizationType":"AWS_IAM"}}`), emptyString},
		{"http iam auth with role", driver.DataSourceHTTP, roleARN,
			raw("httpConfig", `{"endpoint":"https://example.com","authorizationConfig":{"authorizationType":"AWS_IAM"}}`), ""},
		{"rds ok", driver.DataSourceRelational, roleARN, raw("relationalDatabaseConfig", rds), ""},
		{"rds missing config", driver.DataSourceRelational, roleARN, nil, "RelationalDatabaseConfig can't be null."},
		{"rds missing endpoint config", driver.DataSourceRelational, roleARN,
			raw("relationalDatabaseConfig", `{"relationalDatabaseSourceType":"RDS_HTTP_ENDPOINT"}`), "RdsHttpEndpointConfig can't be null."},
		{"eventbridge ok", driver.DataSourceEventBridge, roleARN, raw("eventBridgeConfig", bus), ""},
		{"eventbridge missing config", driver.DataSourceEventBridge, roleARN, nil, "EventBridgeConfig can't be null."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)
			ctx := context.Background()
			api := createAPI(t, m, "api")

			_, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
				APIID: api.APIID, Name: "src", Type: tc.dsType, ServiceRoleArn: tc.role, Extra: tc.extra,
			})
			if tc.wantErr != "" {
				assertBadRequest(t, err, tc.wantErr)
			} else if err != nil {
				t.Fatalf("CreateDataSource: %v", err)
			}

			// UpdateDataSource applies the same rules to an existing source.
			if _, err = m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
				APIID: api.APIID, Name: "base", Type: driver.DataSourceNone,
			}); err != nil {
				t.Fatalf("CreateDataSource base: %v", err)
			}

			_, err = m.UpdateDataSource(ctx, &driver.UpdateDataSourceInput{
				APIID: api.APIID, Name: "base", Type: tc.dsType, ServiceRoleArn: tc.role, Extra: tc.extra,
			})
			if tc.wantErr != "" {
				assertBadRequest(t, err, tc.wantErr)
			} else if err != nil {
				t.Fatalf("UpdateDataSource: %v", err)
			}
		})
	}
}
